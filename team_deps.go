package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path"
	"sort"
	"strings"
)

// A task that changes a JavaScript project's packages gets a dependency
// review (docs/specs/approved/hi_bun.md): the reviewer's brief says what
// the task adds, removes, and updates, and a package newly allowed to run
// its install script needs an owner's yes before the merge. Everything is
// read from git on the host: no project code runs to find it.
//
// Bun runs a package's install script only when the package is in
// package.json's trustedDependencies or on Bun's own list of vetted
// packages, so a new name in trustedDependencies is the change that lets
// new code run on every machine that installs the project.

// teamDependencyProject is what changed in one package.json and its
// lockfile; Dir is the folder, "" for the project's root.
type teamDependencyProject struct {
	Dir     string   `json:"dir,omitempty"`
	Added   []string `json:"added,omitempty"`   // "name range", "(dev)" for a dev dependency
	Removed []string `json:"removed,omitempty"` // names
	Changed []string `json:"changed,omitempty"` // "name a → b"
	// Lockfile is bun.lock or package-lock.json; the Lock lists are every
	// package it installs, including what the direct ones need.
	Lockfile    string   `json:"lockfile,omitempty"`
	LockAdded   []string `json:"lock_added,omitempty"` // name@version
	LockRemoved []string `json:"lock_removed,omitempty"`
	LockChanged []string `json:"lock_changed,omitempty"` // "name a → b"
	// Trusted are names newly in trustedDependencies.
	Trusted []string `json:"trusted,omitempty"`
	// Stale says package.json's dependencies changed but its lockfile
	// didn't, so a frozen install fails.
	Stale bool `json:"stale,omitempty"`
}

func (p teamDependencyProject) empty() bool {
	return len(p.Added)+len(p.Removed)+len(p.Changed)+len(p.LockAdded)+len(p.LockRemoved)+len(p.LockChanged)+len(p.Trusted) == 0 && !p.Stale
}

// teamDependencyChanges compares the package.json files and lockfiles that
// changed between two commits.
func teamDependencyChanges(repo, from, to string) []teamDependencyProject {
	dirs := map[string]bool{}
	for _, file := range strings.Fields(boxGit(repo, "diff", "--name-only", from, to)) {
		name := path.Base(file)
		if strings.Contains("/"+file, "/node_modules/") || (name != "package.json" && name != "bun.lock" && name != "package-lock.json") {
			continue
		}
		dirs[path.Dir(file)] = true
	}
	var projects []teamDependencyProject
	for _, dir := range sortedKeys(dirs) {
		project := teamDependencyProjectChange(repo, from, to, dir)
		if !project.empty() {
			projects = append(projects, project)
		}
	}
	return projects
}

func teamDependencyProjectChange(repo, from, to, dir string) teamDependencyProject {
	file := func(name string) string {
		if dir == "." {
			return name
		}
		return dir + "/" + name
	}
	project := teamDependencyProject{Dir: strings.TrimPrefix(dir, ".")}
	before, after := teamReadPackageJSON(repo, from, file("package.json")), teamReadPackageJSON(repo, to, file("package.json"))
	for name, version := range after.deps {
		if old, ok := before.deps[name]; !ok {
			project.Added = append(project.Added, name+" "+version)
		} else if old != version {
			project.Changed = append(project.Changed, name+" "+old+" → "+version)
		}
	}
	for name := range before.deps {
		if _, ok := after.deps[name]; !ok {
			project.Removed = append(project.Removed, name)
		}
	}
	for _, name := range after.trusted {
		if !containsString(before.trusted, name) {
			project.Trusted = append(project.Trusted, name)
		}
	}
	for _, lockfile := range []string{"bun.lock", "package-lock.json"} {
		head, ok := teamGitShow(repo, to, file(lockfile))
		if !ok {
			continue
		}
		project.Lockfile = lockfile
		base, _ := teamGitShow(repo, from, file(lockfile))
		if base == head {
			project.Stale = len(project.Added)+len(project.Removed)+len(project.Changed) > 0
			break
		}
		project.LockAdded, project.LockRemoved, project.LockChanged = teamLockDiff(teamLockPackages(lockfile, base), teamLockPackages(lockfile, head))
		break
	}
	sort.Strings(project.Added)
	sort.Strings(project.Removed)
	sort.Strings(project.Changed)
	sort.Strings(project.Trusted)
	return project
}

type teamPackageJSON struct {
	deps    map[string]string
	trusted []string
}

func teamReadPackageJSON(repo, rev, file string) teamPackageJSON {
	result := teamPackageJSON{deps: map[string]string{}}
	data, ok := teamGitShow(repo, rev, file)
	if !ok {
		return result
	}
	var pkg struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
		TrustedDependencies  []string          `json:"trustedDependencies"`
	}
	if json.Unmarshal([]byte(data), &pkg) != nil {
		return result
	}
	for _, group := range []struct {
		deps map[string]string
		mark string
	}{{pkg.PeerDependencies, " (peer)"}, {pkg.OptionalDependencies, " (optional)"}, {pkg.DevDependencies, " (dev)"}, {pkg.Dependencies, ""}} {
		for name, version := range group.deps {
			result.deps[name] = version + group.mark
		}
	}
	result.trusted = pkg.TrustedDependencies
	return result
}

// teamLockPackages is every package a lockfile installs, as name →
// versions.
func teamLockPackages(lockfile, data string) map[string][]string {
	packages := map[string][]string{}
	add := func(name, version string) {
		if name != "" && version != "" && !containsString(packages[name], version) {
			packages[name] = append(packages[name], version)
		}
	}
	switch lockfile {
	case "bun.lock":
		// bun.lock is JSON with trailing commas. Each package is
		// ["name@version", registry, details, integrity].
		var lock struct {
			Packages map[string][]json.RawMessage `json:"packages"`
		}
		if json.Unmarshal(teamStripTrailingCommas([]byte(data)), &lock) != nil {
			return packages
		}
		for _, entry := range lock.Packages {
			var spec string
			if len(entry) == 0 || json.Unmarshal(entry[0], &spec) != nil {
				continue
			}
			if at := strings.LastIndex(spec, "@"); at > 0 {
				add(spec[:at], spec[at+1:])
			}
		}
	case "package-lock.json":
		var lock struct {
			Packages map[string]struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"packages"`
		}
		if json.Unmarshal([]byte(data), &lock) != nil {
			return packages
		}
		for key, entry := range lock.Packages {
			at := strings.LastIndex(key, "node_modules/")
			if at < 0 {
				continue // the project itself
			}
			add(firstNonEmpty(entry.Name, key[at+len("node_modules/"):]), entry.Version)
		}
	}
	return packages
}

// teamLockDiff says which packages a lockfile added, removed, and moved to
// another version.
func teamLockDiff(before, after map[string][]string) (added, removed, changed []string) {
	for name, versions := range after {
		old := before[name]
		var gone, fresh []string
		for _, version := range versions {
			if !containsString(old, version) {
				fresh = append(fresh, version)
			}
		}
		for _, version := range old {
			if !containsString(versions, version) {
				gone = append(gone, version)
			}
		}
		switch {
		case len(old) == 0:
			for _, version := range versions {
				added = append(added, name+"@"+version)
			}
		case len(fresh) == 1 && len(gone) == 1:
			changed = append(changed, name+" "+gone[0]+" → "+fresh[0])
		default:
			for _, version := range fresh {
				added = append(added, name+"@"+version)
			}
			for _, version := range gone {
				removed = append(removed, name+"@"+version)
			}
		}
	}
	for name, versions := range before {
		if len(after[name]) == 0 {
			for _, version := range versions {
				removed = append(removed, name+"@"+version)
			}
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	return added, removed, changed
}

// teamStripTrailingCommas drops the commas before a closing bracket that
// bun.lock writes and JSON doesn't allow.
func teamStripTrailingCommas(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString, escaped := false, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case inString:
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
		case c == '"':
			inString = true
		case c == ',':
			j := i + 1
			for j < len(data) && strings.IndexByte(" \t\r\n", data[j]) >= 0 {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

func teamGitShow(repo, rev, file string) (string, bool) {
	out, err := exec.Command("git", "-C", repo, "show", rev+":"+file).Output()
	return string(out), err == nil
}

// teamDependencyTrusted is every name the changes newly trust.
func teamDependencyTrusted(projects []teamDependencyProject) []string {
	var names []string
	for _, project := range projects {
		names = append(names, project.Trusted...)
	}
	return names
}

// teamDependencyText says what changed, for the reviewer and the lead.
func teamDependencyText(projects []teamDependencyProject) string {
	var text strings.Builder
	for _, project := range projects {
		where := "The project"
		if project.Dir != "" {
			where = project.Dir + "/"
		}
		fmt.Fprintf(&text, "%s:\n", where)
		list := func(label string, items []string) {
			if len(items) > 0 {
				fmt.Fprintf(&text, "- %s: %s\n", label, teamClipList(items, 20))
			}
		}
		list("Adds", project.Added)
		list("Removes", project.Removed)
		list("Updates", project.Changed)
		if project.Lockfile != "" && len(project.LockAdded)+len(project.LockRemoved)+len(project.LockChanged) > 0 {
			fmt.Fprintf(&text, "- %s: %d packages added, %d removed, %d updated, counting what the direct ones need\n",
				project.Lockfile, len(project.LockAdded), len(project.LockRemoved), len(project.LockChanged))
			list("  added", project.LockAdded)
			list("  updated", project.LockChanged)
		}
		if len(project.Trusted) > 0 {
			fmt.Fprintf(&text, "- Newly in trustedDependencies, so their install scripts run on every machine that installs the project: %s\n", strings.Join(project.Trusted, ", "))
		}
		if project.Stale {
			fmt.Fprintf(&text, "- package.json changed but %s didn't: the install that previews and checks run would fail\n", project.Lockfile)
		}
	}
	return strings.TrimSpace(text.String())
}

func teamClipList(items []string, limit int) string {
	if len(items) <= limit {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:limit], ", ") + fmt.Sprintf(", and %d more", len(items)-limit)
}
