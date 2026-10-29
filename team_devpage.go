package main

// The team's dev page (docs/specs/approved/hi_team_dev.md, release 3): the
// owners' page in a browser, at an address of its own on NetBird's relay.
// The middle is herdr in the dev box, in a terminal (xterm.js over a
// WebSocket to a herdr client the service runs in the box); on the left,
// the team's tickets and the box's workspaces; on the right, what runs
// where, the settings, and Release, where an owner's click is the yes.
//
// Everything is under /__hi/dev/, which the relay routes to a socket of
// the service's with the person's key from their link, as it does the
// helper's; only owners get past it. The address itself goes to a small
// listener that sends the browser on to the page.

import (
	"cmp"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"
)

const teamDevPagePrefix = teamHelperPrefix + "dev/"

//go:embed helper/dev/page.html
var teamDevPageHTML []byte

//go:embed helper/dev/dev.js
var teamDevPageScript []byte

//go:embed helper/dev/dev.css
var teamDevPageStyle []byte

//go:embed third_party/xterm/xterm.js
var teamXtermScript []byte

//go:embed third_party/xterm/xterm.css
var teamXtermStyle []byte

//go:embed third_party/xterm/addon-fit.js
var teamXtermFit []byte

// teamDevPageSocket is the page's socket, beside the helper's: no box
// mounts it.
func teamDevPageSocket(team string) string { return filepath.Join(teamWebDir(team), "dev.sock") }

func teamDevPagePortFile(team string) string {
	return filepath.Join(teamDir(team), "run", "dev-page.port")
}

func teamDevPageStatePath(team string) string {
	return filepath.Join(teamDir(team), "run", "dev-page.json")
}

// teamDevPageState is the page's address, in run/dev-page.json.
type teamDevPageState struct {
	URL     string    `json:"url"` // with the address's key
	Expose  string    `json:"expose"`
	Expires time.Time `json:"expires"`
}

func loadTeamDevPage(team string) (teamDevPageState, bool) {
	var state teamDevPageState
	data, err := os.ReadFile(teamDevPageStatePath(team))
	if err != nil || json.Unmarshal(data, &state) != nil || state.URL == "" {
		return teamDevPageState{}, false
	}
	return state, true
}

// teamDevPageLink is the page's link for a person's key.
func teamDevPageLink(state teamDevPageState, key string) string {
	link := state.URL
	if strings.Contains(link, "?") {
		link += "&"
	} else {
		link += "?"
	}
	return link + netExposeUserParam + "=" + key
}

// ---------------------------------------------------------------------------
// the address

// listenDevPage opens the page's socket, and the listener the address
// goes to, whose port is in run/dev-page.port.
func (b *teamBroker) listenDevPage(ctx context.Context) ([]func() error, error) {
	if err := os.MkdirAll(teamWebDir(b.team), 0o700); err != nil {
		return nil, err
	}
	serve, err := b.listenWith(ctx, teamDevPageSocket(b.team), b.devPageHandler())
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := writeFileAtomic(teamDevPagePortFile(b.team), []byte(strconv.Itoa(port)+"\n"), 0o600); err != nil {
		listener.Close()
		return nil, err
	}
	root := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, teamDevPagePrefix, http.StatusSeeOther)
	}), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		root.Close()
	}()
	serveRoot := func() error {
		if err := root.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
	return []func() error{serve, serveRoot}, nil
}

// keepDevPage keeps the page on an address: a new one, with the same key,
// before the last ends, and the owners' DMs get its link.
func (b *teamBroker) keepDevPage(done <-chan struct{}, stderr io.Writer) {
	for {
		b.applyDevPage(false, stderr)
		select {
		case <-done:
			return
		case <-time.After(time.Minute):
		}
	}
}

// applyDevPage puts the dev page on the address it should have: its
// internal one, and a public one when an owner made it public. now skips
// the wait after a failed exposure, for an owner's click.
func (b *teamBroker) applyDevPage(now bool, stderr io.Writer) error {
	b.devPageMu.Lock()
	defer b.devPageMu.Unlock()
	state, had := loadTeamDevPage(b.team)
	_, exposed := b.exposed(state.Expose)
	config, _ := loadTeam(b.team)
	kind := had && (teamExposeKindOutdated(config, teamAddressDev, state.Expose) || teamInternalMoved(b.team, teamAddressDev, state.Expose, state.URL))
	if had && exposed && !kind && time.Until(state.Expires) > teamLinkRenew {
		return nil
	}
	if !now && !kind && time.Since(b.devPageFailed) < 10*time.Minute {
		return nil
	}
	next, err := b.exposeDevPage(state)
	if err != nil {
		fmt.Fprintf(stderr, "hi: the dev page's address: %v\n", err)
		b.devPageFailed = time.Now()
		return err
	}
	if exposed && state.Expose != next.Expose && !teamIsInternal(state.Expose) {
		b.run("net", "expose", "stop", state.Expose)
	}
	if next.URL != state.URL {
		go b.refreshLinks()
	}
	return nil
}

func (b *teamBroker) exposeDevPage(current teamDevPageState) (teamDevPageState, error) {
	// Not public: the dev page's internal address, on the dev box's peer.
	if config, err := loadTeam(b.team); err == nil && !teamPublicOn(config, teamAddressDev) {
		next := teamDevPageState{URL: teamInternalURL(b.team, teamAddressDev), Expose: teamInternalExpose + teamAddressDev, Expires: time.Now().AddDate(10, 0, 0).UTC()}
		data, _ := json.MarshalIndent(next, "", "  ")
		return next, writeFileAtomic(teamDevPageStatePath(b.team), append(data, '\n'), 0o600)
	}
	data, err := os.ReadFile(teamDevPagePortFile(b.team))
	if err != nil {
		return current, err
	}
	longest := netExposeMaxMax
	args := []string{"net", "expose", strings.TrimSpace(string(data)), "--follow", teamDevPagePortFile(b.team), "--link", "--detach", "--json",
		"--name", "team-" + b.team + "-dev", "--route-always", teamHelperPrefix + "=unix:" + teamDevPageSocket(b.team)}
	if domain := teamSlotDomain(b.team, teamSlotProduction); domain != "" {
		args = append(args, "--domain", b.team+"-dev."+strings.TrimPrefix(domain, b.team+"."))
		longest = netExposeDomainMax
	}
	args = append(args, "--max", longest.String())
	if key := teamLinkKey(current.URL); key != "" {
		args = append(args, "--key", key)
	}
	if allow := teamAllowIPs(b.team); allow != "" {
		args = append(args, "--allow-ip", allow)
	}
	out, err := b.run(args...)
	if err != nil {
		return current, fmt.Errorf("hi net expose: %w", err)
	}
	var exposure netExposeState
	if err := json.Unmarshal(out, &exposure); err != nil || exposure.URL == "" {
		return current, fmt.Errorf("hi net expose gave no address: %s", qFirstLine(string(out), "no output"))
	}
	next := teamDevPageState{URL: firstNonEmpty(exposure.Link, exposure.URL), Expose: exposure.Name, Expires: exposure.Expires}
	data, _ = json.MarshalIndent(next, "", "  ")
	return next, writeFileAtomic(teamDevPageStatePath(b.team), append(data, '\n'), 0o600)
}

// ---------------------------------------------------------------------------
// the page

// teamDevPageView is what the page shows, every few seconds.
type teamDevPageView struct {
	Team string `json:"team"`
	You  string `json:"you"`
	// Level is the person's: owner or developer, whose page has no
	// owners' buttons, and SSHKey their key for the dev box, if any.
	Level      string            `json:"level"`
	SSHKey     string            `json:"ssh_key,omitempty"`
	Channel    string            `json:"command"`
	Status     teamDevStatusView `json:"status"`
	Changes    []teamDevChange   `json:"changes,omitempty"`
	Settings   []string          `json:"settings"`
	Box        string            `json:"box"` // running, or why not
	Workspaces []teamDevSpace    `json:"workspaces,omitempty"`
	// What took an owner's yes in Slack before release 4.
	Releases  []teamDevRelease `json:"releases,omitempty"`
	Helper    map[string]bool  `json:"helper"`
	Incidents []teamIncident   `json:"incidents,omitempty"` // the open ones
	Rules     []teamJobRule    `json:"rules,omitempty"`
	// Access is the dev box's reach: agents' asks for sites, what it was
	// refused, and what the owners allowed.
	Access teamDevAccessView `json:"access"`
	// Keys is what the team's keys and sign-ins are, never their values.
	Keys []teamDevKeyStatus `json:"keys,omitempty"`
	// PreviewData says the preview has data production could get.
	PreviewData bool `json:"preview_data,omitempty"`
	// Addresses are production's, the preview's, and the dev page's:
	// internal, and public when an owner made them so.
	Addresses []teamDevAddress `json:"addresses"`
}

// teamDevAddress is one of the team's addresses, for the page.
type teamDevAddress struct {
	Address  string `json:"address"` // production, preview, or dev
	Internal string `json:"internal"`
	Joined   bool   `json:"joined"` // its peer is on the NetBird network
	Public   bool   `json:"public"`
	// What a public one has: its link, until when, its name that stays,
	// and the addresses it allows.
	Link     string    `json:"link,omitempty"`
	Until    time.Time `json:"until,omitzero"`
	Domain   string    `json:"domain,omitempty"`
	AllowIPs string    `json:"allow_ips,omitempty"`
}

// devPageAddresses are the team's addresses as they are now.
func (b *teamBroker) devPageAddresses(config teamConfig) []teamDevAddress {
	var addresses []teamDevAddress
	for _, address := range []string{teamSlotProduction, teamSlotPreview, teamAddressDev} {
		item := teamDevAddress{Address: address, Internal: teamInternalURL(b.team, address), Joined: teamPeerJoined(b.team), Public: teamPublicOn(config, address)}
		if item.Public {
			item.AllowIPs = teamAllowIPs(b.team)
			if address == teamAddressDev {
				item.Domain = ""
			} else {
				item.Domain = teamSlotDomain(b.team, address)
			}
			var expose, link string
			var until time.Time
			if address == teamAddressDev {
				if state, ok := loadTeamDevPage(b.team); ok {
					expose, link, until = state.Expose, state.URL, state.Expires
				}
			} else if state, ok := loadTeamSlot(b.team, address); ok {
				expose, link, until = state.Expose, state.URL, state.Expires
			}
			if _, exposed := b.exposed(expose); exposed && !teamIsInternal(expose) {
				item.Link, item.Until = link, until
			}
		}
		addresses = append(addresses, item)
	}
	return addresses
}

// devPagePublic makes an address public too, or internal only, and puts
// it on its addresses now.
func (b *teamBroker) devPagePublic(address string, on bool, by string) error {
	b.mu.Lock()
	config, err := loadTeam(b.team)
	if err == nil {
		err = teamSetPublic(&config, address, on)
	}
	if err == nil {
		err = saveTeam(config)
	}
	b.mu.Unlock()
	if err != nil {
		return err
	}
	if b.channel != nil {
		what := "internal only, on the team's NetBird network"
		if on {
			what = "public too"
		}
		name := map[string]string{teamSlotProduction: "Production", teamSlotPreview: "The preview", teamAddressDev: "The dev page"}[address]
		b.channel.post(config.opsChannel(), fmt.Sprintf(":lock: %s is %s now, as %s chose.", name, what, teamSlackEscape(by)))
	}
	switch address {
	case teamAddressDev:
		return b.applyDevPage(true, io.Discard)
	case teamSlotProduction:
		if versions, err := loadTeamVersions(b.team); err == nil && versions.Production > 0 {
			_, err := b.production(0)
			return err
		}
	case teamSlotPreview:
		if current, ok := loadTeamSlot(b.team, teamSlotPreview); ok {
			_, err := b.refreshPreview(current)
			return err
		}
	}
	return nil
}

// teamDevRelease is a release production can go back to.
type teamDevRelease struct {
	Version  int       `json:"version"`
	Released time.Time `json:"released"`
	Notes    string    `json:"notes,omitempty"`
	By       string    `json:"by,omitempty"`
	// Data says hi kept production's data as this release left it, so a
	// rollback can put that back too.
	Data bool `json:"data,omitempty"`
}

// teamDevChange is a piece of work dev has and production hasn't.
type teamDevChange struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// teamDevSpace is one of herdr's workspaces in the box, with its agent.
type teamDevSpace struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Branch string `json:"branch,omitempty"`
	Agent  string `json:"agent,omitempty"`  // claude, codex, ...
	Status string `json:"status,omitempty"` // working, idle, blocked, ...
	Title  string `json:"title,omitempty"`
	// For a worktree of its own: where it is, how many of its commits dev
	// hasn't, and whether it has changes not committed.
	Path    string `json:"path,omitempty"`
	Ahead   int    `json:"ahead,omitempty"`
	Dirty   bool   `json:"dirty,omitempty"`
	Checked bool   `json:"checked,omitempty"` // Ahead and Dirty are known
}

// teamDevBoxExec runs a command in the dev box; tests replace it.
var teamDevBoxExec = func(team string, command ...string) (string, error) {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return "", err
	}
	return engine.output(append([]string{"exec", teamDevContainer(team)}, command...)...)
}

// teamDevHerdr runs herdr in the dev box and decodes its answer's result.
func teamDevHerdr(team string, out any, args ...string) error {
	text, err := teamDevBoxExec(team, append([]string{"herdr"}, args...)...)
	if err != nil {
		return fmt.Errorf("herdr %s: %s", strings.Join(args, " "), qFirstLine(strings.TrimSpace(text), err.Error()))
	}
	var answer struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(text), &answer); err != nil {
		return fmt.Errorf("herdr %s: %s", strings.Join(args, " "), qFirstLine(text, "no answer"))
	}
	return json.Unmarshal(answer.Result, out)
}

// teamDevSpaces is the box's workspaces, each with its agent's state.
func teamDevSpaces(team string) ([]teamDevSpace, error) {
	var workspaces struct {
		Workspaces []struct {
			ID       string `json:"workspace_id"`
			Label    string `json:"label"`
			Status   string `json:"agent_status"`
			Worktree *struct {
				Path string `json:"checkout_path"`
			} `json:"worktree"`
		} `json:"workspaces"`
	}
	if err := teamDevHerdr(team, &workspaces, "workspace", "list"); err != nil {
		return nil, err
	}
	var agents struct {
		Agents []struct {
			Agent     string `json:"agent"`
			Status    string `json:"agent_status"`
			Workspace string `json:"workspace_id"`
			Title     string `json:"terminal_title_stripped"`
		} `json:"agents"`
	}
	teamDevHerdr(team, &agents, "agent", "list")
	var spaces []teamDevSpace
	for _, w := range workspaces.Workspaces {
		space := teamDevSpace{ID: w.ID, Label: w.Label, Status: w.Status}
		if w.Worktree != nil && w.Worktree.Path != "/box/repo" {
			space.Branch, space.Path = filepath.Base(w.Worktree.Path), w.Worktree.Path
		}
		for _, a := range agents.Agents {
			if a.Workspace == w.ID {
				space.Agent, space.Status, space.Title = a.Agent, a.Status, a.Title
			}
		}
		spaces = append(spaces, space)
	}
	teamDevWorktreeStates(team, spaces)
	return spaces, nil
}

// teamDevWorktreeStates says, for each worktree, how much of it dev
// hasn't: its commits not in dev, and changes not committed. One command
// in the box for all of them, after fetching dev.
func teamDevWorktreeStates(team string, spaces []teamDevSpace) {
	config, err := loadTeam(team)
	if err != nil {
		return
	}
	var paths []string
	for _, space := range spaces {
		if space.Path != "" {
			paths = append(paths, space.Path)
		}
	}
	if len(paths) == 0 {
		return
	}
	script := `git -C /box/repo fetch -q origin 2>/dev/null
dev="origin/$1"; shift
for p in "$@"; do
  ahead=$(git -C "$p" rev-list --count "$dev..HEAD" 2>/dev/null) || ahead=-1
  dirty=$(git -C "$p" status --porcelain 2>/dev/null | head -n 1 | wc -l)
  printf '%s\t%s\t%s\n' "$p" "$ahead" "$dirty"
done`
	out, err := teamDevBoxExec(team, append([]string{"sh", "-c", script, "sh", config.Branch}, paths...)...)
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			continue
		}
		ahead, err := strconv.Atoi(fields[1])
		if err != nil || ahead < 0 {
			continue
		}
		for i := range spaces {
			if spaces[i].Path == fields[0] {
				spaces[i].Ahead, spaces[i].Dirty, spaces[i].Checked = ahead, fields[2] != "0", true
			}
		}
	}
}

// teamDevFinish ends a piece of work in the box once it's shipped: its
// worktree and herdr workspace, with the agent in it, and its branch; and
// the ticket it was started for, when its number starts the branch. Work
// dev hasn't is kept unless discard says to drop it. It says what it did.
// teamDevWorkspace finds a piece of work in the dev box by its workspace or
// branch: a worktree hi could look into, not the project's own clone.
func teamDevWorkspace(team, workspace string) (*teamDevSpace, error) {
	spaces, err := teamDevSpaces(team)
	if err != nil {
		return nil, err
	}
	var space *teamDevSpace
	for i := range spaces {
		if spaces[i].ID == workspace || spaces[i].Branch != "" && spaces[i].Branch == workspace {
			space = &spaces[i]
		}
	}
	switch {
	case space == nil:
		return nil, fmt.Errorf("the dev box has no workspace %s", workspace)
	case space.Path == "":
		return nil, errors.New("that's the project's own clone, not a piece of work: it stays")
	case !space.Checked:
		return nil, errors.New("hi couldn't tell what of that work dev has; try again in a moment")
	}
	return space, nil
}

func teamDevFinish(config teamConfig, workspace, by string, discard bool) (string, error) {
	space, err := teamDevWorkspace(config.Name, workspace)
	if err != nil {
		return "", err
	}
	left := []string{}
	if space.Ahead > 0 {
		left = append(left, fmt.Sprintf("%s that dev hasn't", teamPlural(space.Ahead, "commit")))
	}
	if space.Dirty {
		left = append(left, "changes not committed")
	}
	if len(left) > 0 && !discard {
		return "", fmt.Errorf("%s has %s: ship it first, or discard it", space.Branch, strings.Join(left, " and "))
	}
	remove := []string{"worktree", "remove", "--workspace", space.ID}
	if len(left) > 0 {
		remove = append(remove, "--force")
	}
	var removed any
	if err := teamDevHerdr(config.Name, &removed, remove...); err != nil {
		return "", err
	}
	teamDevBoxExec(config.Name, "git", "-C", "/box/repo", "branch", "-D", space.Branch)
	text := fmt.Sprintf("%s is finished: its worktree, workspace, and branch are gone.", space.Branch)
	if len(left) > 0 {
		text = fmt.Sprintf("%s is discarded, with %s.", space.Branch, strings.Join(left, " and "))
		return text, nil
	}
	// The ticket it was started for, once its work is in dev.
	if number, err := strconv.Atoi(strings.SplitN(space.Branch, "-", 2)[0]); err == nil && number > 0 {
		shipped := ""
		for _, task := range loadTeamTasks(config.Name) {
			if task.Branch == teamDevBranchRef(space.Branch) && task.Status == "merged" {
				shipped = task.ID
			}
		}
		if shipped != "" {
			result, err := teamTicketDo(config.Name, teamTicketRequest{Action: "show", Number: number})
			if err == nil && result.Ticket != nil && result.Ticket.State != "closed" {
				note := fmt.Sprintf("Shipped into dev as %s; finished by %s.", shipped, by)
				if _, err := teamTicketDo(config.Name, teamTicketRequest{Action: "close", Number: number, Body: note, Author: by}); err == nil {
					text += fmt.Sprintf(" Task #%d is closed: shipped as %s.", number, shipped)
				}
			}
		}
	}
	return text, nil
}

// devPageView gathers what the page shows.
func (b *teamBroker) devPageView(person teamPersonKey, level string) teamDevPageView {
	view := teamDevPageView{Team: b.team, You: person.Name, Channel: teamSlashCommand(b.team), Level: level}
	if data, err := os.ReadFile(teamDevKeyPath(b.team, person.User)); err == nil {
		view.SSHKey = teamSSHKeyLabel(string(data))
	}
	view.Status, _ = b.devStatus()
	if report, err := teamVersionsReport(b.team); err == nil {
		for _, task := range report.Changes {
			view.Changes = append(view.Changes, teamDevChange{ID: task.ID, Title: task.Title})
		}
	}
	secrets, _ := loadTeamSecrets(b.team)
	view.Settings = append([]string{}, teamEnvNames(secrets)...)
	if versions, err := loadTeamVersions(b.team); err == nil {
		for i := len(versions.Releases) - 1; i >= 0; i-- {
			release := versions.Releases[i]
			_, data := versions.leftSnapshot(release.Version)
			by := ""
			if release.Approval != nil {
				by = release.Approval.Name
			}
			view.Releases = append(view.Releases, teamDevRelease{Version: release.Version, Released: release.Released, Notes: teamClipText(release.Notes, 500), By: by, Data: data})
		}
	}
	view.Helper = map[string]bool{}
	if config, err := loadTeam(b.team); err == nil {
		for _, slot := range []string{teamSlotPreview, teamSlotProduction} {
			view.Helper[slot] = teamHelperOn(config, slot)
		}
	}
	for _, incident := range loadTeamIncidents(b.team) {
		if incident.Status != "closed" {
			incident.Detail = teamClipText(incident.Detail, 2000)
			view.Incidents = append(view.Incidents, incident)
		}
	}
	view.Rules = loadTeamRules(b.team)
	view.Access = b.devAccessView()
	if level == teamLevelOwner {
		view.Keys = teamDevKeys(b.team) // the team's keys and sign-ins are the owners'
	}
	if config, err := loadTeam(b.team); err == nil {
		view.Addresses = b.devPageAddresses(config)
	}
	_, files, _ := teamDataUsage(teamSlotDataDir(b.team, teamSlotPreview))
	view.PreviewData = files > 0 || teamPreviewOverlay(b.team)
	view.Box = "running"
	if !b.running(teamDevContainer(b.team)) {
		view.Box = "The dev box isn't running; an admin of the machine starts it: hi team dev " + b.team + " restart"
	} else if spaces, err := teamDevSpaces(b.team); err != nil {
		view.Box = err.Error()
	} else {
		view.Workspaces = spaces
	}
	return view
}

// teamDevTicketsCache keeps the tickets for a while: gh and glab take a
// second or two, and the page asks every few seconds.
var teamDevTicketsCache struct {
	sync.Mutex
	team   string
	closed bool // with the closed ones
	at     time.Time
	result teamTicketResult
}

func teamDevPageTickets(team string, fresh bool, closed ...bool) (teamTicketResult, error) {
	withClosed := len(closed) > 0 && closed[0]
	cache := &teamDevTicketsCache
	cache.Lock()
	defer cache.Unlock()
	if !fresh && cache.team == team && cache.closed == withClosed && time.Since(cache.at) < 30*time.Second {
		return cache.result, nil
	}
	result, err := teamTicketDoOnce(team, teamTicketRequest{Action: "list", Closed: withClosed})
	if err != nil {
		return result, err
	}
	cache.team, cache.closed, cache.at, cache.result = team, withClosed, time.Now(), result
	return result, nil
}

// teamDevServeAttachment sends a ticket's file. Pictures, PDFs, and text
// open in the browser; anything else downloads. None of it runs as a page
// on the dev page's address: an HTML file someone attached is a download.
func teamDevServeAttachment(w http.ResponseWriter, r *http.Request, path string) {
	kind := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	switch base, _, _ := strings.Cut(kind, ";"); base {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "application/pdf":
		w.Header().Set("Content-Type", base)
	case "text/plain", "text/csv", "text/markdown", "application/json":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}))
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if kind != "application/pdf" { // Chromium's PDF viewer doesn't open in a sandbox
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'; style-src 'unsafe-inline'")
	}
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeFile(w, r, path)
}

// teamDevAddTicket files a ticket an owner wrote on the dev page, where
// the team's tickets are, with the files they added.
func teamDevAddTicket(team, by, title, body string, files []teamAttachment) (teamTicket, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return teamTicket{}, errors.New("a task needs a title")
	}
	body = strings.TrimSpace(strings.TrimSpace(body) + "\n\nFiled by " + by + " on the dev page.")
	result, err := teamTicketDo(team, teamTicketRequest{Action: "add", Title: title, Body: body, Author: by, Files: files})
	if err != nil {
		return teamTicket{}, err
	}
	if result.Ticket == nil {
		return teamTicket{}, errors.New("the task was filed, but hi didn't get its number")
	}
	return *result.Ticket, nil
}

var teamDevSlugPart = regexp.MustCompile(`[^a-z0-9]+`)

// teamDevTicketBranch is a ticket's branch: its number and a few words of
// its title, such as 12-chart-misses-the-last-day.
func teamDevTicketBranch(ticket teamTicket) string {
	slug := strings.Trim(teamDevSlugPart.ReplaceAllString(strings.ToLower(ticket.Title), "-"), "-")
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	if slug == "" {
		return strconv.Itoa(ticket.Number)
	}
	return fmt.Sprintf("%d-%s", ticket.Number, slug)
}

// teamDevStart starts work on a ticket in the dev box: its branch, from
// dev, a worktree and a herdr workspace, and the agent in it when one is
// named, told to read the ticket. It returns the branch.
func teamDevStart(config teamConfig, number int, agent string) (string, error) {
	if agent != "" && agent != "claude" && agent != "codex" {
		return "", fmt.Errorf("the agent is claude or codex, not %q", agent)
	}
	result, err := teamTicketDo(config.Name, teamTicketRequest{Action: "show", Number: number})
	if err != nil {
		return "", err
	}
	if result.Ticket == nil {
		return "", fmt.Errorf("there is no task %d", number)
	}
	branch := teamDevTicketBranch(*result.Ticket)
	// From dev as it is now: the box's clone fetches from the team's repository.
	teamDevBoxExec(config.Name, "git", "-C", "/box/repo", "fetch", "-q", "origin")
	var created struct {
		Pane struct {
			ID string `json:"pane_id"`
		} `json:"root_pane"`
	}
	if err := teamDevHerdr(config.Name, &created, "worktree", "create", "--cwd", "/box/repo", "--branch", branch, "--base", "origin/"+config.Branch, "--focus"); err != nil {
		return "", err
	}
	if agent != "" && created.Pane.ID != "" {
		prompt := fmt.Sprintf("Work on task #%d: %s. Read it first with hi dev task show %d, and ask me what isn't clear before you start.", number, result.Ticket.Title, number)
		if len(result.Ticket.Files) > 0 {
			prompt += fmt.Sprintf(" It has files (%s): hi dev task files %d puts them in ~/hi/tasks/%d/.", strings.Join(result.Ticket.Files, ", "), number, number)
		}
		if err := teamDevHerdrDo(config.Name, "pane", "run", created.Pane.ID, agent+" "+shellQuote(prompt)); err != nil {
			return branch, fmt.Errorf("the workspace is there, but %s didn't start: %w", agent, err)
		}
	}
	return branch, nil
}

// teamDevStartAdhoc starts work without a task: a branch named after what
// it's for (adhoc-..., never a task's number, so Done closes no task), its
// worktree and workspace, and the agent with the words as its first
// message. It says the branch and the workspace.
func teamDevStartAdhoc(config teamConfig, text, agent string) (string, string, error) {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return "", "", errors.New("say what it's for")
	case len(text) > teamDevChatTextMax:
		return "", "", fmt.Errorf("that's longer than %s", formatDataSize(teamDevChatTextMax))
	case agent != "claude" && agent != "codex":
		return "", "", fmt.Errorf("the agent is claude or codex, not %q", agent)
	}
	words := strings.Fields(strings.Trim(teamDevSlugPart.ReplaceAllString(strings.ToLower(text), " "), " "))
	if len(words) > 6 {
		words = words[:6]
	}
	slug := strings.Join(words, "-")
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	branch := "adhoc-" + firstNonEmpty(slug, "work")
	existing, _ := teamDevBoxExec(config.Name, "git", "-C", "/box/repo", "branch", "--format=%(refname:short)")
	taken := map[string]bool{}
	for _, name := range strings.Fields(existing) {
		taken[name] = true
	}
	for n, base := 2, branch; taken[branch]; n++ {
		branch = fmt.Sprintf("%s-%d", base, n)
	}
	teamDevBoxExec(config.Name, "git", "-C", "/box/repo", "fetch", "-q", "origin")
	var created struct {
		Pane struct {
			ID string `json:"pane_id"`
		} `json:"root_pane"`
	}
	if err := teamDevHerdr(config.Name, &created, "worktree", "create", "--cwd", "/box/repo", "--branch", branch, "--base", "origin/"+config.Branch, "--focus"); err != nil {
		return "", "", err
	}
	workspace, _, _ := strings.Cut(created.Pane.ID, ":")
	if created.Pane.ID != "" {
		if err := teamDevHerdrDo(config.Name, "pane", "run", created.Pane.ID, agent+" "+shellQuote(text)); err != nil {
			return branch, workspace, fmt.Errorf("the workspace is there, but %s didn't start: %w", agent, err)
		}
	}
	return branch, workspace, nil
}

// teamDevPageSetting is a setting an owner changes on the page: both
// slots, or one, as hi team env does.
type teamDevPageSetting struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
	File  string `json:"file,omitempty"` // a file setting's content, base64
	Slot  string `json:"slot,omitempty"` // "", preview, production, or dev
	Unset bool   `json:"unset,omitempty"`
}

func (b *teamBroker) devPageSetting(request teamDevPageSetting) error {
	switch {
	case request.Slot != "" && request.Slot != teamSlotPreview && request.Slot != teamSlotProduction && request.Slot != teamSlotDev:
		return fmt.Errorf("a setting is for both, the preview, production, or the dev box, not %q", request.Slot)
	case !teamEnvName.MatchString(request.Key):
		return fmt.Errorf("%q isn't a variable name: letters, digits, and _, not starting with a digit", request.Key)
	case teamAppEnvReserved[request.Key]:
		return fmt.Errorf("hi sets %s in the app's box itself", request.Key)
	case !request.Unset && request.Value == "" && request.File == "":
		return fmt.Errorf("no value for %s", request.Key)
	case strings.ContainsAny(request.Value, "\r\n"):
		return fmt.Errorf("%s's value has a line break; a setting is one line, or a file", request.Key)
	}
	secrets, err := loadTeamSecrets(b.team)
	if err != nil {
		return err
	}
	switch {
	case request.Unset:
		if !teamEnvUnset(&secrets, request.Slot, request.Key) && !teamFileUnset(&secrets, request.Slot, request.Key) {
			return fmt.Errorf("there's no setting %s%s", request.Key, teamEnvSlotWords(request.Slot))
		}
	case request.File != "":
		data, err := base64.StdEncoding.DecodeString(request.File)
		switch {
		case err != nil:
			return fmt.Errorf("the file for %s: %w", request.Key, err)
		case len(data) > teamFileSettingMax:
			return fmt.Errorf("the file for %s is larger than %s; a file setting is for a certificate or a key", request.Key, formatDataSize(teamFileSettingMax))
		}
		if err := teamSettingCheck(secrets, request.Key, true); err != nil {
			return err
		}
		teamFileSet(&secrets, request.Slot, request.Key, data)
	default:
		if err := teamSettingCheck(secrets, request.Key, false); err != nil {
			return err
		}
		teamEnvSet(&secrets, request.Slot, request.Key, request.Value)
	}
	return saveTeamSecrets(b.team, secrets)
}

// devPagePerson is the owner or developer whose key the relay found, and
// which of the two, or why not.
func (b *teamBroker) devPagePerson(r *http.Request) (teamPersonKey, string, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return teamPersonKey{}, "", err
	}
	person, ok, ended := b.webUser(config, r.Header.Get(netExposeUserHeader))
	switch {
	case ended:
		return person, "", fmt.Errorf("your link has ended: type %s dev in the team's Slack channel for a new one", teamSlashCommand(b.team))
	case !ok:
		return person, "", fmt.Errorf("the dev page is for the team's owners and developers: type %s dev in the team's Slack channel for your link", teamSlashCommand(b.team))
	case !teamBuilds(config, person.User):
		return person, "", errors.New("the dev page is for the team's owners and developers; an owner makes someone a developer")
	}
	return person, teamLevel(config, person.User), nil
}

// teamDevDeveloperRoutes are the dev page's routes a developer may use.
// Every other route is the owners' alone, so a new one is, until it is
// named here: what reaches production, its data and settings, keys, the
// sites the dev box reaches, the addresses, and who is a developer.
var teamDevDeveloperRoutes = map[string]bool{}

func init() {
	for _, route := range []string{
		"GET api/state", "GET api/tickets", "GET api/ticket", "GET api/ticket/file",
		"POST api/ticket", "POST api/ticket/delete", "POST api/ticket/edit", "POST api/ticket/note", "POST api/ticket/state",
		"POST api/start", "POST api/start-adhoc", "POST api/focus", "POST api/ship", "POST api/finish", "POST api/preview-dev",
		"GET api/term", "POST api/term/files", "GET api/chat", "POST api/chat/send", "POST api/chat/keys", "POST api/chat/answer", "POST api/chat/resume",
		"GET api/files/roots", "GET api/files/list", "GET api/files/raw", "POST api/files/upload",
		"POST api/code/start", "POST api/sshkey",
	} {
		method, path, _ := strings.Cut(route, " ")
		teamDevDeveloperRoutes[method+" "+teamDevPagePrefix+path] = true
	}
	code := strings.TrimSuffix(teamDevPagePrefix, "/") + "/code"
	teamDevDeveloperRoutes["GET "+code] = true
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		teamDevDeveloperRoutes[method+" "+code+"/"] = true
	}
}

// teamDevShellRoutes are the developer's routes a Vibe Dev doesn't get:
// the terminal, VS Code, and an SSH key for the dev box. A Vibe Dev works
// in the agents' Chat.
var teamDevShellRoutes = map[string]bool{}

func init() {
	for _, route := range []string{"GET api/term", "POST api/term/files", "POST api/code/start", "POST api/sshkey"} {
		method, path, _ := strings.Cut(route, " ")
		teamDevShellRoutes[method+" "+teamDevPagePrefix+path] = true
	}
	code := strings.TrimSuffix(teamDevPagePrefix, "/") + "/code"
	teamDevShellRoutes["GET "+code] = true
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		teamDevShellRoutes[method+" "+code+"/"] = true
	}
}

// teamDevAccessCheck is how often an open terminal or VS Code asks
// whether its person may still have it; tests shorten it.
var teamDevAccessCheck = 15 * time.Second

// teamDevPageSnapshot is a snapshot as the Data tab lists it.
type teamDevPageSnapshot struct {
	ID   string    `json:"id"`
	Time time.Time `json:"time"`
	Why  string    `json:"why"`
	Size int64     `json:"size,omitempty"`
}

// devPageData is the Data tab: production's and the preview's data
// folders, and the newest snapshots.
func (b *teamBroker) devPageData() map[string]any {
	data := map[string]any{"folders": teamDataFolders(b.team, b.running)}
	snapshots, err := teamListSnapshots(b.team)
	if err != nil {
		data["snapshots_error"] = "hi couldn't list the snapshots: " + err.Error()
		return data
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].Time.After(snapshots[j].Time) })
	list := []teamDevPageSnapshot{}
	for _, snapshot := range snapshots {
		if len(list) == 10 {
			break
		}
		list = append(list, teamDevPageSnapshot{ID: snapshot.ID, Time: snapshot.Time, Why: snapshot.why(), Size: snapshot.Size})
	}
	data["snapshots"] = list
	return data
}

func (b *teamBroker) devPageHandler() http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(value)
	}
	failure := func(w http.ResponseWriter, status int, err error) {
		reply(w, status, map[string]string{"error": err.Error()})
	}
	assets := map[string]bool{}
	asset := func(path, kind string, data []byte) {
		assets["GET "+teamDevPagePrefix+path] = true
		mux.HandleFunc("GET "+teamDevPagePrefix+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", kind)
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if path == "" {
				w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self' wss: ws:; img-src 'self' data: blob:; frame-ancestors 'none'")
				w.Header().Set("Referrer-Policy", "no-referrer")
			}
			w.Write(data)
		})
	}
	asset("", "text/html; charset=utf-8", teamDevPageHTML)
	asset("dev.js", "text/javascript; charset=utf-8", teamDevPageScript)
	asset("dev.css", "text/css; charset=utf-8", teamDevPageStyle)
	asset("xterm.js", "text/javascript; charset=utf-8", teamXtermScript)
	asset("xterm.css", "text/css; charset=utf-8", teamXtermStyle)
	asset("addon-fit.js", "text/javascript; charset=utf-8", teamXtermFit)

	builder := func(w http.ResponseWriter, r *http.Request) (teamPersonKey, bool) {
		person, _, err := b.devPagePerson(r)
		if err != nil {
			failure(w, http.StatusUnauthorized, err)
			return person, false
		}
		return person, true
	}
	decode := func(w http.ResponseWriter, r *http.Request, into any) bool {
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(into); err != nil {
			failure(w, http.StatusBadRequest, err)
			return false
		}
		return true
	}
	b.devFileRoutes(mux, builder, reply, failure)
	b.devChatRoutes(mux, builder, reply, failure)
	b.devCodeRoutes(mux, builder, reply, failure)
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/site", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			Host   string `json:"host"`
			Allow  bool   `json:"allow"`
			Remove bool   `json:"remove"`
		}
		if !decode(w, r, &request) {
			return
		}
		var err error
		if request.Remove {
			err = b.devSiteRemove(request.Host)
		} else {
			err = b.devSiteDecide(person, request.Host, request.Allow)
		}
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("GET "+teamDevPagePrefix+"api/state", func(w http.ResponseWriter, r *http.Request) {
		if person, level, err := b.devPagePerson(r); err != nil {
			failure(w, http.StatusUnauthorized, err)
		} else {
			reply(w, http.StatusOK, b.devPageView(person, level))
		}
	})
	b.devPeopleRoutes(mux, builder, reply, failure)
	mux.HandleFunc("GET "+teamDevPagePrefix+"api/tickets", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		result, err := teamDevPageTickets(b.team, r.URL.Query().Get("fresh") != "", r.URL.Query().Get("closed") != "")
		if err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, result)
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/ticket", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			Title string           `json:"title"`
			Body  string           `json:"body"`
			Files []teamAttachment `json:"files,omitempty"`
		}
		// The files come base64 in the JSON: a third more than they are.
		if err := json.NewDecoder(io.LimitReader(r.Body, teamMaxAttachments/3*4+64<<10)).Decode(&request); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		ticket, err := teamDevAddTicket(b.team, person.Name, request.Title, request.Body, request.Files)
		if err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, ticket)
	})
	mux.HandleFunc("GET "+teamDevPagePrefix+"api/ticket", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		number, _ := strconv.Atoi(r.URL.Query().Get("number"))
		result, err := teamTicketDo(b.team, teamTicketRequest{Action: "show", Number: number})
		if err != nil || result.Ticket == nil {
			failure(w, http.StatusBadGateway, cmp.Or(err, fmt.Errorf("no task #%d", number)))
			return
		}
		reply(w, http.StatusOK, result.Ticket)
	})
	mux.HandleFunc("GET "+teamDevPagePrefix+"api/ticket/file", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		number, _ := strconv.Atoi(r.URL.Query().Get("number"))
		name := r.URL.Query().Get("name")
		if !slices.Contains(teamTicketFileNames(b.team, number), name) {
			failure(w, http.StatusNotFound, fmt.Errorf("task #%d has no file %q", number, name))
			return
		}
		teamDevServeAttachment(w, r, filepath.Join(teamTicketFilesDir(b.team, number), name))
	})
	// Deleting a comment (with the files it attached) or a file: an
	// owner's, logged with their name like the page's other actions.
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/ticket/delete", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		var request struct {
			Number  int    `json:"number"`
			Comment string `json:"comment,omitempty"`
			Name    string `json:"name,omitempty"`
		}
		if !decode(w, r, &request) {
			return
		}
		action := teamTicketRequest{Action: "delete-comment", Number: request.Number, Comment: request.Comment}
		if request.Name != "" {
			action = teamTicketRequest{Action: "delete-file", Number: request.Number, Name: request.Name}
		}
		if _, err := teamTicketDo(b.team, action); err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	// Editing the ticket's title and text, or a comment, as on GitHub.
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/ticket/edit", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		var request struct {
			Number  int    `json:"number"`
			Comment string `json:"comment,omitempty"`
			Title   string `json:"title,omitempty"`
			Body    string `json:"body"`
		}
		if !decode(w, r, &request) {
			return
		}
		action := teamTicketRequest{Action: "edit", Number: request.Number, Title: strings.TrimSpace(request.Title), Body: strings.TrimSpace(request.Body)}
		switch {
		case request.Comment != "":
			action.Action, action.Comment, action.Title = "edit-comment", request.Comment, ""
			if action.Body == "" {
				failure(w, http.StatusBadRequest, errors.New("a comment needs text; delete it instead"))
				return
			}
		case action.Title == "":
			failure(w, http.StatusBadRequest, errors.New("a task needs a title"))
			return
		}
		if _, err := teamTicketDo(b.team, action); err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/ticket/note", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			Number int              `json:"number"`
			Body   string           `json:"body"`
			Files  []teamAttachment `json:"files,omitempty"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, teamMaxAttachments/3*4+64<<10)).Decode(&request); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		body := strings.TrimSpace(request.Body)
		if body == "" && len(request.Files) == 0 {
			failure(w, http.StatusBadRequest, errors.New("the note is empty"))
			return
		}
		if body != "" {
			body += "\n\n"
		}
		body += "From " + person.Name + " on the dev page."
		if _, err := teamTicketDo(b.team, teamTicketRequest{Action: "comment", Number: request.Number, Body: body, Author: person.Name, Files: request.Files}); err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/start", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		var request struct {
			Ticket int    `json:"ticket"`
			Agent  string `json:"agent,omitempty"`
		}
		if !decode(w, r, &request) {
			return
		}
		config, err := loadTeam(b.team)
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		branch, err := teamDevStart(config, request.Ticket, request.Agent)
		if err != nil && branch == "" {
			failure(w, http.StatusBadRequest, err)
			return
		}
		answer := map[string]string{"branch": branch}
		if err != nil {
			answer["warning"] = err.Error()
		}
		reply(w, http.StatusOK, answer)
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/start-adhoc", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		var request struct {
			Text  string `json:"text"`
			Agent string `json:"agent"`
		}
		if !decode(w, r, &request) {
			return
		}
		config, err := loadTeam(b.team)
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		branch, workspace, err := teamDevStartAdhoc(config, request.Text, request.Agent)
		if err != nil && branch == "" {
			failure(w, http.StatusBadRequest, err)
			return
		}
		answer := map[string]string{"branch": branch, "workspace": workspace}
		if err != nil {
			answer["warning"] = err.Error()
		}
		reply(w, http.StatusOK, answer)
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/focus", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		var request struct {
			Workspace string `json:"workspace"`
		}
		if !decode(w, r, &request) {
			return
		}
		// herdr names workspaces w1, w2, ..., and since 0.9 wB, wC, ...
		if !regexp.MustCompile(`^w[0-9A-Za-z]{1,8}$`).MatchString(request.Workspace) {
			failure(w, http.StatusBadRequest, fmt.Errorf("%q isn't a workspace", request.Workspace))
			return
		}
		var focused any
		if err := teamDevHerdr(b.team, &focused, "workspace", "focus", request.Workspace); err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/public", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			Address string `json:"address"`
			On      bool   `json:"on"`
		}
		if !decode(w, r, &request) {
			return
		}
		if err := b.devPagePublic(request.Address, request.On, person.Name+" on the dev page"); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	// Close or reopen a task from the page: Done on a task, or Reopen.
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/ticket/state", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			Number int    `json:"number"`
			Close  bool   `json:"close"`
			Note   string `json:"note,omitempty"`
		}
		if !decode(w, r, &request) {
			return
		}
		action, said := "reopen", "Reopened"
		if request.Close {
			action, said = "close", "Closed"
		}
		body := strings.TrimSpace(strings.TrimSpace(request.Note) + "\n\n" + said + " by " + person.Name + " on the dev page.")
		if _, err := teamTicketDo(b.team, teamTicketRequest{Action: action, Number: request.Number, Body: body, Author: person.Name}); err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{"text": fmt.Sprintf("Task #%d is %s.", request.Number, map[bool]string{true: "closed", false: "open again"}[request.Close])})
	})
	// Show dev: dev as it is on the preview again, after a branch was tried
	// there.
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/preview-dev", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		state, err := b.preview(teamPreviewRequest{})
		if err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{"text": "Dev is on the preview again: " + firstNonEmpty(state.URL, "(no address yet)")})
	})
	// Ship and Preview, beside a piece of work: what hi dev ship and hi dev
	// preview do in the box, by an owner's click.
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/ship", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		var request struct {
			Workspace string `json:"workspace"`
			Title     string `json:"title,omitempty"`
			Preview   bool   `json:"preview,omitempty"` // only the preview
		}
		if !decode(w, r, &request) {
			return
		}
		space, err := teamDevWorkspace(b.team, request.Workspace)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		if space.Ahead == 0 {
			why := "it has no commits that dev hasn't"
			if space.Dirty {
				why += "; its changes aren't committed yet: commit them in the terminal, or ask its agent to"
			}
			failure(w, http.StatusBadRequest, fmt.Errorf("%s has nothing to ship: %s", space.Branch, why))
			return
		}
		left := ""
		if space.Dirty {
			left = " Its changes not committed stay in the worktree."
		}
		if request.Preview {
			state, err := b.previewDevBranch(space.Branch)
			if err != nil {
				failure(w, http.StatusBadGateway, err)
				return
			}
			reply(w, http.StatusOK, map[string]string{"text": fmt.Sprintf("%s is on the preview: %s%s", space.Branch, state.URL, left)})
			return
		}
		result, err := b.shipDevBranch(teamDevShipRequest{Branch: space.Branch, Title: request.Title})
		if err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		text := fmt.Sprintf("%s is in dev as %s: %s.", space.Branch, result.Task.ID, result.Task.Title)
		if result.Preview.URL != "" {
			text += " Dev is on the preview."
		}
		for _, line := range []string{result.Warning, result.Task.Error} {
			if line != "" {
				text += " " + line
			}
		}
		reply(w, http.StatusOK, map[string]string{"text": text + left})
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/finish", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			Workspace string `json:"workspace"`
			Discard   bool   `json:"discard,omitempty"`
		}
		if !decode(w, r, &request) {
			return
		}
		config, err := loadTeam(b.team)
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		text, err := teamDevFinish(config, request.Workspace, person.Name+" on the dev page", request.Discard)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{"text": text})
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/settings", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		var request teamDevPageSetting
		if !decode(w, r, &request) {
			return
		}
		if err := b.devPageSetting(request); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/release", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			Notes string `json:"notes"`
			// PreviewData starts production with a copy of the preview's
			// data, at the first release.
			PreviewData bool `json:"preview_data,omitempty"`
		}
		if !decode(w, r, &request) {
			return
		}
		// The owner's click is the yes, logged with their name.
		release := teamReleaseRequest{Notes: request.Notes, host: person.Name + " on the dev page"}
		if request.PreviewData {
			release.DataFrom = teamSlotPreview
		}
		result, err := b.release(release)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, result)
	})
	// What took an owner's yes in Slack: the click is the yes, with the
	// owner's name in the record.
	byOwner := func(person teamPersonKey) string { return person.Name + " on the dev page" }
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/rollback", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			Version int  `json:"version"`
			Data    bool `json:"data,omitempty"`
		}
		if !decode(w, r, &request) {
			return
		}
		if request.Version <= 0 {
			failure(w, http.StatusBadRequest, errors.New("name the release to go back to"))
			return
		}
		result, err := b.release(teamReleaseRequest{Version: request.Version, Data: request.Data, host: byOwner(person)})
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, result)
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/dev/reset", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		dropped, err := b.resetDev(byOwner(person))
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]int{"dropped": len(dropped)})
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/data/copy", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		// To production by default; {"to": "preview"} gives the preview a
		// fresh copy of production's.
		var request struct {
			To string `json:"to"`
		}
		if r.ContentLength != 0 && !decode(w, r, &request) {
			return
		}
		copy := teamDataCopyRequest{From: teamSlotPreview, To: teamSlotProduction, owner: byOwner(person)}
		if request.To == teamSlotPreview {
			copy.From, copy.To = teamSlotProduction, teamSlotPreview
		}
		result, err := b.copyData(copy)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		result.Kept = ""
		reply(w, http.StatusOK, result)
	})
	mux.HandleFunc("GET "+teamDevPagePrefix+"api/data", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); ok {
			reply(w, http.StatusOK, b.devPageData())
		}
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/data/snapshot", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		snapshot, err := b.snapshot(teamSnapshotManual, "")
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, snapshot)
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/data/preview", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		var request struct {
			Empty bool `json:"empty"`
		}
		if !decode(w, r, &request) {
			return
		}
		mode := "fork"
		if request.Empty {
			mode = "empty"
		}
		if err := teamPreviewDataMode(b.team, mode, io.Discard); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/helper", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request teamHelperRequest
		if !decode(w, r, &request) {
			return
		}
		request.owner = byOwner(person)
		if _, err := b.setHelper(request); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/incident", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			ID      string `json:"id"`
			Action  string `json:"action"` // restart or close
			Preview bool   `json:"preview,omitempty"`
			Because string `json:"because,omitempty"`
		}
		if !decode(w, r, &request) {
			return
		}
		because := firstNonEmpty(strings.TrimSpace(request.Because), person.Name+" did it on the dev page")
		var err error
		switch request.Action {
		case "restart":
			_, err = b.restartApp(teamJobsRequest{Incident: request.ID, Preview: request.Preview, Because: because, owner: byOwner(person)})
		case "close":
			_, err = b.incidentAction("close", teamIncidentRequest{ID: request.ID, Because: because, owner: byOwner(person)})
		default:
			err = fmt.Errorf("no incident action %q", request.Action)
		}
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/rule", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			Job      string `json:"job"`
			Rule     string `json:"rule"`
			Incident string `json:"incident,omitempty"`
			Remove   bool   `json:"remove,omitempty"`
		}
		if !decode(w, r, &request) {
			return
		}
		var err error
		if request.Remove {
			err = b.removeRule(request.Job, request.Rule)
		} else {
			_, err = b.addRule(teamIncidentRequest{Job: request.Job, Text: request.Rule, ID: request.Incident, owner: byOwner(person)})
		}
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("GET "+teamDevPagePrefix+"api/term", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); ok {
			b.devTerminal(w, r)
		}
	})
	// One gate for every route but the page's files: an owner or a
	// developer, and for a developer, only the routes that are theirs.
	allowed := func(r *http.Request) (int, error) {
		_, pattern := mux.Handler(r)
		if pattern == "" || assets[pattern] {
			return http.StatusOK, nil
		}
		_, level, err := b.devPagePerson(r)
		switch {
		case err != nil:
			return http.StatusUnauthorized, err
		case level != teamLevelOwner && !teamDevDeveloperRoutes[pattern]:
			return http.StatusForbidden, errors.New("only the team's owners do that; developers build, and owners release and look after production")
		case level == teamLevelVibe && teamDevShellRoutes[pattern]:
			return http.StatusForbidden, errors.New("a Vibe Dev works in the agents' Chat; the terminal and VS Code are for Pro Devs and owners")
		}
		return http.StatusOK, nil
	}
	gate := func(w http.ResponseWriter, r *http.Request) bool {
		if status, err := allowed(r); err != nil {
			failure(w, status, err)
			return false
		}
		return true
	}
	// Every action is in the service's log, with how it went.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !gate(w, r) {
			return
		}
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			// A terminal, or VS Code: it ends when its person may no
			// longer have it, not only at their next click.
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			r = r.WithContext(ctx)
			go func() {
				tick := time.NewTicker(teamDevAccessCheck)
				defer tick.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-tick.C:
						if _, err := allowed(r); err != nil {
							cancel()
							return
						}
					}
				}
			}()
		}
		if r.Method != http.MethodPost {
			mux.ServeHTTP(w, r)
			return
		}
		record := &teamDevPageRecorder{ResponseWriter: w, status: http.StatusOK}
		mux.ServeHTTP(record, r)
		who := "someone"
		if person, _, err := b.devPagePerson(r); err == nil {
			who = person.Name
		}
		line := fmt.Sprintf("hi: the dev page: %s %s: %d", who, strings.TrimPrefix(r.URL.Path, teamDevPagePrefix), record.status)
		if record.status != http.StatusOK && record.failure != "" {
			line += " " + record.failure
		}
		fmt.Fprintln(os.Stderr, line)
	})
}

// teamDevPageRecorder keeps an answer's status, and its error, for the log.
type teamDevPageRecorder struct {
	http.ResponseWriter
	status  int
	failure string
}

func (r *teamDevPageRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *teamDevPageRecorder) Write(data []byte) (int, error) {
	if r.status != http.StatusOK && r.failure == "" {
		var failure struct{ Error string }
		if json.Unmarshal(data, &failure) == nil {
			r.failure = failure.Error
		}
	}
	return r.ResponseWriter.Write(data)
}

// ---------------------------------------------------------------------------
// the terminal

// teamDevTermCommand is the herdr client the page's terminal runs in the
// box; tests replace it.
var teamDevTermCommand = func(team string) []string {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return nil
	}
	return []string{engine.bin, "exec", "-it", "-e", "TERM=xterm-256color", "-e", "COLORTERM=truecolor", teamDevContainer(team), "herdr"}
}

// teamDevSameOrigin says a WebSocket comes from the page itself: its
// Origin is the page's address, as hi exposed it, or the host the request
// came for. NetBird's proxy needn't pass on the host the browser asked
// for, so the exposed address is the one to trust.
func teamDevSameOrigin(team string, r *http.Request) bool {
	origin, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || origin.Host == "" {
		return false
	}
	hosts := []string{r.Header.Get("X-Forwarded-Host"), r.Host}
	if state, ok := loadTeamDevPage(team); ok {
		if page, err := url.Parse(state.URL); err == nil {
			hosts = append(hosts, page.Host)
		}
	}
	for _, host := range hosts {
		if host != "" && strings.EqualFold(origin.Host, host) {
			return true
		}
	}
	fmt.Fprintf(os.Stderr, "hi: the dev page refused a terminal from %s (the request was for %s)\n", origin.Host, strings.Join(hosts, ", "))
	return false
}

// devTerminal is one browser's terminal: a herdr client under a terminal
// of its own on this machine, its output to the page, and the page's keys
// to it. Binary messages are the terminal's bytes; a text message is
// {"cols":..,"rows":..} when the page's size changes.
func (b *teamBroker) devTerminal(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return teamDevSameOrigin(b.team, r) }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hi: the dev page's terminal: %v\n", err)
		return
	}
	defer conn.Close()
	// Its person lost the dev page (devPageHandler's gate): it closes.
	go func() {
		<-r.Context().Done()
		conn.Close()
	}()
	fail := func(text string) {
		conn.WriteMessage(websocket.BinaryMessage, []byte("\r\n\x1b[31m"+text+"\x1b[0m\r\n"))
	}
	command := teamDevTermCommand(b.team)
	if len(command) == 0 {
		fail("hi found no box engine on the machine")
		return
	}
	master, slave, err := teamOpenPTY()
	if err != nil {
		fail("no terminal: " + err.Error())
		return
	}
	defer master.Close()
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	teamResizePTY(master, cols, rows)
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		slave.Close()
		fail("herdr didn't start: " + err.Error())
		return
	}
	slave.Close()
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()
	defer func() {
		// The client goes; herdr's server, and the agents, stay.
		if cmd.Process != nil {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP)
		}
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}()
	var writeMu sync.Mutex
	go func() {
		buffer := make([]byte, 32<<10)
		for {
			n, err := master.Read(buffer)
			if n > 0 {
				writeMu.Lock()
				werr := conn.WriteMessage(websocket.BinaryMessage, buffer[:n])
				writeMu.Unlock()
				if werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		writeMu.Lock()
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "herdr ended"), time.Now().Add(time.Second))
		writeMu.Unlock()
		conn.Close()
	}()
	conn.SetReadLimit(1 << 20)
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if kind == websocket.TextMessage {
			var size struct{ Cols, Rows int }
			if json.Unmarshal(data, &size) == nil {
				teamResizePTY(master, size.Cols, size.Rows)
			}
			continue
		}
		if _, err := master.Write(data); err != nil {
			return
		}
	}
}

// teamOpenPTY opens a pseudo-terminal: its master, and its other end for
// the process.
func teamOpenPTY() (*os.File, *os.File, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		master.Close()
		return nil, nil, err
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	slave, err := os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}

func teamResizePTY(master *os.File, cols, rows int) {
	if cols <= 0 || rows <= 0 || cols > 1000 || rows > 500 {
		return
	}
	unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(cols), Row: uint16(rows)})
}
