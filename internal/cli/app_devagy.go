package cli

// Antigravity (agy) as an agent of the dev box, beside Claude Code and
// Codex. Unlike theirs, agy's sign-in can't stay out of the box: agy
// calls Google at an address of its own, with no setting to send it
// through the box's proxy, so the box holds the sign-in itself, and
// anyone with SSH into the box (owners, Pro Devs) can copy it. So it's
// the app's own Google account's, made for the app (hi app signin <app>
// agy --file), or, when an admin says so, this machine's (--machine).
// Without one, the dev box has no agy. On each start of the app's
// service hi links the machine's agy into the box's /box/bin, puts the
// sign-in in its home, and lets the box reach Google's agy sites.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// appAgySignIn is an app's Antigravity sign-in, in secrets.json: the
// token file agy wrote for the app's Google account, or this machine's.
type appAgySignIn struct {
	Token   string    `json:"token,omitempty"` // agy's antigravity-oauth-token, as it wrote it
	Machine bool      `json:"machine,omitempty"`
	Email   string    `json:"email,omitempty"` // the account, as the token says
	At      time.Time `json:"at"`
}

// appDevAgyHome is where agy keeps its sign-in and settings in the box.
const appDevAgyHome = ".gemini/antigravity-cli"

// appDevAgyMarker says the sign-in in the box's home is hi's, so hi may
// take it away again.
const appDevAgyMarker = ".hi-signin"

// appAgyTokenCheck reads an agy token file: its account's email, or why
// it isn't one.
func appAgyTokenCheck(data []byte) (string, error) {
	var token struct {
		Token struct {
			Refresh string `json:"refresh_token"`
		} `json:"token"`
		Email string `json:"id_token_email"`
	}
	if err := json.Unmarshal(data, &token); err != nil || token.Token.Refresh == "" {
		return "", errors.New("that isn't agy's sign-in: it's the file ~/.gemini/antigravity-cli/antigravity-oauth-token, after signing in to agy")
	}
	return token.Email, nil
}

// appAgyToken is an app's agy sign-in as agy keeps it, or "" when the app
// has none.
func appAgyToken(team string) (string, error) {
	secrets, err := loadTeamSecrets(team)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil // no secrets: no sign-in either
	} else if err != nil || secrets.Agy == nil {
		return "", err
	}
	if secrets.Agy.Machine {
		data, err := os.ReadFile(agyTokenPath())
		if err != nil {
			return "", errors.New("the app uses this machine's agy sign-in, and there's none: run agy once and sign in with Google")
		}
		return string(data), nil
	}
	return secrets.Agy.Token, nil
}

// appDevAgyHosts are the sites agy needs, when the app has a sign-in.
func appDevAgyHosts(team string) []string {
	if secrets, err := loadTeamSecrets(team); err != nil || secrets.Agy == nil {
		return nil
	}
	return agentHosts["agy"]
}

// prepareTeamDevAgy puts agy in the dev box, with the app's sign-in, or
// takes hi's sign-in away when the app has none. It says whether the box
// has agy.
func prepareTeamDevAgy(team string) (bool, error) {
	home := appDevHome(team)
	state := filepath.Join(home, filepath.FromSlash(appDevAgyHome))
	token, err := appAgyToken(team)
	if err != nil {
		return false, err
	}
	bin := filepath.Join(appDevDir(team), "bin", "agy")
	if token == "" {
		if fileExists(filepath.Join(state, appDevAgyMarker)) {
			os.Remove(filepath.Join(state, "antigravity-oauth-token"))
			os.Remove(filepath.Join(state, appDevAgyMarker))
		}
		os.Remove(bin)
		return false, nil
	}
	binary, err := boxHostBinary("agy")
	if err != nil {
		return false, errors.New("Antigravity isn't installed on this machine: hi install agy")
	}
	if err := appDevLinkBinary(binary, bin); err != nil {
		return false, err
	}
	if err := boxedMkdirAll(home, state, 0o700); err != nil {
		return false, err
	}
	// The box keeps its own copy fresh (agy refreshes the access token in
	// it); a new sign-in, or another account's, replaces it.
	current, _ := boxedReadFile(home, filepath.Join(state, "antigravity-oauth-token"))
	if appAgySameAccount(current, []byte(token)) != nil {
		if err := boxedWriteFile(home, filepath.Join(state, "antigravity-oauth-token"), []byte(token), 0o600); err != nil {
			return false, err
		}
	}
	if err := boxedWriteFile(home, filepath.Join(state, appDevAgyMarker), []byte("The sign-in beside this is hi's (hi app signin "+team+" agy): it's replaced or removed with it.\n"), 0o600); err != nil {
		return false, err
	}
	// agy's first run asks for a color scheme: the box's terminal's own,
	// as an owner may still change in agy.
	onboarding := filepath.Join(state, "cache", "onboarding.json")
	if _, err := boxedReadFile(home, onboarding); errors.Is(err, fs.ErrNotExist) {
		if err := boxedMkdirAll(home, filepath.Dir(onboarding), 0o700); err == nil {
			boxedWriteFile(home, onboarding, []byte(`{"consumerOnboardingComplete": true, "enterpriseOnboardingComplete": false, "onboardingComplete": true}`+"\n"), 0o600)
		}
	}
	// hi's hook, for the agent's state, beside the box's others.
	hooksFile := filepath.Join(home, ".gemini", "config", "hooks.json")
	if err := boxedMkdirAll(home, filepath.Dir(hooksFile), 0o700); err != nil {
		return false, err
	}
	if data, ok := boxedReadToUpdate(home, hooksFile); ok {
		hooks := map[string]any{}
		if len(data) == 0 || json.Unmarshal(data, &hooks) == nil {
			if appDevAgyHooks(hooks) {
				out, _ := json.MarshalIndent(hooks, "", "  ")
				if err := boxedWriteFile(home, hooksFile, append(out, '\n'), 0o600); err != nil {
					return false, err
				}
			}
		}
	}
	// The box's folders are trusted, so agy doesn't ask about each.
	settings := map[string]any{}
	data, ok := boxedReadToUpdate(home, filepath.Join(state, "settings.json"))
	if !ok {
		return true, nil
	}
	if len(data) > 0 {
		json.Unmarshal(data, &settings)
	}
	trusted, _ := settings["trustedWorkspaces"].([]any)
	changed := false
	for _, dir := range []string{"/box/repo", "/box/home/.herdr/worktrees", "/box/home"} {
		if !slices.Contains(trusted, any(dir)) {
			trusted = append(trusted, dir)
			changed = true
		}
	}
	if changed {
		settings["trustedWorkspaces"] = trusted
		out, _ := json.MarshalIndent(settings, "", "  ")
		if err := boxedWriteFile(home, filepath.Join(state, "settings.json"), out, 0o600); err != nil {
			return false, err
		}
	}
	return true, nil
}

// appAgySameAccount is nil when the box's sign-in is the same account's
// refresh token as the app's: agy's own refreshes of it stay.
func appAgySameAccount(current, token []byte) error {
	var a, b struct {
		Token struct {
			Refresh string `json:"refresh_token"`
		} `json:"token"`
	}
	if json.Unmarshal(current, &a) != nil || json.Unmarshal(token, &b) != nil || a.Token.Refresh == "" || a.Token.Refresh != b.Token.Refresh {
		return errors.New("another sign-in")
	}
	return nil
}

// appDevLinkBinary puts a binary of the machine's in the box's /box/bin:
// a hard link, which costs nothing, else a copy; again when the machine's
// changed.
func appDevLinkBinary(from, to string) error {
	src, err := os.Stat(from)
	if err != nil {
		return err
	}
	if dst, err := os.Stat(to); err == nil && os.SameFile(src, dst) {
		return nil
	} else if err == nil && dst.Size() == src.Size() && dst.ModTime().Equal(src.ModTime()) {
		return nil
	}
	os.MkdirAll(filepath.Dir(to), 0o700)
	tmp := to + ".new"
	os.Remove(tmp)
	if err := os.Link(from, tmp); err != nil {
		in, err := os.Open(from)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			os.Remove(tmp)
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		os.Chtimes(tmp, src.ModTime(), src.ModTime())
	}
	return os.Rename(tmp, to)
}

// appSigninAgy is hi app signin <name> agy --file <token> | --machine | --unset.
func appSigninAgy(config appConfig, args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi app signin <name> agy --file <antigravity-oauth-token> | --machine | --unset"}
	var sign *appAgySignIn
	switch {
	case len(args) == 1 && args[0] == "--unset":
	case len(args) == 1 && args[0] == "--machine":
		data, err := os.ReadFile(agyTokenPath())
		if err != nil {
			return errors.New("agy isn't signed in on this machine: run agy once and sign in with Google")
		}
		email, err := appAgyTokenCheck(data)
		if err != nil {
			return err
		}
		sign = &appAgySignIn{Machine: true, Email: email, At: time.Now().UTC()}
		fmt.Fprintf(stdout, "Careful: the dev box holds agy's sign-in, so anyone with SSH into it (the app's owners and Pro Devs) can copy this machine's Google sign-in (%s). Use an account made for the app where that matters: hi app signin %s agy --file <token>.\n", firstNonEmpty(email, "its account"), config.Name)
	case len(args) == 2 && args[0] == "--file":
		data, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		email, err := appAgyTokenCheck(data)
		if err != nil {
			return err
		}
		sign = &appAgySignIn{Token: string(data), Email: email, At: time.Now().UTC()}
	default:
		return usage
	}
	if err := updateTeamSecrets(config.Name, func(secrets *appSecrets) error {
		secrets.Agy = sign
		return nil
	}); err != nil {
		return err
	}
	if sign == nil {
		fmt.Fprintf(stdout, "App %s has no agy sign-in now: its dev box loses agy and Google's agy sites at the app's next start.\n", config.Name)
	} else {
		fmt.Fprintf(stdout, "App %s's dev box runs agy as %s from the app's next start (hi app restart %s, or hi update --restart); its agents start it with agy.\n", config.Name, firstNonEmpty(sign.Email, "that account"), config.Name)
	}
	return nil
}

// appAgyState is the app's agy sign-in, for the dev page's keys.
func appAgyState(secrets appSecrets) string {
	switch {
	case secrets.Agy == nil:
		return "None: no agy in the dev box"
	case secrets.Agy.Machine:
		return "This machine's (" + firstNonEmpty(secrets.Agy.Email, "its Google account") + ")"
	}
	return "The app's own (" + firstNonEmpty(secrets.Agy.Email, "a Google account") + "), since " + secrets.Agy.At.Format("Jan 2, 2006")
}

// appDevHasAgy says the dev box runs agy, for the page's agents to start.
func appDevHasAgy(team string) bool {
	return fileExists(filepath.Join(appDevDir(team), "bin", "agy"))
}

// ---------------------------------------------------------------------------
// agy in Chat: its transcript, its questions, its sessions

// appDevAgyBrain is where agy keeps each conversation, in the box: its
// transcript is <id>/.system_generated/logs/transcript.jsonl.
const (
	appDevAgyBrain      = "/box/home/.gemini/antigravity-cli/brain"
	appDevAgyTranscript = "/.system_generated/logs/transcript.jsonl"
)

func appDevIsAgySession(session string) bool {
	clean := path.Clean(session)
	return strings.HasPrefix(clean, appDevAgyBrain+"/") && strings.HasSuffix(clean, appDevAgyTranscript) &&
		strings.Count(strings.TrimSuffix(strings.TrimPrefix(clean, appDevAgyBrain+"/"), appDevAgyTranscript), "/") == 0
}

// appDevAgyID is a conversation's id, from its transcript's path.
func appDevAgyID(session string) string {
	return strings.TrimSuffix(strings.TrimPrefix(path.Clean(session), appDevAgyBrain+"/"), appDevAgyTranscript)
}

// appDevAgyIDPattern is an agy conversation's id.
var appDevAgyIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var (
	appDevAgyRequest = regexp.MustCompile(`(?s)<USER_REQUEST>\s*(.*?)\s*</USER_REQUEST>`)
	appDevAgyStamps  = regexp.MustCompile(`(?m)\A(?:(?:Created|Completed) At: [^\n]*\n)+`)
)

// appDevAgyEntries are one step of agy's transcript, as Chat draws it. A
// tool call's result is the step after it (the n-th call of step s is
// step s+1+n), so each call takes its result's step as its id.
func appDevAgyEntries(line []byte) []appDevChatEntry {
	var step struct {
		Index     int       `json:"step_index"`
		Type      string    `json:"type"`
		Status    string    `json:"status"`
		Source    string    `json:"source"`
		At        time.Time `json:"created_at"`
		Content   string    `json:"content"`
		Thinking  string    `json:"thinking"`
		ToolCalls []struct {
			Name string            `json:"name"`
			Args map[string]string `json:"args"`
		} `json:"tool_calls"`
	}
	if json.Unmarshal(line, &step) != nil {
		return nil
	}
	at := step.At
	switch step.Type {
	case "USER_INPUT":
		said := step.Content
		if m := appDevAgyRequest.FindStringSubmatch(said); m != nil {
			said = m[1]
		} else if strings.HasPrefix(strings.TrimSpace(said), "<") {
			return nil // what agy adds, not the person's words
		}
		if said = strings.TrimSpace(said); said == "" {
			return nil
		}
		return []appDevChatEntry{{T: "user", Text: said, At: at, UUID: strconv.Itoa(step.Index)}}
	case "PLANNER_RESPONSE":
		var entries []appDevChatEntry
		if s := strings.TrimSpace(step.Thinking); s != "" {
			entries = append(entries, appDevChatEntry{T: "thinking", Text: appDevClip(s, appDevChatResultMax), At: at})
		}
		if s := strings.TrimSpace(step.Content); s != "" {
			entries = append(entries, appDevChatEntry{T: "text", Text: s, At: at})
		}
		for i, call := range step.ToolCalls {
			arg := func(key string) string {
				var s string
				if json.Unmarshal([]byte(call.Args[key]), &s) == nil {
					return s
				}
				return call.Args[key]
			}
			name, summary, detail := call.Name, "", ""
			switch call.Name {
			case "run_command":
				name, detail = "Bash", arg("CommandLine")
				summary = firstNonEmpty(arg("toolSummary"), strings.SplitN(detail, "\n", 2)[0])
			case "read_file", "view_file":
				name, summary = "Read", arg("Path")
			case "edit_file", "write_file", "replace_file_content", "multi_replace_file_content":
				name, summary = "Edit", arg("Path")
			case "ask_question":
				name, summary = "Question", appDevAgyQuestions(arg("questions"))
				detail = summary
			default:
				summary = arg("toolSummary")
			}
			if detail == "" {
				data, _ := json.MarshalIndent(call.Args, "", "  ")
				detail = string(data)
			}
			entries = append(entries, appDevChatEntry{T: "tool", ID: strconv.Itoa(step.Index + 1 + i), Name: name, Summary: summary,
				Detail: appDevClip(detail, appDevChatInputMax), At: at})
		}
		return entries
	case "GENERIC":
		if step.Source != "MODEL" {
			return nil
		}
		text := appDevAgyStamps.ReplaceAllString(step.Content, "")
		failed := step.Status == "ERROR" || regexp.MustCompile(`(?m)^Command exited with code [1-9]`).MatchString(text)
		return []appDevChatEntry{{T: "result", ID: strconv.Itoa(step.Index), Text: appDevClip(strings.TrimSpace(text), appDevChatResultMax), Error: failed, At: at}}
	}
	return nil
}

// appDevAgyQuestions are ask_question's questions, a line each.
func appDevAgyQuestions(raw string) string {
	var questions []struct {
		Question string `json:"question"`
	}
	if json.Unmarshal([]byte(raw), &questions) != nil {
		return "a question"
	}
	var lines []string
	for _, q := range questions {
		if q.Question != "" {
			lines = append(lines, q.Question)
		}
	}
	return firstNonEmpty(strings.Join(lines, " · "), "a question")
}

// appDevAgyAsking says agy waits for answers to its questions: its last
// ask_question has no result yet.
func appDevAgyAsking(team, host string) bool {
	f, err := boxedOpenFile(appDevHome(team), host)
	if err != nil {
		return false
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > 512<<10 {
		f.Seek(info.Size()-512<<10, io.SeekStart)
	}
	reader := bufio.NewReaderSize(f, 1<<20)
	asked, answered := map[string]bool{}, map[string]bool{}
	for {
		line, _, err := appReadBoxLine(reader)
		if err != nil {
			break
		}
		for _, entry := range appDevAgyEntries(line) {
			switch {
			case entry.T == "tool" && entry.Name == "Question":
				asked[entry.ID] = true
			case entry.T == "result":
				answered[entry.ID] = true
			}
		}
	}
	for id := range asked {
		if !answered[id] {
			return true
		}
	}
	return false
}

type appDevAgyFile struct {
	box, host, id string
	at            time.Time
}

// appDevAgyFiles are agy's transcripts in the box, newest first.
func appDevAgyFiles(team string) []appDevAgyFile {
	root, ok := appDevHostPath(team, appDevAgyBrain)
	if !ok {
		return nil
	}
	entries, _ := os.ReadDir(root)
	var files []appDevAgyFile
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		box := appDevAgyBrain + "/" + entry.Name() + appDevAgyTranscript
		host, _ := appDevHostPath(team, box)
		info, err := os.Lstat(host)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, appDevAgyFile{box: box, host: host, id: entry.Name(), at: info.ModTime().UTC()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].at.After(files[j].at) })
	return files
}

// appDevAgyIn says a transcript is of work in cwd: agy's transcript names
// no folder of its own, but its tools' paths and commands' output do.
func appDevAgyIn(team, host, cwd string) bool {
	f, err := boxedOpenFile(appDevHome(team), host)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 512<<10)
	n, _ := io.ReadFull(f, head)
	return bytes.Contains(head[:n], []byte(cwd+"/")) || bytes.Contains(head[:n], []byte(cwd+"\\r\\n")) || bytes.Contains(head[:n], []byte(cwd+"\\n"))
}

// appDevLatestAgySession is the newest of a folder's agy conversations.
func appDevLatestAgySession(team, cwd string) string {
	for _, found := range appDevAgyFiles(team) {
		if appDevAgyIn(team, found.host, cwd) {
			return found.box
		}
	}
	return ""
}

// appDevAgyTrust trusts a folder for agy before it starts there: agy asks
// for each folder, not for the folders in one it trusts.
func appDevAgyTrust(team, dir string) error {
	home := appDevHome(team)
	settingsPath := filepath.Join(home, filepath.FromSlash(appDevAgyHome), "settings.json")
	data, ok := boxedReadToUpdate(home, settingsPath)
	if !ok {
		return nil
	}
	settings := map[string]any{}
	if len(data) > 0 {
		json.Unmarshal(data, &settings)
	}
	trusted, _ := settings["trustedWorkspaces"].([]any)
	if slices.Contains(trusted, any(dir)) {
		return nil
	}
	settings["trustedWorkspaces"] = append(trusted, dir)
	out, _ := json.MarshalIndent(settings, "", "  ")
	if err := boxedMkdirAll(home, filepath.Dir(settingsPath), 0o700); err != nil {
		return err
	}
	return boxedWriteFile(home, settingsPath, out, 0o600)
}
