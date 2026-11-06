package cli

// hi admin: one web page per machine for the people who run it, with
// every app on the machine, so they need the command line only for what
// a web page can't do (docs/specs/approved/hi_admin.md). This file is its
// state and its commands; admin_serve.go is the page's server,
// admin_reach.go how people reach it, admin_jobs.go the long actions.

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	adminPort = 1984
	adminUnit = "hi-admin.service"
	// adminLinkLasts is how long an admin's link signs in, from when it
	// is made; adminSessionLasts how long a sign-in lasts from its last use.
	adminLinkLasts    = 14 * 24 * time.Hour
	adminSessionLasts = 14 * 24 * time.Hour
	adminCookie       = "hi_admin"
	adminKeyParam     = "key"
)

// How people reach the admin page (admin_reach.go).
const (
	adminReachPeer    = "peer"    // a NetBird peer of its own
	adminReachNetBird = "netbird" // the machine's NetBird address
	adminReachLocal   = "local"   // 127.0.0.1 only
)

// adminDir holds the admin page's files: admin.json, keys.json,
// activity.jsonl, jobs/, and its peer's identity.
func adminDir() string {
	if dir := os.Getenv("HI_ADMIN_DIR"); dir != "" {
		return dir
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "hi", "admin")
}

func adminConfigPath() string { return filepath.Join(adminDir(), "admin.json") }
func adminKeysPath() string   { return filepath.Join(adminDir(), "keys.json") }
func adminSocketDir() string  { return filepath.Join(adminDir(), "sockets") }
func adminHTTPSocket() string { return filepath.Join(adminSocketDir(), "http.sock") }

// adminConfig is admin.json: how the page is reached, and its admins.
type adminConfig struct {
	Reach string `json:"reach"`
	// Host is a name the IT team keeps for the page (hi admin address),
	// and HTTPS says their proxy ends TLS for it.
	Host   string        `json:"host,omitempty"`
	HTTPS  bool          `json:"https,omitempty"`
	Admins []adminPerson `json:"admins"`
}

// adminPerson is one of the machine's admins on the page.
type adminPerson struct {
	Name  string    `json:"name"`
	Slack string    `json:"slack,omitempty"` // their Slack member ID
	Added time.Time `json:"added"`
	By    string    `json:"by,omitempty"`
}

func loadAdminConfig() (adminConfig, bool) {
	var config adminConfig
	data, err := os.ReadFile(adminConfigPath())
	if err != nil || json.Unmarshal(data, &config) != nil {
		return adminConfig{}, false
	}
	return config, true
}

func saveAdminConfig(config adminConfig) error {
	if err := os.MkdirAll(adminDir(), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(config, "", "  ")
	return writeFileAtomic(adminConfigPath(), append(data, '\n'), 0o600)
}

// admin finds an admin by name, any case, or by Slack member ID.
func (c adminConfig) admin(who string) (adminPerson, bool) {
	who = strings.TrimSpace(strings.Trim(strings.TrimSpace(who), "<@>"))
	for _, admin := range c.Admins {
		if strings.EqualFold(admin.Name, who) || admin.Slack != "" && admin.Slack == who {
			return admin, true
		}
	}
	return adminPerson{}, false
}

func adminNames(config adminConfig) string {
	var names []string
	for _, admin := range config.Admins {
		names = append(names, admin.Name)
	}
	return strings.Join(names, ", ")
}

// adminCheckName is an admin's name as people write it: a name, not a
// command.
func adminCheckName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || len(name) > 60 || strings.ContainsAny(name, "<>\"`\\") {
		return "", fmt.Errorf("an admin's name is a name, as people call them, up to 60 letters: %q", name)
	}
	return name, nil
}

// ---------------------------------------------------------------------------
// links and sign-ins

// adminKeys is keys.json: the links that sign in, and the sign-ins they
// made, each kept as its key's hash. A link is shown or sent once and
// never needed again, so hi keeps no key it could give away.
type adminKeys struct {
	Links    []adminKey `json:"links"`
	Sessions []adminKey `json:"sessions"`
}

type adminKey struct {
	Hash  string    `json:"hash"`
	Admin string    `json:"admin"`
	Made  time.Time `json:"made"`
	Used  time.Time `json:"used,omitzero"`
	Ends  time.Time `json:"ends"`
}

// adminMu keeps this process's changes to keys.json and admin.json in
// turn; adminLock keeps the commands' and the page's.
var adminMu sync.Mutex

// adminLock holds the admin page's lock until the returned function is
// called: hi admin on the command line and the page change the same files.
func adminLock() func() {
	adminMu.Lock()
	os.MkdirAll(adminDir(), 0o700)
	file, err := os.OpenFile(filepath.Join(adminDir(), ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return adminMu.Unlock
	}
	syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
	return func() {
		syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
		adminMu.Unlock()
	}
}

func loadAdminKeys() adminKeys {
	var keys adminKeys
	if data, err := os.ReadFile(adminKeysPath()); err == nil {
		json.Unmarshal(data, &keys)
	}
	return keys
}

func saveAdminKeys(keys adminKeys) error {
	now := time.Now()
	// What has ended goes.
	keys.Links = slices.DeleteFunc(keys.Links, func(k adminKey) bool { return now.After(k.Ends) })
	keys.Sessions = slices.DeleteFunc(keys.Sessions, func(k adminKey) bool { return now.After(k.Ends) })
	data, _ := json.MarshalIndent(keys, "", "  ")
	return writeFileAtomic(adminKeysPath(), append(data, '\n'), 0o600)
}

func adminNewKey() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func adminHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// adminFind is the key whose hash is key's, among keys that haven't
// ended.
func adminFind(list []adminKey, key string) int {
	if key == "" {
		return -1
	}
	hash := adminHash(key)
	now := time.Now()
	for i, candidate := range list {
		if subtle.ConstantTimeCompare([]byte(candidate.Hash), []byte(hash)) == 1 && now.Before(candidate.Ends) {
			return i
		}
	}
	return -1
}

// adminMakeLink makes a new link for an admin; with endOld, the links they
// had end (hi admin link: the old one ends). It returns the link's key.
func adminMakeLink(admin string, endOld bool) (string, error) {
	key, err := adminNewKey()
	if err != nil {
		return "", err
	}
	unlock := adminLock()
	defer unlock()
	keys := loadAdminKeys()
	if endOld {
		keys.Links = slices.DeleteFunc(keys.Links, func(k adminKey) bool { return k.Admin == admin })
	}
	now := time.Now().UTC()
	keys.Links = append(keys.Links, adminKey{Hash: adminHash(key), Admin: admin, Made: now, Ends: now.Add(adminLinkLasts)})
	return key, saveAdminKeys(keys)
}

// adminSignIn turns a link's key into a sign-in of its own, which lasts
// while it's used. The link keeps working until it ends, for another
// device.
func adminSignIn(linkKey string) (session string, admin adminPerson, err error) {
	config, ok := loadAdminConfig()
	if !ok {
		return "", admin, errors.New("the admin page isn't set up")
	}
	unlock := adminLock()
	defer unlock()
	keys := loadAdminKeys()
	i := adminFind(keys.Links, linkKey)
	if i < 0 {
		return "", admin, errors.New("that link has ended")
	}
	admin, ok = config.admin(keys.Links[i].Admin)
	if !ok {
		return "", admin, errors.New("that link's admin isn't an admin any more")
	}
	session, err = adminNewKey()
	if err != nil {
		return "", admin, err
	}
	now := time.Now().UTC()
	keys.Links[i].Used = now
	keys.Sessions = append(keys.Sessions, adminKey{Hash: adminHash(session), Admin: admin.Name, Made: now, Used: now, Ends: now.Add(adminSessionLasts)})
	return session, admin, saveAdminKeys(keys)
}

// adminSessionAdmin is the admin a sign-in belongs to. A sign-in used
// keeps going: its end moves on, once a day at most.
func adminSessionAdmin(session string) (adminPerson, bool) {
	config, ok := loadAdminConfig()
	if !ok || session == "" {
		return adminPerson{}, false
	}
	keys := loadAdminKeys()
	i := adminFind(keys.Sessions, session)
	if i < 0 {
		return adminPerson{}, false
	}
	admin, ok := config.admin(keys.Sessions[i].Admin)
	if !ok {
		return adminPerson{}, false
	}
	if now := time.Now(); now.Sub(keys.Sessions[i].Used) > 24*time.Hour {
		unlock := adminLock()
		keys = loadAdminKeys()
		if i = adminFind(keys.Sessions, session); i >= 0 {
			keys.Sessions[i].Used, keys.Sessions[i].Ends = now.UTC(), now.UTC().Add(adminSessionLasts)
			saveAdminKeys(keys)
		}
		unlock()
	}
	return admin, true
}

// adminEndSession signs one browser out.
func adminEndSession(session string) error {
	unlock := adminLock()
	defer unlock()
	keys := loadAdminKeys()
	if i := adminFind(keys.Sessions, session); i >= 0 {
		keys.Sessions = slices.Delete(keys.Sessions, i, i+1)
	}
	return saveAdminKeys(keys)
}

// adminEndAll ends an admin's links and sign-ins.
func adminEndAll(admin string) error {
	unlock := adminLock()
	defer unlock()
	keys := loadAdminKeys()
	gone := func(k adminKey) bool { return strings.EqualFold(k.Admin, admin) }
	keys.Links = slices.DeleteFunc(keys.Links, gone)
	keys.Sessions = slices.DeleteFunc(keys.Sessions, gone)
	return saveAdminKeys(keys)
}

// adminLinkURL is the link that signs an admin in.
func adminLinkURL(config adminConfig, key string) string {
	return adminURL(config) + "?" + adminKeyParam + "=" + key
}

// adminLinkText is the DM with an admin's link.
func adminLinkText(config adminConfig, admin adminPerson, key string) string {
	ends := time.Now().Add(adminLinkLasts)
	return fmt.Sprintf(":key: Your link to the admin page of *%s*, %s: <%s|open the admin page>\n"+
		"It works until <!date^%d^{date_short_pretty}|%s>. Keep it to yourself: whoever opens it can do what an admin of the machine does.",
		adminMachineName(), appSlackEscape(admin.Name), adminLinkURL(config, key), ends.Unix(), ends.Format("2 Jan"))
}

// adminMachineName is the machine's short name, as the page shows it.
func adminMachineName() string {
	name, _ := os.Hostname()
	name, _, _ = strings.Cut(strings.ToLower(name), ".")
	return firstNonEmpty(name, "this machine")
}

// ---------------------------------------------------------------------------
// what admins did

// adminActivity is one line of activity.jsonl.
type adminActivity struct {
	At   time.Time `json:"at"`
	Who  string    `json:"who"`
	What string    `json:"what"`
	App  string    `json:"app,omitempty"`
}

func adminActivityPath() string { return filepath.Join(adminDir(), "activity.jsonl") }

// adminRecord adds what an admin did to Activity.
func adminRecord(who, app, what string) {
	os.MkdirAll(adminDir(), 0o700)
	file, err := os.OpenFile(adminActivityPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	data, _ := json.Marshal(adminActivity{At: time.Now().UTC(), Who: who, What: what, App: app})
	file.Write(append(data, '\n'))
}

// adminActivities are the newest lines first, at most limit.
func adminActivities(limit int) []adminActivity {
	data, err := os.ReadFile(adminActivityPath())
	if err != nil {
		return []adminActivity{}
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	list := []adminActivity{}
	for i := len(lines) - 1; i >= 0 && len(list) < limit; i-- {
		var entry adminActivity
		if json.Unmarshal([]byte(lines[i]), &entry) == nil {
			list = append(list, entry)
		}
	}
	return list
}

// adminActor is who a change on the command line was made by.
func adminActor() string {
	return firstNonEmpty(os.Getenv("USER"), "an admin") + " on the machine"
}

// ---------------------------------------------------------------------------
// the commands

const adminUsage = `usage: hi admin                         whether the admin page runs, its address, its admins
       hi admin start                   start it and keep it up, also after a reboot; the first
                                        time, makes you its first admin and prints your link
           [--netbird <setup key file>] its NetBird peer's setup key, the first time
           [--local]                    on this machine only, 127.0.0.1:1984
           [--name <name>] [--slack <member ID>]
                                        the first admin, without being asked
       hi admin stop                    stop it; nothing else changes
       hi admin link [<admin>]          a new sign-in link, for 14 days; the old one ends
       hi admin admins [add <name> [--slack <member ID>] | remove <name>]
                                        the admins
       hi admin address [<host> [--https] | default]
                                        the name the IT team keeps for it; admin.<domain> by default`

func runAdmin(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return exitCode(adminStatus(stdout), stderr)
	}
	command, rest := args[0], args[1:]
	var err error
	switch command {
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, adminUsage)
		return 0
	case "status":
		err = adminStatus(stdout)
	case "start":
		err = adminStart(rest, stdin, stdout)
	case "stop":
		err = adminStop(stdout)
	case "link":
		err = adminLinkCommand(rest, stdout)
	case "admins", "admin":
		err = adminAdminsCommand(rest, stdout)
	case "address":
		err = adminAddressCommand(rest, stdout)
	case "serve":
		err = adminServe(stderr)
	case "__job":
		return adminJobMain(rest, stderr)
	default:
		fmt.Fprintf(stderr, "hi: unknown admin command %q\n\n%s\n", command, adminUsage)
		return 2
	}
	return exitCode(err, stderr)
}

// adminUnitPath is hi-admin.service, a user service as the apps' are.
func adminUnitPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", adminUnit)
}

func adminUnitText(executable string) string {
	env := ""
	for _, name := range []string{"HI_APPS_DIR", "HI_ADMIN_DIR"} {
		if value := os.Getenv(name); value != "" {
			env += "Environment=" + name + "=" + value + "\n"
		}
	}
	// The commands it runs find podman, git, and the agents as the shell
	// that ran hi admin start did.
	env += "Environment=PATH=" + os.Getenv("PATH") + "\n"
	return fmt.Sprintf(`[Unit]
Description=hi admin page
After=network-online.target
Wants=network-online.target

[Service]
%sExecStart=%s admin serve
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`, env, executable)
}

// adminExecutable is this hi, as the unit and the jobs run it.
func adminExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	return executable, nil
}

// adminDetectReach is how this machine's admin page is reached when hi
// admin start doesn't say: on a peer of its own where podman (or docker)
// runs, as apps' addresses are, or on the machine's NetBird address.
var adminDetectReach = func() (string, error) {
	if _, _, err := detectBoxEngine(); err == nil {
		return adminReachPeer, nil
	}
	if _, err := netbirdAddress(); err == nil {
		return adminReachNetBird, nil
	}
	return "", errors.New("this machine has neither podman nor NetBird, so nobody else could reach the admin page; hi admin start --local has it on this machine only, at http://127.0.0.1:1984/")
}

// adminAsk asks a question on a terminal, with a default.
func adminAsk(stdin io.Reader, stdout io.Writer, question, fallback string) string {
	if fallback != "" {
		fmt.Fprintf(stdout, "%s [%s]: ", question, fallback)
	} else {
		fmt.Fprintf(stdout, "%s: ", question)
	}
	answer, _ := readLine(stdin)
	return firstNonEmpty(strings.TrimSpace(answer), fallback)
}

func adminStart(args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("hi admin start", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	keyFile := flags.String("netbird", "", "the peer's setup key, in a file")
	local := flags.Bool("local", false, "on this machine only")
	name := flags.String("name", "", "the first admin's name")
	slackID := flags.String("slack", "", "the first admin's Slack member ID")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return usageError{adminUsage}
	}
	config, _ := loadAdminConfig()
	if *local {
		config.Reach = adminReachLocal
	} else {
		reach, err := adminDetectReach()
		if err != nil {
			return err
		}
		config.Reach = reach
	}
	if *keyFile != "" {
		if config.Reach != adminReachPeer {
			return errors.New("--netbird is for a machine with podman, where the admin page has a NetBird peer of its own; this one is reached on the machine's own NetBird address")
		}
		if err := adminSaveSetupKey(*keyFile); err != nil {
			return err
		}
	}
	first := len(config.Admins) == 0
	var made adminPerson
	if first {
		who := *name
		if who == "" && interactive(stdin, stdout) {
			who = adminAsk(stdin, stdout, "Your name, as the admin page shows it", currentUserName())
		}
		who, err := adminCheckName(firstNonEmpty(who, currentUserName()))
		if err != nil {
			return err
		}
		id := strings.TrimSpace(*slackID)
		if id == "" && *name == "" && interactive(stdin, stdout) {
			id = adminAsk(stdin, stdout, "Your Slack member ID, for new links by DM (optional; in Slack: your profile, ⋮, Copy member ID)", "")
		}
		if id != "" && !slackIDPattern.MatchString(id) {
			return fmt.Errorf("%q isn't a Slack member ID, such as U012AB3CD", id)
		}
		made = adminPerson{Name: who, Slack: id, Added: time.Now().UTC(), By: adminActor()}
		config.Admins = append(config.Admins, made)
	}
	if err := saveAdminConfig(config); err != nil {
		return err
	}
	executable, err := adminExecutable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(adminUnitPath()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(adminUnitPath(), []byte(adminUnitText(executable)), 0o644); err != nil {
		return err
	}
	for _, step := range [][]string{{"daemon-reload"}, {"enable", adminUnit}, {"restart", adminUnit}} {
		if out, err := appSystemctl(step...); err != nil {
			return fmt.Errorf("systemctl --user %s: %s", strings.Join(step, " "), qFirstLine(out, err.Error()))
		}
	}
	if first {
		adminRecord(made.Name, "", "set up the admin page, with "+made.Name+" its first admin")
	}
	answered := adminWaitUp(adminStartWait)
	config, _ = loadAdminConfig()
	if answered {
		fmt.Fprintf(stdout, "The admin page runs, at %s\n", adminURL(config))
	} else {
		fmt.Fprintf(stdout, "The admin page is starting, at %s (journalctl --user -u %s says how it goes).\n", adminURL(config), adminUnit)
	}
	fmt.Fprintln(stdout, adminReachLine(config))
	if first {
		key, err := adminMakeLink(made.Name, true)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "\nYour link, %s, for %d days. Keep it to yourself: whoever opens it can do what you can on this machine.\n  %s\n",
			made.Name, int(adminLinkLasts.Hours()/24), adminLinkURL(config, key))
	} else {
		fmt.Fprintf(stdout, "Admins: %s. hi admin link <admin> makes a new sign-in link.\n", adminNames(config))
	}
	if config.Reach != adminReachLocal {
		fmt.Fprintf(stdout, "\nNetBird decides who reaches it. Ask your IT team for a policy from the admins' group to %s, TCP %d%s.\n",
			adminPolicyTarget(config), adminPort, map[bool]string{true: " and 443", false: ""}[adminHasDomain(config)])
	}
	if !appLingers() {
		fmt.Fprintf(stdout, "\nIt stops when you log out, and doesn't start at boot, until you run:\n  sudo loginctl enable-linger %s\n", currentUserName())
	}
	return nil
}

// adminStartWait is how long hi admin start waits for the page to
// answer; tests shorten it.
var adminStartWait = 15 * time.Second

// adminWaitUp waits for the page to answer on its socket.
func adminWaitUp(wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if adminAnswers() {
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

// adminAnswers says whether the page answers on its socket.
func adminAnswers() bool {
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Dial: func(string, string) (net.Conn, error) {
		return net.Dial("unix", adminHTTPSocket())
	}}}
	response, err := client.Get("http://admin/healthz")
	if err != nil {
		return false
	}
	response.Body.Close()
	return response.StatusCode == http.StatusOK
}

func adminStatus(stdout io.Writer) error {
	config, ok := loadAdminConfig()
	if !ok {
		fmt.Fprintln(stdout, "The admin page isn't set up on this machine. hi admin start starts it and prints your link.")
		return nil
	}
	state, _ := appSystemctl("is-active", adminUnit)
	switch {
	case state == "active" && adminAnswers():
		fmt.Fprintf(stdout, "The admin page runs, at %s\n", adminURL(config))
	case state == "active":
		fmt.Fprintf(stdout, "The admin page's service runs, but the page doesn't answer yet; journalctl --user -u %s says why.\n", adminUnit)
	default:
		fmt.Fprintf(stdout, "The admin page is stopped. hi admin start starts it again, at %s\n", adminURL(config))
	}
	fmt.Fprintln(stdout, adminReachLine(config))
	fmt.Fprintln(stdout, "Admins:")
	for _, admin := range config.Admins {
		slack := "no Slack"
		if admin.Slack != "" {
			slack = "Slack " + admin.Slack
		}
		fmt.Fprintf(stdout, "  %-24s %s\n", admin.Name, slack)
	}
	return nil
}

func adminStop(stdout io.Writer) error {
	if out, err := appSystemctl("disable", "--now", adminUnit); err != nil && fileExists(adminUnitPath()) {
		return fmt.Errorf("systemctl --user disable --now %s: %s", adminUnit, qFirstLine(out, err.Error()))
	}
	adminStopPeer()
	adminRecord(adminActor(), "", "stopped the admin page")
	fmt.Fprintln(stdout, "The admin page is stopped. Its admins and its peer stay; hi admin start starts it again.")
	return nil
}

func adminLinkCommand(args []string, stdout io.Writer) error {
	config, ok := loadAdminConfig()
	if !ok {
		return errors.New("the admin page isn't set up on this machine; hi admin start sets it up")
	}
	if len(args) > 1 {
		return usageError{"usage: hi admin link [<admin>]"}
	}
	var admin adminPerson
	switch {
	case len(args) == 1:
		if admin, ok = config.admin(args[0]); !ok {
			return fmt.Errorf("%s isn't an admin; the admins are %s", args[0], adminNames(config))
		}
	case len(config.Admins) == 1:
		admin = config.Admins[0]
	default:
		if admin, ok = config.admin(currentUserName()); !ok {
			return fmt.Errorf("say whose: hi admin link <admin>; the admins are %s", adminNames(config))
		}
	}
	key, err := adminMakeLink(admin.Name, true)
	if err != nil {
		return err
	}
	adminRecord(adminActor(), "", "made a new link for "+admin.Name)
	fmt.Fprintf(stdout, "%s's link, for %d days; their old one has ended. Whoever opens it can do what an admin of the machine does.\n  %s\n",
		admin.Name, int(adminLinkLasts.Hours()/24), adminLinkURL(config, key))
	return nil
}

func adminAdminsCommand(args []string, stdout io.Writer) error {
	config, ok := loadAdminConfig()
	if !ok {
		return errors.New("the admin page isn't set up on this machine; hi admin start sets it up")
	}
	if len(args) == 0 {
		for _, admin := range config.Admins {
			fmt.Fprintf(stdout, "%-24s %-14s added %s by %s\n", admin.Name, firstNonEmpty(admin.Slack, "-"), admin.Added.Local().Format("2 Jan 2006"), firstNonEmpty(admin.By, "-"))
		}
		return nil
	}
	usage := usageError{"usage: hi admin admins [add <name> [--slack <member ID>] | remove <name>]"}
	switch args[0] {
	case "add":
		var words []string
		slackID := ""
		for i := 1; i < len(args); i++ {
			switch {
			case args[i] == "--slack" && i+1 < len(args):
				i++
				slackID = args[i]
			case strings.HasPrefix(args[i], "--slack="):
				slackID = strings.TrimPrefix(args[i], "--slack=")
			case strings.HasPrefix(args[i], "-"):
				return usage
			default:
				words = append(words, args[i])
			}
		}
		if len(words) == 0 {
			return usage
		}
		admin, err := adminAdd(strings.Join(words, " "), slackID, adminActor())
		if err != nil {
			return err
		}
		key, err := adminMakeLink(admin.Name, true)
		if err != nil {
			return err
		}
		config, _ = loadAdminConfig()
		fmt.Fprintf(stdout, "%s is an admin of the admin page. Their link, for %d days, to give them yourself:\n  %s\n", admin.Name, int(adminLinkLasts.Hours()/24), adminLinkURL(config, key))
		return nil
	case "remove", "rm":
		if len(args) < 2 {
			return usage
		}
		removed, err := adminRemove(strings.Join(args[1:], " "), adminActor())
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s isn't an admin any more; their links and sign-ins have ended.\n", removed.Name)
		return nil
	}
	return usage
}

// adminAdd adds an admin, as hi admin admins add and the page's Machine
// view do.
func adminAdd(name, slackID, by string) (adminPerson, error) {
	name, err := adminCheckName(name)
	if err != nil {
		return adminPerson{}, err
	}
	slackID = strings.TrimSpace(strings.Trim(strings.TrimSpace(slackID), "<@>"))
	if slackID != "" && !slackIDPattern.MatchString(slackID) {
		return adminPerson{}, fmt.Errorf("%q isn't a Slack member ID, such as U012AB3CD", slackID)
	}
	unlock := adminLock()
	config, ok := loadAdminConfig()
	if !ok {
		unlock()
		return adminPerson{}, errors.New("the admin page isn't set up on this machine; hi admin start sets it up")
	}
	if _, taken := config.admin(name); taken {
		unlock()
		return adminPerson{}, fmt.Errorf("%s is an admin already", name)
	}
	if slackID != "" {
		if other, taken := config.admin(slackID); taken {
			unlock()
			return adminPerson{}, fmt.Errorf("%s is %s's Slack member ID", slackID, other.Name)
		}
	}
	admin := adminPerson{Name: name, Slack: slackID, Added: time.Now().UTC(), By: by}
	config.Admins = append(config.Admins, admin)
	err = saveAdminConfig(config)
	unlock()
	if err == nil {
		adminRecord(by, "", "made "+name+" an admin")
	}
	return admin, err
}

// adminRemove takes an admin off the list, and ends their links and
// sign-ins. The last admin stays: the page would have nobody.
func adminRemove(name, by string) (adminPerson, error) {
	unlock := adminLock()
	config, ok := loadAdminConfig()
	if !ok {
		unlock()
		return adminPerson{}, errors.New("the admin page isn't set up on this machine")
	}
	admin, found := config.admin(name)
	if !found {
		unlock()
		return adminPerson{}, fmt.Errorf("%s isn't an admin; the admins are %s", name, adminNames(config))
	}
	if len(config.Admins) == 1 {
		unlock()
		return adminPerson{}, fmt.Errorf("%s is the only admin; add another first", admin.Name)
	}
	config.Admins = slices.DeleteFunc(config.Admins, func(a adminPerson) bool { return a.Name == admin.Name })
	err := saveAdminConfig(config)
	unlock()
	if err != nil {
		return admin, err
	}
	adminEndAll(admin.Name)
	adminRecord(by, "", "took "+admin.Name+" off the admins")
	return admin, nil
}

func adminAddressCommand(args []string, stdout io.Writer) error {
	config, ok := loadAdminConfig()
	if !ok {
		return errors.New("the admin page isn't set up on this machine; hi admin start sets it up")
	}
	usage := usageError{"usage: hi admin address [<host> [--https] | default]"}
	if len(args) == 0 {
		fmt.Fprintf(stdout, "The admin page is at %s\n", adminURL(config))
		if config.Host == "" {
			fmt.Fprintln(stdout, "hi admin address <host> has it at a name your IT team keeps, an A record to its peer or their proxy.")
		}
		return nil
	}
	https := false
	var words []string
	for _, arg := range args {
		switch arg {
		case "--https":
			https = true
		default:
			if strings.HasPrefix(arg, "-") {
				return usage
			}
			words = append(words, arg)
		}
	}
	if len(words) != 1 {
		return usage
	}
	if words[0] == "default" {
		config.Host, config.HTTPS = "", false
	} else {
		host := strings.ToLower(strings.TrimSuffix(words[0], "."))
		if err := appCheckHost(host); err != nil {
			return err
		}
		config.Host, config.HTTPS = host, https
	}
	unlock := adminLock()
	err := saveAdminConfig(config)
	unlock()
	if err != nil {
		return err
	}
	adminRecord(adminActor(), "", "moved the admin page to "+adminURL(config))
	fmt.Fprintf(stdout, "The admin page is at %s\n", adminURL(config))
	if config.Host != "" {
		fmt.Fprintf(stdout, "Ask your IT team for %s: an A record to %s, or their proxy to it on port 80. The admin page answers there in a minute.\n", config.Host, adminPolicyTarget(config))
	}
	return nil
}
