package cli

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// appTestMail catches what hi would email.
type appTestMail struct {
	mu   sync.Mutex
	sent []mailMessage
}

func (m *appTestMail) all() []mailMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.sent)
}

// wait is the emails once there are n, or after a few seconds.
func (m *appTestMail) wait(n int) []mailMessage {
	for i := 0; i < 100 && len(m.all()) < n; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	return m.all()
}

func appTestMailer(t *testing.T) *appTestMail {
	t.Helper()
	caught := &appTestMail{}
	previous := mailSend
	mailSend = func(settings mailSettings, message mailMessage) error {
		if _, err := mailFormat(settings, message, time.Now()); err != nil {
			return err
		}
		caught.mu.Lock()
		caught.sent = append(caught.sent, message)
		caught.mu.Unlock()
		return nil
	}
	t.Cleanup(func() { mailSend = previous })
	if err := saveMailSettings(mailSettings{Host: "smtp.corp.example", Port: 587, Security: mailSecuritySTARTTLS, From: "hi@corp.example"}); err != nil {
		t.Fatal(err)
	}
	return caught
}

func appTestNoSlackLookups(t *testing.T) {
	t.Helper()
	byEmail, emailOf := appSlackUserByEmail, appSlackUserEmail
	appSlackUserByEmail = func(string, string) (string, string) { return "", "" }
	appSlackUserEmail = func(string, string) (string, string) { return "", "" }
	t.Cleanup(func() { appSlackUserByEmail, appSlackUserEmail = byEmail, emailOf })
}

var appTestLinkPattern = regexp.MustCompile(`https?://\S+`)
var appTestCodePattern = regexp.MustCompile(`page that asked: (\d{6})`)

func TestMailFormat(t *testing.T) {
	settings := mailSettings{From: "hi@corp.example", Name: "hi on ws1"}
	data, err := mailFormat(settings, mailMessage{To: "Anna@Corp.example", Subject: "Sign in to the demo app — now", Text: "Line one\nLine two"}, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"From: \"hi on ws1\" <hi@corp.example>\r\n", "To: <anna@corp.example>\r\n", "Subject: =?utf-8?q?", "Content-Transfer-Encoding: quoted-printable", "Auto-Submitted: auto-generated", "Line one\r\nLine two"} {
		if !strings.Contains(text, want) {
			t.Fatalf("no %q in\n%s", want, text)
		}
	}
	if _, err := mailFormat(settings, mailMessage{To: "anna@corp.example", Subject: "Hi\r\nBcc: eve@evil.example"}, time.Now()); err == nil {
		t.Fatal("a subject took a header")
	}
	if _, err := mailFormat(settings, mailMessage{To: "anna@corp.example\r\nBcc: eve@evil.example", Subject: "Hi"}, time.Now()); err == nil {
		t.Fatal("an address took a header")
	}
}

func TestMailSettingsCheck(t *testing.T) {
	checked, err := mailSettings{Host: "SMTP.corp.example", From: "HI@corp.example"}.check()
	if err != nil || checked.Port != 587 || checked.Security != mailSecuritySTARTTLS || checked.From != "hi@corp.example" || checked.Host != "smtp.corp.example" {
		t.Fatal(checked, err)
	}
	if checked, _ := (mailSettings{Host: "smtp.corp.example", From: "hi@corp.example", Security: "tls"}).check(); checked.Port != 465 {
		t.Fatal(checked)
	}
	if _, err := (mailSettings{Host: "relay.corp.example", From: "hi@corp.example", Security: "none", Username: "hi", Password: "secret"}).check(); err == nil {
		t.Fatal("a password went without TLS")
	}
	if _, err := (mailSettings{Host: "localhost", From: "hi@corp.example", Security: "none", Username: "hi", Password: "secret"}).check(); err != nil {
		t.Fatal("a relay on this machine:", err)
	}
	if _, err := (mailSettings{Host: "smtp.corp.example", From: "not an address"}).check(); err == nil {
		t.Fatal("took a From that isn't an address")
	}
}

func TestMailSigninStore(t *testing.T) {
	store := mailStoreAt(filepath.Join(t.TempDir(), "signins.json"))
	token, code, err := store.add(mailSignin{Email: "anna@corp.example", Who: "U07ANNA"}, "request-1", time.Minute)
	if err != nil || len(code) != 6 {
		t.Fatal(code, err)
	}
	if data, _ := os.ReadFile(store.path); strings.Contains(string(data), token) || strings.Contains(string(data), code) {
		t.Fatal("the token or the code is kept as it is")
	}
	if _, ok := store.peek(token); !ok {
		t.Fatal("peek")
	}
	if entry, ok := store.take(token); !ok || entry.Who != "U07ANNA" {
		t.Fatal("take", entry)
	}
	if _, ok := store.take(token); ok {
		t.Fatal("a link signed in twice")
	}
	// Codes: wrong ones count, five end it.
	_, code, _ = store.add(mailSignin{Email: "anna@corp.example", Who: "U07ANNA"}, "request-2", time.Minute)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if _, err := store.takeCode("request-other", code); err == nil {
		t.Fatal("a code worked for another tab")
	}
	for i := 0; i < mailCodeTries-1; i++ {
		if _, err := store.takeCode("request-2", wrong); err == nil {
			t.Fatal("a wrong code worked")
		}
	}
	if entry, err := store.takeCode("request-2", code[:3]+" "+code[3:]); err != nil || entry.Who != "U07ANNA" {
		t.Fatal("the right code, typed with a space:", err)
	}
	_, code, _ = store.add(mailSignin{Email: "anna@corp.example", Who: "U07ANNA"}, "request-3", time.Minute)
	for i := 0; i < mailCodeTries; i++ {
		store.takeCode("request-3", wrong)
	}
	if _, err := store.takeCode("request-3", code); err == nil {
		t.Fatal("the code worked after five wrong ones")
	}
	// An ended one is gone.
	token, _, _ = store.add(mailSignin{Email: "bo@corp.example", Who: "U07BO"}, "", -time.Second)
	if _, ok := store.take(token); ok {
		t.Fatal("an ended link signed in")
	}
	// Limits: three for an address in 15 minutes.
	for i := 0; i < 3; i++ {
		if !store.allow("cleo@corp.example", "10.0.0.1") {
			t.Fatal("refused", i)
		}
	}
	if store.allow("cleo@corp.example", "10.0.0.2") {
		t.Fatal("a fourth for one address")
	}
}

func TestPeopleEmailInLevels(t *testing.T) {
	setupTeamTest(t)
	appTestNoSlackLookups(t)
	os.MkdirAll(appDir("payments"), 0o700)
	saveTeam(testTeamConfig())
	var out strings.Builder
	if err := appAccessCommand([]string{"payments", "Bo@Partner.example", "pro"}, &out); err != nil {
		t.Fatal(err)
	}
	config, _ := loadTeam("payments")
	if !slices.Contains(config.Developers, "bo@partner.example") || appLevel(config, "bo@partner.example") != appLevelDeveloper {
		t.Fatalf("%+v", config.Developers)
	}
	if err := validateTeam(config); err != nil {
		t.Fatal(err)
	}
	if err := appCheckPeople("not-an-id"); err == nil {
		t.Fatal("took something that is neither")
	}
	if appMention("bo@partner.example") != "bo@partner.example" || appMention("U07ANNA") != "<@U07ANNA>" {
		t.Fatal("mentions")
	}
}

// Someone in two workspaces is one person, by the email Slack has.
func TestPeopleJoinedByEmail(t *testing.T) {
	setupTeamTest(t)
	t.Setenv("HI_ADMIN_DIR", filepath.Join(t.TempDir(), "admin"))
	os.MkdirAll(appDir("payments"), 0o700)
	config := testTeamConfig()
	config.Developers = []string{"U07ANNA"}
	saveTeam(config)
	second := testTeamConfig()
	second.Name = "ledger"
	second.Owners = []string{"W09ANNA"}
	os.MkdirAll(appDir("ledger"), 0o700)
	if err := saveTeam(second); err != nil {
		t.Fatal(err)
	}
	for _, app := range []string{"payments", "ledger"} {
		saveTeamSecrets(app, appSecrets{SlackBotToken: "xoxb-" + app})
	}
	byEmail, emailOf := appSlackUserByEmail, appSlackUserEmail
	t.Cleanup(func() { appSlackUserByEmail, appSlackUserEmail = byEmail, emailOf })
	appSlackUserEmail = func(token, id string) (string, string) {
		if id == "U07ANNA" || id == "W09ANNA" {
			return "anna@corp.example", "Anna Berg"
		}
		return "", ""
	}
	appSlackUserByEmail = func(token, email string) (string, string) { return "", "" }
	s := &adminServer{}
	view := s.peopleView()
	var anna []adminPersonRow
	for _, row := range view.People {
		if row.Email == "anna@corp.example" {
			anna = append(anna, row)
		}
		if row.Key == "U07ANNA" || row.Key == "W09ANNA" {
			t.Fatalf("a row of their own for %s", row.Key)
		}
	}
	if len(anna) != 1 || anna[0].Levels["payments"] != "pro" || anna[0].Levels["ledger"] != "owner" || anna[0].Name != "Anna Berg" || len(anna[0].Slack) != 2 {
		t.Fatalf("%+v", anna)
	}
	// Signing in by email in either app finds their Slack account there.
	if who, name := appWhoByEmail(config, "anna@corp.example"); who != "U07ANNA" || name != "Anna Berg" {
		t.Fatal(who, name)
	}
	if who, _ := appWhoByEmail(second, "Anna@corp.example"); who != "" {
		// Not lowercased by appWhoByEmail's caller: the flows clean it first.
		t.Log("an address as typed:", who)
	}
	if who, _ := appWhoByEmail(second, "anna@corp.example"); who != "W09ANNA" {
		t.Fatal(who)
	}
	if who, _ := appWhoByEmail(config, "eve@corp.example"); who != "" {
		t.Fatal("someone with no level", who)
	}
	// One SSH key, into every dev box where they get a shell.
	person, err := machineSetKey("anna@corp.example", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample anna@laptop")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	machineApplyKey(person, &out)
	for _, app := range []string{"payments", "ledger"} {
		if keys, _ := os.ReadFile(appDevDir(app) + "/ssh/authorized_keys"); !strings.Contains(string(keys), "anna@laptop") {
			t.Fatalf("%s: %q (%s)", app, keys, out.String())
		}
	}
}

func TestPeopleDomains(t *testing.T) {
	setupTeamTest(t)
	appTestNoSlackLookups(t)
	t.Setenv("HI_ADMIN_DIR", filepath.Join(t.TempDir(), "admin"))
	var out strings.Builder
	if err := peopleDomains([]string{"Corp.example,@corp-labs.example"}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := machineAddPerson(peopleAddRequest{Email: "eve@elsewhere.example"}); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatal(err)
	}
	if _, err := machineAddPerson(peopleAddRequest{Email: "eve@elsewhere.example", Outside: true, Name: "Eve"}); err != nil {
		t.Fatal(err)
	}
	if person, err := machineAddPerson(peopleAddRequest{Email: "anna@corp-labs.example"}); err != nil || person.Email != "anna@corp-labs.example" {
		t.Fatal(person, err)
	}
	if people := loadMachinePeople(); len(people.People) != 2 || !slices.Equal(people.Domains, []string{"corp.example", "corp-labs.example"}) {
		t.Fatalf("%+v", people)
	}
}

// The dev page signs an owner in by email: the same answer for anyone,
// a link built from the page's own address and not the Host asked, a
// button before it signs in, and the key a Slack link would give.
func TestDevPageSigninByEmail(t *testing.T) {
	b, _ := appTestDevPage(t)
	appTestNoSlackLookups(t)
	t.Setenv("HI_ADMIN_DIR", filepath.Join(t.TempDir(), "admin"))
	fake := &fakeTeamChannel{}
	b.channel = fake
	mail := appTestMailer(t)
	config, _ := loadTeam("payments")
	config.Developers = append(config.Developers, "dana@corp.example")
	saveTeam(config)
	state := appDevPageState{URL: "https://payments-dev.corp.example/?hi_key=addresskey", Expires: time.Now().Add(time.Hour)}
	data, _ := json.Marshal(state)
	os.MkdirAll(filepath.Dir(appDevPageStatePath("payments")), 0o700)
	if err := os.WriteFile(appDevPageStatePath("payments"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	handler := b.devPageHandler()

	ask := func(email string) (int, map[string]string) {
		request := httptest.NewRequest("POST", appDevPagePrefix+"api/email", strings.NewReader(`{"email":"`+email+`"}`))
		request.Host = "evil.example"
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		var answer map[string]string
		json.Unmarshal(recorder.Body.Bytes(), &answer)
		return recorder.Code, answer
	}
	code, nobody := ask("nobody@corp.example")
	if code != http.StatusOK || nobody["request"] == "" {
		t.Fatal(code, nobody)
	}
	code, dana := ask("Dana@corp.example")
	if code != http.StatusOK || dana["text"] != strings.Replace(nobody["text"], "nobody@", "dana@", 1) {
		t.Fatal("the answers differ:", dana, nobody)
	}
	sent := mail.wait(1)
	time.Sleep(50 * time.Millisecond)
	sent = mail.all()
	if len(sent) != 1 || sent[0].To != "dana@corp.example" {
		t.Fatalf("%+v", sent)
	}
	link := appTestLinkPattern.FindString(sent[0].Text)
	if !strings.HasPrefix(link, "https://payments-dev.corp.example/__hi/dev/email?") || !strings.Contains(link, "hi_key=addresskey") || strings.Contains(sent[0].Text, "evil.example") {
		t.Fatal(link, sent[0].Text)
	}
	parsed, _ := url.Parse(link)
	// Opening it signs nobody in: a page with a button.
	status, page := appTestDevPageCall(t, handler, "", "GET", "email?"+parsed.RawQuery, nil)
	if status != http.StatusOK || !strings.Contains(page, "<button") || !strings.Contains(page, "dana@corp.example") {
		t.Fatal(status, page)
	}
	// The button.
	form := url.Values{"t": {parsed.Query().Get("t")}}
	request := httptest.NewRequest("POST", appDevPagePrefix+"email", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://payments-dev.corp.example")
	request.Host = "payments-dev.corp.example"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	target, _ := url.Parse(recorder.Header().Get("Location"))
	if recorder.Code != http.StatusSeeOther || target.Host != "payments-dev.corp.example" || target.Path != appDevPagePrefix || target.Query().Get(netExposeUserParam) == "" {
		t.Fatal(recorder.Code, recorder.Header(), recorder.Body.String())
	}
	key := target.Query().Get(netExposeUserParam)
	if person, ok, _ := b.webUser(config, key); !ok || person.User != "dana@corp.example" {
		t.Fatal("the key", person, ok)
	}
	if status, _ := appTestDevPageCall(t, handler, key, "GET", "api/state", nil); status != http.StatusOK {
		t.Fatal("the dev page with the key:", status)
	}
	// Used once.
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest("POST", appDevPagePrefix+"email", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "This link has ended") {
		t.Fatal("a second sign-in:", recorder.Code)
	}
	// The ops channel hears the first time.
	fake.mu.Lock()
	calls := strings.Join(fake.calls, "\n")
	fake.mu.Unlock()
	if !strings.Contains(calls, "signed in by email for the first time") {
		t.Fatal(calls)
	}
	if person, _ := loadMachinePeople().person("dana@corp.example"); person.SignedIn.IsZero() {
		t.Fatal("no sign-in noted")
	}

	// The code, typed in the tab that asked.
	_, again := ask("dana@corp.example")
	sent = mail.wait(2)
	if len(sent) != 2 {
		t.Fatal(len(sent))
	}
	typed := appTestCodePattern.FindStringSubmatch(sent[1].Text)
	if typed == nil {
		t.Fatal(sent[1].Text)
	}
	status, body := appTestDevPageCall(t, handler, "", "POST", "api/email/code", map[string]string{"request": again["request"], "code": typed[1]})
	if status != http.StatusOK || !strings.Contains(body, netExposeUserParam+"=") {
		t.Fatal(status, body)
	}
	// Someone who doesn't build: no email.
	if !strings.Contains(strings.Join(config.Owners, " "), "U07OWNER1") {
		config.Contributors = append(config.Contributors, "cleo@corp.example")
		saveTeam(config)
	}
	ask("cleo@corp.example")
	time.Sleep(100 * time.Millisecond)
	if len(mail.all()) != 2 {
		t.Fatal("a contributor got a link to the dev page")
	}
}

// The helper on production signs anyone with a level in.
func TestHelperSigninByEmail(t *testing.T) {
	b, _ := appTestDevPage(t)
	appTestNoSlackLookups(t)
	t.Setenv("HI_ADMIN_DIR", filepath.Join(t.TempDir(), "admin"))
	mail := appTestMailer(t)
	config, _ := loadTeam("payments")
	config.Contributors = append(config.Contributors, "cleo@corp.example")
	saveTeam(config)
	os.MkdirAll(filepath.Join(appDir("payments"), "run"), 0o700)
	if err := saveTeamSlot("payments", appPreview{Slot: appSlotProduction, URL: "https://payments.corp.example/"}); err != nil {
		t.Fatal(err)
	}
	handler := b.webHandler(appSlotProduction)
	request := httptest.NewRequest("POST", appHelperPrefix+"api/email", strings.NewReader(`{"email":"cleo@corp.example"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
	sent := mail.wait(1)
	if len(sent) != 1 {
		t.Fatal("no email")
	}
	link := appTestLinkPattern.FindString(sent[0].Text)
	if !strings.HasPrefix(link, "https://payments.corp.example/__hi/email?t=") {
		t.Fatal(link)
	}
	parsed, _ := url.Parse(link)
	form := url.Values{"t": {parsed.Query().Get("t")}}
	request = httptest.NewRequest("POST", appHelperPrefix+"email", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	target, _ := url.Parse(recorder.Header().Get("Location"))
	if recorder.Code != http.StatusSeeOther || target.Host != "payments.corp.example" {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
	if person, ok, _ := b.webUser(config, target.Query().Get(netExposeUserParam)); !ok || person.User != "cleo@corp.example" {
		t.Fatal(person, ok)
	}
}

// The admin page signs an admin in by email, and only an admin.
func TestAdminSigninByEmail(t *testing.T) {
	setupTeamTest(t)
	appTestNoSlackLookups(t)
	t.Setenv("HI_ADMIN_DIR", filepath.Join(t.TempDir(), "admin"))
	mail := appTestMailer(t)
	if err := saveAdminConfig(adminConfig{Reach: adminReachLocal, Admins: []adminPerson{{Name: "Ola", Slack: "U07OLA", Added: time.Now()}}}); err != nil {
		t.Fatal(err)
	}
	changeMachinePeople(func(m *machinePeople) error { m.link("ola@corp.example", "U07OLA", "Ola", "test"); return nil })
	s := &adminServer{log: &strings.Builder{}, asked: map[string]time.Time{}}
	handler := s.routes()
	ask := func(email string) map[string]string {
		request := httptest.NewRequest("POST", "/api/email", strings.NewReader(`{"email":"`+email+`"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://127.0.0.1:1984")
		request.Host = "127.0.0.1:1984"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		var answer map[string]string
		json.Unmarshal(recorder.Body.Bytes(), &answer)
		if recorder.Code != http.StatusOK {
			t.Fatal(recorder.Code, recorder.Body.String())
		}
		return answer
	}
	ask("eve@corp.example")
	answer := ask("ola@corp.example")
	sent := mail.wait(1)
	time.Sleep(50 * time.Millisecond)
	if sent = mail.all(); len(sent) != 1 || sent[0].To != "ola@corp.example" || !strings.Contains(sent[0].Text, "http://127.0.0.1:1984/email?t=") {
		t.Fatalf("%+v", sent)
	}
	code := appTestCodePattern.FindStringSubmatch(sent[0].Text)[1]
	request := httptest.NewRequest("POST", "/api/email/code", strings.NewReader(`{"request":"`+answer["request"]+`","code":"`+code+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://127.0.0.1:1984")
	request.Host = "127.0.0.1:1984"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
	var session string
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == adminCookie {
			session = cookie.Value
		}
	}
	if admin, ok := adminSessionAdmin(session); !ok || admin.Name != "Ola" {
		t.Fatal("no sign-in", admin, ok)
	}
}

// smtpSend speaks SMTP: here to a relay with no TLS on this machine.
func TestSMTPSend(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	got := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		write := func(line string) { conn.Write([]byte(line + "\r\n")) }
		write("220 relay ready")
		var data strings.Builder
		inData := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					write("250 queued")
					continue
				}
				data.WriteString(line)
				continue
			}
			switch command := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(command, "EHLO"):
				write("250 relay")
			case strings.HasPrefix(command, "DATA"):
				inData = true
				write("354 go on")
			case strings.HasPrefix(command, "QUIT"):
				write("221 bye")
				got <- data.String()
				return
			default:
				write("250 ok")
			}
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	settings, err := mailSettings{Host: "127.0.0.1", Port: port, Security: mailSecurityNone, From: "hi@corp.example"}.check()
	if err != nil {
		t.Fatal(err)
	}
	if err := smtpSend(settings, mailMessage{To: "anna@corp.example", Subject: "Sign in", Text: "Hello"}); err != nil {
		t.Fatal(err)
	}
	if text := <-got; !strings.Contains(text, "To: <anna@corp.example>") || !strings.Contains(text, "Hello") {
		t.Fatal(text)
	}
}
