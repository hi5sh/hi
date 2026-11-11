package cli

// Signing in by email (docs/specs/approved/hi_people.md): on the dev
// page, in the helper, and on the admin page, beside the links Slack
// sends. Someone types their email; if it may sign in there, hi emails a
// link and a code, each good once, for 15 minutes. The link opens a page
// with a button, since the scanners in front of company mailboxes open
// every link in an email and would spend a link that signed in on its
// own. The code is typed in the tab that asked, for mail read on a phone.
// Either ends in the same sign-in a Slack link gives.
//
// The link in the email is built from the address's own setting, never
// from the request's Host header: a forged Host would otherwise have hi
// mail a working link to someone else's site.

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	mailSigninLasts = 15 * time.Minute
	mailInviteLasts = 72 * time.Hour
	mailCodeTries   = 5
)

// mailSignin is one sign-in under way. Its token, request, and code are
// kept as hashes.
type mailSignin struct {
	Token   string    `json:"token"`
	Request string    `json:"request,omitempty"`
	Code    string    `json:"code,omitempty"`
	Email   string    `json:"email"`
	Who     string    `json:"who"` // their entry in an app's lists, or the admin's name
	Name    string    `json:"name,omitempty"`
	Tries   int       `json:"tries,omitempty"`
	Invite  bool      `json:"invite,omitempty"`
	Made    time.Time `json:"made"`
	Ends    time.Time `json:"ends"`
}

type mailSigninFile struct {
	Pending []mailSignin `json:"pending"`
}

// mailSigninStore keeps a page's sign-ins under way in a file of its
// own, so they outlast a restart, and counts what was asked, in memory.
type mailSigninStore struct {
	path string
	mu   sync.Mutex
	// asked is when sign-ins were asked for, by email, by the address
	// they came from, and for everyone ("").
	asked map[string][]time.Time
}

var mailStores = struct {
	sync.Mutex
	at map[string]*mailSigninStore
}{at: map[string]*mailSigninStore{}}

// mailStoreAt is the store kept at path, one per process.
func mailStoreAt(path string) *mailSigninStore {
	mailStores.Lock()
	defer mailStores.Unlock()
	if store := mailStores.at[path]; store != nil {
		return store
	}
	store := &mailSigninStore{path: path, asked: map[string][]time.Time{}}
	mailStores.at[path] = store
	return store
}

func mailHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func mailRandom() string {
	raw := make([]byte, 24)
	rand.Read(raw)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func mailCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("%06d", n.Int64())
}

func (s *mailSigninStore) load() mailSigninFile {
	var file mailSigninFile
	if data, err := os.ReadFile(s.path); err == nil {
		json.Unmarshal(data, &file)
	}
	now := time.Now()
	file.Pending = slices.DeleteFunc(file.Pending, func(p mailSignin) bool { return now.After(p.Ends) })
	return file
}

func (s *mailSigninStore) save(file mailSigninFile) error {
	os.MkdirAll(filepath.Dir(s.path), 0o700)
	data, _ := json.MarshalIndent(file, "", "  ")
	return writeFileAtomic(s.path, append(data, '\n'), 0o600)
}

// allow says whether one more sign-in may be asked for: three in 15
// minutes for an address, ten an hour from where it came from, and a
// hundred an hour for the page.
func (s *mailSigninStore) allow(email, from string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	limits := []struct {
		key    string
		within time.Duration
		most   int
	}{{"email " + email, 15 * time.Minute, 3}, {"from " + from, time.Hour, 10}, {"", time.Hour, 100}}
	for _, limit := range limits {
		s.asked[limit.key] = slices.DeleteFunc(s.asked[limit.key], func(at time.Time) bool { return now.Sub(at) > limit.within })
		if len(s.asked[limit.key]) >= limit.most {
			return false
		}
	}
	for _, limit := range limits {
		s.asked[limit.key] = append(s.asked[limit.key], now)
	}
	return true
}

// add keeps a sign-in, and gives its token and, for one asked for on a
// page, its code.
func (s *mailSigninStore) add(entry mailSignin, request string, lasts time.Duration) (token, code string, err error) {
	token = mailRandom()
	entry.Token = mailHash(token)
	if request != "" {
		code = mailCode()
		entry.Request, entry.Code = mailHash(request), mailHash(request, code)
	}
	now := time.Now().UTC()
	entry.Made, entry.Ends = now, now.Add(lasts)
	s.mu.Lock()
	defer s.mu.Unlock()
	file := s.load()
	// A newer sign-in for the same person and page ends the older ones,
	// but not an invitation.
	file.Pending = slices.DeleteFunc(file.Pending, func(p mailSignin) bool { return p.Email == entry.Email && !p.Invite && !entry.Invite })
	file.Pending = append(file.Pending, entry)
	return token, code, s.save(file)
}

// peek is the sign-in a link's token is for, without spending it.
func (s *mailSigninStore) peek(token string) (mailSignin, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.find(s.load(), token)
}

func (s *mailSigninStore) find(file mailSigninFile, token string) (mailSignin, bool) {
	if token == "" {
		return mailSignin{}, false
	}
	hash := mailHash(token)
	for _, p := range file.Pending {
		if subtle.ConstantTimeCompare([]byte(p.Token), []byte(hash)) == 1 {
			return p, true
		}
	}
	return mailSignin{}, false
}

// take spends a link's token.
func (s *mailSigninStore) take(token string) (mailSignin, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file := s.load()
	entry, ok := s.find(file, token)
	if !ok {
		return entry, false
	}
	file.Pending = slices.DeleteFunc(file.Pending, func(p mailSignin) bool { return p.Token == entry.Token })
	if s.save(file) != nil {
		return mailSignin{}, false
	}
	return entry, true
}

// takeCode spends a code typed in the tab that asked; five wrong ones end
// the sign-in.
func (s *mailSigninStore) takeCode(request, code string) (mailSignin, error) {
	code = strings.Join(strings.Fields(code), "")
	s.mu.Lock()
	defer s.mu.Unlock()
	file := s.load()
	requestHash := mailHash(request)
	i := slices.IndexFunc(file.Pending, func(p mailSignin) bool {
		return p.Request != "" && subtle.ConstantTimeCompare([]byte(p.Request), []byte(requestHash)) == 1
	})
	ended := errors.New("that code has ended: ask for a new email")
	if request == "" || i < 0 {
		return mailSignin{}, ended
	}
	if subtle.ConstantTimeCompare([]byte(file.Pending[i].Code), []byte(mailHash(request, code))) != 1 {
		file.Pending[i].Tries++
		if file.Pending[i].Tries >= mailCodeTries {
			file.Pending = slices.Delete(file.Pending, i, i+1)
			s.save(file)
			return mailSignin{}, errors.New("that code is wrong, five times: ask for a new email")
		}
		s.save(file)
		return mailSignin{}, errors.New("that code is wrong; it is the six digits in the email")
	}
	entry := file.Pending[i]
	file.Pending = slices.Delete(file.Pending, i, i+1)
	if err := s.save(file); err != nil {
		return mailSignin{}, err
	}
	return entry, nil
}

// ---------------------------------------------------------------------------
// the routes, for any page

// mailSigninFlow is one page's sign-in by email.
type mailSigninFlow struct {
	store *mailSigninStore
	// what the page is, in a sentence: "the demo app's dev page".
	what func() string
	// link is the link in the email for a token, from the address's own
	// setting; "" when the address has none.
	link func(token string) string
	// who is who an email signs in as here, and their name; "" when it
	// may not.
	who func(email string) (who, name string)
	// done signs them in, and says where the browser goes next.
	done func(w http.ResponseWriter, r *http.Request, entry mailSignin) (string, error)
	log  func(format string, args ...any)
}

// mailFrom is where a request came from, as the relay or the page's own
// listener saw it.
func mailFrom(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func mailReply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

// start is POST {email}: the same answer whether or not the email may
// sign in here, and the email goes out after it, so neither the answer
// nor its time says who may.
func (f mailSigninFlow) start(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
		mailReply(w, http.StatusBadRequest, map[string]string{"error": "give your email address"})
		return
	}
	email, err := appCheckEmail(request.Email)
	if err != nil {
		mailReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if _, ok := loadMailSettings(); !ok {
		mailReply(w, http.StatusServiceUnavailable, map[string]string{"error": "This machine doesn't send email yet, so sign in with the link Slack sends, or ask an admin of the machine to set up email on the admin page (Keys)."})
		return
	}
	if !f.store.allow(email, mailFrom(r)) {
		mailReply(w, http.StatusTooManyRequests, map[string]string{"error": "That's a lot of emails asked for; wait a few minutes, and look for the ones already sent."})
		return
	}
	requestID := mailRandom()
	what := f.what()
	go func() {
		who, name := f.who(email)
		if who == "" {
			f.log("an email sign-in to %s for someone who may not: nothing sent", what)
			return
		}
		token, code, err := f.store.add(mailSignin{Email: email, Who: who, Name: name}, requestID, mailSigninLasts)
		if err != nil {
			f.log("an email sign-in: %v", err)
			return
		}
		link := f.link(token)
		if link == "" {
			f.log("an email sign-in to %s: the address has no link to send", what)
			return
		}
		err = sendMail(mailMessage{To: email, Subject: "Sign in to " + what,
			Text: fmt.Sprintf("Someone, hopefully you, asked to sign in to %s as %s.\n\nSign in:\n%s\n\nOr type this code on the page that asked: %s\n\n"+
				"The link and the code work once, for %d minutes. If it wasn't you, ignore this email: nobody gets in without it.\n\nhi on %s\n",
				what, email, link, code, int(mailSigninLasts.Minutes()), adminMachineName())})
		if err != nil {
			f.log("an email sign-in to %s: %v", what, err)
			return
		}
		f.log("an email sign-in to %s: sent to %s", what, email)
	}()
	mailReply(w, http.StatusOK, map[string]string{"request": requestID,
		"text": fmt.Sprintf("If %s may sign in here, a link and a code are on their way to it. They work for %d minutes.", email, int(mailSigninLasts.Minutes()))})
}

// code is POST {request, code}: the code typed in the tab that asked.
func (f mailSigninFlow) code(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Request string `json:"request"`
		Code    string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
		mailReply(w, http.StatusBadRequest, map[string]string{"error": "give the code from the email"})
		return
	}
	entry, err := f.store.takeCode(request.Request, request.Code)
	if err != nil {
		mailReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	target, err := f.done(w, r, entry)
	if err != nil {
		mailReply(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	mailReply(w, http.StatusOK, map[string]string{"go": target})
}

var mailConfirmPage = template.Must(template.New("confirm").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="same-origin"><title>{{if .Email}}Sign in{{else}}This link has ended{{end}}</title>
<style>
body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: #1f1f1f; color: #ccc; font: 15px/1.5 system-ui, sans-serif; }
main { max-width: 26rem; padding: 2rem; background: #181818; border: 1px solid #2b2b2b; border-radius: 10px; }
h1 { font-size: 1.15rem; margin: 0 0 .75rem; color: #e7e7e7; }
p { margin: 0 0 1rem; }
button { font: inherit; font-weight: 600; padding: .55rem 1.2rem; border-radius: 6px; border: 0; background: #0078d4; color: #fff; cursor: pointer; }
.muted { color: #9d9d9d; font-size: .875rem; }
</style></head>
<body><main>
{{if .Email}}
<h1>Sign in to {{.What}}</h1>
<p>As <strong>{{.Email}}</strong>{{if .Name}} ({{.Name}}){{end}}.</p>
<form method="post"><input type="hidden" name="t" value="{{.Token}}"><button type="submit">Sign in</button></form>
<p class="muted" style="margin-top:1rem">The link works once. Someone else's browser that opens it signs in as you, so keep it to yourself.</p>
{{else}}
<h1>This link has ended</h1>
<p>It was used already, or more than {{.Minutes}} minutes went by. Open the page again and sign in with your email for a new one.</p>
{{end}}
</main></body></html>
`))

// confirm is the link in the email: a page with a button, which signs in
// when pressed.
func (f mailSigninFlow) confirm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// same-origin, not no-referrer: with none, a browser sends the
	// button's POST with Origin: null, which the pages' same-origin
	// checks refuse. The token still never leaves the address.
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	if r.Method == http.MethodPost {
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
			http.Error(w, "Sign in from the page the email's link opens.", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		entry, ok := f.store.take(r.PostFormValue("t"))
		if !ok {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			mailConfirmPage.Execute(w, map[string]any{"Minutes": int(mailSigninLasts.Minutes())})
			return
		}
		target, err := f.done(w, r, entry)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	token := r.URL.Query().Get("t")
	entry, ok := f.store.peek(token)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if !ok {
		mailConfirmPage.Execute(w, map[string]any{"Minutes": int(mailSigninLasts.Minutes())})
		return
	}
	name := entry.Name
	if name == entry.Email {
		name = ""
	}
	mailConfirmPage.Execute(w, map[string]any{"What": f.what(), "Email": entry.Email, "Name": name, "Token": token})
}

// appAddressLink is an address's link with another path and more in its
// query, keeping what it carries (a public address's key).
func appAddressLink(base, path string, values url.Values) string {
	if base == "" {
		return ""
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.Path = path
	query := parsed.Query()
	for key, list := range values {
		for _, value := range list {
			query.Add(key, value)
		}
	}
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return parsed.String()
}

// ---------------------------------------------------------------------------
// an app's pages: the dev page, and the helper on Stage and production

func (b *appBroker) mailStore() *mailSigninStore {
	return mailStoreAt(filepath.Join(appDir(b.team), "signins.json"))
}

func (b *appBroker) mailLog(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "hi: "+format+"\n", args...)
}

// devMailFlow is the dev page's sign-in by email, for owners and
// developers.
func (b *appBroker) devMailFlow() mailSigninFlow {
	return mailSigninFlow{
		store: b.mailStore(),
		what:  func() string { return "the " + appBotName(b.team) + " app's dev page" },
		link: func(token string) string {
			page, ok := loadTeamDevPage(b.team)
			if !ok {
				return ""
			}
			return appAddressLink(page.URL, appDevPagePrefix+"email", url.Values{"t": {token}})
		},
		who: func(email string) (string, string) {
			config, err := loadTeam(b.team)
			if err != nil {
				return "", ""
			}
			who, name := appWhoByEmail(config, email)
			if who == "" || !appBuilds(config, who) {
				return "", ""
			}
			return who, name
		},
		done: func(w http.ResponseWriter, r *http.Request, entry mailSignin) (string, error) {
			config, err := loadTeam(b.team)
			if err != nil || !appBuilds(config, entry.Who) {
				return "", errors.New("the dev page is for the app's owners and developers")
			}
			key, err := b.mailSignedIn(config, entry, "dev page")
			if err != nil {
				return "", err
			}
			page, _ := loadTeamDevPage(b.team)
			return firstNonEmpty(appAddressLink(page.URL, appDevPagePrefix, url.Values{netExposeUserParam: {key.Key}}), appDevPagePrefix+"?"+netExposeUserParam+"="+key.Key), nil
		},
		log: b.mailLog,
	}
}

// slotMailFlow is the helper's sign-in by email on Stage or production,
// for anyone with a level: Report, and the chat with the lead.
func (b *appBroker) slotMailFlow(slot string) mailSigninFlow {
	slotURL := func() string {
		state, ok := loadTeamSlot(b.team, slot)
		if !ok {
			return ""
		}
		return state.URL
	}
	return mailSigninFlow{
		store: b.mailStore(),
		what:  func() string { return "the " + appBotName(b.team) + " app (" + appAddressLabel(slot) + ")" },
		link: func(token string) string {
			return appAddressLink(slotURL(), appHelperPrefix+"email", url.Values{"t": {token}})
		},
		who: func(email string) (string, string) {
			config, err := loadTeam(b.team)
			if err != nil {
				return "", ""
			}
			return appWhoByEmail(config, email)
		},
		done: func(w http.ResponseWriter, r *http.Request, entry mailSignin) (string, error) {
			config, err := loadTeam(b.team)
			if err != nil || appLevel(config, entry.Who) == appLevelNone {
				return "", errors.New("you have no level in this app any more; an owner gives you one")
			}
			key, err := b.mailSignedIn(config, entry, appAddressLabel(slot))
			if err != nil {
				return "", err
			}
			return firstNonEmpty(appAddressLink(slotURL(), "/", url.Values{netExposeUserParam: {key.Key}}), "/?"+netExposeUserParam+"="+key.Key), nil
		},
		log: b.mailLog,
	}
}

// mailRoutes puts a flow's routes on a page's mux: api/email and
// api/email/code under prefix, and the email's link at prefix + email.
func mailRoutes(mux *http.ServeMux, prefix string, flow mailSigninFlow) []string {
	routes := []string{"POST " + prefix + "api/email", "POST " + prefix + "api/email/code", "GET " + prefix + "email", "POST " + prefix + "email"}
	mux.HandleFunc(routes[0], flow.start)
	mux.HandleFunc(routes[1], flow.code)
	mux.HandleFunc(routes[2], flow.confirm)
	mux.HandleFunc(routes[3], flow.confirm)
	return routes
}

// mailSignedIn makes the key a sign-in by email ends in: one of its own,
// beside the person's other devices', as a Slack link's is. The machine
// notes when they signed in, and the app's ops channel hears the first
// time, so an email typed wrong is noticed.
func (b *appBroker) mailSignedIn(config appConfig, entry mailSignin, where string) (appPersonKey, error) {
	now := time.Now().UTC()
	key := appPersonKey{Key: mailRandom() + mailRandom()[:8], User: entry.Who, Name: firstNonEmpty(entry.Name, entry.Email), Made: now, Ends: now.Add(appKeyLasts)}
	b.peopleMu.Lock()
	people := loadTeamPeople(b.team)
	people.Keys = slices.DeleteFunc(people.Keys, func(old appPersonKey) bool { return now.After(old.Ends.Add(30 * 24 * time.Hour)) })
	people.Keys = append(people.Keys, key)
	err := saveTeamPeople(b.team, people)
	b.peopleMu.Unlock()
	if err != nil {
		return key, err
	}
	first := false
	changeMachinePeople(func(m *machinePeople) error {
		slackID := ""
		if !appIsEmail(entry.Who) {
			slackID = entry.Who
		}
		m.link(entry.Email, slackID, entry.Name, "hi, at a sign-in by email")
		i := m.find(entry.Email)
		first = m.People[i].SignedIn.IsZero()
		m.People[i].SignedIn = now
		return nil
	})
	b.mailLog("%s signed in by email to the %s", entry.Email, where)
	if first && b.channel != nil {
		b.channel.post(config.opsChannel(), fmt.Sprintf(":email: %s signed in by email for the first time (%s), to the %s, as %s. If that isn't them, take their level away.",
			appSlackEscape(firstNonEmpty(entry.Name, entry.Email)), appSlackEscape(entry.Email), where, appLevelWords(appLevel(config, entry.Who))))
	}
	return key, nil
}

// appMailInvite is an invitation's link to the app for someone with a
// level: the dev page's for someone who builds, else production's, else
// Stage's. Good once, within three days.
type appMailInvite struct {
	Link  string `json:"link"`
	What  string `json:"what"`
	Level string `json:"level"`
}

func (b *appBroker) mailInvite(email string) (appMailInvite, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return appMailInvite{}, err
	}
	who, name := appWhoByEmail(config, email)
	if who == "" {
		return appMailInvite{}, fmt.Errorf("%s has no level in %s", email, b.team)
	}
	flow := b.devMailFlow()
	if !appBuilds(config, who) || flow.link("x") == "" {
		flow = b.slotMailFlow(appSlotProduction)
		if flow.link("x") == "" {
			flow = b.slotMailFlow(appSlotPreview)
		}
	}
	token, _, err := b.mailStore().add(mailSignin{Email: email, Who: who, Name: name, Invite: true}, "", mailInviteLasts)
	if err != nil {
		return appMailInvite{}, err
	}
	link := flow.link(token)
	if link == "" {
		return appMailInvite{}, fmt.Errorf("%s has no address yet", b.team)
	}
	return appMailInvite{Link: link, What: flow.what(), Level: appLevelWords(appLevel(config, who))}, nil
}

// machineInvite emails someone their way in to every app they have a
// level in, in one email, and says how it went for each.
func machineInvite(person machinePerson, by string) []string {
	var lines, parts []string
	for _, config := range machineAppConfigs() {
		if machinePersonIn(config, person) == "" {
			continue
		}
		var invite appMailInvite
		asked, err := appHostCall(config.Name, "/emailinvite", map[string]string{"email": person.Email}, &invite)
		switch {
		case !asked:
			lines = append(lines, config.Name+": it isn't up, so no link; invite them again when it is")
		case err != nil:
			lines = append(lines, config.Name+": "+err.Error())
		default:
			parts = append(parts, fmt.Sprintf("• %s, as %s:\n  %s", invite.What, invite.Level, invite.Link))
			lines = append(lines, config.Name+": a sign-in link is in the email")
		}
	}
	if admin, ok := adminByEmail(person.Email); ok {
		if link, err := adminInviteLink(admin); err == nil {
			parts = append(parts, fmt.Sprintf("• the admin page of %s, as an admin:\n  %s", adminMachineName(), link))
			lines = append(lines, "the admin page: a sign-in link is in the email")
		}
	}
	if len(parts) == 0 {
		return append(lines, "no email sent: they have nothing to sign in to here yet")
	}
	who := strings.TrimSuffix(strings.TrimSpace(by), " on the admin page")
	err := sendMail(mailMessage{To: person.Email, Subject: "Your way in, on " + adminMachineName(),
		Text: fmt.Sprintf("%s gave you access, %s:\n\n%s\n\nEach link works once, within %d days. After that, open the page and sign in with this email (%s): hi emails you a new link.\n\nhi on %s\n",
			firstNonEmpty(who, "An admin"), firstNonEmpty(person.Name, person.Email), strings.Join(parts, "\n\n"), int(mailInviteLasts.Hours()/24), person.Email, adminMachineName())})
	if err != nil {
		return append(lines, "the email wasn't sent: "+err.Error())
	}
	changeMachinePeople(func(m *machinePeople) error {
		if i := m.find(person.Email); i >= 0 {
			m.People[i].Invited = time.Now().UTC()
		}
		return nil
	})
	adminRecord(by, "", "emailed "+person.Email+" their way in")
	return lines
}

// ---------------------------------------------------------------------------
// the admin page

// adminByEmail is the admin an email belongs to: theirs on the page, or
// the machine's person with that email and the admin's Slack account.
func adminByEmail(email string) (adminPerson, bool) {
	config, ok := loadAdminConfig()
	if !ok {
		return adminPerson{}, false
	}
	person, known := loadMachinePeople().person(email)
	for _, admin := range config.Admins {
		if strings.EqualFold(admin.Email, email) || known && admin.Slack != "" && slices.Contains(person.Slack, admin.Slack) {
			return admin, true
		}
	}
	return adminPerson{}, false
}

func adminMailStore() *mailSigninStore {
	return mailStoreAt(filepath.Join(adminDir(), "signins.json"))
}

// adminInviteLink is an admin's link to the page, good once within three
// days.
func adminInviteLink(admin adminPerson) (string, error) {
	config, ok := loadAdminConfig()
	if !ok {
		return "", errors.New("the admin page isn't set up")
	}
	token, _, err := adminMailStore().add(mailSignin{Email: strings.ToLower(admin.Email), Who: admin.Name, Name: admin.Name, Invite: true}, "", mailInviteLasts)
	if err != nil {
		return "", err
	}
	return adminURL(config) + "email?t=" + url.QueryEscape(token), nil
}

func (s *adminServer) mailFlow() mailSigninFlow {
	return mailSigninFlow{
		store: adminMailStore(),
		what:  func() string { return "the admin page of " + adminMachineName() },
		link: func(token string) string {
			config, ok := loadAdminConfig()
			if !ok {
				return ""
			}
			return adminURL(config) + "email?t=" + url.QueryEscape(token)
		},
		who: func(email string) (string, string) {
			admin, ok := adminByEmail(email)
			if !ok {
				return "", ""
			}
			return admin.Name, admin.Name
		},
		done: func(w http.ResponseWriter, r *http.Request, entry mailSignin) (string, error) {
			session, admin, err := adminNewSession(entry.Who)
			if err != nil {
				return "", err
			}
			http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: session, Path: "/", MaxAge: int(adminSessionLasts / time.Second),
				HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: adminSecure(r)})
			adminRememberWho(w, r, admin.Name)
			adminRecord(admin.Name, "", "signed in by email")
			changeMachinePeople(func(m *machinePeople) error {
				if i := m.find(entry.Email); i >= 0 {
					m.People[i].SignedIn = time.Now().UTC()
				}
				return nil
			})
			return "/", nil
		},
		log: s.logf,
	}
}
