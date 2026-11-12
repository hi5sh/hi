package cli

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// Someone's project on GitLab, adopted as GitHub's is: fetched with this
// machine's GitLab sign-in (glab) when it's private, and, when that
// sign-in may push there and the owner says so, the project the app
// pushes its merges to, as a GitHub project of the owner's own is.

// appGitLabSource says whether a repository's address is a project on a
// GitLab this machine's glab is signed in to: its host and its path,
// group/name with any subgroups. GitHub's addresses never are.
var appGitLabSource = func(address string) (host, path string, ok bool) {
	host, path, ok = appGitLabAddress(address)
	if !ok {
		return "", "", false
	}
	if _, err := appGlabLookPath(); err != nil {
		return "", "", false
	}
	if _, err := appGlab(host, "", "auth", "status", "--hostname", host); err != nil {
		return "", "", false
	}
	return host, path, true
}

// appGitLabAddress takes a repository's address apart: https://host/
// group/name(.git), or git@host:group/name(.git).
func appGitLabAddress(address string) (host, path string, ok bool) {
	address = strings.TrimSpace(address)
	if rest, found := strings.CutPrefix(address, "git@"); found {
		host, path, ok = strings.Cut(rest, ":")
	} else {
		parsed, err := url.Parse(address)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
			return "", "", false
		}
		host, path, ok = parsed.Host, parsed.Path, true
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if !ok || host == "" || host == "github.com" || !appGitLabName.MatchString(host+"/"+path) {
		return "", "", false
	}
	return host, path, true
}

// appGitLabCredential is git's setting that signs in to host through glab.
func appGitLabCredential(host string) (key, value string) {
	return "credential.https://" + host + ".helper", "!glab auth git-credential"
}

// appGitLabProject is what GitLab says about a project, and how far this
// machine's sign-in may go in it.
type appGitLabProject struct {
	Info appRepoInfo
	// Access is GitLab's level: 30 Developer, 40 Maintainer, 50 Owner.
	Access int
}

// appGitLabPushLevel is the least access that may push: a Developer's, as
// GitHub's write. A protected branch can still refuse it, as on GitHub.
const appGitLabPushLevel = 30

func (project appGitLabProject) canPush() bool {
	return project.Access >= appGitLabPushLevel && !project.Info.Archived
}

// appGitLabLook asks GitLab about a project; tests replace it.
var appGitLabLook = func(host, path string) (appGitLabProject, error) {
	var project appGitLabProject
	id := url.PathEscape(path) // group%2Fname, as GitLab takes a project's path
	out, err := appGlab(host, "", "api", "projects/"+id+"?statistics=true")
	if err != nil {
		return project, errors.New(qFirstLine(out, err.Error()))
	}
	var answer struct {
		Description   string `json:"description"`
		DefaultBranch string `json:"default_branch"`
		Visibility    string `json:"visibility"`
		Archived      bool   `json:"archived"`
		Statistics    struct {
			RepositorySize int64 `json:"repository_size"`
		} `json:"statistics"`
		Permissions struct {
			Project *struct {
				Level int `json:"access_level"`
			} `json:"project_access"`
			Group *struct {
				Level int `json:"access_level"`
			} `json:"group_access"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(out), &answer); err != nil {
		return project, errors.New("GitLab's answer isn't what hi expects")
	}
	project.Info = appRepoInfo{Description: answer.Description, DefaultBranch: answer.DefaultBranch,
		SizeKB: int(answer.Statistics.RepositorySize >> 10), Private: answer.Visibility != "public", Archived: answer.Archived}
	if p := answer.Permissions.Project; p != nil {
		project.Access = max(project.Access, p.Level)
	}
	if g := answer.Permissions.Group; g != nil {
		project.Access = max(project.Access, g.Level)
	}
	// Access through a parent group shows in neither; the project's
	// members, with those it inherits, say.
	if project.Access < appGitLabPushLevel {
		if out, err := appGlab(host, "", "api", "user"); err == nil {
			var user struct {
				ID int `json:"id"`
			}
			if json.Unmarshal([]byte(out), &user) == nil && user.ID != 0 {
				if out, err := appGlab(host, "", "api", "projects/"+id+"/members/all/"+strconv.Itoa(user.ID)); err == nil {
					var member struct {
						Level int `json:"access_level"`
					}
					if json.Unmarshal([]byte(out), &member) == nil {
						project.Access = max(project.Access, member.Level)
					}
				}
			}
		}
	}
	return project, nil
}
