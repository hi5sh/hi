package main

// A team's bill of materials (hi watch, release 2): what production runs,
// as CycloneDX 1.6 JSON, made on the team's machine from git and the
// images without running project code. The heartbeat names production's
// bill by its digest; the server asks for it when it doesn't have it, and
// matches it against advisories every night (server_advisories.go).
//
// Spec: docs/specs/approved/hi_watch.md.

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// The properties hi puts on a component that advisories can match: the
// ecosystem, name, and version as OSV knows them. For a Debian or Ubuntu
// package that is its source package, which is what advisories name.
const (
	bomPart       = "hi:part"
	bomOSVSystem  = "hi:osv:ecosystem"
	bomOSVName    = "hi:osv:name"
	bomOSVVersion = "hi:osv:version"
)

type cdxBom struct {
	Format          string             `json:"bomFormat"`
	Spec            string             `json:"specVersion"`
	Serial          string             `json:"serialNumber,omitempty"`
	Version         int                `json:"version"`
	Metadata        *cdxMetadata       `json:"metadata,omitempty"`
	Components      []cdxComponent     `json:"components,omitempty"`
	Services        []cdxService       `json:"services,omitempty"`
	Vulnerabilities []cdxVulnerability `json:"vulnerabilities,omitempty"`
}

type cdxMetadata struct {
	Timestamp  string        `json:"timestamp"`
	Tools      *cdxTools     `json:"tools,omitempty"`
	Component  *cdxComponent `json:"component,omitempty"`
	Properties []cdxProperty `json:"properties,omitempty"`
}

type cdxTools struct {
	Components []cdxComponent `json:"components"`
}

type cdxComponent struct {
	Type       string        `json:"type"`
	Ref        string        `json:"bom-ref,omitempty"`
	Group      string        `json:"group,omitempty"`
	Name       string        `json:"name"`
	Version    string        `json:"version,omitempty"`
	Purl       string        `json:"purl,omitempty"`
	Hashes     []cdxHash     `json:"hashes,omitempty"`
	Properties []cdxProperty `json:"properties,omitempty"`
}

type cdxHash struct {
	Alg     string `json:"alg"`
	Content string `json:"content"`
}

type cdxProperty struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type cdxService struct {
	Ref        string        `json:"bom-ref,omitempty"`
	Name       string        `json:"name"`
	Endpoints  []string      `json:"endpoints,omitempty"`
	Properties []cdxProperty `json:"properties,omitempty"`
}

// cdxVulnerability is a VEX statement: an advisory an owner said doesn't
// apply, and why.
type cdxVulnerability struct {
	ID       string       `json:"id"`
	Analysis *cdxAnalysis `json:"analysis,omitempty"`
	Affects  []cdxAffects `json:"affects,omitempty"`
}

type cdxAnalysis struct {
	State         string `json:"state"`
	Justification string `json:"justification,omitempty"`
	Detail        string `json:"detail,omitempty"`
	Updated       string `json:"lastUpdated,omitempty"`
}

type cdxAffects struct {
	Ref string `json:"ref"`
}

func (c cdxComponent) property(name string) string {
	for _, property := range c.Properties {
		if property.Name == name {
			return property.Value
		}
	}
	return ""
}

func teamBomDir(team string) string { return filepath.Join(teamDir(team), "boms") }

func teamBomPath(team, version string) string {
	return filepath.Join(teamBomDir(team), version+".cdx.json")
}

// teamBomDigest is a bill file's digest, as the heartbeat names it.
func teamBomDigest(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// what goes in it

// teamBomMachine reads what isn't in git; tests replace it.
type teamBomMachine struct {
	// image is an image's ID and its packages, by running dpkg-query in it
	// with no network, and the runtimes hi's base image has.
	image func(image string) (teamImageList, error)
	// tool is a command's version on this machine, or "".
	tool func(name string, args ...string) string
	// baseImage is the image the app and the lead run in.
	baseImage func() string
}

type teamImageList struct {
	ID       string            `json:"id"`
	Digest   string            `json:"digest,omitempty"`
	OS       string            `json:"os"`       // "ubuntu", "debian"
	Release  string            `json:"release"`  // "24.04", "12"
	Packages [][4]string       `json:"packages"` // source, source version, binary, binary version
	Runtimes map[string]string `json:"runtimes,omitempty"`
}

var teamBomTools = teamBomMachine{
	image: teamListImage,
	tool: func(name string, args ...string) string {
		if _, err := exec.LookPath(name); err != nil {
			return ""
		}
		out, err := exec.Command(name, args...).Output()
		if err != nil {
			return ""
		}
		return teamBomVersionPattern.FindString(string(out))
	},
	baseImage: boxBaseImage,
}

var teamBomVersionPattern = regexp.MustCompile(`\d+\.\d+(\.\d+)?([.-][0-9A-Za-z.]+)?`)

var teamImageListMu sync.Mutex

// teamListImage lists an image's packages once per image ID, kept in hi's
// cache: an image doesn't change under its ID.
func teamListImage(image string) (teamImageList, error) {
	teamImageListMu.Lock()
	defer teamImageListMu.Unlock()
	b := &teamBroker{}
	out, err := b.engineOutput("image", "inspect", "-f", "{{.Id}} {{.Digest}}", image)
	fields := strings.Fields(out)
	if err != nil || len(fields) == 0 {
		return teamImageList{}, fmt.Errorf("no image %s", image)
	}
	list := teamImageList{ID: fields[0]}
	if len(fields) > 1 && fields[1] != "<none>" {
		list.Digest = fields[1]
	}
	base, _ := os.UserCacheDir()
	cache := filepath.Join(base, "hi", "bom-images", strings.TrimPrefix(list.ID, "sha256:")+".json")
	if data, err := os.ReadFile(cache); err == nil && json.Unmarshal(data, &list) == nil && len(list.Packages) > 0 {
		return list, nil
	}
	script := `. /etc/os-release; echo "os $ID $VERSION_ID"; ` +
		`for tool in node bun uv python3; do command -v $tool >/dev/null && echo "runtime $tool $($tool --version 2>&1 | head -1)"; done; ` +
		`dpkg-query -W -f '${source:Package}\t${source:Version}\t${Package}\t${Version}\n' 2>/dev/null`
	out, err = b.engineOutput("run", "--rm", "--network", "none", "--entrypoint", "sh", image, "-c", script)
	if err != nil {
		return teamImageList{}, fmt.Errorf("listing %s's packages: %s", image, qFirstLine(out, err.Error()))
	}
	list.Runtimes = map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "os "); ok {
			parts := strings.Fields(rest)
			if len(parts) == 2 {
				list.OS, list.Release = parts[0], strings.Trim(parts[1], `"`)
			}
			continue
		}
		if rest, ok := strings.CutPrefix(line, "runtime "); ok {
			if name, version, ok := strings.Cut(rest, " "); ok {
				list.Runtimes[name] = teamBomVersionPattern.FindString(version)
			}
			continue
		}
		if parts := strings.Split(line, "\t"); len(parts) == 4 && parts[2] != "" {
			list.Packages = append(list.Packages, [4]string{parts[0], parts[1], parts[2], parts[3]})
		}
	}
	if len(list.Packages) > 0 {
		os.MkdirAll(filepath.Dir(cache), 0o700)
		data, _ := json.Marshal(list)
		writeFileAtomic(cache, data, 0o600)
	}
	return list, nil
}

// teamOSVEcosystem is the OSV ecosystem of an image's OS packages.
func teamOSVEcosystem(os, release string) string {
	switch os {
	case "ubuntu":
		year, month, _ := strings.Cut(release, ".")
		if month == "04" && len(year) == 2 && (year[1]-'0')%2 == 0 {
			return "Ubuntu:" + release + ":LTS"
		}
		return "Ubuntu:" + release
	case "debian":
		major, _, _ := strings.Cut(release, ".")
		return "Debian:" + major
	case "alpine":
		major, minor, _ := strings.Cut(release, ".")
		minor, _, _ = strings.Cut(minor, ".")
		return "Alpine:v" + major + "." + minor
	}
	return ""
}

// teamPurl is a package URL; a scoped npm name keeps its scope as the
// namespace.
func teamPurl(kind, name, version string) string {
	switch kind {
	case "npm":
		if scope, rest, ok := strings.Cut(name, "/"); ok && strings.HasPrefix(scope, "@") {
			return "pkg:npm/%40" + url.PathEscape(strings.TrimPrefix(scope, "@")) + "/" + url.PathEscape(rest) + "@" + url.PathEscape(version)
		}
	case "golang":
		return "pkg:golang/" + name + "@" + url.PathEscape(version)
	}
	return "pkg:" + kind + "/" + url.PathEscape(name) + "@" + url.PathEscape(version)
}

func teamBomPackage(part, ecosystem, kind, name, version string) cdxComponent {
	purl := teamPurl(kind, name, version)
	return cdxComponent{Type: "library", Ref: purl, Name: name, Version: version, Purl: purl, Properties: []cdxProperty{
		{bomPart, part}, {bomOSVSystem, ecosystem}, {bomOSVName, name}, {bomOSVVersion, version}}}
}

// teamBomLockfiles lists every package the app's lockfiles install, at a
// commit: bun.lock and package-lock.json (npm), uv.lock (PyPI), go.mod
// (Go).
func teamBomLockfiles(repo, commit string) []cdxComponent {
	var components []cdxComponent
	seen := map[string]bool{}
	add := func(component cdxComponent) {
		if !seen[component.Ref] {
			seen[component.Ref] = true
			components = append(components, component)
		}
	}
	for _, file := range strings.Split(boxGit(repo, "ls-tree", "-r", "--name-only", commit), "\n") {
		if strings.Contains("/"+file, "/node_modules/") {
			continue
		}
		switch path.Base(file) {
		case "bun.lock", "package-lock.json":
			data, ok := teamGitShow(repo, commit, file)
			if !ok {
				continue
			}
			for name, versions := range teamLockPackages(path.Base(file), data) {
				for _, version := range versions {
					add(teamBomPackage("app", "npm", "npm", name, version))
				}
			}
		case "uv.lock":
			data, ok := teamGitShow(repo, commit, file)
			if !ok {
				continue
			}
			for _, pkg := range teamUVLockPackages(data) {
				add(teamBomPackage("app", "PyPI", "pypi", pkg[0], pkg[1]))
			}
		case "go.mod":
			data, ok := teamGitShow(repo, commit, file)
			if !ok {
				continue
			}
			for _, pkg := range teamGoModRequires(data) {
				add(teamBomPackage("app", "Go", "golang", pkg[0], pkg[1]))
			}
		}
	}
	return components
}

// teamUVLockPackages reads uv.lock's [[package]] names and versions.
func teamUVLockPackages(data string) [][2]string {
	var packages [][2]string
	name, version, inPackage := "", "", false
	flush := func() {
		if inPackage && name != "" && version != "" {
			packages = append(packages, [2]string{name, version})
		}
		name, version = "", ""
	}
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "["):
			flush()
			inPackage = line == "[[package]]"
		case inPackage && strings.HasPrefix(line, "name = "):
			name = strings.Trim(strings.TrimPrefix(line, "name = "), `"`)
		case inPackage && strings.HasPrefix(line, "version = "):
			version = strings.Trim(strings.TrimPrefix(line, "version = "), `"`)
		}
	}
	flush()
	return packages
}

// teamGoModRequires reads go.mod's required modules.
func teamGoModRequires(data string) [][2]string {
	var modules [][2]string
	inBlock := false
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(strings.Split(line, "//")[0])
		switch {
		case line == "require (":
			inBlock = true
			continue
		case inBlock && line == ")":
			inBlock = false
			continue
		case strings.HasPrefix(line, "require "):
			line = strings.TrimPrefix(line, "require ")
		case !inBlock:
			continue
		}
		if fields := strings.Fields(line); len(fields) >= 2 {
			modules = append(modules, [2]string{fields[0], fields[1]})
		}
	}
	return modules
}

// teamBomImage lists an image as a component and its OS packages and
// runtimes.
func teamBomImage(part, image string, list teamImageList) []cdxComponent {
	properties := []cdxProperty{{bomPart, part}, {"hi:image:id", list.ID}}
	if list.Digest != "" {
		properties = append(properties, cdxProperty{"hi:image:digest", list.Digest})
	}
	name, tag, _ := strings.Cut(image, ":")
	components := []cdxComponent{{Type: "container", Ref: "image:" + image, Name: name, Version: tag, Properties: properties,
		Hashes: []cdxHash{{Alg: "SHA-256", Content: strings.TrimPrefix(firstNonEmpty(list.Digest, list.ID), "sha256:")}}}}
	ecosystem := teamOSVEcosystem(list.OS, list.Release)
	distro := list.OS + "-" + list.Release
	for _, pkg := range list.Packages {
		source, sourceVersion, binary, version := pkg[0], pkg[1], pkg[2], pkg[3]
		purl := "pkg:deb/" + list.OS + "/" + url.PathEscape(binary) + "@" + url.PathEscape(version) + "?distro=" + distro
		component := cdxComponent{Type: "library", Ref: part + ":" + purl, Name: binary, Version: version, Purl: purl,
			Properties: []cdxProperty{{bomPart, part}}}
		if ecosystem != "" && source != "" {
			component.Properties = append(component.Properties, cdxProperty{bomOSVSystem, ecosystem}, cdxProperty{bomOSVName, source}, cdxProperty{bomOSVVersion, firstNonEmpty(sourceVersion, version)})
		}
		components = append(components, component)
	}
	names := sortedKeys(list.Runtimes)
	for _, name := range names {
		if list.Runtimes[name] != "" {
			components = append(components, cdxComponent{Type: "application", Ref: part + ":runtime:" + name, Name: name, Version: list.Runtimes[name],
				Properties: []cdxProperty{{bomPart, part}}})
		}
	}
	return components
}

// teamMakeBom makes the bill for a commit of the team's app.
func teamMakeBom(team, version, commit string, tools teamBomMachine) (cdxBom, error) {
	config, err := loadTeam(team)
	if err != nil {
		return cdxBom{}, err
	}
	repo := filepath.Join(teamDir(team), "repo")
	serial := make([]byte, 16)
	rand.Read(serial)
	serial[6], serial[8] = serial[6]&0x0f|0x40, serial[8]&0x3f|0x80
	bom := cdxBom{Format: "CycloneDX", Spec: "1.6", Version: 1,
		Serial: fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", serial[0:4], serial[4:6], serial[6:8], serial[8:10], serial[10:]),
		Metadata: &cdxMetadata{Timestamp: time.Now().UTC().Format(time.RFC3339),
			Tools:      &cdxTools{Components: []cdxComponent{{Type: "application", Name: "hi", Version: version_()}}},
			Component:  &cdxComponent{Type: "application", Ref: "team:" + team, Name: team, Version: version},
			Properties: []cdxProperty{{"hi:team", team}, {"hi:commit", commit}}}}
	bom.Components = append(bom.Components, teamBomLockfiles(repo, commit)...)
	if base := tools.baseImage(); base != "" {
		if list, err := tools.image(base); err == nil {
			bom.Components = append(bom.Components, teamBomImage("image:base", base, list)...)
		} else {
			bom.Metadata.Properties = append(bom.Metadata.Properties, cdxProperty{"hi:missing", err.Error()})
		}
	}
	if teamMemoryOn(config) {
		if list, err := tools.image(teamHindsightImage); err == nil {
			bom.Components = append(bom.Components, teamBomImage("image:memory", teamHindsightImage, list)...)
		}
	}
	bom.Components = append(bom.Components, teamBomPackage("hi", "Go", "golang", "github.com/hi5sh/hi", version_()))
	for _, agent := range []struct{ command, npm string }{{"claude", "@anthropic-ai/claude-code"}, {"codex", "@openai/codex"}} {
		if v := tools.tool(agent.command, "--version"); v != "" {
			bom.Components = append(bom.Components, teamBomPackage("agent", "npm", "npm", agent.npm, v))
		}
	}
	if hermes := teamHermesVersion(team); hermes[0] != "" {
		bom.Components = append(bom.Components, cdxComponent{Type: "application", Ref: "agent:hermes", Name: "hermes-agent", Version: hermes[0],
			Purl: "pkg:github/NousResearch/hermes-agent@" + firstNonEmpty(hermes[1], hermes[0]), Properties: []cdxProperty{{bomPart, "agent"}}})
	}
	// Skills are code the agents run: the lead's, by name.
	if entries, err := os.ReadDir(filepath.Join(teamDir(team), "lead", "skills")); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				bom.Components = append(bom.Components, cdxComponent{Type: "data", Ref: "skill:lead:" + entry.Name(), Name: entry.Name(),
					Properties: []cdxProperty{{bomPart, "skill"}, {"hi:role", "lead"}}})
			}
		}
	}
	// Services: the models, through hi's proxies, and what the lead may
	// reach.
	var models []cdxProperty
	for _, role := range sortedKeys(config.Members) {
		if model := config.Members[role].Model; model != "" {
			models = append(models, cdxProperty{"hi:model:" + role, model})
		}
	}
	bom.Services = append(bom.Services, cdxService{Ref: "service:openrouter", Name: "OpenRouter", Endpoints: []string{"https://openrouter.ai/api/v1"}, Properties: models},
		cdxService{Ref: "service:slack", Name: "Slack", Endpoints: []string{"https://slack.com/api"}},
		cdxService{Ref: "service:lead-allowlist", Name: "The lead's allowlist", Endpoints: teamLeadHosts})
	if roles := teamChatGPTRoles(config); len(roles) > 0 {
		var on []cdxProperty
		for _, role := range roles {
			on = append(on, cdxProperty{"hi:model:" + role, config.Members[role].Model})
		}
		bom.Services = append(bom.Services, cdxService{Ref: "service:chatgpt", Name: "ChatGPT", Endpoints: []string{chatGPTBackend + strings.TrimSuffix(chatGPTPath, "/")}, Properties: on})
	}
	sort.SliceStable(bom.Components, func(i, j int) bool { return bom.Components[i].property(bomPart) < bom.Components[j].property(bomPart) })
	return bom, nil
}

// version_ is hi's version, for the bill.
func version_() string { return firstNonEmpty(version, "dev") }

// teamHermesVersion is the lead's Hermes version and commit, from the state
// its gateway writes.
func teamHermesVersion(team string) [2]string {
	var state struct {
		Version string `json:"code_version"`
		Sha     string `json:"code_sha"`
	}
	data, err := os.ReadFile(filepath.Join(teamDir(team), "lead", "gateway_state.json"))
	if err == nil {
		json.Unmarshal(data, &state)
	}
	return [2]string{state.Version, state.Sha}
}

// teamWriteBom makes and keeps the bill for a version ("v4", or
// "preview"), and returns its path.
func teamWriteBom(team, version, commit string, tools teamBomMachine) (string, error) {
	bom, err := teamMakeBom(team, version, commit, tools)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(teamBomDir(team), 0o700); err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(bom, "", "  ")
	path := teamBomPath(team, version)
	return path, writeFileAtomic(path, append(data, '\n'), 0o600)
}

// teamProductionBom is production's bill, made the first time it's
// asked for after a release, and again when hi's base image is built
// again (hi team rebuild), and its version.
func teamProductionBom(team string, tools teamBomMachine) (string, string, error) {
	versions, err := loadTeamVersions(team)
	if err != nil || versions.Production == 0 {
		return "", "", errors.New("nothing is released")
	}
	version := fmt.Sprintf("v%d", versions.Production)
	path := teamBomPath(team, version)
	if fileExists(path) && !teamBomBaseMoved(path, tools) {
		return path, version, nil
	}
	commit := ""
	for _, release := range versions.Releases {
		if release.Version == versions.Production {
			commit = release.Commit
		}
	}
	if commit == "" {
		return "", "", fmt.Errorf("no commit for %s", version)
	}
	path, err = teamWriteBom(team, version, commit, tools)
	return path, version, err
}

// teamBomBaseMoved says a bill names hi's base image by another ID than
// the image has now.
func teamBomBaseMoved(path string, tools teamBomMachine) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var bom cdxBom
	if json.Unmarshal(data, &bom) != nil {
		return false
	}
	base := tools.baseImage()
	if base == "" {
		return false
	}
	for _, component := range bom.Components {
		if component.Type == "container" && component.property(bomPart) == "image:base" {
			list, err := tools.image(base)
			return err == nil && list.ID != component.property("hi:image:id")
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// what owners said doesn't apply

// teamAdvisoryIgnore is an advisory an owner said doesn't apply to a
// package, with why: kept in advisories-ignored.json, and as VEX in
// boms/vex.cdx.json.
type teamAdvisoryIgnore struct {
	ID       string    `json:"id"`
	Package  string    `json:"package"`
	Because  string    `json:"because"`
	By       string    `json:"by,omitempty"`
	At       time.Time `json:"at"`
	Modified string    `json:"modified,omitempty"` // the advisory as it was when ignored
}

func teamAdvisoriesIgnoredPath(team string) string {
	return filepath.Join(teamDir(team), "advisories-ignored.json")
}

func loadTeamAdvisoriesIgnored(team string) []teamAdvisoryIgnore {
	var ignored []teamAdvisoryIgnore
	if data, err := os.ReadFile(teamAdvisoriesIgnoredPath(team)); err == nil {
		json.Unmarshal(data, &ignored)
	}
	return ignored
}

func saveTeamAdvisoriesIgnored(team string, ignored []teamAdvisoryIgnore) error {
	data, _ := json.MarshalIndent(ignored, "", "  ")
	if err := writeFileAtomic(teamAdvisoriesIgnoredPath(team), append(data, '\n'), 0o600); err != nil {
		return err
	}
	vex := cdxBom{Format: "CycloneDX", Spec: "1.6", Version: 1, Metadata: &cdxMetadata{Timestamp: time.Now().UTC().Format(time.RFC3339)}}
	for _, ignore := range ignored {
		vex.Vulnerabilities = append(vex.Vulnerabilities, cdxVulnerability{ID: ignore.ID, Affects: []cdxAffects{{Ref: ignore.Package}},
			Analysis: &cdxAnalysis{State: "not_affected", Justification: "code_not_reachable", Detail: ignore.Because, Updated: ignore.At.UTC().Format(time.RFC3339)}})
	}
	data, _ = json.MarshalIndent(vex, "", "  ")
	os.MkdirAll(teamBomDir(team), 0o700)
	return writeFileAtomic(filepath.Join(teamBomDir(team), "vex.cdx.json"), append(data, '\n'), 0o600)
}

// ---------------------------------------------------------------------------
// the commands

// teamBomCommand is hi team bom <name> [version|preview].
func teamBomCommand(args []string, stdout io.Writer) error {
	if len(args) < 1 || len(args) > 2 {
		return usageError{"usage: hi team bom <name> [v<n>|preview]"}
	}
	team := args[0]
	if _, err := loadTeam(team); err != nil {
		return err
	}
	var path string
	var err error
	switch {
	case len(args) == 1:
		path, _, err = teamProductionBom(team, teamBomTools)
	case args[1] == "preview":
		state, ok := loadTeamSlot(team, teamSlotPreview)
		if !ok || state.Commit == "" {
			return errors.New("the preview isn't running")
		}
		path, err = teamWriteBom(team, "preview", state.Commit, teamBomTools)
	default:
		path = teamBomPath(team, args[1])
		if !fileExists(path) {
			err = fmt.Errorf("no bill for %s: hi keeps one per release since v1.5.0, made within a minute of the release", args[1])
		}
	}
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(stdout, bufio.NewReader(file))
	return err
}

// teamAdvisoriesCommand is hi team advisories <name>: what matches
// production now, from the server's last answer, and what was ignored.
func teamAdvisoriesCommand(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return usageError{"usage: hi team advisories <name>"}
	}
	if _, err := loadTeam(args[0]); err != nil {
		return err
	}
	printTeamAdvisories(teamAdvisoriesReport(args[0]), stdout)
	return nil
}

type teamAdvisoriesView struct {
	Watched    bool               `json:"watched"`
	At         time.Time          `json:"at,omitzero"`
	Bom        string             `json:"bom,omitempty"`
	Advisories []apiAdvisory      `json:"advisories"`
	Counts     []apiAdvisoryCount `json:"counts,omitempty"`
}

func teamAdvisoriesReport(team string) teamAdvisoriesView {
	state := loadTeamWatchState(team)
	client, _ := teamWatchClient()
	view := teamAdvisoriesView{Watched: client != nil, At: state.AdvisoriesAt, Advisories: state.Advisories, Counts: state.AdvisoryCounts}
	if view.Advisories == nil {
		view.Advisories = []apiAdvisory{}
	}
	if versions, err := loadTeamVersions(team); err == nil && versions.Production > 0 {
		view.Bom = fmt.Sprintf("v%d", versions.Production)
	}
	return view
}

func printTeamAdvisories(view teamAdvisoriesView, w io.Writer) {
	switch {
	case !view.Watched:
		fmt.Fprintln(w, "No advisories: this machine isn't connected to a hi server (hi connect), which matches production's packages against them every night.")
		return
	case view.Bom == "":
		fmt.Fprintln(w, "Nothing is released, so production runs no packages to match.")
		return
	case view.At.IsZero():
		fmt.Fprintln(w, "The hi server hasn't matched production's packages yet; it does within a day of a release.")
		return
	}
	loud, report, ignored := 0, 0, 0
	for _, advisory := range view.Advisories {
		switch advisory.tier() {
		case "kev", "incident":
			loud++
		case "report":
			report++
		default:
			ignored++
		}
	}
	fmt.Fprintf(w, "Production %s: %d advisories to act on, %d for the weekly report, %d ignored (as of %s ago).\n", view.Bom, loud, report, ignored, teamAgo(time.Since(view.At)))
	for _, advisory := range view.Advisories {
		mark := map[string]string{"kev": "EXPLOITED", "incident": "act", "report": "report", "": "ignored"}[advisory.tier()]
		line := fmt.Sprintf("  %-9s %s %s %s (%s)", mark, advisory.ID, advisory.Package, advisory.Version, advisory.Part)
		if len(advisory.Fixed) > 0 {
			line += ", fixed in " + strings.Join(advisory.Fixed, ", ")
		}
		if advisory.EPSS >= 0.01 {
			line += fmt.Sprintf(", EPSS %.2f", advisory.EPSS)
		}
		if advisory.Ignored != "" {
			line += ": " + advisory.Ignored
		}
		fmt.Fprintln(w, line)
	}
	for _, count := range view.Counts {
		fmt.Fprintf(w, "  %s: %d more for its OS packages, %d with a fix in a newer package; the team's hi server admins see them\n", advisoryPartName(count.Part), count.Total, count.Fixable)
	}
}

// teamAdvisoryCommand is hi team advisory <name> ignore <id> --because
// "<why>" [--package <name>], or unignore <id>: an owner's word that a
// match doesn't apply, until the advisory changes.
func teamAdvisoryCommand(args []string, stdout io.Writer) error {
	usage := usageError{`usage: hi team advisory <name> ignore <id> --because "<why>" [--package <name>]
       hi team advisory <name> unignore <id>`}
	var words []string
	because, pkg := "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--because", "--package":
			if i+1 >= len(args) {
				return usage
			}
			if args[i] == "--because" {
				because = args[i+1]
			} else {
				pkg = args[i+1]
			}
			i++
		default:
			words = append(words, args[i])
		}
	}
	if len(words) != 3 || (words[1] != "ignore" && words[1] != "unignore") {
		return usage
	}
	team, action, id := words[0], words[1], words[2]
	if _, err := loadTeam(team); err != nil {
		return err
	}
	ignored := loadTeamAdvisoriesIgnored(team)
	if action == "unignore" {
		var kept []teamAdvisoryIgnore
		for _, ignore := range ignored {
			if ignore.ID != id || (pkg != "" && ignore.Package != pkg) {
				kept = append(kept, ignore)
			}
		}
		if len(kept) == len(ignored) {
			return fmt.Errorf("%s isn't ignored", id)
		}
		if err := saveTeamAdvisoriesIgnored(team, kept); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s is raised again if it matches production.\n", id)
		return nil
	}
	if strings.TrimSpace(because) == "" {
		return errors.New(`say why it doesn't apply: --because "<why>"; it is kept with the team, and the server shows it`)
	}
	var matches []apiAdvisory
	for _, advisory := range loadTeamWatchState(team).Advisories {
		if advisory.ID == id && (pkg == "" || advisory.Package == pkg) {
			matches = append(matches, advisory)
		}
	}
	switch len(matches) {
	case 0:
		return fmt.Errorf("%s doesn't match production now; hi team advisories %s lists what does", id, team)
	case 1:
	default:
		var names []string
		for _, match := range matches {
			names = append(names, match.Package)
		}
		return fmt.Errorf("%s matches %s: name one with --package", id, strings.Join(names, ", "))
	}
	match := matches[0]
	var kept []teamAdvisoryIgnore
	for _, ignore := range ignored {
		if ignore.ID != match.ID || ignore.Package != match.Package {
			kept = append(kept, ignore)
		}
	}
	kept = append(kept, teamAdvisoryIgnore{ID: match.ID, Package: match.Package, Because: strings.TrimSpace(because), By: currentUserName(), At: time.Now().UTC(), Modified: match.Modified})
	if err := saveTeamAdvisoriesIgnored(team, kept); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s no longer counts for %s, as long as the advisory stays as it is; the hi server hears of it with the next heartbeat, within a minute.\n", match.ID, match.Package)
	return nil
}
