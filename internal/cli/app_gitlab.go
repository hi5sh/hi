package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// A team's project can go to GitLab instead of GitHub, gitlab.com or a
// GitLab of the organisation's own, with this machine's own sign-in
// (glab), as GitHub's goes with gh: hi creates or connects the project,
// and the broker pushes every merge from the host, through glab's
// credential helper, set in the app's repository only. No GitLab
// credential enters a box.

// appGitLabName is host/group/name, with any subgroups: gitlab.com/acme/
// billing, or gitlab.example.com/team/sub/billing.
var appGitLabName = regexp.MustCompile(`^[A-Za-z0-9.-]+\.[A-Za-z]{2,}(?::\d+)?(?:/[A-Za-z0-9_.][A-Za-z0-9_.-]*){2,20}$`)

// appGitLabParts splits a project into its host and path; a path without
// a host is on gitlab.com.
func appGitLabParts(name string) (host, path string, err error) {
	name = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(name), "https://"), "http://"), ".git")
	name = strings.TrimSuffix(name, "/")
	if first, _, _ := strings.Cut(name, "/"); !strings.Contains(first, ".") {
		name = "gitlab.com/" + name
	}
	if !appGitLabName.MatchString(name) {
		return "", "", fmt.Errorf("give the GitLab project as group/name, or host/group/name for a GitLab of your own, such as gitlab.example.com/team/billing, not %q", name)
	}
	host, path, _ = strings.Cut(name, "/")
	return host, path, nil
}

// appIsGitLabAnswer says an answer to "GitHub or GitLab" names a GitLab
// project: it starts with a host, such as gitlab.com/group/name. GitHub's
// are owner/name.
func appIsGitLabAnswer(answer string) bool {
	answer = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(answer), "https://"), "http://")
	first, _, _ := strings.Cut(answer, "/")
	return strings.Contains(first, ".") && first != "github.com"
}

// appGlab runs glab, for host; tests replace it.
var appGlab = func(host, dir string, args ...string) (string, error) {
	command := exec.Command("glab", args...)
	command.Dir = dir
	command.Env = os.Environ()
	if host != "gitlab.com" {
		command.Env = append(command.Env, "GITLAB_HOST="+host)
	}
	out, err := command.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

var appGlabLookPath = func() (string, error) { return exec.LookPath("glab") }

// appCheckGitLab checks the name, and that glab is installed and signed in
// to the project's GitLab.
func appCheckGitLab(name string) error {
	host, _, err := appGitLabParts(name)
	if err != nil {
		return err
	}
	if _, err := appGlabLookPath(); err != nil {
		return errors.New("GitLab needs glab on this machine: hi install glab, then glab auth login")
	}
	if out, err := appGlab(host, "", "auth", "status", "--hostname", host); err != nil {
		return fmt.Errorf("glab isn't signed in to %s (run glab auth login --hostname %s): %s", host, host, qFirstLine(out, err.Error()))
	}
	return nil
}

// appConnectGitLab puts the team's project on GitLab: a new private
// project, or an existing empty one, as the remote it pushes merges to.
func appConnectGitLab(config *appConfig, name string, stdout io.Writer) error {
	host, path, err := appGitLabParts(name)
	if err != nil {
		return err
	}
	repo := appDir(config.Name) + "/repo"
	if current := boxGit(repo, "remote", "get-url", "origin"); current != "" {
		return fmt.Errorf("the app's project already pushes to %s", current)
	}
	url := "https://" + host + "/" + path + ".git"
	existed := true
	if _, err := appGlab(host, "", "repo", "view", path); err != nil {
		existed = false
		if out, err := appGlab(host, "", "repo", "create", path, "--private"); err != nil {
			return fmt.Errorf("glab repo create: %s", qFirstLine(out, err.Error()))
		}
	}
	// Pushes from the app's repository sign in through glab, for this host
	// only; the machine's other git settings stay as they are.
	if out, err := appGit(repo, "config", "credential.https://"+host+".helper", "!glab auth git-credential"); err != nil {
		return fmt.Errorf("git config: %s", out)
	}
	if out, err := appGit(repo, "remote", "add", "origin", url); err != nil {
		return fmt.Errorf("git remote add: %s", out)
	}
	if out, err := appGit(repo, "push", "-q", "-u", "origin", config.Branch); err != nil {
		appGit(repo, "remote", "remove", "origin")
		if existed {
			return fmt.Errorf("%s exists and didn't take the app's project (is it empty?): %s", name, qFirstLine(out, err.Error()))
		}
		return fmt.Errorf("git push to %s: %s", url, qFirstLine(out, err.Error()))
	}
	if existed {
		fmt.Fprintf(stdout, "The project is on GitLab in %s, which already existed.\n", host+"/"+path)
	} else {
		fmt.Fprintf(stdout, "The project is on GitLab in a new private project: https://%s/%s\n", host, path)
	}
	config.Remote = url
	if err := saveTeam(*config); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "The app pushes every merge there, with this machine's GitLab sign-in (glab).")
	return nil
}
