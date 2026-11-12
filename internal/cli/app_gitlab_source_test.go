package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// No test asks the machine's glab: a GitLab project is one only when a
// test says so.
var realGitLabSource = appGitLabSource

func init() {
	appGitLabSource = func(string) (string, string, bool) { return "", "", false }
}

// fakeGitLabSource makes address's host a GitLab this machine is signed in
// to, and answers for its project.
func fakeGitLabSource(t *testing.T, project appGitLabProject) {
	t.Helper()
	savedSource, savedLook := appGitLabSource, appGitLabLook
	appGitLabSource = func(address string) (string, string, bool) { return appGitLabAddress(address) }
	appGitLabLook = func(host, path string) (appGitLabProject, error) {
		if host+"/"+path != "gitlab.com/acme/web/portal" {
			t.Errorf("asked GitLab about %s/%s", host, path)
		}
		return project, nil
	}
	t.Cleanup(func() { appGitLabSource, appGitLabLook = savedSource, savedLook })
}

func TestAppGitLabAddress(t *testing.T) {
	for address, want := range map[string]string{
		"https://gitlab.com/acme/web/portal":          "gitlab.com acme/web/portal",
		"https://gitlab.com/acme/web/portal.git":      "gitlab.com acme/web/portal",
		"https://gitlab.example.com/team/billing/":    "gitlab.example.com team/billing",
		"git@gitlab.example.com:team/sub/billing.git": "gitlab.example.com team/sub/billing",
		"https://github.com/ada/pocket-kanban.git":    "",
		"https://user@gitlab.com/acme/portal":         "",
		"file:///srv/acme/portal":                     "",
		"https://gitlab.com/portal":                   "",
	} {
		host, path, ok := appGitLabAddress(address)
		got := ""
		if ok {
			got = host + " " + path
		}
		if got != want {
			t.Errorf("%s: %q, want %q", address, got, want)
		}
	}
}

func TestAppGitLabSourceNeedsGlabSignedIn(t *testing.T) {
	savedLook, savedGlab := appGlabLookPath, appGlab
	t.Cleanup(func() { appGlabLookPath, appGlab = savedLook, savedGlab })
	var asked []string
	appGlabLookPath = func() (string, error) { return "/usr/bin/glab", nil }
	appGlab = func(host, dir string, args ...string) (string, error) {
		asked = append(asked, host+" "+strings.Join(args, " "))
		if host == "gitlab.example.com" {
			return "not logged in", errors.New("exit status 1")
		}
		return "", nil
	}
	if host, path, ok := realGitLabSource("https://gitlab.com/acme/web/portal.git"); !ok || host != "gitlab.com" || path != "acme/web/portal" {
		t.Fatal(host, path, ok)
	}
	if _, _, ok := realGitLabSource("https://gitlab.example.com/team/billing"); ok {
		t.Fatal("a GitLab glab isn't signed in to")
	}
	if _, _, ok := realGitLabSource("https://github.com/ada/pocket-kanban.git"); ok || len(asked) != 2 {
		t.Fatal("GitHub taken for GitLab, or glab asked about it:", asked)
	}
	appGlabLookPath = func() (string, error) { return "", errors.New("no glab") }
	if _, _, ok := realGitLabSource("https://gitlab.com/acme/web/portal"); ok {
		t.Fatal("without glab")
	}
}

func TestAppGitLabLook(t *testing.T) {
	saved := appGlab
	t.Cleanup(func() { appGlab = saved })
	answers := map[string]string{}
	var asked []string
	appGlab = func(host, dir string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		asked = append(asked, call)
		if answer, ok := answers[call]; ok {
			return answer, nil
		}
		return "404 Not Found", errors.New("exit status 1")
	}
	project := "api projects/acme%2Fweb%2Fportal?statistics=true"

	// Owner through the project's group.
	answers[project] = `{"description": "The portal", "default_branch": "main", "visibility": "private", "statistics": {"repository_size": 3372466},
		"permissions": {"project_access": null, "group_access": {"access_level": 50}}}`
	got, err := appGitLabLook("gitlab.com", "acme/web/portal")
	if err != nil || got.Access != 50 || !got.canPush() || got.Info.Description != "The portal" || got.Info.DefaultBranch != "main" ||
		!got.Info.Private || got.Info.SizeKB != 3293 {
		t.Fatalf("%+v %v", got, err)
	}

	// Through a parent group: neither says, the project's members do.
	asked = nil
	answers[project] = `{"visibility": "public", "permissions": {"project_access": null, "group_access": null}}`
	answers["api user"] = `{"id": 42}`
	answers["api projects/acme%2Fweb%2Fportal/members/all/42"] = `{"access_level": 30}`
	if got, err = appGitLabLook("gitlab.com", "acme/web/portal"); err != nil || got.Access != 30 || !got.canPush() || got.Info.Private || len(asked) != 3 {
		t.Fatalf("%+v %v %v", got, err, asked)
	}

	// A Reporter reads but can't push; nor can anyone push to an archive.
	answers["api projects/acme%2Fweb%2Fportal/members/all/42"] = `{"access_level": 20}`
	if got, _ = appGitLabLook("gitlab.com", "acme/web/portal"); got.canPush() {
		t.Fatal("a Reporter can push")
	}
	answers[project] = `{"archived": true, "permissions": {"project_access": {"access_level": 50}}}`
	if got, _ = appGitLabLook("gitlab.com", "acme/web/portal"); got.canPush() {
		t.Fatal("pushes to an archived project")
	}

	delete(answers, project)
	if _, err := appGitLabLook("gitlab.com", "acme/web/portal"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatal(err)
	}
}

func TestAdminLookAtGitLab(t *testing.T) {
	fakeGitLabSource(t, appGitLabProject{Info: appRepoInfo{Description: "The portal", DefaultBranch: "main", Private: true, SizeKB: 3300}, Access: 40})
	look := adminLookAt("https://gitlab.com/acme/web/portal")
	if look.Error != "" || look.GitLab != "gitlab.com/acme/web/portal" || look.GitHub != "" || !look.CanPush || !look.Private ||
		look.Branch != "main" || look.Description != "The portal" || look.SizeMB != 3 {
		t.Fatalf("%+v", look)
	}
	fakeGitLabSource(t, appGitLabProject{Info: appRepoInfo{DefaultBranch: "main"}, Access: 20})
	if look := adminLookAt("https://gitlab.com/acme/web/portal"); look.CanPush || look.GitLab == "" {
		t.Fatalf("%+v", look)
	}
}

func TestTeamAdoptAsksGitLab(t *testing.T) {
	setupTeamTest(t)
	upstream := makeTestUpstream(t)
	fetched := fakeAdoptGitHub(t, upstream, appRepoInfo{}, false)
	fakeGitLabSource(t, appGitLabProject{Info: appRepoInfo{DefaultBranch: "main", Private: true}, Access: 40})
	dir := t.TempDir() + "/team-portal"

	adopt := &appAdoption{Source: "https://gitlab.com/acme/web/portal"}
	var out bytes.Buffer
	if ok, err := adopt.prepare(dir, strings.NewReader("y\n"), &out); !ok || err != nil || !adopt.Push || len(*fetched) != 0 {
		t.Fatal(ok, err, adopt.Push, out.String())
	}
	if !strings.Contains(out.String(), "You can push to gitlab.com/acme/web/portal with this machine's GitLab sign-in (glab).") {
		t.Fatal(out.String())
	}

	// No: the app keeps its own copy, and its name is the project's.
	adopt = &appAdoption{Source: "https://gitlab.com/acme/web/portal"}
	if ok, err := adopt.prepare(dir, strings.NewReader("\n\n"), &out); !ok || err != nil || adopt.Push || !fileExists(adopt.stage) || adopt.projectName() != "portal" {
		t.Fatal(ok, err, adopt.Push)
	}
	adopt.discard()

	// A Reporter isn't asked.
	fakeGitLabSource(t, appGitLabProject{Info: appRepoInfo{DefaultBranch: "main"}, Access: 20})
	out.Reset()
	adopt = &appAdoption{Source: "https://gitlab.com/acme/web/portal"}
	if ok, err := adopt.prepare(dir, strings.NewReader("\n"), &out); !ok || err != nil || adopt.Push || strings.Contains(out.String(), "push its merges") {
		t.Fatal(ok, err, out.String())
	}
	adopt.discard()
}

// The app works on the GitLab project itself: cloned and pushed to with
// glab's sign-in, set in the app's repository only.
func TestTeamAdoptPushesToGitLab(t *testing.T) {
	setupTeamTest(t)
	upstream := makeTestUpstream(t)
	fakeAdoptGitHub(t, upstream, appRepoInfo{}, false)
	fakeGitLabSource(t, appGitLabProject{Info: appRepoInfo{DefaultBranch: "main"}, Access: 40})
	// git fetches gitlab.com's address from the upstream folder.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+upstream+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://gitlab.com/acme/web/portal")

	var out bytes.Buffer
	answers := writeAdoptAnswers(t, `, "push_to_source": true`)
	if err := appAdoptCommand([]string{"portal", "https://gitlab.com/acme/web/portal", "--from", answers}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	config, err := loadTeam("portal")
	if err != nil {
		t.Fatal(err)
	}
	repo := appDir("portal") + "/repo"
	if config.Remote != "https://gitlab.com/acme/web/portal" || config.Adopted != nil || config.Branch != "main" {
		t.Fatalf("%+v", config)
	}
	if got := boxGit(repo, "config", "--local", "credential.https://gitlab.com.helper"); got != "!glab auth git-credential" {
		t.Fatalf("no glab sign-in in the app's repository: %q", got)
	}
	if boxGit(repo, "rev-parse", "HEAD") != boxGit(upstream, "rev-parse", "main") {
		t.Fatal("not the project's commit")
	}
}
