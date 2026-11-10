package cli

// The admin page's server (hi_admin.md): hi admin serve, in
// hi-admin.service. It serves the page and its API on a socket the peer
// passes ports 1984 and 80 to, on 127.0.0.1:1984, and on the machine's
// NetBird address when that's how it's reached. Every request but the
// page itself and asking for a new link needs an admin's sign-in, and
// every change comes from the page's own address.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	hi "github.com/hi5sh/hi"
)

var (
	adminPageHTML   = hi.AdminPageHTML
	adminPageScript = hi.AdminPageScript
	adminPageStyle  = hi.AdminPageStyle
	adminWallHTML   = hi.AdminWallHTML
	adminWallScript = hi.AdminWallScript
)

var adminJobID = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[a-z0-9]{6}$`)

type adminServer struct {
	hi      string // this binary, which jobs run
	log     io.Writer
	certs   appHTTPSCerts
	wake    chan struct{}
	handler http.Handler

	mu       sync.Mutex
	latest   string // the newest hi, as GitHub says
	latestAt time.Time
	asked    map[string]time.Time // new links sent by DM, by admin
	askedAll []time.Time
	lingers  bool
	lingerAt time.Time
	// askpass keeps the sudo password of a job that installs what needs
	// root, for that job alone (admin_needs.go).
	askpass adminAskpass
	// sampler is what the machine and its apps use (admin_usage.go).
	sampler adminSampler
	// names are people's names from Slack, for a while (admin_settings.go).
	names adminNameCache
}

// lingering says whether the account's services run without a login,
// asked of loginctl once a minute at most.
func (s *adminServer) lingering() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.lingerAt) > time.Minute {
		s.lingers, s.lingerAt = appLingers(), time.Now()
	}
	return s.lingers
}

// adminLatest is the newest hi; tests replace it.
var adminLatest = latestRelease

func newAdminServer(hiPath string, log io.Writer) *adminServer {
	s := &adminServer{hi: hiPath, log: log, wake: make(chan struct{}, 1), asked: map[string]time.Time{}}
	s.handler = s.routes()
	return s
}

func (s *adminServer) logf(format string, args ...any) {
	fmt.Fprintf(s.log, "hi: "+format+"\n", args...)
}

// adminServe is hi admin serve.
func adminServe(stderr io.Writer) error {
	if _, ok := loadAdminConfig(); !ok {
		return errors.New("the admin page isn't set up on this machine; hi admin start sets it up")
	}
	executable, err := adminExecutable()
	if err != nil {
		return err
	}
	s := newAdminServer(executable, stderr)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := os.MkdirAll(adminSocketDir(), 0o700); err != nil {
		return err
	}
	listen := func(path string) (net.Listener, error) {
		os.Remove(path)
		listener, err := net.Listen("unix", path)
		if err == nil {
			os.Chmod(path, 0o600)
		}
		return listener, err
	}
	plain, err := listen(adminHTTPSocket())
	if err != nil {
		return err
	}
	go s.serveOn(plain)
	secure, err := listen(adminHTTPSSocket())
	if err != nil {
		return err
	}
	go s.serveOn(serveHTTPS(secure, &s.certs))
	// The jobs' sudo asks here for the password typed on the page.
	if askpass, err := listen(adminAskpassSocket()); err != nil {
		s.logf("the password socket: %v", err)
	} else {
		go s.serveAskpass(askpass)
	}
	// On the machine itself, for an SSH tunnel; another account's admin
	// page may have the port.
	if local, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", adminPort)); err != nil {
		s.logf("listening on 127.0.0.1:%d: %v", adminPort, err)
	} else {
		go s.serveOn(local)
	}
	go s.keepReach(ctx)
	go s.latestVersion()
	config, _ := loadAdminConfig()
	s.logf("the admin page runs hi %s, at %s", version, adminURL(config))
	<-ctx.Done()
	return nil
}

func (s *adminServer) serveOn(listener net.Listener) {
	server := &http.Server{Handler: s.handler, ReadHeaderTimeout: 15 * time.Second}
	server.Serve(listener)
}

// wakeReach has keepReach look again now: a setup key came.
func (s *adminServer) wakeReach() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// latestVersion is the newest hi, asked of GitHub at most once an hour.
func (s *adminServer) latestVersion() string {
	s.mu.Lock()
	latest, at := s.latest, s.latestAt
	stale := time.Since(at) > time.Hour
	if stale {
		s.latestAt = time.Now()
	}
	s.mu.Unlock()
	if stale {
		go func() {
			if tag, err := adminLatest(); err == nil {
				s.mu.Lock()
				s.latest = tag
				s.mu.Unlock()
			}
		}()
	}
	if latest != "" && olderRelease(version, latest) {
		return latest
	}
	return ""
}

// ---------------------------------------------------------------------------
// who is asking

// admin is the admin a request's sign-in belongs to.
func (s *adminServer) admin(r *http.Request) (adminPerson, bool) {
	cookie, err := r.Cookie(adminCookie)
	if err != nil {
		return adminPerson{}, false
	}
	return adminSessionAdmin(cookie.Value)
}

// adminMay says whether an admin may make a change: the org's admins
// change the org, the machine's the machine and its apps. On a machine
// whose lists are one, each admin is both.
func adminMay(admin adminPerson, path string) error {
	switch {
	case path == "/api/signout":
		return nil
	case path == "/api/org" || path == "/api/walls":
		if !admin.Org {
			return errors.New("you aren't one of the org's admins: you see the org, and its owner and admins change it")
		}
	case !admin.Machine:
		return errors.New("you are an admin of the org, not of the machine: you see the machine and its apps, and the machine's admins change them")
	}
	return nil
}

// adminSameOrigin says a change comes from the page itself: its Origin is
// the address it was asked at. A browser sends Sec-Fetch-Site only over
// https and to localhost; it sends Origin on every POST.
func adminSameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") == "same-origin"
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	hosts := []string{r.Host, r.Header.Get("X-Forwarded-Host")}
	if config, ok := loadAdminConfig(); ok && config.Host != "" {
		hosts = append(hosts, config.Host)
	}
	for _, host := range hosts {
		if host != "" && (strings.EqualFold(parsed.Host, host) || strings.EqualFold(parsed.Hostname(), host)) {
			return true
		}
	}
	return false
}

// adminSecure says the browser reached the page over https: its cookie
// is then a Secure one.
func adminSecure(r *http.Request) bool {
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		return true
	}
	config, _ := loadAdminConfig()
	return config.HTTPS && config.Host != "" && strings.EqualFold(strings.Split(r.Host, ":")[0], config.Host)
}

// ---------------------------------------------------------------------------
// the routes

func (s *adminServer) routes() http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(value)
	}
	failure := func(w http.ResponseWriter, status int, err error) {
		reply(w, status, map[string]string{"error": err.Error()})
	}
	decode := func(w http.ResponseWriter, r *http.Request, into any) bool {
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(into); err != nil {
			failure(w, http.StatusBadRequest, err)
			return false
		}
		return true
	}
	asset := func(path, kind string, data []byte) {
		mux.HandleFunc("GET /"+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", kind)
			w.Header().Set("Cache-Control", "no-cache")
			w.Write(data)
		})
	}
	asset("admin.js", "text/javascript; charset=utf-8", adminPageScript)
	asset("admin.css", "text/css; charset=utf-8", adminPageStyle)
	asset("dev.css", "text/css; charset=utf-8", appDevPageStyle)
	asset("xterm.js", "text/javascript; charset=utf-8", appXtermScript)
	asset("xterm.css", "text/css; charset=utf-8", appXtermStyle)
	asset("addon-fit.js", "text/javascript; charset=utf-8", appXtermFit)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]string{"version": version})
	})
	mux.HandleFunc("GET /{$}", s.page)

	// A change: signed in, from the page, as JSON.
	change := func(w http.ResponseWriter, r *http.Request) bool {
		if !adminSameOrigin(r) || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			failure(w, http.StatusForbidden, errors.New("changes come from the admin page itself"))
			return false
		}
		return true
	}
	signedIn := func(handle func(http.ResponseWriter, *http.Request, adminPerson)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			admin, ok := s.admin(r)
			if !ok {
				reply(w, http.StatusUnauthorized, map[string]any{"error": "sign in with your link", "signin": true})
				return
			}
			if r.Method != http.MethodGet && !change(w, r) {
				return
			}
			if r.Method != http.MethodGet {
				if err := adminMay(admin, r.URL.Path); err != nil {
					failure(w, http.StatusForbidden, err)
					return
				}
			}
			handle(w, r, admin)
		}
	}

	mux.HandleFunc("POST /api/new-link", func(w http.ResponseWriter, r *http.Request) {
		if !change(w, r) {
			return
		}
		who := ""
		if cookie, err := r.Cookie(adminWhoCookie); err == nil {
			who, _ = url.QueryUnescape(cookie.Value)
		}
		go s.sendNewLink(who)
		reply(w, http.StatusOK, map[string]string{"text": "It's in your Slack DMs in a moment."})
	})
	mux.HandleFunc("POST /api/signout", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		if cookie, err := r.Cookie(adminCookie); err == nil {
			adminEndSession(cookie.Value)
		}
		http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: adminSecure(r)})
		reply(w, http.StatusOK, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("GET /api/state", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		reply(w, http.StatusOK, s.state(admin))
	}))
	mux.HandleFunc("GET /api/app", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		detail, err := adminAppDetailOf(r.URL.Query().Get("name"), admin)
		if err != nil {
			// An app moved to another machine: where, and how it went.
			if moved, ok := loadAppMovedRecord(r.URL.Query().Get("name")); ok && !fileExists(filepath.Join(appsDir(), moved.App)) {
				reply(w, http.StatusOK, map[string]any{"name": moved.App, "moved": moved})
				return
			}
			failure(w, http.StatusNotFound, err)
			return
		}
		reply(w, http.StatusOK, detail)
	}))
	mux.HandleFunc("GET /api/needs", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		reply(w, http.StatusOK, adminNeedsOf(r.URL.Query().Get("name")))
	}))
	mux.HandleFunc("POST /api/needs", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		var request struct {
			IDs      []string `json:"ids"`
			Password string   `json:"password"`
		}
		if !decode(w, r, &request) {
			return
		}
		job, err := s.installJob(request.IDs, request.Password, admin)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, job)
	}))
	mux.HandleFunc("GET /api/app/terminal", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		view, err := adminTermViewOf(r.URL.Query().Get("name"), admin)
		if err != nil {
			failure(w, http.StatusNotFound, err)
			return
		}
		reply(w, http.StatusOK, view)
	}))
	mux.HandleFunc("GET /api/app/term", signedIn(s.terminal))
	mux.HandleFunc("POST /api/action", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		var request struct {
			App    string `json:"app"`
			Action string `json:"action"`
			Why    string `json:"why"`
			Text   string `json:"text"`
			// Machine is where Move… moves the app.
			Machine string `json:"machine"`
		}
		if !decode(w, r, &request) {
			return
		}
		if request.Action == "move" {
			request.Text = request.Machine
		}
		job, err := adminActionJob(request.App, request.Action, request.Why, request.Text, admin)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		if job, err = s.startJob(job); err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		reply(w, http.StatusOK, job)
	}))
	mux.HandleFunc("POST /api/devpage", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		var request struct {
			App string `json:"app"`
		}
		if !decode(w, r, &request) {
			return
		}
		answer, err := adminOpenDevPage(request.App, admin)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, answer)
	}))
	mux.HandleFunc("POST /api/owner", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		var request struct {
			App string `json:"app"`
		}
		if !decode(w, r, &request) {
			return
		}
		if _, err := loadTeam(request.App); err != nil || !appNamePattern.MatchString(request.App) {
			failure(w, http.StatusNotFound, fmt.Errorf("no app %q on this machine", request.App))
			return
		}
		if admin.Slack == "" {
			failure(w, http.StatusBadRequest, errors.New("the admin page has no Slack member ID of yours, so the app couldn't tell it's you; add yours in Machine, then try again"))
			return
		}
		job, err := s.startJob(adminJob{Title: "Make " + admin.Name + " an owner of " + request.App, App: request.App, By: admin.Name, Main: true,
			Args: []string{"app", "owners", request.App, "add", admin.Slack}, Said: "made themselves an owner of the app", Tried: "become an owner of the app"})
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		reply(w, http.StatusOK, job)
	}))
	mux.HandleFunc("GET /api/machines", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		view, err := adminMachines()
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		reply(w, http.StatusOK, view)
	}))
	mux.HandleFunc("POST /api/machines", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		var request struct {
			Action string `json:"action"` // to, forget, take, or untake
			Name   string `json:"name"`
			Key    string `json:"key"`
		}
		if !decode(w, r, &request) {
			return
		}
		answer, err := adminChangeMachines(admin, request.Action, request.Name, request.Key)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, answer)
	}))
	mux.HandleFunc("POST /api/move/check", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		var request struct {
			App     string `json:"app"`
			Machine string `json:"machine"`
		}
		if !decode(w, r, &request) {
			return
		}
		view, err := adminMoveCheck(request.App, request.Machine)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, view)
	}))
	mux.HandleFunc("POST /api/update", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		job, err := s.startJob(adminJob{Title: "Update hi", By: admin.Name, Args: []string{"update", "--restart"}})
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		reply(w, http.StatusOK, job)
	}))
	mux.HandleFunc("POST /api/admins", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		var request struct {
			Action string `json:"action"` // add, remove, link, or slack
			Name   string `json:"name"`
			Slack  string `json:"slack"`
			Email  string `json:"email"`
			Send   bool   `json:"send"` // the link by Slack DM, not shown
		}
		if !decode(w, r, &request) {
			return
		}
		answer, err := s.changeAdmins(admin, request.Action, request.Name, request.Slack, request.Email, request.Send)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, answer)
	}))
	mux.HandleFunc("POST /api/netbird", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		var request struct {
			Key string `json:"key"`
		}
		if !decode(w, r, &request) {
			return
		}
		config, _ := loadAdminConfig()
		if config.Reach != adminReachPeer {
			failure(w, http.StatusBadRequest, errors.New("this machine has no podman, so the admin page has no peer of its own; it is on the machine's NetBird address"))
			return
		}
		if adminPeerJoined() {
			failure(w, http.StatusBadRequest, errors.New("the admin page's peer has joined NetBird already"))
			return
		}
		if err := adminSaveSetupKeyText(request.Key); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		adminRecord(admin.Name, "", "gave the admin page's NetBird peer its setup key")
		s.wakeReach()
		reply(w, http.StatusOK, map[string]string{"text": "The peer " + adminPeerHostname() + " joins NetBird in a minute. The page then moves to " + adminPeerHostname() + ": open it from your link again, or make a new one in Machine."})
	}))
	mux.HandleFunc("GET /api/jobs", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		reply(w, http.StatusOK, adminJobs())
	}))
	mux.HandleFunc("GET /api/job", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		id := r.URL.Query().Get("id")
		job, err := loadAdminJob(id)
		if err != nil {
			failure(w, http.StatusNotFound, err)
			return
		}
		from, _ := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
		text, next := adminJobOutput(id, from)
		reply(w, http.StatusOK, map[string]any{"job": job, "text": text, "next": next})
	}))
	mux.HandleFunc("GET /api/logs", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		lines, cursor, err := adminLogs(r.URL.Query().Get("app"), r.URL.Query().Get("cursor"))
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]any{"lines": lines, "cursor": cursor})
	}))
	mux.HandleFunc("GET /api/activity", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		reply(w, http.StatusOK, adminActivitiesWithOrg(300))
	}))
	mux.HandleFunc("GET /api/incidents", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		reply(w, http.StatusOK, adminIncidents())
	}))
	s.orgRoutes(mux, reply, decode, signedIn)
	s.newRoutes(mux, reply, decode, signedIn)
	s.settingsRoutes(mux, reply, decode, signedIn)
	s.networkRoutes(mux, reply, decode, signedIn)
	return adminHeaders(mux)
}

// adminHeaders are every answer's: no frames, no sniffing, no referrer.
func adminHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// page is the admin page, and a link's sign-in: its key becomes a
// sign-in of its own, in a cookie, and the browser goes on without it.
func (s *adminServer) page(w http.ResponseWriter, r *http.Request) {
	if key := r.URL.Query().Get(adminKeyParam); key != "" {
		w.Header().Set("Cache-Control", "no-store")
		session, admin, err := adminSignIn(key)
		if err != nil {
			http.Redirect(w, r, "/?ended=1", http.StatusSeeOther)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: session, Path: "/", MaxAge: int(adminSessionLasts / time.Second),
			HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: adminSecure(r)})
		adminRememberWho(w, r, admin.Name)
		adminRecord(admin.Name, "", "signed in")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if admin, ok := s.admin(r); ok {
		adminRememberWho(w, r, admin.Name)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; form-action 'self'; base-uri 'none'")
	w.Write(adminPageHTML)
}

// adminRememberWho keeps, for a year, which admin this browser signs in
// as: Send me a new link sends theirs. It holds a name, no key: changed
// by hand, it sends another admin their own link, and no more.
func adminRememberWho(w http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(w, &http.Cookie{Name: adminWhoCookie, Value: url.QueryEscape(name), Path: "/", MaxAge: 365 * 24 * 3600,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: adminSecure(r)})
}

// sendNewLink is Send me a new link: a new link, by Slack DM, to the
// admin this browser last signed in as, else to the machine's owner;
// once every two minutes at most, and twenty an hour for everyone.
func (s *adminServer) sendNewLink(who string) {
	config, ok := loadAdminConfig()
	if !ok {
		return
	}
	admin, ok := config.admin(who)
	if !ok {
		admin, ok = config.admin(config.machineOwner())
	}
	if !ok || admin.Slack == "" {
		return
	}
	s.mu.Lock()
	now := time.Now()
	s.askedAll = slices.DeleteFunc(s.askedAll, func(at time.Time) bool { return now.Sub(at) > time.Hour })
	if now.Sub(s.asked[admin.Name]) < 2*time.Minute || len(s.askedAll) >= 20 {
		s.mu.Unlock()
		return
	}
	s.asked[admin.Name] = now
	s.askedAll = append(s.askedAll, now)
	s.mu.Unlock()
	key, err := adminMakeLink(admin.Name, false)
	if err != nil {
		s.logf("a new link for %s: %v", admin.Name, err)
		return
	}
	if err := adminSendDM(admin.Slack, adminLinkText(config, admin, key)); err != nil {
		s.logf("a new link for %s: %v", admin.Name, err)
		return
	}
	adminRecord(admin.Name, "", "asked for a new link, sent by Slack DM")
}

// adminMayActFor says why by may not see admin's link or set where it
// goes, or "": themselves, or the machine's owner for an admin with no
// role the owner lacks (the org's, its ownership).
func adminMayActFor(by, admin adminPerson) string {
	switch {
	case strings.EqualFold(by.Name, admin.Name):
		return ""
	case !by.MachineOwner && !(by.Owner && by.Machine):
		return "only the machine's owner sees another admin's link or sets their Slack member ID"
	case admin.Org && !by.Org, admin.Owner && !by.Owner:
		return admin.Name + " runs the org too, which you don't"
	}
	return ""
}

// changeAdmins is Machine's admins: add, remove, a new link, or an
// admin's Slack member ID.
func (s *adminServer) changeAdmins(by adminPerson, action, name, slackID, email string, send bool) (map[string]string, error) {
	who := by.Name + " on the admin page"
	link := func(admin adminPerson) (map[string]string, error) {
		config, _ := loadAdminConfig()
		key, err := adminMakeLink(admin.Name, true)
		if err != nil {
			return nil, err
		}
		if send {
			if admin.Slack == "" {
				return nil, fmt.Errorf("%s has no Slack member ID here; show the link instead", admin.Name)
			}
			if err := adminSendDM(admin.Slack, adminLinkText(config, admin, key)); err != nil {
				return nil, err
			}
			return map[string]string{"text": admin.Name + "'s link is in their Slack DMs."}, nil
		}
		return map[string]string{"link": adminLinkURL(config, key), "text": admin.Name + "'s link, for 14 days. It is shown only now."}, nil
	}
	config, _ := loadAdminConfig()
	merged := config.org != "" && !config.OrgApart
	if merged && (action == "add" || action == "remove") && !by.Owner {
		return nil, errors.New("on the org server's machine the admins are the org's, and its owner adds and removes them")
	}
	switch action {
	case "add":
		if merged {
			who = by.orgName() // the org's audit records the hi user name
		}
		admin, err := adminAdd(name, slackID, email, who)
		if err != nil {
			return nil, err
		}
		return link(admin)
	case "remove":
		if merged {
			who = by.orgName()
		}
		admin, err := adminRemove(name, who)
		if err != nil {
			return nil, err
		}
		return map[string]string{"text": admin.Name + " isn't an admin any more; their links and sign-ins have ended."}, nil
	case "link":
		config, _ := loadAdminConfig()
		admin, ok := config.admin(name)
		if !ok {
			return nil, fmt.Errorf("%s isn't an admin", name)
		}
		// Someone else's link signs in as them: it goes to their own Slack
		// DMs, or is shown only to the machine's owner, for an admin who
		// can't do more than the owner can.
		if !send {
			if why := adminMayActFor(by, admin); why != "" {
				return nil, errors.New(why + "; send it to their Slack DMs instead")
			}
		}
		answer, err := link(admin)
		if err == nil {
			adminRecord(by.Name, "", "made a new link for "+admin.Name)
		}
		return answer, err
	case "slack":
		if merged {
			return nil, errors.New("the org's admins have their Slack member IDs already: hi org admins")
		}
		slackID = strings.TrimSpace(strings.Trim(strings.TrimSpace(slackID), "<@>"))
		if slackID != "" && !slackIDPattern.MatchString(slackID) {
			return nil, fmt.Errorf("%q isn't a Slack member ID, such as U012AB3CD", slackID)
		}
		unlock := adminLock()
		config, _ := loadAdminConfig()
		admin, ok := config.admin(name)
		if !ok {
			unlock()
			return nil, fmt.Errorf("%s isn't an admin", name)
		}
		// Where someone else's new links go is the machine owner's to set.
		if why := adminMayActFor(by, admin); why != "" {
			unlock()
			return nil, errors.New(why)
		}
		if other, taken := config.admin(slackID); slackID != "" && taken && other.Name != admin.Name {
			unlock()
			return nil, fmt.Errorf("%s is %s's Slack member ID", slackID, other.Name)
		}
		for i := range config.Admins {
			if config.Admins[i].Name == admin.Name {
				config.Admins[i].Slack = slackID
			}
		}
		err := saveAdminConfig(config)
		unlock()
		if err != nil {
			return nil, err
		}
		adminRecord(by.Name, "", "set "+admin.Name+"'s Slack member ID")
		return map[string]string{"text": "Saved."}, nil
	}
	return nil, fmt.Errorf("no such change to the admins: %q", action)
}

// ---------------------------------------------------------------------------
// what the page shows

// adminAppView is an app as Apps and Home show it.
type adminAppView struct {
	Name       string         `json:"name"`
	State      string         `json:"state"` // up, down, frozen, paused, or what systemd says
	Purpose    string         `json:"purpose,omitempty"`
	Production int            `json:"production"`
	Incidents  int            `json:"incidents"`
	Addresses  []adminAddress `json:"addresses"`
	Joined     bool           `json:"joined"` // its peer is on NetBird, which every address goes through
	Error      string         `json:"error,omitempty"`
}

type adminAddress struct {
	What string `json:"what"`
	URL  string `json:"url"`
}

// adminAppState is an app's state as people say it: frozen or paused
// over up.
func adminAppState(name string) string {
	state := appState(name)
	stop := loadTeamWatchStop(name)
	switch {
	case stop.Frozen:
		return "frozen"
	case stop.Paused && state == "up":
		return "paused"
	}
	return state
}

func adminAppOf(name string) adminAppView {
	view := adminAppView{Name: name, Addresses: []adminAddress{}}
	config, err := loadTeam(name)
	if err != nil {
		view.State, view.Error = "broken", err.Error()
		return view
	}
	view.State, view.Purpose, view.Joined = adminAppState(name), config.Purpose, appPeerJoined(name)
	if versions, err := loadTeamVersions(name); err == nil {
		view.Production = versions.Production
	}
	for _, incident := range loadTeamIncidents(name) {
		if incident.Status != "closed" {
			view.Incidents++
		}
	}
	for _, slot := range []string{appSlotProduction, appSlotPreview} {
		if state, ok := loadTeamSlot(name, slot); ok && state.URL != "" {
			view.Addresses = append(view.Addresses, adminAddress{What: appAddressLabel(slot), URL: state.URL})
		}
	}
	return view
}

// adminApps are the machine's apps, by name.
func adminApps() []adminAppView {
	entries, _ := os.ReadDir(appsDir())
	apps := []adminAppView{}
	for _, entry := range entries {
		if appNamePattern.MatchString(entry.Name()) {
			apps = append(apps, adminAppOf(entry.Name()))
		}
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })
	return apps
}

// adminStateView is what the page reads every few seconds.
type adminStateView struct {
	Machine  string         `json:"machine"`
	Version  string         `json:"version"`
	Latest   string         `json:"latest,omitempty"` // a newer hi
	Me       adminPerson    `json:"me"`
	Address  string         `json:"address"`
	Reach    string         `json:"reach"`
	Peer     string         `json:"peer,omitempty"` // the peer's name
	Joined   bool           `json:"joined"`
	NeedsKey bool           `json:"needs_key"`
	Line     string         `json:"line"` // how it is reached
	Policy   string         `json:"policy,omitempty"`
	Apps     []adminAppView `json:"apps"`
	Admins   []adminPerson  `json:"admins"`
	Running  int            `json:"running"` // jobs
	Failed   int            `json:"failed"`  // jobs that failed in the last day
	Lingers  bool           `json:"lingers"`
	User     string         `json:"user"`
	// Org is the org server, on its machine.
	Org *adminOrgSummary `json:"org,omitempty"`
	// OrgApart says the machine's admins and the org's are two lists.
	OrgApart bool `json:"org_apart,omitempty"`
	// Usage is what the machine and each app use.
	Usage *adminLoad `json:"usage,omitempty"`
	// Moved are the apps this machine moved to another (hi app move).
	Moved []appMoved `json:"moved"`
	// MoveName is this machine as others reach it, for moving apps.
	MoveName string `json:"move_name"`
}

func (s *adminServer) state(admin adminPerson) adminStateView {
	config, _ := loadAdminConfig()
	view := adminStateView{Machine: adminMachineName(), Version: version, Latest: s.latestVersion(), Me: admin, Address: adminURL(config),
		Reach: config.Reach, Line: adminReachLine(config), Apps: adminApps(), Admins: config.Admins, Lingers: s.lingering(), User: currentUserName(),
		Org: adminOrgSummaryOf(), Usage: s.usage(), OrgApart: config.apart(), Moved: appsMovedAway(), MoveName: appMoveThisMachine()}
	if config.Reach == adminReachPeer {
		view.Peer, view.Joined = adminPeerHostname(), adminPeerJoined()
		view.NeedsKey = !view.Joined && !fileExists(adminSetupKeyPath())
	}
	if config.Reach != adminReachLocal {
		view.Policy = fmt.Sprintf("A NetBird policy from the admins' group to %s, TCP %d", adminPolicyTarget(config), adminPort)
		if adminHasDomain(config) {
			view.Policy += " and 443"
		}
	}
	for _, job := range adminJobs() {
		switch {
		case job.State == "running":
			view.Running++
		case job.State == "failed" && time.Since(job.Ended) < 24*time.Hour:
			view.Failed++
		}
	}
	return view
}

// adminAppDetail is an app's Overview.
type adminAppDetail struct {
	adminAppView
	Channel    string             `json:"channel"`
	OpsChannel string             `json:"ops_channel,omitempty"`
	Stopped    *appWatchStopState `json:"stopped,omitempty"`
	Members    []adminMember      `json:"members"`
	Dev        string             `json:"dev,omitempty"`
	Releases   []adminRelease     `json:"releases"`
	Snapshot   string             `json:"snapshot,omitempty"`
	Heartbeat  string             `json:"heartbeat,omitempty"`
	DevPage    string             `json:"dev_page,omitempty"` // the dev page's address, without a key
	Level      string             `json:"level"`              // the admin's, in the app
	Owners     []string           `json:"owners"`
}

type adminMember struct {
	Role  string `json:"role"`
	Label string `json:"label"`
}

type adminRelease struct {
	Version  int       `json:"version"`
	Notes    string    `json:"notes,omitempty"`
	Released time.Time `json:"released"`
}

func adminAppDetailOf(name string, admin adminPerson) (adminAppDetail, error) {
	if !appNamePattern.MatchString(name) {
		return adminAppDetail{}, fmt.Errorf("no app %q on this machine", name)
	}
	config, err := loadTeam(name)
	if err != nil {
		return adminAppDetail{}, fmt.Errorf("no app %q on this machine", name)
	}
	detail := adminAppDetail{adminAppView: adminAppOf(name), Channel: config.Channel, OpsChannel: config.OpsChannel,
		Members: []adminMember{}, Releases: []adminRelease{}, Snapshot: appSnapshotLine(name), Heartbeat: appHeartbeatLine(config), Level: appLevelNone}
	if stop := loadTeamWatchStop(name); stop.Frozen || stop.Paused {
		detail.Stopped = &appWatchStopState{Frozen: stop.Frozen, Paused: stop.Paused, By: stop.By, Because: stop.Because, At: stop.At}
	}
	for _, role := range appRoles(config) {
		detail.Members = append(detail.Members, adminMember{Role: role, Label: appMemberLabel(config.Members[role])})
	}
	if view, err := appVersionsReport(name); err == nil {
		detail.Dev = view.Dev
		for i := len(view.Releases) - 1; i >= 0 && len(detail.Releases) < 5; i-- {
			release := view.Releases[i]
			detail.Releases = append(detail.Releases, adminRelease{Version: release.Version, Notes: release.Notes, Released: release.Released})
		}
	}
	if page, ok := loadTeamDevPage(name); ok && page.URL != "" {
		if parsed, err := url.Parse(page.URL); err == nil {
			parsed.RawQuery = ""
			detail.DevPage = parsed.String()
		}
	}
	if admin.Slack != "" {
		detail.Level = appLevel(config, admin.Slack)
	}
	names := appPeopleNames(config, config.Owners)
	for _, owner := range config.Owners {
		detail.Owners = append(detail.Owners, firstNonEmpty(names[owner], owner))
	}
	return detail, nil
}

// adminActionJob is Overview's buttons, each the command it stands for.
func adminActionJob(app, action, why, text string, admin adminPerson) (adminJob, error) {
	if !appNamePattern.MatchString(app) {
		return adminJob{}, fmt.Errorf("no app %q on this machine", app)
	}
	if _, err := loadTeam(app); err != nil {
		return adminJob{}, fmt.Errorf("no app %q on this machine", app)
	}
	job := adminJob{App: app, By: admin.Name}
	why = strings.TrimSpace(why)
	switch action {
	case "start":
		job.Title, job.Args, job.Said, job.Tried = "Start "+app, []string{"app", "up", app}, "started the app", "start the app"
	case "restart":
		job.Title, job.Args, job.Said, job.Tried = "Restart "+app, []string{"app", "up", app}, "restarted the app", "restart the app"
	case "stop":
		job.Title, job.Args, job.Said, job.Tried = "Stop "+app, []string{"app", "down", app}, "stopped the app", "stop the app"
	case "rebuild":
		job.Title, job.Args, job.Said, job.Tried = "Rebuild "+app, []string{"app", "rebuild", app}, "rebuilt the app on Ubuntu's current packages", "rebuild the app"
	case "snapshot":
		job.Title, job.Args, job.Said, job.Tried = "Snapshot of "+app, []string{"app", "snapshot", app}, "took a snapshot of the app's data", "take a snapshot of the app's data"
	case "freeze", "pause":
		if why == "" {
			return job, fmt.Errorf("say why you %s it: the app's ops channel is told", action)
		}
		// The app's broker says it in the ops channel itself.
		job.Title, job.Args = appCapitalize(action)+" "+app, []string{"app", action, app, "--why", why}
	case "unfreeze", "unpause":
		job.Title, job.Args = appCapitalize(action)+" "+app, []string{"app", action, app}
	case "move":
		target, err := newAppMoveTarget(strings.TrimSpace(text))
		if err != nil {
			return job, err
		}
		job.Title, job.Args = "Move "+app+" to "+target.machine, []string{"app", "move", app, target.machine, "--yes"}
		job.Said, job.Tried = "moved the app to "+target.machine, "move the app to "+target.machine
	case "say":
		text = strings.TrimSpace(text)
		if text == "" {
			return job, errors.New("say what to tell the lead")
		}
		job.Title, job.Args = "Tell "+app+"'s lead", []string{"app", "say", app, text}
	default:
		return job, fmt.Errorf("no such action: %q", action)
	}
	return job, nil
}

// adminOpenDevPage is Open ▾'s dev page: as the admin is in the app, with
// a link of their own from the app's service; or, without a level there,
// what the page offers instead.
func adminOpenDevPage(app string, admin adminPerson) (map[string]any, error) {
	if !appNamePattern.MatchString(app) {
		return nil, fmt.Errorf("no app %q on this machine", app)
	}
	config, err := loadTeam(app)
	if err != nil {
		return nil, fmt.Errorf("no app %q on this machine", app)
	}
	if admin.Slack == "" {
		return map[string]any{"builds": false, "why": "The admin page has no Slack member ID of yours, so it can't tell who you are in " + app + ". Add yours in Machine."}, nil
	}
	if !appBuilds(config, admin.Slack) {
		return map[string]any{"builds": false, "level": appLevel(config, admin.Slack),
			"why": "The dev page is for " + app + "'s owners and developers, and you're neither."}, nil
	}
	var answer struct {
		Link string `json:"link"`
	}
	asked, err := appHostCall(app, "/devlink", map[string]string{"user": admin.Slack, "by": admin.Name + " on the admin page"}, &answer)
	if !asked {
		return nil, fmt.Errorf("app %s isn't up, or runs a hi from before the admin page; its dev page is there while it runs", app)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"builds": true, "link": answer.Link}, nil
}

// ---------------------------------------------------------------------------
// logs and incidents

type adminLogLine struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// adminJournal runs journalctl; tests replace it.
var adminJournal = func(args ...string) ([]byte, error) {
	return exec.Command("journalctl", args...).Output()
}

// adminLogs are an app's lead and service, as hi app logs shows them, or
// the admin page's own, after a cursor.
// adminOrgLogs is the org server's logs, in Logs.
const adminOrgLogs = ":org"

func adminLogs(app, cursor string) ([]adminLogLine, string, error) {
	args := []string{"--user", "-u", adminUnit}
	if app == adminOrgLogs {
		if adminOrgDir() == "" {
			return nil, cursor, errors.New("this machine runs no org server")
		}
		args = []string{"--user", "-u", orgUnit}
		if adminOrgService() == "system" {
			args = []string{"-u", systemOrgUnit()}
		}
	} else if app != "" {
		if !appNamePattern.MatchString(app) {
			return nil, cursor, fmt.Errorf("no app %q on this machine", app)
		}
		if _, err := loadTeam(app); err != nil {
			return nil, cursor, fmt.Errorf("no app %q on this machine", app)
		}
		args = []string{"--user", "-u", appUnitName(app), "-t", "hi"}
	}
	args = append(args, "--no-pager", "-o", "json")
	if cursor != "" {
		args = append(args, "--after-cursor", cursor, "-n", "1000")
	} else {
		args = append(args, "-n", "300")
	}
	out, err := adminJournal(args...)
	if err != nil && len(out) == 0 {
		return []adminLogLine{}, cursor, nil
	}
	lines := []adminLogLine{}
	for _, line := range strings.Split(string(out), "\n") {
		var entry struct {
			Cursor  string          `json:"__CURSOR"`
			At      string          `json:"__REALTIME_TIMESTAMP"`
			Message json.RawMessage `json:"MESSAGE"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil || entry.Cursor == "" {
			continue
		}
		cursor = entry.Cursor
		var text string
		if json.Unmarshal(entry.Message, &text) != nil {
			var raw []byte
			var numbers []int
			if json.Unmarshal(entry.Message, &numbers) == nil {
				for _, n := range numbers {
					raw = append(raw, byte(n))
				}
			}
			text = string(raw)
		}
		micros, _ := strconv.ParseInt(entry.At, 10, 64)
		lines = append(lines, adminLogLine{At: time.UnixMicro(micros).UTC(), Text: stripTerminalCodes(text)})
	}
	return lines, cursor, nil
}

// adminIncident is an incident as Incidents lists it.
type adminIncident struct {
	App    string    `json:"app"`
	ID     string    `json:"id"`
	Status string    `json:"status"`
	Title  string    `json:"title"`
	Where  string    `json:"where"`
	Opened time.Time `json:"opened"`
	Last   string    `json:"last,omitempty"` // what was done last
}

// adminIncidents are every app's, and the org's (app ":org"): the open
// ones first, then the last month's.
func adminIncidents() []adminIncident {
	now := time.Now()
	list := []adminIncident{}
	for _, app := range adminApps() {
		for _, incident := range appIncidentsReport(app.Name, now) {
			view := adminIncident{App: app.Name, ID: incident.ID, Status: incident.Status, Title: incident.Title, Where: appIncidentWhere(incident), Opened: incident.Opened}
			if n := len(incident.Events); n > 0 {
				event := incident.Events[n-1]
				view.Last = appIncidentWho(event.Who) + ": " + firstNonEmpty(event.Result, event.Because, appIncidentActionText(event))
			}
			list = append(list, view)
		}
	}
	list = append(list, adminOrgIncidents(now)...)
	sort.SliceStable(list, func(i, j int) bool {
		openI, openJ := list[i].Status != "closed", list[j].Status != "closed"
		if openI != openJ {
			return openI
		}
		return list[i].Opened.After(list[j].Opened)
	})
	if len(list) > 100 {
		list = list[:100]
	}
	return list
}
