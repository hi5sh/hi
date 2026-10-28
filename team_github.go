package main

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

// A team's project goes to GitHub with this machine's own sign-in (gh):
// hi creates or connects the repository, and the broker pushes every merge
// from the host. No GitHub credential enters a box, since anyone in the
// channel can steer the lead.

var teamGitHubName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$`)

// teamGH runs gh; tests replace it.
var teamGH = func(dir string, args ...string) (string, error) {
	command := exec.Command("gh", args...)
	command.Dir = dir
	out, err := command.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// teamCheckGitHub checks the name, and that gh is installed and signed in.
func teamCheckGitHub(name string) error {
	if !teamGitHubName.MatchString(name) {
		return fmt.Errorf("give the repository as owner/name, such as octocat/world-clock, not %q", name)
	}
	if _, err := teamGHLookPath(); err != nil {
		return errors.New("GitHub needs gh on this machine: hi install gh, then gh auth login")
	}
	if out, err := teamGH("", "auth", "status"); err != nil {
		return fmt.Errorf("gh isn't signed in to GitHub (run gh auth login): %s", qFirstLine(out, err.Error()))
	}
	return nil
}

var teamGHLookPath = func() (string, error) { return exec.LookPath("gh") }

// teamConnectGitHub puts the team's project on GitHub: a new private
// repository, or an existing empty one, as the remote it pushes merges to.
func teamConnectGitHub(config *teamConfig, name string, stdout io.Writer) error {
	repo := teamDir(config.Name) + "/repo"
	if current := boxGit(repo, "remote", "get-url", "origin"); current != "" {
		return fmt.Errorf("the team's project already pushes to %s", current)
	}
	url := "https://github.com/" + name + ".git"
	if _, err := teamGH("", "repo", "view", name, "--json", "name"); err == nil {
		// It exists: connect it, if it takes the team's history.
		if out, err := teamGit(repo, "remote", "add", "origin", url); err != nil {
			return fmt.Errorf("git remote add: %s", out)
		}
		if out, err := teamGit(repo, "push", "-q", "-u", "origin", config.Branch); err != nil {
			teamGit(repo, "remote", "remove", "origin")
			return fmt.Errorf("%s exists and didn't take the team's project (is it empty?): %s", name, qFirstLine(out, err.Error()))
		}
		fmt.Fprintf(stdout, "The project is on GitHub in %s, which already existed.\n", name)
	} else {
		if out, err := teamGH(repo, "repo", "create", name, "--private", "--source", ".", "--remote", "origin", "--push"); err != nil {
			return fmt.Errorf("gh repo create: %s", qFirstLine(out, err.Error()))
		}
		fmt.Fprintf(stdout, "The project is on GitHub in a new private repository: https://github.com/%s\n", name)
	}
	config.Remote = url
	if err := saveTeam(*config); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "The team pushes every merge there, with this machine's GitHub sign-in.")
	return nil
}
