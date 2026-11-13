package cli

// hi app move: an app from this machine to another with hi, with a stop of
// about a minute and the same addresses (docs/specs/ideas/hi_app_move.md,
// release 1).
//
// An app is a folder, and its addresses are its own NetBird peer's, whose
// identity is in the folder (dev/netbird). So moving the folder, secrets
// and identity with it, moves the app to the same addresses. What lives
// beside the folder goes too (its snapshots and their key) or is made
// again by hi app up on the other machine (its link, units, and boxes).
//
// The machines talk over SSH, with a key of hi's own. On the receiving
// machine that key runs only hi app __receive (a forced command), which
// checks, takes files into the folder being received, starts the app, and
// reports; it gives no shell.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	appMovedFile     = "moved.json"    // in a folder that moved away
	appReceivingFile = ".hi-receiving" // in a folder being received
	appMoveKeyMarker = "hi-move-from:" // the comment on a sending machine's key
)

// These are replaced in tests.
var (
	appMoveStartPatience = 10 * time.Minute
	appMovePoll          = 5 * time.Second
)

// appMoveDir holds this machine's key for moving apps, the hosts it has
// pinned, and the apps it is receiving.
func appMoveDir() string { return filepath.Join(filepath.Dir(appsDir()), "move") }

func appMoveKey() string        { return filepath.Join(appMoveDir(), "id_ed25519") }
func appMoveKnownHosts() string { return filepath.Join(appMoveDir(), "known_hosts") }
func appMoveIncoming(name string) string {
	return filepath.Join(appMoveDir(), "incoming", name+".json")
}

// appMoved is moved.json: where a folder's app went, and how the move
// went. The sending machine keeps a copy in move/moved/<app>.json, since
// the app's link here goes with the move.
type appMoved struct {
	App      string    `json:"app,omitempty"`
	To       string    `json:"to"`
	At       time.Time `json:"at"`
	By       string    `json:"by,omitempty"`
	Folder   string    `json:"folder,omitempty"`   // what it left here
	Size     int64     `json:"size,omitempty"`     // the folder and its snapshots, copied
	Took     int       `json:"took,omitempty"`     // seconds, the whole move
	Stopped  int       `json:"stopped,omitempty"`  // seconds the app was stopped
	Snapshot string    `json:"snapshot,omitempty"` // taken just before it
	Admin    string    `json:"admin,omitempty"`    // the other machine's admin page
}

func appMovedRecordPath(name string) string {
	return filepath.Join(appMoveDir(), "moved", name+".json")
}

// loadAppMovedRecord is this machine's record of an app it moved away.
func loadAppMovedRecord(name string) (appMoved, bool) {
	var moved appMoved
	if !appNamePattern.MatchString(name) {
		return moved, false
	}
	data, err := os.ReadFile(appMovedRecordPath(name))
	if err != nil || json.Unmarshal(data, &moved) != nil || moved.To == "" {
		return moved, false
	}
	moved.App = name
	return moved, true
}

// appsMovedAway are the apps this machine moved away and doesn't run
// again, newest first.
func appsMovedAway() []appMoved {
	entries, _ := os.ReadDir(filepath.Join(appMoveDir(), "moved"))
	list := []appMoved{}
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".json")
		if moved, ok := loadAppMovedRecord(name); ok && !fileExists(filepath.Join(appsDir(), name)) {
			list = append(list, moved)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].At.After(list[j].At) })
	return list
}

// appMovedAway says where the app in folder went, or "" when it didn't.
func appMovedAway(folder string) (appMoved, bool) {
	var moved appMoved
	data, err := os.ReadFile(filepath.Join(folder, appMovedFile))
	if err != nil {
		return moved, false
	}
	json.Unmarshal(data, &moved)
	return moved, true
}

// appMovedError refuses to start an app whose folder moved away, or is
// being received.
func appMovedError(name, folder string) error {
	if moved, ok := appMovedAway(folder); ok {
		return fmt.Errorf("app %s moved to %s on %s, and runs there; this folder is what it left. To bring it back, on %s: hi app move %s %s",
			name, moved.To, moved.At.Local().Format("Jan 2 15:04"), moved.To, name, appMoveThisMachine())
	}
	if fileExists(filepath.Join(folder, appReceivingFile)) {
		return fmt.Errorf("app %s is being moved here, and starts when the move is done", name)
	}
	return nil
}

// appMoveThisMachine is this machine's name as other machines reach it:
// its name on NetBird (ws1.corp.example), or its hostname without one.
// Tests replace it.
var appMoveThisMachine = func() string {
	appMoveNameOnce.Do(func() {
		appMoveName, _ = os.Hostname()
		if out, err := exec.Command("netbird", "status", "--json").Output(); err == nil {
			var status struct {
				FQDN string `json:"fqdn"`
			}
			if json.Unmarshal(out, &status) == nil && strings.Contains(status.FQDN, ".") {
				appMoveName = strings.TrimSuffix(status.FQDN, ".")
			}
		}
		if appMoveName == "" {
			appMoveName = "this machine"
		}
	})
	return appMoveName
}

var (
	appMoveNameOnce sync.Once
	appMoveName     string
)

// appEnsureMoveKey makes this machine's key for moving apps once, and
// returns its public half.
func appEnsureMoveKey() (string, error) {
	if !fileExists(appMoveKey()) {
		if err := os.MkdirAll(appMoveDir(), 0o700); err != nil {
			return "", err
		}
		out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "hi-move@"+appMoveThisMachine(), "-f", appMoveKey()).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("ssh-keygen: %s", qFirstLine(string(out), err.Error()))
		}
	}
	data, err := os.ReadFile(appMoveKey() + ".pub")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return "", fmt.Errorf("%s.pub isn't a public key", appMoveKey())
	}
	return fields[0] + " " + fields[1], nil
}

// ---------------------------------------------------------------------------
// hi app machines

func appAuthorizedKeys() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh", "authorized_keys")
}

var appMachineName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)

func appMachinesCommand(args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi app machines [add <machine> <key> | rm <machine>]"}
	switch {
	case len(args) == 0:
		key, err := appEnsureMoveKey()
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "This machine's key for moving apps:\n  %s\n\n", key)
		fmt.Fprintf(stdout, "To let it move apps to another machine with hi, run there, as the account that runs hi:\n  hi app machines add %s %s\n", appMoveThisMachine(), key)
		var to []string
		for _, machine := range loadAppMoveTo() {
			to = append(to, machine.Name)
		}
		if len(to) > 0 {
			fmt.Fprintf(stdout, "\nThis machine moves apps to: %s.\n", strings.Join(to, ", "))
		}
		from := appMachinesFrom()
		if len(from) == 0 {
			fmt.Fprintln(stdout, "\nThis machine takes apps from no other machine.")
		} else {
			fmt.Fprintf(stdout, "\nThis machine takes apps from: %s.\n", strings.Join(from, ", "))
		}
		return nil
	case args[0] == "add" && len(args) >= 4:
		return appMachinesAdd(args[1], strings.Join(args[2:], " "), stdout)
	case args[0] == "rm" && len(args) == 2:
		removed, err := appMachinesRemove(args[1])
		if err != nil {
			return err
		}
		if !removed {
			return fmt.Errorf("this machine doesn't take apps from %s", args[1])
		}
		fmt.Fprintf(stdout, "This machine no longer takes apps from %s.\n", args[1])
		return nil
	}
	return usage
}

// appMachinesFrom are the machines whose keys may send apps here.
func appMachinesFrom() []string {
	data, _ := os.ReadFile(appAuthorizedKeys())
	var names []string
	for _, line := range strings.Split(string(data), "\n") {
		if _, after, ok := strings.Cut(line, appMoveKeyMarker); ok {
			names = append(names, strings.TrimSpace(after))
		}
	}
	return names
}

// appMachinesAdd lets machine's key send apps here: a line in
// authorized_keys that runs only hi app __receive.
func appMachinesAdd(machine, key string, stdout io.Writer) error {
	if !appMachineName.MatchString(machine) {
		return fmt.Errorf("%q isn't a machine's name", machine)
	}
	fields := strings.Fields(key)
	if len(fields) < 2 || fields[0] != "ssh-ed25519" {
		return errors.New("the key should be the ssh-ed25519 line that hi app machines prints on the other machine")
	}
	if _, err := base64.StdEncoding.DecodeString(fields[1]); err != nil {
		return errors.New("the key isn't a key: copy the whole line hi app machines printed")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if _, err := appMachinesRemove(machine); err != nil {
		return err
	}
	line := fmt.Sprintf("restrict,command=\"%s app __receive %s\" %s %s %s%s\n", executable, machine, fields[0], fields[1], appMoveKeyMarker, machine)
	if err := os.MkdirAll(filepath.Dir(appAuthorizedKeys()), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(appAuthorizedKeys(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.WriteString(line); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "This machine takes apps from %s now. Its key runs only hi's receiving end here, not a shell.\n", machine)
	return nil
}

func appMachinesRemove(machine string) (bool, error) {
	data, err := os.ReadFile(appAuthorizedKeys())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var kept []string
	removed := false
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), appMoveKeyMarker+machine) {
			removed = true
			continue
		}
		kept = append(kept, line)
	}
	if !removed {
		return false, nil
	}
	return true, writeFileAtomic(appAuthorizedKeys(), []byte(strings.Join(kept, "\n")+"\n"), 0o600)
}

// ---------------------------------------------------------------------------
// The receiving end: hi app __receive <from>, run by sshd for the sending
// machine's key, with the asked command in SSH_ORIGINAL_COMMAND.

// appMoveNeeds is what the sending machine says the app needs here.
type appMoveNeeds struct {
	App           string `json:"app"`
	Folder        string `json:"folder"`
	Size          int64  `json:"size"`
	Version       string `json:"version"`
	MachineKey    bool   `json:"machine_key,omitempty"`    // the lead uses the machine's OpenRouter key
	Domain        string `json:"domain,omitempty"`         // its https names are on this domain
	GitHub        bool   `json:"github,omitempty"`         // the broker pushes with gh
	Org           string `json:"org,omitempty"`            // the org server it reports to
	ClaudeMachine bool   `json:"claude_machine,omitempty"` // a member uses the machine's Claude Code sign-in
	CodexMachine  bool   `json:"codex_machine,omitempty"`  // a member uses the machine's Codex sign-in
}

// appMoveReport is the receiving machine's answer to a check: what stops
// the move, and what works differently there.
type appMoveReport struct {
	Machine  string   `json:"machine"`
	Version  string   `json:"version"`
	Problems []string `json:"problems,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// appIncoming is an app this machine is receiving.
type appIncoming struct {
	App    string    `json:"app"`
	Folder string    `json:"folder"`
	From   string    `json:"from"`
	Since  time.Time `json:"since"`
}

func loadAppIncoming(name string) (appIncoming, bool) {
	var incoming appIncoming
	data, err := os.ReadFile(appMoveIncoming(name))
	if err != nil || json.Unmarshal(data, &incoming) != nil || incoming.Folder == "" {
		return incoming, false
	}
	return incoming, true
}

func runAppReceive(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	from := "another machine"
	if len(args) == 1 {
		from = args[0]
	}
	words := strings.Fields(os.Getenv("SSH_ORIGINAL_COMMAND"))
	if len(words) == 0 {
		fmt.Fprintln(stderr, "hi app __receive takes apps from "+from+"; it runs only what hi app move asks")
		return 2
	}
	if err := appReceive(from, words, stdin, stdout, stderr); err != nil {
		var exit appReceiveExit
		if errors.As(err, &exit) {
			return int(exit)
		}
		fmt.Fprintf(stderr, "hi on %s: %v\n", appMoveThisMachine(), err)
		return 1
	}
	return 0
}

func appReceive(from string, words []string, stdin io.Reader, stdout, stderr io.Writer) error {
	command, rest := words[0], words[1:]
	appArg := func() (string, error) {
		if len(rest) < 1 || !appNamePattern.MatchString(rest[0]) {
			return "", errors.New("no app named")
		}
		return rest[0], nil
	}
	switch command {
	case "version":
		fmt.Fprintln(stdout, version)
		return nil
	case "check":
		var needs appMoveNeeds
		if err := json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&needs); err != nil {
			return fmt.Errorf("the check: %w", err)
		}
		return json.NewEncoder(stdout).Encode(appReceiveCheck(needs))
	case "prepare":
		name, err := appArg()
		if err != nil || len(rest) != 2 {
			return errors.New("usage: prepare <app> <folder>")
		}
		return appReceivePrepare(from, name, rest[1], stdout)
	case "rsync":
		return appReceiveRsync(words, stdin, stdout, stderr)
	case "start":
		name, err := appArg()
		if err != nil {
			return err
		}
		return appReceiveStart(name, stdout)
	case "status":
		name, err := appArg()
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(appReceiveStatus(name))
	case "abort":
		name, err := appArg()
		if err != nil {
			return err
		}
		return appReceiveAbort(name, stdout)
	case "done":
		name, err := appArg()
		if err != nil {
			return err
		}
		os.Remove(appMoveIncoming(name))
		// Where this machine's admin page is, for the other's to link to.
		answer := map[string]string{}
		if config, ok := loadAdminConfig(); ok && config.Reach != adminReachLocal {
			answer["admin"] = adminURL(config)
		}
		return json.NewEncoder(stdout).Encode(answer)
	}
	return fmt.Errorf("hi app __receive doesn't do %q", command)
}

// appReceiveCheck is what this machine has and lacks for an app.
func appReceiveCheck(needs appMoveNeeds) appMoveReport {
	here := appMoveThisMachine()
	report := appMoveReport{Machine: here, Version: version}
	problem := func(format string, args ...any) {
		report.Problems = append(report.Problems, fmt.Sprintf(format, args...))
	}
	warn := func(format string, args ...any) {
		report.Warnings = append(report.Warnings, fmt.Sprintf(format, args...))
	}
	if !appNamePattern.MatchString(needs.App) {
		problem("%q isn't an app's name", needs.App)
		return report
	}
	if version != "dev" && needs.Version != "dev" && compareVersions(strings.TrimPrefix(version, "v"), strings.TrimPrefix(needs.Version, "v")) < 0 {
		problem("hi on %s is %s, older than the app's %s: hi update on %s", here, version, needs.Version, here)
	}
	if !filepath.IsAbs(needs.Folder) || strings.ContainsAny(needs.Folder, " \t\n\"'\\") {
		problem("%q can't be the app's folder on "+here+": give a plain absolute path with --dir", needs.Folder)
	} else if err := appReceiveFolderFree(needs.App, needs.Folder); err != nil {
		problem("%v", err)
	}
	if current := appDir(needs.App); fileExists(filepath.Join(current, appConfigFile)) && filepath.Clean(current) != filepath.Clean(needs.Folder) {
		if _, moved := appMovedAway(current); !moved {
			problem("an app named %s is on %s already, in %s", needs.App, here, current)
		}
	}
	for _, tool := range []string{"podman", "rsync"} {
		if _, err := exec.LookPath(tool); err != nil {
			problem("%s isn't installed on %s: hi install there", tool, here)
		}
	}
	if err := hermesInstalled(); err != nil {
		problem("%v", err)
	}
	if !appResticInstalled() {
		warn("restic isn't installed on %s, so the app takes no snapshots there until it is: hi install restic there", here)
	}
	if !appLingers() {
		warn("on %s the app stops when its account logs out, and doesn't start at boot, until someone with sudo runs there: sudo loginctl enable-linger %s", here, currentUserName())
	}
	if free, ok := appFreeSpace(needs.Folder); ok && free < needs.Size+needs.Size/10+1<<30 {
		problem("there's %s free for the app's folder on %s, and it needs %s and some room", qSize(free), here, qSize(needs.Size))
	}
	if needs.MachineKey && hermesReady() != nil {
		problem("the app's lead uses the machine's OpenRouter key, and Hermes on %s has none: hermes setup there, or give the app a key of its own before moving it (hi app key %s)", here, needs.App)
	}
	if needs.Domain != "" {
		if domain := loadTeamsSettings().Domain; domain != needs.Domain {
			if domain == "" {
				warn("%s has no domain, so the app's https names under %s stop working there; it keeps its NetBird name, on http. To keep them: hi net domain %s on %s", here, needs.Domain, needs.Domain, here)
			} else {
				warn("%s's domain is %s, not %s: the app's https names change to %s ones", here, domain, needs.Domain, domain)
			}
		}
	}
	if needs.GitHub {
		if out, err := exec.Command("gh", "auth", "status").CombinedOutput(); err != nil {
			problem("the app pushes to GitHub, and gh isn't signed in on %s (%s): gh auth login there", here, qFirstLine(string(out), err.Error()))
		}
	}
	org := ""
	if connection, err := loadServerConnection(); err == nil && connection != nil {
		org = connection.URL
	}
	if needs.Org != org {
		switch {
		case org == "":
			warn("%s isn't joined to a server, so the app's heartbeats to %s stop there", here, needs.Org)
		case needs.Org == "":
			warn("%s is joined to a server (%s), and the app will report to it", here, org)
		default:
			warn("%s is joined to another server (%s): the app reports there, not to %s", here, org, needs.Org)
		}
	}
	home, _ := os.UserHomeDir()
	if needs.ClaudeMachine && !fileExists(filepath.Join(home, ".claude", ".credentials.json")) && !fileExists(filepath.Join(home, ".claude.json")) {
		warn("a member uses the machine's Claude Code sign-in, and Claude Code isn't signed in on %s: claude there, then /login", here)
	}
	if needs.CodexMachine && !fileExists(filepath.Join(home, ".codex", "auth.json")) {
		warn("a member uses the machine's Codex sign-in, and Codex isn't signed in on %s: codex login there", here)
	}
	return report
}

// appReceiveFolderFree says whether the app may be received into folder:
// it isn't there, or it is a copy hi left (one being received, or one
// the app moved away from).
func appReceiveFolderFree(name, folder string) error {
	if !fileExists(folder) {
		return nil
	}
	if fileExists(filepath.Join(folder, appReceivingFile)) {
		return nil
	}
	if _, moved := appMovedAway(folder); moved {
		data, err := os.ReadFile(filepath.Join(folder, appConfigFile))
		var config appConfig
		if err == nil && json.Unmarshal(data, &config) == nil && config.Name == name {
			return nil
		}
	}
	entries, err := os.ReadDir(folder)
	if err == nil && len(entries) == 0 {
		return nil
	}
	return fmt.Errorf("%s is in the way here: move it, or give the app another folder with --dir", folder)
}

// appFreeSpace is the room on the disk that would hold path.
func appFreeSpace(path string) (int64, bool) {
	for dir := path; ; dir = filepath.Dir(dir) {
		var stat syscall.Statfs_t
		if syscall.Statfs(dir, &stat) == nil {
			return int64(stat.Bavail) * int64(stat.Bsize), true
		}
		if dir == filepath.Dir(dir) {
			return 0, false
		}
	}
}

func appReceivePrepare(from, name, folder string, stdout io.Writer) error {
	if !filepath.IsAbs(folder) || strings.ContainsAny(folder, " \t\n\"'\\") {
		return fmt.Errorf("%q isn't a plain absolute path", folder)
	}
	folder = filepath.Clean(folder)
	if err := appReceiveFolderFree(name, folder); err != nil {
		return err
	}
	if incoming, ok := loadAppIncoming(name); ok && incoming.Folder != folder {
		return fmt.Errorf("app %s is being received into %s already", name, incoming.Folder)
	}
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	// A copy the app left here may hold what a box made under another of
	// this account's ids, which rsync can't change or delete.
	for _, dir := range []string{folder, appSnapshotsDir(name)} {
		if err := appOwnAll(dir); err != nil {
			return err
		}
	}
	marker := fmt.Sprintf("from %s, %s\n", from, time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(folder, appReceivingFile), []byte(marker), 0o600); err != nil {
		return err
	}
	if err := os.MkdirAll(appSnapshotsDir(name), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(appIncoming{App: name, Folder: folder, From: from, Since: time.Now().UTC()}, "", "  ")
	if err := os.MkdirAll(filepath.Dir(appMoveIncoming(name)), 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(appMoveIncoming(name), append(data, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Fprintln(stdout, appSnapshotsDir(name))
	return nil
}

// appOwnAll gives this account back what in dir a box made under another
// of its ids (a container's root is this account; its other users are the
// account's subordinate ids), so rsync can update and delete it.
var appOwnAll = func(dir string) error {
	uid := os.Getuid()
	others := false
	filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || others {
			return filepath.SkipDir
		}
		if info, err := entry.Info(); err == nil {
			if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != uid {
				others = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	if !others {
		return nil
	}
	if out, err := exec.Command("podman", "unshare", "chown", "-R", "0:0", dir).CombinedOutput(); err != nil {
		return fmt.Errorf("%s holds files of a box's, under another id, which hi couldn't make this account's (podman unshare chown): %s", dir, qFirstLine(string(out), err.Error()))
	}
	return nil
}

// appRsyncLongOptions are the long options hi app move's rsync sends.
var appRsyncLongOptions = map[string]bool{
	"--server": true, "--delete": true, "--delete-during": true, "--delete-delay": true, "--delete-after": true, "--partial": true, "--inplace": true, "--numeric-ids": true,
}

var appRsyncShortOptions = regexp.MustCompile(`^-[A-Za-z.]+$`)

// appReceiveRsync runs rsync's receiving end for a folder being received,
// or its snapshots, and nothing else.
func appReceiveRsync(words []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(words) < 5 || words[1] != "--server" || words[len(words)-2] != "." {
		return errors.New("only rsync's receiving end runs here")
	}
	for _, word := range words[1 : len(words)-2] {
		if word == "--sender" || (strings.HasPrefix(word, "--") && !appRsyncLongOptions[word]) || (!strings.HasPrefix(word, "--") && !appRsyncShortOptions.MatchString(word)) {
			return fmt.Errorf("rsync's %s isn't taken here", word)
		}
	}
	target := words[len(words)-1]
	if !filepath.IsAbs(target) {
		return errors.New("rsync's target must be an absolute path")
	}
	target = filepath.Clean(target)
	allowed := false
	entries, _ := os.ReadDir(filepath.Dir(appMoveIncoming("x")))
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".json")
		incoming, ok := loadAppIncoming(name)
		if !ok {
			continue
		}
		if target == incoming.Folder || target == filepath.Clean(appSnapshotsDir(name)) {
			allowed = true
		}
	}
	if !allowed {
		return fmt.Errorf("%s isn't an app being received here", target)
	}
	command := exec.Command("rsync", words[1:]...)
	command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return appReceiveExit(exit.ExitCode())
		}
		return err
	}
	return nil
}

// appReceiveExit is rsync's exit code, passed on as it is: rsync on the
// other end has said what went wrong.
type appReceiveExit int

func (e appReceiveExit) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

func appReceiveStart(name string, stdout io.Writer) error {
	incoming, ok := loadAppIncoming(name)
	if !ok {
		return fmt.Errorf("app %s isn't being received here", name)
	}
	os.Remove(filepath.Join(incoming.Folder, appMovedFile))
	os.Remove(filepath.Join(incoming.Folder, appReceivingFile))
	adopted, err := appAdopt(incoming.Folder, stdout)
	if err == nil && adopted != name {
		err = fmt.Errorf("the folder holds app %s, not %s", adopted, name)
	}
	if err == nil {
		err = appUp(name, stdout)
	}
	if err != nil {
		os.WriteFile(filepath.Join(incoming.Folder, appReceivingFile), []byte("start failed\n"), 0o600)
		return err
	}
	// It runs here again, if it had moved away from here before.
	os.Remove(appMovedRecordPath(name))
	adminRecord("hi app move", name, "moved the app here from "+incoming.From)
	return nil
}

// appReceivedStatus is how a received app is doing.
type appReceivedStatus struct {
	State string `json:"state"`
	Lead  bool   `json:"lead"`
	Peer  bool   `json:"peer"`
}

func appReceiveStatus(name string) appReceivedStatus {
	return appReceivedStatus{State: appState(name), Lead: appMoveContainerRunning("hi-box-app-" + name), Peer: appMoveContainerRunning(appDevNetContainer(name))}
}

// appMoveContainerRunning says whether a container runs; tests replace it.
var appMoveContainerRunning = func(container string) bool {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return false
	}
	out, err := engine.output("inspect", "-f", "{{.State.Running}}", container)
	return err == nil && strings.TrimSpace(out) == "true"
}

// appReceiveAbort takes back a start that didn't work: the app's service,
// boxes, and link go, and its folder stays, as a copy being received.
func appReceiveAbort(name string, stdout io.Writer) error {
	incoming, ok := loadAppIncoming(name)
	if !ok {
		return fmt.Errorf("app %s isn't being received here", name)
	}
	appStopEverywhere(name)
	os.Remove(appUnitPath(name))
	appRemoveSnapshotTimer(name)
	appSystemctl("daemon-reload")
	if target, err := filepath.EvalSymlinks(filepath.Join(appsDir(), name)); err == nil && filepath.Clean(target) == incoming.Folder {
		os.Remove(filepath.Join(appsDir(), name))
	}
	os.WriteFile(filepath.Join(incoming.Folder, appReceivingFile), []byte("aborted\n"), 0o600)
	fmt.Fprintf(stdout, "App %s is stopped here; its copy stays in %s for the next try.\n", name, incoming.Folder)
	return nil
}

// appStopEverywhere stops an app's service, timer, and every box of its,
// the NetBird peer too, so nothing of it runs on this machine.
func appStopEverywhere(name string) {
	appSystemctl("disable", "--now", appUnitName(name))
	appSystemctl("disable", "--now", appSnapshotUnitName(name)+".timer")
	var boxes []string
	for _, task := range loadTeamTasks(name) {
		if task.Box != "" && fileExists(boxStateFile(task.Box)) {
			boxes = append(boxes, task.Box)
		}
	}
	appRemoveContainers(name, boxes)
}

// ---------------------------------------------------------------------------
// hi app move <app> <machine>

type appMoveOptions struct {
	app, machine, dir string
	check, now, yes   bool
}

func appMoveCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	usage := usageError{"usage: hi app move <app> <machine> [--check] [--now] [--yes] [--dir <folder there>]"}
	var options appMoveOptions
	var words []string
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--check":
			options.check = true
		case arg == "--now":
			options.now = true
		case arg == "--yes" || arg == "-y":
			options.yes = true
		case arg == "--dir" && i+1 < len(args):
			i++
			options.dir = args[i]
		case strings.HasPrefix(arg, "--dir="):
			options.dir = strings.TrimPrefix(arg, "--dir=")
		case strings.HasPrefix(arg, "-"):
			return usageError{"unknown option " + arg}
		default:
			words = append(words, arg)
		}
	}
	if len(words) != 2 {
		return usage
	}
	options.app, options.machine = words[0], words[1]
	return appMove(options, stdin, stdout)
}

// appMoveTarget is the other machine, over SSH with this machine's key.
type appMoveTarget struct {
	machine string // as given
	dest    string // user@host
}

func newAppMoveTarget(machine string) (appMoveTarget, error) {
	user, host, ok := strings.Cut(machine, "@")
	if !ok {
		user, host = currentUserName(), machine
	}
	if !appMachineName.MatchString(host) || !regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`).MatchString(user) {
		return appMoveTarget{}, fmt.Errorf("%q isn't a machine: give its name on the network, such as ws2 or ws2.corp.example", machine)
	}
	return appMoveTarget{machine: host, dest: user + "@" + host}, nil
}

func (t appMoveTarget) sshOptions() []string {
	return []string{"-i", appMoveKey(), "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes",
		"-o", "UserKnownHostsFile=" + appMoveKnownHosts(), "-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=20", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=4"}
}

// call runs one of hi app __receive's commands there.
var appMoveCall = func(t appMoveTarget, stdin io.Reader, stdout, stderr io.Writer, words ...string) error {
	args := append(t.sshOptions(), "--", t.dest)
	args = append(args, words...)
	command := exec.Command("ssh", args...)
	command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
	return command.Run()
}

// output runs a command there and returns what it printed, with its error
// line when it fails.
func (t appMoveTarget) output(stdin io.Reader, words ...string) (string, error) {
	var out, errOut strings.Builder
	err := appMoveCall(t, stdin, &out, &errOut, words...)
	if err != nil {
		text := strings.TrimSpace(errOut.String())
		if strings.Contains(text, "Permission denied") {
			return "", appMoveNoAccess
		}
		return out.String(), errors.New(qFirstLine(lastLines(text, 1), err.Error()))
	}
	return out.String(), nil
}

var appMoveNoAccess = errors.New("the machine doesn't take apps from this one yet")

// appMoveRsync copies from to there, exactly (--delete), leaving out what
// only means something on this machine while it runs.
var appMoveRsync = func(t appMoveTarget, from, to string, stdout io.Writer) error {
	ssh := "ssh " + strings.Join(t.sshOptions(), " ")
	// Owners and groups aren't kept: an account that isn't root can't set
	// them, and the boxes run as that account on both machines.
	args := []string{"-aH", "--no-owner", "--no-group", "--delete", "--exclude=*.sock", "--exclude=postmaster.pid", "--exclude=/" + appReceivingFile, "--exclude=/" + appMovedFile,
		"-e", ssh, strings.TrimSuffix(from, "/") + "/", t.dest + ":" + strings.TrimSuffix(to, "/") + "/"}
	var errOut strings.Builder
	command := exec.Command("rsync", args...)
	command.Stdout, command.Stderr = io.Discard, &errOut
	if err := command.Run(); err != nil {
		// 24: files vanished while they were copied, which a warm pass
		// expects of a running app.
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 24 {
			return nil
		}
		return fmt.Errorf("copying %s: %s", from, appRsyncErrors(errOut.String(), err))
	}
	return nil
}

// appRsyncErrors is what rsync said went wrong: its first few lines about
// files, not its summary or hi's own line from the other end.
func appRsyncErrors(text string, err error) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "rsync:") || strings.HasPrefix(line, "rsync warning:") {
			lines = append(lines, line)
		}
	}
	if len(lines) > 3 {
		lines = append(lines[:3], fmt.Sprintf("and %d more", len(lines)-3))
	}
	if len(lines) == 0 {
		return qFirstLine(lastLines(text, 1), err.Error())
	}
	return strings.Join(lines, "; ")
}

func appMoveSize(dir string) int64 {
	var size int64
	filepath.WalkDir(dir, func(_ string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			if info, err := entry.Info(); err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	return size
}

// appMoveNeedsOf is what the app needs on the other machine.
func appMoveNeedsOf(name, folder string) appMoveNeeds {
	config, _ := loadTeam(name)
	secrets, _ := loadTeamSecrets(name)
	needs := appMoveNeeds{App: name, Folder: folder, Size: appMoveSize(appDir(name)) + appMoveSize(appSnapshotsDir(name)), Version: version,
		MachineKey: secrets.OpenRouterAPIKey == "", Domain: appHTTPSDomain(name), GitHub: config.Remote != ""}
	if needs.Domain == "" {
		needs.Domain = appSlotDomainSetting(name)
	}
	if connection, err := loadServerConnection(); err == nil && connection != nil {
		needs.Org = connection.URL
	}
	for role, member := range config.Members {
		if role == "lead" {
			continue
		}
		switch member.Agent {
		case "claude":
			needs.ClaudeMachine = needs.ClaudeMachine || secrets.ClaudeToken == ""
		case "codex":
			needs.CodexMachine = true
		}
	}
	return needs
}

// appSlotDomainSetting is the machine's domain when the app has its names
// on it (hi app domain on), else "".
func appSlotDomainSetting(name string) string {
	if production := appSlotDomain(name, appSlotProduction); production != "" {
		return strings.TrimPrefix(production, name+".")
	}
	return ""
}

func appMove(options appMoveOptions, stdin io.Reader, stdout io.Writer) error {
	name := options.app
	config, err := loadTeam(name)
	if err != nil {
		return err
	}
	folder := appDir(name)
	if err := appMovedError(name, folder); err != nil {
		return err
	}
	target, err := newAppMoveTarget(options.machine)
	if err != nil {
		return err
	}
	began := time.Now()
	destFolder := folder
	if options.dir != "" {
		if destFolder, err = filepath.Abs(options.dir); err != nil {
			return err
		}
	}

	// 1. What the machine has and lacks.
	fmt.Fprintf(stdout, "Checking %s for app %s...\n", target.machine, name)
	report, needs, err := appMoveCheck(name, target, destFolder)
	if err != nil {
		return err
	}
	appMovePrintReport(report, target, needs, stdout)
	if len(report.Problems) > 0 {
		return fmt.Errorf("app %s can't move to %s until the above is done", name, target.machine)
	}
	if options.check {
		fmt.Fprintf(stdout, "App %s can move to %s: hi app move %s %s\n", name, target.machine, name, target.machine)
		return nil
	}
	if err := confirm(stdin, stdout, options.yes, fmt.Sprintf("Move app %s to %s? The lead, production, Stage, and the dev page stop for about a minute.", name, target.machine)); err != nil {
		return err
	}

	// 2. A warm copy while the app runs.
	prepared, err := target.output(nil, "prepare", name, destFolder)
	if err != nil {
		return fmt.Errorf("getting %s ready: %v", target.machine, err)
	}
	// Where the app's snapshots go there, as that machine keeps them.
	snapshotsThere := strings.TrimSpace(prepared)
	if !filepath.IsAbs(snapshotsThere) {
		return fmt.Errorf("%s didn't say where the app's snapshots go (%s)", target.machine, qFirstLine(prepared, "nothing"))
	}
	copyAll := func() error {
		if err := appMoveRsync(target, folder, destFolder, stdout); err != nil {
			return err
		}
		if fileExists(appSnapshotsDir(name)) {
			return appMoveRsync(target, appSnapshotsDir(name), snapshotsThere, stdout)
		}
		return nil
	}
	fmt.Fprintf(stdout, "Copying the app's folder (%s) to %s while it runs...\n", qSize(needs.Size), target.machine)
	started := time.Now()
	if err := copyAll(); err != nil {
		return err
	}
	if time.Since(started) > time.Minute {
		fmt.Fprintln(stdout, "Copying what changed meanwhile...")
		if err := copyAll(); err != nil {
			return err
		}
	}

	// 3. Wait for work that shouldn't be cut off.
	if !options.now {
		if err := appMoveWaitForWork(name, stdout); err != nil {
			return err
		}
	}

	// 4. A snapshot, to go back to.
	snapshotID := ""
	if appState(name) == "up" && appResticInstalled() {
		var snapshot appSnapshot
		if asked, err := appHostCall(name, "/snapshot", appSnapshotRequest{Kind: appSnapshotManual}, &snapshot); asked && err == nil {
			snapshotID = snapshot.ID
			fmt.Fprintf(stdout, "Snapshot %s taken first.\n", snapshot.ID)
		} else if err != nil {
			fmt.Fprintf(stdout, "Warning: no snapshot before the move: %v\n", err)
		}
	}

	// 5. The stop.
	secrets, _ := loadTeamSecrets(name)
	appMovePost(config, secrets, "I'm moving to another machine: production, Stage, the dev page, and I are away for about a minute.")
	peerIP, peerErr := appPeerIP(name)
	fmt.Fprintf(stdout, "Stopping app %s here...\n", name)
	stopped := time.Now()
	appStopEverywhere(name)
	goBack := func(cause error) error {
		fmt.Fprintf(stdout, "The move didn't work, so app %s starts here again.\n", name)
		target.output(nil, "abort", name)
		if err := appUp(name, io.Discard); err != nil {
			return fmt.Errorf("%v; and starting it here again failed too: %v (hi app up %s)", cause, err, name)
		}
		appMovePost(config, secrets, "I'm back, on the same machine: the move didn't happen.")
		return cause
	}
	fmt.Fprintln(stdout, "Copying the last changes...")
	if err := copyAll(); err != nil {
		return goBack(err)
	}

	// 6. Start it there.
	fmt.Fprintf(stdout, "Starting app %s on %s...\n", name, target.machine)
	if out, err := target.output(nil, "start", name); err != nil {
		return goBack(fmt.Errorf("starting it on %s: %v", target.machine, err))
	} else {
		for _, warning := range appStartWarnings(out) {
			fmt.Fprintf(stdout, "  %s: %s\n", target.machine, warning)
		}
	}
	if err := appMoveWaitUp(target, name, stdout); err != nil {
		return goBack(err)
	}
	down := time.Since(stopped).Round(time.Second)

	// 7. It runs there: here, only the folder stays, which says where it went.
	var done struct {
		Admin string `json:"admin"`
	}
	if out, err := target.output(nil, "done", name); err == nil {
		json.Unmarshal([]byte(out), &done)
	}
	os.Remove(appUnitPath(name))
	appRemoveSnapshotTimer(name)
	appSystemctl("daemon-reload")
	record := appMoved{App: name, To: target.machine, At: time.Now().UTC(), By: firstNonEmpty(os.Getenv(adminByEnv), adminActor()), Folder: folder,
		Size: needs.Size, Took: int(time.Since(began).Seconds() + 0.5), Stopped: int(down.Seconds()), Snapshot: snapshotID, Admin: done.Admin}
	moved, _ := json.MarshalIndent(record, "", "  ")
	if err := writeFileAtomic(filepath.Join(folder, appMovedFile), append(moved, '\n'), 0o600); err != nil {
		fmt.Fprintf(stdout, "Warning: %v\n", err)
	}
	os.MkdirAll(filepath.Dir(appMovedRecordPath(name)), 0o700)
	writeFileAtomic(appMovedRecordPath(name), append(moved, '\n'), 0o600)
	os.Remove(filepath.Join(appsDir(), name))
	appMovePost(config, secrets, "I'm back, on another machine. Nothing changes for you: the same links and the same channel.")
	fmt.Fprintf(stdout, "\nApp %s runs on %s now, at the same addresses. The move copied %s and took %s; the app was stopped for %s.\n",
		name, target.machine, qSize(needs.Size), time.Since(began).Round(time.Second), down)
	if done.Admin != "" {
		fmt.Fprintf(stdout, "%s's admin page: %s\n", target.machine, done.Admin)
	}
	if peerErr == nil {
		if appMoveReachable(peerIP.String()) {
			fmt.Fprintf(stdout, "Its NetBird address, %s, answers from here: its dev page is at %s.\n", peerIP, appInternalURL(name, appAddressDev))
		} else {
			fmt.Fprintf(stdout, "Warning: its NetBird address, %s, doesn't answer from here yet (NetBird's policies may keep this machine off it).\n", peerIP)
		}
	}
	fmt.Fprintf(stdout, "Its folder here, %s, stays as it was at the move, with a note of where the app went; delete it when you no longer want it.\n", folder)
	fmt.Fprintf(stdout, "Manage it on %s now (its admin page, or hi app status %s there). To bring it back: hi app move %s %s, on %s.\n", target.machine, name, name, appMoveThisMachine(), target.machine)
	return nil
}

// appMoveCheck asks the other machine what it has and lacks for the app,
// adding what this machine knows (a public address starts again there),
// and remembers the machine for the next move.
func appMoveCheck(name string, target appMoveTarget, destFolder string) (appMoveReport, appMoveNeeds, error) {
	var report appMoveReport
	key, err := appEnsureMoveKey()
	if err != nil {
		return report, appMoveNeeds{}, err
	}
	needs := appMoveNeedsOf(name, destFolder)
	data, _ := json.Marshal(needs)
	out, err := target.output(strings.NewReader(string(data)), "check")
	if errors.Is(err, appMoveNoAccess) {
		return report, needs, fmt.Errorf("%s doesn't take apps from this machine yet. On %s, as the account that runs hi, run:\n  hi app machines add %s %s\nor add it on %s's admin page (Machine, Other machines, Takes apps from); then move the app again", target.machine, target.machine, appMoveThisMachine(), key, target.machine)
	}
	if err != nil {
		return report, needs, fmt.Errorf("asking %s: %v", target.machine, err)
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		return report, needs, fmt.Errorf("%s answered something hi doesn't read (%s): is hi there older? hi update there", target.machine, qFirstLine(out, "nothing"))
	}
	var exposed []string
	for _, slot := range []string{appSlotProduction, appSlotPreview} {
		if state, ok := loadTeamSlot(name, slot); ok && state.Expose != "" && !strings.HasPrefix(state.Expose, appInternalExpose) {
			exposed = append(exposed, map[string]string{appSlotProduction: "production", appSlotPreview: "Stage"}[slot])
		}
	}
	if len(exposed) > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("%s %s on a public address; it starts again from %s, and keeps its link only with a name that stays (hi app domain)", strings.Join(exposed, " and "), map[bool]string{true: "is", false: "are"}[len(exposed) == 1], target.machine))
	}
	appMoveRemember(target.machine)
	return report, needs, nil
}

// appMoveTo is a machine this one moves apps to, as move/to.json keeps it.
type appMoveTo struct {
	Name  string    `json:"name"`
	Added time.Time `json:"added"`
}

func appMoveToPath() string { return filepath.Join(appMoveDir(), "to.json") }

func loadAppMoveTo() []appMoveTo {
	var machines []appMoveTo
	if data, err := os.ReadFile(appMoveToPath()); err == nil {
		json.Unmarshal(data, &machines)
	}
	return machines
}

func saveAppMoveTo(machines []appMoveTo) error {
	if err := os.MkdirAll(appMoveDir(), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(machines, "", "  ")
	return writeFileAtomic(appMoveToPath(), append(data, '\n'), 0o600)
}

// appMoveRemember adds a machine to the ones this one moves apps to.
func appMoveRemember(machine string) {
	machines := loadAppMoveTo()
	for _, known := range machines {
		if known.Name == machine {
			return
		}
	}
	saveAppMoveTo(append(machines, appMoveTo{Name: machine, Added: time.Now().UTC()}))
}

// appMoveForget takes a machine off them.
func appMoveForget(machine string) bool {
	machines := loadAppMoveTo()
	for i, known := range machines {
		if known.Name == machine {
			saveAppMoveTo(append(machines[:i], machines[i+1:]...))
			return true
		}
	}
	return false
}

// appMoveProbe says whether a machine takes apps from this one, and the
// hi it runs; it gives up after a few seconds.
var appMoveProbe = func(machine string) (takes bool, version string, problem string) {
	target, err := newAppMoveTarget(machine)
	if err != nil {
		return false, "", err.Error()
	}
	if _, err := appEnsureMoveKey(); err != nil {
		return false, "", err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := append(target.sshOptions(), "-o", "ConnectTimeout=6", "--", target.dest, "version")
	var out, errOut strings.Builder
	command := exec.CommandContext(ctx, "ssh", args...)
	command.Stdout, command.Stderr = &out, &errOut
	if err := command.Run(); err != nil {
		text := strings.TrimSpace(errOut.String())
		switch {
		case strings.Contains(text, "Permission denied"):
			return false, "", "doesn't take apps from this machine yet"
		case ctx.Err() != nil || strings.Contains(text, "timed out") || strings.Contains(text, "No route") || strings.Contains(text, "Could not resolve"):
			return false, "", "doesn't answer"
		}
		return false, "", qFirstLine(lastLines(text, 1), err.Error())
	}
	return true, strings.TrimSpace(out.String()), ""
}

// appStartWarnings are the warnings hi app up printed on the other
// machine, each with the lines that go with it (a command to run), up to
// the next blank line.
func appStartWarnings(out string) []string {
	var warnings []string
	current := ""
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "Warning"):
			if current != "" {
				warnings = append(warnings, current)
			}
			current = trimmed
		case current != "" && trimmed != "":
			current += " " + trimmed
		default:
			if current != "" {
				warnings = append(warnings, current)
			}
			current = ""
		}
	}
	if current != "" {
		warnings = append(warnings, current)
	}
	return warnings
}

func appMovePrintReport(report appMoveReport, target appMoveTarget, needs appMoveNeeds, stdout io.Writer) {
	fmt.Fprintf(stdout, "  %s runs hi %s; the folder goes to %s there.\n", target.machine, report.Version, needs.Folder)
	for _, problem := range report.Problems {
		fmt.Fprintf(stdout, "  Missing: %s\n", problem)
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(stdout, "  Note: %s\n", warning)
	}
}

// appMoveWaitForWork waits for a release, a snapshot, and running tasks to
// end, up to appRestartPatient.
func appMoveWaitForWork(name string, stdout io.Writer) error {
	pid := 0
	if out, err := appSystemctl("show", "-p", "MainPID", "--value", appUnitName(name)); err == nil {
		pid, _ = strconv.Atoi(strings.TrimSpace(out))
	}
	if pid > 0 && !appWaitIdle(name, pid, stdout) {
		return fmt.Errorf("app %s is still busy; move it when that's done, or with --now", name)
	}
	deadline := time.Now().Add(appRestartPatient)
	said := false
	for {
		var running []string
		for _, task := range loadTeamTasks(name) {
			if task.Status == "running" || task.Status == "starting" {
				running = append(running, task.ID)
			}
		}
		if len(running) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("tasks %s still run; move it when they're done, or with --now (their committed work stays on their branches)", strings.Join(running, ", "))
		}
		if !said {
			fmt.Fprintf(stdout, "Waiting for %s to finish (--now doesn't wait)...\n", strings.Join(running, ", "))
			said = true
		}
		time.Sleep(appRestartPoll)
	}
}

// appMoveWaitUp waits until the app's service, lead, and NetBird peer run
// there.
func appMoveWaitUp(target appMoveTarget, name string, stdout io.Writer) error {
	deadline := time.Now().Add(appMoveStartPatience)
	var status appReceivedStatus
	for time.Now().Before(deadline) {
		out, err := target.output(nil, "status", name)
		if err == nil && json.Unmarshal([]byte(out), &status) == nil && status.State == "up" && status.Lead && status.Peer {
			return nil
		}
		time.Sleep(appMovePoll)
	}
	return fmt.Errorf("app %s didn't come up on %s within %s (service %s, lead running %t, peer running %t)", name, target.machine, appMoveStartPatience, status.State, status.Lead, status.Peer)
}

// appMoveReachable says whether the app's dev page answers at its
// NetBird address from here within two minutes.
var appMoveReachable = func(ip string) bool {
	address := net.JoinHostPort(ip, "8888")
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); time.Sleep(3 * time.Second) {
		if conn, err := net.DialTimeout("tcp", address, 3*time.Second); err == nil {
			conn.Close()
			return true
		}
	}
	return false
}

// appMovePost says something in the app's channel, as the app's bot.
var appMovePost = func(config appConfig, secrets appSecrets, text string) {
	if secrets.SlackBotToken == "" || config.Channel == "" {
		return
	}
	newLiveTeamChannel(secrets.SlackBotToken).post(config.Channel, text)
}
