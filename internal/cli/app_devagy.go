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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
