package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const testBunLock = `{
  "lockfileVersion": 1,
  "workspaces": {
    "": {
      "name": "app",
      "dependencies": {
        "react": "^19.3.0",
      },
    },
  },
  "packages": {
    "@types/react": ["@types/react@19.3.0", "", { "dependencies": { "csstype": "^3.0.2" } }, "sha512-a"],
    "csstype": ["csstype@3.1.3", "", {}, "sha512-b"],
    "react": ["react@19.3.0", "", {}, "sha512-c"],
  }
}
`

func TestTeamLockPackagesReadsBunLock(t *testing.T) {
	packages := teamLockPackages("bun.lock", testBunLock)
	if len(packages) != 3 || packages["@types/react"][0] != "19.3.0" || packages["react"][0] != "19.3.0" {
		t.Fatalf("%v", packages)
	}
	// A comma inside a string stays.
	if out := string(teamStripTrailingCommas([]byte(`{"a": "x,}", "b": [1, 2,],}`))); out != `{"a": "x,}", "b": [1, 2]}` {
		t.Fatal(out)
	}
	lock := `{"packages": {"": {"name": "app"}, "node_modules/react": {"version": "19.3.0"}, "node_modules/a/node_modules/react": {"version": "18.0.0"}}}`
	if packages := teamLockPackages("package-lock.json", lock); len(packages["react"]) != 2 || len(packages) != 1 {
		t.Fatalf("%v", packages)
	}
}

func TestTeamLockDiff(t *testing.T) {
	added, removed, changed := teamLockDiff(
		map[string][]string{"react": {"19.2.0"}, "lodash": {"4.17.21"}, "csstype": {"3.1.3"}},
		map[string][]string{"react": {"19.3.0"}, "zod": {"4.1.0"}, "csstype": {"3.1.3"}})
	if strings.Join(added, ",") != "zod@4.1.0" || strings.Join(removed, ",") != "lodash@4.17.21" || strings.Join(changed, ",") != "react 19.2.0 → 19.3.0" {
		t.Fatal(added, removed, changed)
	}
}

func TestTeamDependencyChanges(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		os.MkdirAll(filepath.Dir(filepath.Join(repo, name)), 0o755)
		os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644)
	}
	git("init", "-q", "-b", "main")
	write("web/package.json", `{"dependencies": {"react": "^19.3.0", "lodash": "^4"}, "devDependencies": {"vite": "^8.3.2"}}`)
	write("web/bun.lock", testBunLock)
	write("README.md", "x\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	base := git("rev-parse", "HEAD")

	if projects := teamDependencyChanges(repo, base, base); len(projects) != 0 {
		t.Fatalf("%+v", projects)
	}
	write("web/package.json", `{"dependencies": {"react": "^19.3.0", "sharp": "^0.34.0"}, "devDependencies": {"vite": "^8.4.0"}, "trustedDependencies": ["sharp"]}`)
	write("web/bun.lock", strings.Replace(testBunLock, `"react": ["react@19.3.0"`, `"sharp": ["sharp@0.34.0", "", {}, "sha512-d"],
    "react": ["react@19.3.0"`, 1))
	write("web/node_modules/x/package.json", `{"dependencies": {"y": "1"}}`)
	git("add", "-A")
	git("commit", "-q", "-m", "sharp")
	projects := teamDependencyChanges(repo, base, "HEAD")
	if len(projects) != 1 {
		t.Fatalf("%+v", projects)
	}
	p := projects[0]
	if p.Dir != "web" || strings.Join(p.Added, ",") != "sharp ^0.34.0" || strings.Join(p.Removed, ",") != "lodash" ||
		strings.Join(p.Changed, ",") != "vite ^8.3.2 (dev) → ^8.4.0 (dev)" || strings.Join(p.Trusted, ",") != "sharp" ||
		p.Lockfile != "bun.lock" || strings.Join(p.LockAdded, ",") != "sharp@0.34.0" || p.Stale {
		t.Fatalf("%+v", p)
	}
	text := teamDependencyText(projects)
	for _, want := range []string{"web/:", "Adds: sharp ^0.34.0", "Removes: lodash", "bun.lock: 1 packages added", "trustedDependencies", "sharp"} {
		if !strings.Contains(text, want) {
			t.Fatalf("no %q in\n%s", want, text)
		}
	}

	// package.json changed, bun.lock didn't.
	write("web/package.json", `{"dependencies": {"react": "^19.3.0", "sharp": "^0.34.0", "zod": "^4"}, "devDependencies": {"vite": "^8.4.0"}, "trustedDependencies": ["sharp"]}`)
	git("commit", "-q", "-am", "zod")
	if projects := teamDependencyChanges(repo, "HEAD^", "HEAD"); len(projects) != 1 || !projects[0].Stale || len(projects[0].Trusted) != 0 {
		t.Fatalf("%+v", projects)
	}
}
