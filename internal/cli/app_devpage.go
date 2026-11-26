package cli

// The team's dev page (docs/specs/approved/hi_app_dev.md, release 3): the
// owners' page in a browser, at an address of its own on NetBird's relay.
// The middle is herdr in the dev box, in a terminal (xterm.js over a
// WebSocket to a herdr client the service runs in the box); on the left,
// the team's tickets and the box's workspaces; on the right, what runs
// where, the settings, and Release, where an owner's or a developer's
// click is the yes.
//
// Everything is under /__hi/dev/, which the relay routes to a socket of
// the service's with the person's key from their link, as it does the
// helper's; only owners get past it. The address itself goes to a small
// listener that sends the browser on to the page.

import (
	"bytes"
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

	hi "github.com/hi5sh/hi"

	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"
)

const appDevPagePrefix = appHelperPrefix + "dev/"

var appDevPageHTML = hi.AppDevPageHTML

var appDevPageScript = hi.AppDevPageScript

var appDevPageStyle = hi.AppDevPageStyle

// appAssetCache is how long a browser keeps one of the pages' files: a
// font for a year, as dev.css and the helper ask for it by its version
// (geist.woff2?v=1.9.2), so a new one is a new address; the rest it
// checks again each time, as they change with hi.
func appAssetCache(kind string) string {
	if strings.HasPrefix(kind, "font/") {
		return "public, max-age=31536000, immutable"
	}
	return "no-cache"
}

var appXtermScript = hi.AppXtermScript

var appXtermStyle = hi.AppXtermStyle

var appXtermFit = hi.AppXtermFit

// appXtermAddons are the terminal's other add-ons, served as one script.
var appXtermAddons = bytes.Join([][]byte{hi.AppXtermWebgl, hi.AppXtermSearch, hi.AppXtermUnicode11, hi.AppXtermWebLinks, hi.AppXtermImage}, []byte("\n;\n"))

// appDevPageSocket is the page's socket, beside the helper's: no box
// mounts it.
func appDevPageSocket(team string) string { return filepath.Join(appWebDir(team), "dev.sock") }

func appDevPagePortFile(team string) string {
	return filepath.Join(appDir(team), "run", "dev-page.port")
}

func appDevPageStatePath(team string) string {
	return filepath.Join(appDir(team), "run", "dev-page.json")
}

// appDevPageState is the page's address, in run/dev-page.json.
type appDevPageState struct {
	URL     string    `json:"url"` // with the address's key
	Expose  string    `json:"expose"`
	Expires time.Time `json:"expires"`
}

func loadTeamDevPage(team string) (appDevPageState, bool) {
	var state appDevPageState
	data, err := os.ReadFile(appDevPageStatePath(team))
	if err != nil || json.Unmarshal(data, &state) != nil || state.URL == "" {
		return appDevPageState{}, false
	}
	return state, true
}

// appDevPageLink is the page's link for a person's key.
func appDevPageLink(state appDevPageState, key string) string {
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
func (b *appBroker) listenDevPage(ctx context.Context) ([]func() error, error) {
	if err := os.MkdirAll(appWebDir(b.team), 0o700); err != nil {
		return nil, err
	}
	serve, err := b.listenWith(ctx, appDevPageSocket(b.team), b.devPageHandler())
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := writeFileAtomic(appDevPagePortFile(b.team), []byte(strconv.Itoa(port)+"\n"), 0o600); err != nil {
		listener.Close()
		return nil, err
	}
	root := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, appDevPagePrefix, http.StatusSeeOther)
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
func (b *appBroker) keepDevPage(done <-chan struct{}, stderr io.Writer) {
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
func (b *appBroker) applyDevPage(now bool, stderr io.Writer) error {
	b.devPageMu.Lock()
	defer b.devPageMu.Unlock()
	state, had := loadTeamDevPage(b.team)
	_, exposed := b.exposed(state.Expose)
	config, err := loadTeam(b.team)
	if err != nil {
		return nil // settings that can't be read move nothing
	}
	kind := had && (appExposeKindOutdated(config, appAddressDev, state.Expose) || appInternalMoved(b.team, appAddressDev, state.Expose, state.URL))
	if had && exposed && !kind && time.Until(state.Expires) > appLinkRenew {
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
	if exposed && state.Expose != next.Expose && !appIsInternal(state.Expose) {
		b.run("net", "expose", "stop", state.Expose)
	}
	if next.URL != state.URL {
		go b.refreshLinks()
	}
	return nil
}

func (b *appBroker) exposeDevPage(current appDevPageState) (appDevPageState, error) {
	// Not public: the dev page's internal address, on the dev box's peer.
	config, err := loadTeam(b.team)
	if err != nil {
		return current, fmt.Errorf("the app's settings can't be read, so the dev page's address stays as it is: %w", err)
	}
	if !appPublicOn(config, appAddressDev) {
		next := appDevPageState{URL: appInternalURL(b.team, appAddressDev), Expose: appInternalExpose + appAddressDev, Expires: time.Now().AddDate(10, 0, 0).UTC()}
		data, _ := json.MarshalIndent(next, "", "  ")
		return next, writeFileAtomic(appDevPageStatePath(b.team), append(data, '\n'), 0o600)
	}
	data, err := os.ReadFile(appDevPagePortFile(b.team))
	if err != nil {
		return current, err
	}
	longest := netExposeMaxMax
	args := []string{"net", "expose", strings.TrimSpace(string(data)), "--follow", appDevPagePortFile(b.team), "--link", "--detach", "--json",
		"--name", "app-" + b.team + "-dev", "--route-always", appHelperPrefix + "=unix:" + appDevPageSocket(b.team)}
	if domain := appSlotDomain(b.team, appSlotProduction); domain != "" {
		args = append(args, "--domain", b.team+"-dev."+strings.TrimPrefix(domain, b.team+"."))
		longest = netExposeDomainMax
	}
	args = append(args, "--max", longest.String())
	if key := appLinkKey(current.URL); key != "" {
		args = append(args, "--key", key)
	}
	if allow := appAllowIPs(b.team); allow != "" {
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
	next := appDevPageState{URL: firstNonEmpty(exposure.Link, exposure.URL), Expose: exposure.Name, Expires: exposure.Expires}
	data, _ = json.MarshalIndent(next, "", "  ")
	return next, writeFileAtomic(appDevPageStatePath(b.team), append(data, '\n'), 0o600)
}

// ---------------------------------------------------------------------------
// the page

// appDevPageView is what the page shows, every few seconds.
type appDevPageView struct {
	Team string `json:"team"`
	You  string `json:"you"`
	// Level is the person's: owner or developer, whose page has no
	// owners' buttons, and SSHKey their key for the dev box, if any.
	Level    string           `json:"level"`
	SSHKey   string           `json:"ssh_key,omitempty"`
	Channel  string           `json:"command"`
	Status   appDevStatusView `json:"status"`
	Changes  []appDevChange   `json:"changes,omitempty"`
	Settings []string         `json:"settings"`
	// Shared are the shared keys an admin shared to the app, which a
	// setting can take its value from.
	Shared     []string      `json:"shared,omitempty"`
	Box        string        `json:"box"` // running, or why not
	Workspaces []appDevSpace `json:"workspaces,omitempty"`
	// Pushed are the branches people pushed to the app's GitHub or GitLab
	// project (app_pushed.go).
	Pushed *appPushedView `json:"pushed,omitempty"`
	// What took an owner's yes in Slack before release 4.
	Releases  []appDevRelease `json:"releases,omitempty"`
	Helper    map[string]bool `json:"helper"`
	Incidents []appIncident   `json:"incidents,omitempty"` // the open ones
	Rules     []appJobRule    `json:"rules,omitempty"`
	// Access is the dev box's reach: agents' asks for sites, what it was
	// refused, and what the owners allowed.
	Access appDevAccessView `json:"access"`
	// Keys is what the team's keys and sign-ins are, never their values.
	Keys []appDevKeyStatus `json:"keys,omitempty"`
	// Admin is the machine's admin page, for someone who is one of its
	// admins: the notes that send people to an admin link there.
	Admin string `json:"admin,omitempty"`
	// PreviewData says the preview has data production could get.
	PreviewData bool `json:"preview_data,omitempty"`
	// Addresses are production's, the preview's, and the dev page's:
	// internal, and public when an owner made them so.
	Addresses []appDevAddress `json:"addresses"`
	// SSH is the dev box for the person's own editor, an owner's or a
	// developer's (app_deveditor.go).
	SSH *appDevSSHView `json:"ssh,omitempty"`
	// Agy says the dev box runs agy, so the page offers it to start.
	Agy bool `json:"agy,omitempty"`
}

// appDevAddress is one of the team's addresses, for the page.
type appDevAddress struct {
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
func (b *appBroker) devPageAddresses(config appConfig) []appDevAddress {
	var addresses []appDevAddress
	for _, address := range []string{appSlotProduction, appSlotPreview, appAddressDev} {
		item := appDevAddress{Address: address, Internal: appInternalURL(b.team, address), Joined: appPeerJoined(b.team), Public: appPublicOn(config, address)}
		if item.Public {
			item.AllowIPs = appAllowIPs(b.team)
			if address == appAddressDev {
				item.Domain = ""
			} else {
				item.Domain = appSlotDomain(b.team, address)
			}
			var expose, link string
			var until time.Time
			if address == appAddressDev {
				if state, ok := loadTeamDevPage(b.team); ok {
					expose, link, until = state.Expose, state.URL, state.Expires
				}
			} else if state, ok := loadTeamSlot(b.team, address); ok {
				expose, link, until = state.Expose, state.URL, state.Expires
			}
			if _, exposed := b.exposed(expose); exposed && !appIsInternal(expose) {
				item.Link, item.Until = link, until
			}
		}
		addresses = append(addresses, item)
	}
	return addresses
}

// devPagePublic makes an address public too, or internal only, and puts
// it on its addresses now.
func (b *appBroker) devPagePublic(address string, on bool, by string) error {
	b.mu.Lock()
	config, err := loadTeam(b.team)
	if err == nil {
		err = appSetPublic(&config, address, on)
	}
	if err == nil {
		err = saveTeam(config)
	}
	b.mu.Unlock()
	if err != nil {
		return err
	}
	if b.channel != nil {
		what := "internal only, on the app's NetBird network"
		if on {
			what = "public too"
		}
		name := map[string]string{appSlotProduction: "Production", appSlotPreview: "The preview", appAddressDev: "The dev page"}[address]
		b.channel.post(config.opsChannel(), fmt.Sprintf(":lock: %s is %s now, as %s chose.", name, what, appSlackEscape(by)))
	}
	switch address {
	case appAddressDev:
		return b.applyDevPage(true, io.Discard)
	case appSlotProduction:
		if versions, err := loadTeamVersions(b.team); err == nil && versions.Production > 0 {
			_, err := b.production(0)
			return err
		}
	case appSlotPreview:
		if current, ok := loadTeamSlot(b.team, appSlotPreview); ok {
			_, err := b.refreshPreview(current)
			return err
		}
	}
	return nil
}

// appDevRelease is a release production can go back to.
type appDevRelease struct {
	Version  int       `json:"version"`
	Released time.Time `json:"released"`
	Notes    string    `json:"notes,omitempty"`
	By       string    `json:"by,omitempty"`
	// Data says hi kept production's data as this release left it, so a
	// rollback can put that back too.
	Data bool `json:"data,omitempty"`
}

// appDevChange is a piece of work dev has and production hasn't.
type appDevChange struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// appDevSpace is one of herdr's workspaces in the box, with its agent.
type appDevSpace struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Branch string `json:"branch,omitempty"`
	Agent  string `json:"agent,omitempty"`  // claude, codex, ...
	Status string `json:"status,omitempty"` // working, idle, blocked, ...
	Title  string `json:"title,omitempty"`
	// From the agent's hooks (app_devhooks.go), when they say.
	Tool    string    `json:"tool,omitempty"`
	Waiting string    `json:"waiting,omitempty"`
	Since   time.Time `json:"since,omitzero"`
	// Note is the agent's own line on where its work stands (hi dev note).
	Note   string    `json:"note,omitempty"`
	NoteAt time.Time `json:"note_at,omitzero"`
	By     string    `json:"by,omitempty"` // who started it, when hi started it
	// For a worktree of its own: where it is, how many of its commits dev
	// hasn't, and whether it has changes not committed.
	Path    string `json:"path,omitempty"`
	Ahead   int    `json:"ahead,omitempty"`
	Dirty   bool   `json:"dirty,omitempty"`
	Checked bool   `json:"checked,omitempty"` // Ahead and Dirty are known
	// Out says its change was in dev and was taken out since: Add to
	// Preview puts it back.
	Out bool `json:"out,omitempty"`
	// Shipped says its work is in dev, added and not taken out since: a
	// worktree with no commits of its own is as level with dev, but it
	// isn't done, and Publish doesn't clean it up.
	Shipped bool `json:"shipped,omitempty"`
}

// appDevBoxExec runs a command in the dev box; tests replace it.
var appDevBoxExec = func(team string, command ...string) (string, error) {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return "", err
	}
	return engine.output(append([]string{"exec", appDevContainer(team)}, command...)...)
}

// appDevHerdr runs herdr in the dev box and decodes its answer's result.
// errAppDevWorkspaceGone is a workspace herdr no longer has: the dev box
// restarted, and herdr numbered its workspaces again, or it was closed.
var errAppDevWorkspaceGone = errors.New("that workspace is gone (the dev box restarted, or it was closed): pick another")

func appDevHerdr(team string, out any, args ...string) error {
	text, err := appDevBoxExec(team, append([]string{"herdr"}, args...)...)
	if err != nil {
		if strings.Contains(text+err.Error(), "workspace_not_found") {
			return errAppDevWorkspaceGone
		}
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

// appDevSpaces is the box's workspaces, each with its agent's state.
func appDevSpaces(team string) ([]appDevSpace, error) {
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
	if err := appDevHerdr(team, &workspaces, "workspace", "list"); err != nil {
		return nil, err
	}
	var agents struct {
		Agents []struct {
			Agent     string `json:"agent"`
			Status    string `json:"agent_status"`
			Workspace string `json:"workspace_id"`
			Title     string `json:"terminal_title_stripped"`
			Session   *struct {
				Value string `json:"value"`
			} `json:"agent_session"`
		} `json:"agents"`
	}
	appDevHerdr(team, &agents, "agent", "list")
	var spaces []appDevSpace
	for _, w := range workspaces.Workspaces {
		space := appDevSpace{ID: w.ID, Label: w.Label, Status: w.Status}
		if w.Worktree != nil && w.Worktree.Path != "/box/repo" {
			space.Branch, space.Path = filepath.Base(w.Worktree.Path), w.Worktree.Path
		}
		for _, a := range agents.Agents {
			if a.Workspace == w.ID {
				space.Agent, space.Status, space.Title = a.Agent, a.Status, a.Title
				session := ""
				if a.Session != nil {
					session = a.Session.Value
				}
				status, hook := appDevHookStatus(team, w.ID, a.Agent, a.Status, session, time.Now())
				space.Status = status
				if hook != nil {
					space.Tool, space.Waiting, space.Since = hook.Tool, hook.Waiting, hook.Since
				}
			}
		}
		spaces = append(spaces, space)
	}
	starters := appDevStarters(team)
	notes := appDevNotes(team)
	for i := range spaces {
		spaces[i].By = starters[spaces[i].Branch]
		if note, ok := notes[firstNonEmpty(spaces[i].Branch, spaces[i].Label)]; ok {
			spaces[i].Note, spaces[i].NoteAt = note.Text, note.At
		}
	}
	appDevWorktreeStates(team, spaces)
	return spaces, nil
}

func appDevStartersPath(team string) string {
	return filepath.Join(appDir(team), "run", "dev-starts.json")
}

// appDevStarters is who started each branch's work in the dev box: an
// owner or developer on the dev page, the one who asked the lead, or the
// machine's admin.
func appDevStarters(team string) map[string]string {
	starters := map[string]string{}
	if data, err := os.ReadFile(appDevStartersPath(team)); err == nil {
		json.Unmarshal(data, &starters)
	}
	return starters
}

var appDevStartersMu sync.Mutex

func appDevStarted(team, branch, by string) {
	if by == "" {
		return
	}
	appDevStartersMu.Lock()
	defer appDevStartersMu.Unlock()
	starters := appDevStarters(team)
	starters[branch] = by
	if data, err := json.Marshal(starters); err == nil {
		os.MkdirAll(filepath.Dir(appDevStartersPath(team)), 0o700)
		writeFileAtomic(appDevStartersPath(team), data, 0o600)
	}
}

// appDevWorktreeStates says, for each worktree, how much of it dev
// hasn't: its commits not in dev, and changes not committed. One command
// in the box for all of them, after fetching dev.
func appDevWorktreeStates(team string, spaces []appDevSpace) {
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
	out, err := appDevBoxExec(team, append([]string{"sh", "-c", script, "sh", config.Branch}, paths...)...)
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
	tasks := loadTeamTasks(team)
	for i := range spaces {
		if spaces[i].Checked && spaces[i].Ahead == 0 && spaces[i].Branch != "" {
			spaces[i].Out = appDevTakenOut(tasks, spaces[i].Branch) != nil
			spaces[i].Shipped = appDevShipped(tasks, spaces[i].Branch)
		}
	}
}

// appDevFinish ends a piece of work in the box once it's shipped: its
// worktree and herdr workspace, with the agent in it, and its branch; and
// the ticket it was started for, when its number starts the branch. Work
// dev hasn't is kept unless discard says to drop it. It says what it did.
// appDevWorkspace finds a piece of work in the dev box by its workspace or
// branch: a worktree hi could look into, not the project's own clone.
func appDevWorkspace(team, workspace string) (*appDevSpace, error) {
	spaces, err := appDevSpaces(team)
	if err != nil {
		return nil, err
	}
	var space *appDevSpace
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

func appDevFinish(config appConfig, workspace, by string, discard bool) (string, error) {
	space, err := appDevWorkspace(config.Name, workspace)
	if err != nil {
		return "", err
	}
	left := []string{}
	if space.Ahead > 0 {
		left = append(left, fmt.Sprintf("%s that dev hasn't", appPlural(space.Ahead, "commit")))
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
	err = appDevHerdr(config.Name, &removed, remove...)
	appDevSpacesForget() // gone, or half gone: ask herdr again
	if err != nil {
		return "", err
	}
	appDevBoxExec(config.Name, "git", "-C", "/box/repo", "branch", "-D", space.Branch)
	text := fmt.Sprintf("%s is finished: its worktree, workspace, and branch are gone.", space.Branch)
	if len(left) > 0 {
		text = fmt.Sprintf("%s is discarded, with %s.", space.Branch, strings.Join(left, " and "))
		return text, nil
	}
	// The ticket it was started for, once its work is in dev.
	if number, err := strconv.Atoi(strings.SplitN(space.Branch, "-", 2)[0]); err == nil && number > 0 {
		shipped := ""
		for _, task := range loadTeamTasks(config.Name) {
			if task.Branch == appDevBranchRef(space.Branch) && task.Status == "merged" {
				shipped = task.ID
			}
		}
		if shipped != "" {
			result, err := appTicketDo(config.Name, appTicketRequest{Action: "show", Number: number})
			if err == nil && result.Ticket != nil && result.Ticket.State != "closed" {
				note := fmt.Sprintf("Shipped into dev as %s; finished by %s.", shipped, by)
				if _, err := appTicketDo(config.Name, appTicketRequest{Action: "close", Number: number, Body: note, Author: by}); err == nil {
					text += fmt.Sprintf(" Task #%d is closed: shipped as %s.", number, shipped)
				}
			}
		}
	}
	return text, nil
}

// devPageView gathers what the page shows.
func (b *appBroker) devPageView(person appPersonKey, level string) appDevPageView {
	view := appDevPageView{Team: b.team, You: person.Name, Channel: appSlashCommand(b.team), Level: level, Admin: adminPageFor(person.User)}
	if data, err := os.ReadFile(appDevKeyPath(b.team, person.User)); err == nil {
		view.SSHKey = appSSHKeyLabel(string(data))
	}
	view.SSH = appDevSSH(b.team, level)
	view.Agy = appDevHasAgy(b.team)
	view.Status, _ = b.devStatus()
	if report, err := appVersionsReport(b.team); err == nil {
		for _, task := range report.Changes {
			view.Changes = append(view.Changes, appDevChange{ID: task.ID, Title: task.Title})
		}
	}
	secrets, _ := loadTeamSecrets(b.team)
	devConfig, _ := loadTeam(b.team)
	view.Settings = append(append([]string{}, appEnvNames(secrets)...), appEnvRefNames(devConfig)...)
	view.Shared = appSharedTo(b.team)
	if versions, err := loadTeamVersions(b.team); err == nil {
		for i := len(versions.Releases) - 1; i >= 0; i-- {
			release := versions.Releases[i]
			_, data := versions.leftSnapshot(release.Version)
			by := ""
			if release.Approval != nil {
				by = release.Approval.Name
			}
			view.Releases = append(view.Releases, appDevRelease{Version: release.Version, Released: release.Released, Notes: appClipText(release.Notes, 500), By: by, Data: data})
		}
	}
	view.Helper = map[string]bool{}
	if config, err := loadTeam(b.team); err == nil {
		for _, slot := range []string{appSlotPreview, appSlotProduction} {
			view.Helper[slot] = appHelperOn(config, slot)
		}
	}
	for _, incident := range loadTeamIncidents(b.team) {
		if incident.Status != "closed" {
			incident.Detail = appClipText(incident.Detail, 2000)
			view.Incidents = append(view.Incidents, incident)
		}
	}
	view.Rules = loadTeamRules(b.team)
	view.Access = b.devAccessView()
	if level == appLevelOwner {
		view.Keys = appDevKeys(b.team) // the team's keys and sign-ins are the owners'
	}
	if config, err := loadTeam(b.team); err == nil {
		view.Addresses = b.devPageAddresses(config)
		if config.Remote != "" {
			pushed := appPushedBranches(config)
			view.Pushed = &pushed
		}
	}
	_, files, _ := appDataUsage(appSlotDataDir(b.team, appSlotPreview))
	view.PreviewData = files > 0 || appPreviewOverlay(b.team)
	view.Box = "running"
	if !b.running(appDevContainer(b.team)) {
		view.Box = "The dev box isn't running; an admin of the machine starts it: hi app dev " + b.team + " restart"
	} else if spaces, err := appDevSpaces(b.team); err != nil {
		view.Box = err.Error()
	} else {
		view.Workspaces = spaces
	}
	return view
}

// appDevTicketsCache keeps the tickets for a while: gh and glab take a
// second or two, and the page asks every few seconds.
var appDevTicketsCache struct {
	sync.Mutex
	team   string
	closed bool // with the closed ones
	at     time.Time
	result appTicketResult
}

func appDevPageTickets(team string, fresh bool, closed ...bool) (appTicketResult, error) {
	withClosed := len(closed) > 0 && closed[0]
	cache := &appDevTicketsCache
	cache.Lock()
	defer cache.Unlock()
	if !fresh && cache.team == team && cache.closed == withClosed && time.Since(cache.at) < 30*time.Second {
		return cache.result, nil
	}
	result, err := appTicketDoOnce(team, appTicketRequest{Action: "list", Closed: withClosed})
	if err != nil {
		return result, err
	}
	cache.team, cache.closed, cache.at, cache.result = team, withClosed, time.Now(), result
	return result, nil
}

// appDevServeAttachment sends a ticket's file. Pictures, PDFs, and text
// open in the browser; anything else downloads. None of it runs as a page
// on the dev page's address: an HTML file someone attached is a download.
func appDevServeAttachment(w http.ResponseWriter, r *http.Request, path string) {
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

// appDevAddTicket files a ticket an owner wrote on the dev page, where
// the team's tickets are, with the files they added.
func appDevAddTicket(team, by, title, body string, files []appAttachment) (appTicket, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return appTicket{}, errors.New("a task needs a title")
	}
	body = strings.TrimSpace(strings.TrimSpace(body) + "\n\nFiled by " + by + " on the dev page.")
	result, err := appTicketDo(team, appTicketRequest{Action: "add", Title: title, Body: body, Author: by, Files: files})
	if err != nil {
		return appTicket{}, err
	}
	if result.Ticket == nil {
		return appTicket{}, errors.New("the task was filed, but hi didn't get its number")
	}
	return *result.Ticket, nil
}

var appDevSlugPart = regexp.MustCompile(`[^a-z0-9]+`)

// appDevTicketBranch is a ticket's branch: its number and a few words of
// its title, such as 12-chart-misses-the-last-day.
func appDevTicketBranch(ticket appTicket) string {
	slug := strings.Trim(appDevSlugPart.ReplaceAllString(strings.ToLower(ticket.Title), "-"), "-")
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	if slug == "" {
		return strconv.Itoa(ticket.Number)
	}
	return fmt.Sprintf("%d-%s", ticket.Number, slug)
}

// appDevStart starts work on a ticket in the dev box: its branch, from
// dev, a worktree and a herdr workspace, and the agent in it when one is
// named, told to read the ticket; by is who started it, which the dev
// page shows. It returns the branch.
func appDevStart(config appConfig, number int, agent, by string) (string, error) {
	branch, _, err := appDevStartIn(config, number, agent, by)
	return branch, err
}

// appDevSpacesNow are the box's workspaces as herdr has them now, or none.
func appDevSpacesNow(team string) []appDevSpace {
	spaces, _ := appDevSpaces(team)
	return spaces
}

// appDevAgentOK says an agent can start in the dev box: Claude Code,
// Codex, agy when the app has its sign-in, or (with shell) none.
func appDevAgentOK(team, agent string, shell bool) error {
	switch {
	case agent == "claude", agent == "codex", agent == "" && shell:
		return nil
	case agent == "agy" && appDevHasAgy(team):
		return nil
	case agent == "agy":
		return fmt.Errorf("the dev box has no agy: an admin gives the app its own Google sign-in for it, with hi app signin %s agy --file <token>", team)
	}
	return fmt.Errorf("the agent is claude, codex, or agy, not %q", agent)
}

// appDevAgentCommand starts an agent in a workspace's pane with its first
// message. agy asks before each tool unless told not to: the box is the
// boundary, as for the others (their settings say so).
func appDevAgentCommand(agent, prompt string) string {
	if agent == "agy" {
		return "agy --dangerously-skip-permissions -i " + shellQuote(prompt)
	}
	return agent + " " + shellQuote(prompt)
}

// appDevStartIn is appDevStart, and says the herdr workspace too.
func appDevStartIn(config appConfig, number int, agent, by string) (string, string, error) {
	if err := appDevAgentOK(config.Name, agent, true); err != nil {
		return "", "", err
	}
	result, err := appTicketDo(config.Name, appTicketRequest{Action: "show", Number: number})
	if err != nil {
		return "", "", err
	}
	if result.Ticket == nil {
		return "", "", fmt.Errorf("there is no task %d", number)
	}
	branch := appDevTicketBranch(*result.Ticket)
	// From dev as it is now: the box's clone fetches from the team's repository.
	appDevBoxExec(config.Name, "git", "-C", "/box/repo", "fetch", "-q", "origin")
	var created struct {
		Pane struct {
			ID string `json:"pane_id"`
		} `json:"root_pane"`
	}
	if err := appDevHerdr(config.Name, &created, "worktree", "create", "--cwd", "/box/repo", "--branch", branch, "--base", "origin/"+config.Branch, "--focus"); err != nil {
		return "", "", err
	}
	appDevStarted(config.Name, branch, by)
	appDevSpacesForget()
	workspace, _, _ := strings.Cut(created.Pane.ID, ":")
	prompt := fmt.Sprintf("Work on task #%d: %s. Read it first with hi dev task show %d, and ask me what isn't clear before you start.", number, result.Ticket.Title, number)
	if len(result.Ticket.Files) > 0 {
		prompt += fmt.Sprintf(" It has files (%s): hi dev task files %d puts them in ~/hi/tasks/%d/.", strings.Join(result.Ticket.Files, ", "), number, number)
	}
	return branch, workspace, appDevStartAgent(config, branch, created.Pane.ID, agent, prompt)
}

// appDevStartAgent starts an agent in a new workspace's pane, with its
// first message.
func appDevStartAgent(config appConfig, branch, pane, agent, prompt string) error {
	if agent == "" {
		return nil
	}
	// Each agent asks to trust a folder on its first start there, in the
	// terminal, where Chat can't answer: trusted first.
	path := "/box/home/.herdr/worktrees/repo/" + branch
	for _, space := range appDevSpacesNow(config.Name) {
		if space.Branch == branch && space.Path != "" {
			path = space.Path
		}
	}
	if err := appDevTrust(config.Name, agent, path); err != nil {
		return fmt.Errorf("the workspace is there, but %s's settings: %w", agent, err)
	}
	if pane == "" {
		return nil
	}
	if err := appDevHerdrDo(config.Name, "pane", "run", pane, appDevAgentCommand(agent, prompt)); err != nil {
		return fmt.Errorf("the workspace is there, but %s didn't start: %w", agent, err)
	}
	return nil
}

// appDevStartPushed starts work in the dev box on a branch someone pushed
// to the app's remote: a workspace whose branch, of the same name, starts
// where theirs is, with an agent that first says what it does. A
// workspace on that branch already is opened instead.
func appDevStartPushed(config appConfig, branch appPushedBranch, agent, by string) (string, string, error) {
	if err := appDevAgentOK(config.Name, agent, true); err != nil {
		return "", "", err
	}
	name := firstNonEmpty(branch.Workspace, branch.Name)
	for _, space := range appDevSpacesNow(config.Name) {
		if space.Branch == name && space.ID != "" {
			return name, space.ID, nil
		}
	}
	if name == config.Branch || !appDevBranchPattern.MatchString(name) {
		return "", "", fmt.Errorf("%s can't be a workspace's branch: add it from Publish, or start new work and ask the agent to bring in origin/%s", name, appPushedRef(branch.Name))
	}
	appDevBoxExec(config.Name, "git", "-C", "/box/repo", "fetch", "-q", "origin")
	var created struct {
		Pane struct {
			ID string `json:"pane_id"`
		} `json:"root_pane"`
	}
	if err := appDevHerdr(config.Name, &created, "worktree", "create", "--cwd", "/box/repo", "--branch", name, "--base", "origin/"+appPushedRef(branch.Name), "--focus"); err != nil {
		return "", "", err
	}
	appDevStarted(config.Name, name, by)
	appDevSpacesForget()
	workspace, _, _ := strings.Cut(created.Pane.ID, ":")
	from := "the branch " + branch.Name
	if branch.Dev {
		from = "what was pushed straight to " + branch.Name
	}
	prompt := fmt.Sprintf("This workspace starts from %s, which %s pushed to %s from their own machine: %s dev hasn't, the last %q. "+
		"Read what it changes (git log --oneline origin/%s..HEAD, and git diff --stat origin/%s...HEAD, then the code), "+
		"and tell me in a few lines what it does, and whether it looks ready to add to the preview. Change nothing until I say what to do.",
		from, firstNonEmpty(branch.Author, "someone"), appForgeName(config.Remote), appPlural(branch.Ahead, "commit"), branch.Subject, config.Branch, config.Branch)
	return name, workspace, appDevStartAgent(config, name, created.Pane.ID, agent, prompt)
}

// appDevStartNew is Start something new: the words, and the pictures
// pasted in them, filed as a task, which starts at once, so work from the
// chat is a task, its branch, and its worktree like any other. It says
// the task, the branch, and the workspace.
func appDevStartNew(config appConfig, by, text, agent string, files []appAttachment) (appTicket, string, string, error) {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return appTicket{}, "", "", errors.New("say what it's for")
	case len(text) > appDevChatTextMax:
		return appTicket{}, "", "", fmt.Errorf("that's longer than %s", formatDataSize(appDevChatTextMax))
	case appDevAgentOK(config.Name, agent, false) != nil:
		return appTicket{}, "", "", appDevAgentOK(config.Name, agent, false)
	}
	ticket, err := appDevAddTicket(config.Name, by, appDevNewTitle(text), text, files)
	if err != nil {
		return appTicket{}, "", "", err
	}
	branch, workspace, err := appDevStartPage(config, ticket.Number, agent, by)
	if err != nil && branch == "" {
		err = fmt.Errorf("task #%d is filed, but it didn't start: %w", ticket.Number, err)
	}
	return ticket, branch, workspace, err
}

// appDevStartPage starts a task from the dev page, and says so on the
// task, as the lead does for the channel: who started it, and where.
func appDevStartPage(config appConfig, number int, agent, by string) (string, string, error) {
	branch, workspace, err := appDevStartIn(config, number, agent, by)
	if branch != "" {
		with := ""
		if agent != "" {
			with = ", with " + map[string]string{"claude": "Claude Code", "codex": "Codex"}[agent]
		}
		appTicketDo(config.Name, appTicketRequest{Action: "comment", Number: number, Author: by,
			Body: fmt.Sprintf("Started in the dev box by %s on the dev page: branch %s%s.", by, branch, with)})
	}
	return branch, workspace, err
}

var appDevPictureRef = regexp.MustCompile(`\[(?:image|file): [^\]]*\]`)

// appDevNewTitle is a task's title from what someone wrote: its first
// line, without the pictures' references, cut at a word near 70.
func appDevNewTitle(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.Join(strings.Fields(appDevPictureRef.ReplaceAllString(line, " ")), " ")
		if line == "" {
			continue
		}
		if len([]rune(line)) <= 70 {
			return line
		}
		cut := string([]rune(line)[:70])
		if at := strings.LastIndex(cut, " "); at > 30 {
			cut = cut[:at]
		}
		return strings.TrimRight(cut, " ,.;:-") + "…"
	}
	return "Pictures from the dev page"
}

// appDevPageSetting is a setting an owner changes on the page: both
// slots, or one, as hi app env does.
// appDevSettingAll is the dev page's setting for the dev box, the
// preview, and production.
const appDevSettingAll = "all"

type appDevPageSetting struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
	File  string `json:"file,omitempty"` // a file setting's content, base64
	Slot  string `json:"slot,omitempty"` // "", preview, production, dev, or all (the two and the dev box)
	Unset bool   `json:"unset,omitempty"`
	// From is a shared key the setting takes its value from, shared/<name>.
	From string `json:"from,omitempty"`
}

func (b *appBroker) devPageSetting(request appDevPageSetting) error {
	// All: the preview and production, and the dev box the same, so
	// the code finds the variable wherever it runs. The page's default.
	if request.Slot == appDevSettingAll {
		if request.Unset {
			request.Slot = ""
			return b.devPageSetting(request)
		}
		for _, slot := range []string{"", appSlotDev} {
			one := request
			one.Slot = slot
			if err := b.devPageSetting(one); err != nil {
				return err
			}
		}
		return nil
	}
	switch {
	case request.Slot != "" && request.Slot != appSlotPreview && request.Slot != appSlotProduction && request.Slot != appSlotDev:
		return fmt.Errorf("a setting is for both, the preview, production, or the dev box, not %q", request.Slot)
	case !appEnvName.MatchString(request.Key):
		return fmt.Errorf("%q isn't a variable name: letters, digits, and _, not starting with a digit", request.Key)
	case appBoxEnvReserved[request.Key]:
		return fmt.Errorf("hi sets %s in the app's box itself", request.Key)
	case !request.Unset && request.Value == "" && request.File == "" && request.From == "":
		return fmt.Errorf("no value for %s", request.Key)
	case strings.ContainsAny(request.Value, "\r\n"):
		return fmt.Errorf("%s's value has a line break; a setting is one line, or a file", request.Key)
	}
	secrets, err := loadTeamSecrets(b.team)
	if err != nil {
		return err
	}
	config, err := loadTeam(b.team)
	if err != nil {
		return err
	}
	switch {
	case request.From != "" && !request.Unset:
		if err := appEnvFrom(&config, &secrets, request.Slot, request.Key, request.From); err != nil {
			return err
		}
		if err := saveTeamSecrets(b.team, secrets); err != nil {
			return err
		}
		return saveTeam(config)
	case request.Unset:
		// A value, a file, or a shared key's reference: in the slot asked,
		// or, for "", in every slot.
		reference := appEnvFromUnset(&config, request.Slot, request.Key)
		if request.Slot == "" {
			for _, scope := range []string{appSlotProduction, appSlotPreview, appSlotDev} {
				reference = appEnvFromUnset(&config, scope, request.Key) || reference
			}
		}
		if reference {
			if err := saveTeam(config); err != nil {
				return err
			}
		}
		if !appEnvUnset(&secrets, request.Slot, request.Key) && !appFileUnset(&secrets, request.Slot, request.Key) && !reference {
			return fmt.Errorf("there's no setting %s%s", request.Key, appEnvSlotWords(request.Slot))
		}
	case request.File != "":
		data, err := base64.StdEncoding.DecodeString(request.File)
		switch {
		case err != nil:
			return fmt.Errorf("the file for %s: %w", request.Key, err)
		case len(data) > appFileSettingMax:
			return fmt.Errorf("the file for %s is larger than %s: files bigger than this belong in the app's data, not its settings", request.Key, formatDataSize(appFileSettingMax))
		}
		if err := appSettingCheck(secrets, request.Key, true); err != nil {
			return err
		}
		appFileSet(&secrets, request.Slot, request.Key, data)
	default:
		if err := appSettingCheck(secrets, request.Key, false); err != nil {
			return err
		}
		appEnvSet(&secrets, request.Slot, request.Key, request.Value)
	}
	return saveTeamSecrets(b.team, secrets)
}

// devPagePerson is the owner or developer whose key the relay found, and
// which of the two, or why not.
func (b *appBroker) devPagePerson(r *http.Request) (appPersonKey, string, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return appPersonKey{}, "", err
	}
	person, ok, ended := b.webUser(config, r.Header.Get(netExposeUserHeader))
	switch {
	case ended:
		return person, "", fmt.Errorf("your link has ended: type %s dev in the app's Slack channel for a new one", appSlashCommand(b.team))
	case !ok:
		return person, "", fmt.Errorf("the dev page is for the app's owners and developers: type %s dev in the app's Slack channel for your link", appSlashCommand(b.team))
	case !appBuilds(config, person.User):
		return person, "", errors.New("the dev page is for the app's owners and developers; an owner makes someone a developer")
	}
	return person, appLevel(config, person.User), nil
}

// appDevDeveloperRoutes are the dev page's routes a developer may use.
// Every other route is the owners' alone, so a new one is, until it is
// named here: what reaches production, its data and settings, keys, the
// sites the dev box reaches, the addresses, and who is a developer.
var appDevDeveloperRoutes = map[string]bool{}

func init() {
	for _, route := range []string{
		"GET api/state", "GET api/tickets", "GET api/ticket", "GET api/ticket/file",
		"POST api/ticket", "POST api/ticket/delete", "POST api/ticket/edit", "POST api/ticket/note", "POST api/ticket/state",
		"POST api/start", "POST api/start-new", "POST api/focus", "POST api/ship", "POST api/help", "POST api/finish", "POST api/preview-dev", "POST api/release",
		"GET api/term", "POST api/term/files", "GET api/chat", "POST api/chat/send", "POST api/chat/fork", "GET api/chat/image", "POST api/chat/queue", "POST api/chat/queue/steer", "POST api/chat/queue/remove", "POST api/chat/keys", "POST api/chat/answer", "POST api/chat/resume", "GET api/chat/points", "POST api/chat/rewind", "POST api/chat/model",
		"GET api/files/roots", "GET api/files/list", "GET api/files/raw", "GET api/files/stat", "POST api/files/upload", "POST api/files/save",
		"GET api/git", "GET api/git/diff", "POST api/git", "GET api/search", "POST api/pick/locate", "GET api/checks", "POST api/checks", "GET api/logs",
		"POST api/sshkey", "GET api/decisions", "POST api/decision", "GET api/presence",
	} {
		method, path, _ := strings.Cut(route, " ")
		appDevDeveloperRoutes[method+" "+appDevPagePrefix+path] = true
	}
	code := strings.TrimSuffix(appDevPagePrefix, "/") + "/code"
	appDevDeveloperRoutes["GET "+code] = true
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		appDevDeveloperRoutes[method+" "+code+"/"] = true
	}
}

// appDevAccessCheck is how often an open terminal asks
// whether its person may still have it; tests shorten it.
var appDevAccessCheck = 15 * time.Second

// appDevPageSnapshot is a snapshot as Data lists it.
type appDevPageSnapshot struct {
	ID   string    `json:"id"`
	Time time.Time `json:"time"`
	Why  string    `json:"why"`
	Size int64     `json:"size,omitempty"`
}

// devPageData is Data: production's and the preview's data
// folders, and the newest snapshots.
func (b *appBroker) devPageData() map[string]any {
	data := map[string]any{"folders": appDataFolders(b.team, b.running)}
	snapshots, err := appListSnapshots(b.team)
	if err != nil {
		data["snapshots_error"] = "hi couldn't list the snapshots: " + err.Error()
		return data
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].Time.After(snapshots[j].Time) })
	list := []appDevPageSnapshot{}
	for _, snapshot := range snapshots {
		if len(list) == 10 {
			break
		}
		list = append(list, appDevPageSnapshot{ID: snapshot.ID, Time: snapshot.Time, Why: snapshot.why(), Size: snapshot.Size})
	}
	data["snapshots"] = list
	return data
}

// appDevPageManifest makes the dev page an app people install, on a
// phone, a tablet, or a desktop: its own window, named for the app, and
// on the dev page's own path, so each app's dev page is an app of its own.
func appDevPageManifest(team string) []byte {
	icon := func(src, size, purpose string) map[string]string {
		return map[string]string{"src": src, "sizes": size, "type": "image/png", "purpose": purpose}
	}
	data, _ := json.MarshalIndent(map[string]any{
		"id":               "./",
		"name":             team + " · dev",
		"short_name":       team,
		"description":      "The dev page of " + team + ", a hi app",
		"start_url":        "./",
		"scope":            "./",
		"display":          "standalone",
		"background_color": "#151515",
		"theme_color":      "#151515",
		"icons": []map[string]string{
			icon("icon-192.png", "192x192", "any"), icon("icon-512.png", "512x512", "any"),
			icon("icon-maskable-512.png", "512x512", "maskable"),
		},
	}, "", "  ")
	return data
}

func (b *appBroker) devPageHandler() http.Handler {
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
		assets["GET "+appDevPagePrefix+path] = true
		mux.HandleFunc("GET "+appDevPagePrefix+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", kind)
			w.Header().Set("Cache-Control", appAssetCache(kind))
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if path == "" {
				w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self' wss: ws:; img-src 'self' data: blob:; "+b.devPageFrames()+"; frame-ancestors 'none'")
				w.Header().Set("Referrer-Policy", "no-referrer")
			}
			w.Write(data)
		})
	}
	asset("", "text/html; charset=utf-8", appDevPageHTML)
	asset("dev.js", "text/javascript; charset=utf-8", appDevPageScript)
	asset("presence.js", "text/javascript; charset=utf-8", hi.AppPresenceScript)
	asset("theme.js", "text/javascript; charset=utf-8", hi.AppThemeScript)
	asset("reveal.js", "text/javascript; charset=utf-8", hi.AppRevealScript)
	asset("dev.css", "text/css; charset=utf-8", appDevPageStyle)
	asset("xterm.js", "text/javascript; charset=utf-8", appXtermScript)
	asset("xterm.css", "text/css; charset=utf-8", appXtermStyle)
	asset("addon-fit.js", "text/javascript; charset=utf-8", appXtermFit)
	asset("xterm-addons.js", "text/javascript; charset=utf-8", appXtermAddons)
	asset("geist.woff2", "font/woff2", hi.AppFontSans)
	asset("geist-mono.woff2", "font/woff2", hi.AppFontMono)
	for _, icon := range []string{"icon-192.png", "icon-512.png", "icon-maskable-512.png", "apple-touch-icon.png"} {
		data, _ := hi.AppIcons.ReadFile("helper/icons/" + icon)
		asset(icon, "image/png", data)
	}
	assets["GET "+appDevPagePrefix+"manifest.webmanifest"] = true
	mux.HandleFunc("GET "+appDevPagePrefix+"manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(appDevPageManifest(b.team))
	})

	builder := func(w http.ResponseWriter, r *http.Request) (appPersonKey, bool) {
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
	// Signing in by email, before anyone is signed in (hi_people.md).
	for _, route := range mailRoutes(mux, appDevPagePrefix, b.devMailFlow()) {
		assets[route] = true
	}
	b.devFileRoutes(mux, builder, reply, failure)
	b.devGitRoutes(mux, builder, reply, failure)
	b.devToolRoutes(mux, builder, reply, failure)
	b.devPickRoutes(mux, builder, reply, failure)
	b.devChatRoutes(mux, builder, reply, failure)
	b.devDecisionRoutes(mux, builder, reply, failure, decode)
	mux.HandleFunc("POST "+appDevPagePrefix+"api/site", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			Host   string `json:"host"`
			Allow  bool   `json:"allow"`
			Remove bool   `json:"remove"`
			Keep   bool   `json:"keep"` // what hi's check allowed: keep it, or
			Undo   bool   `json:"undo"` // take it off and say no
		}
		if !decode(w, r, &request) {
			return
		}
		var err error
		switch {
		case request.Keep:
			err = b.devSiteKeep(person, request.Host)
		case request.Undo:
			err = b.devSiteUndo(person, request.Host)
		case request.Remove:
			err = b.devSiteRemove(request.Host)
		default:
			err = b.devSiteDecide(person, request.Host, request.Allow)
		}
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("GET "+appDevPagePrefix+"api/state", func(w http.ResponseWriter, r *http.Request) {
		if person, level, err := b.devPagePerson(r); err != nil {
			failure(w, http.StatusUnauthorized, err)
		} else {
			reply(w, http.StatusOK, b.devPageView(person, level))
		}
	})
	b.devPeopleRoutes(mux, builder, reply, failure)
	mux.HandleFunc("GET "+appDevPagePrefix+"api/tickets", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		result, err := appDevPageTickets(b.team, r.URL.Query().Get("fresh") != "", r.URL.Query().Get("closed") != "")
		if err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, result)
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/ticket", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			Title string          `json:"title"`
			Body  string          `json:"body"`
			Files []appAttachment `json:"files,omitempty"`
		}
		// The files come base64 in the JSON: a third more than they are.
		if err := json.NewDecoder(io.LimitReader(r.Body, appMaxAttachments/3*4+64<<10)).Decode(&request); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		ticket, err := appDevAddTicket(b.team, person.Name, request.Title, request.Body, request.Files)
		if err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, ticket)
	})
	mux.HandleFunc("GET "+appDevPagePrefix+"api/ticket", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		number, _ := strconv.Atoi(r.URL.Query().Get("number"))
		result, err := appTicketDo(b.team, appTicketRequest{Action: "show", Number: number})
		if err != nil || result.Ticket == nil {
			failure(w, http.StatusBadGateway, cmp.Or(err, fmt.Errorf("no task #%d", number)))
			return
		}
		reply(w, http.StatusOK, result.Ticket)
	})
	mux.HandleFunc("GET "+appDevPagePrefix+"api/ticket/file", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		number, _ := strconv.Atoi(r.URL.Query().Get("number"))
		name := r.URL.Query().Get("name")
		if !slices.Contains(appTicketFileNames(b.team, number), name) {
			failure(w, http.StatusNotFound, fmt.Errorf("task #%d has no file %q", number, name))
			return
		}
		appDevServeAttachment(w, r, filepath.Join(appTicketFilesDir(b.team, number), name))
	})
	// Deleting a comment (with the files it attached) or a file: an
	// owner's, logged with their name like the page's other actions.
	mux.HandleFunc("POST "+appDevPagePrefix+"api/ticket/delete", func(w http.ResponseWriter, r *http.Request) {
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
		action := appTicketRequest{Action: "delete-comment", Number: request.Number, Comment: request.Comment}
		if request.Name != "" {
			action = appTicketRequest{Action: "delete-file", Number: request.Number, Name: request.Name}
		}
		if _, err := appTicketDo(b.team, action); err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	// Editing the ticket's title and text, or a comment, as on GitHub.
	mux.HandleFunc("POST "+appDevPagePrefix+"api/ticket/edit", func(w http.ResponseWriter, r *http.Request) {
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
		action := appTicketRequest{Action: "edit", Number: request.Number, Title: strings.TrimSpace(request.Title), Body: strings.TrimSpace(request.Body)}
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
		if _, err := appTicketDo(b.team, action); err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/ticket/note", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			Number int             `json:"number"`
			Body   string          `json:"body"`
			Files  []appAttachment `json:"files,omitempty"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, appMaxAttachments/3*4+64<<10)).Decode(&request); err != nil {
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
		if _, err := appTicketDo(b.team, appTicketRequest{Action: "comment", Number: request.Number, Body: body, Author: person.Name, Files: request.Files}); err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/start", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
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
		branch, workspace, err := appDevStartPage(config, request.Ticket, request.Agent, person.Name)
		appDevStartedUser(b.team, branch, person.User)
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
	mux.HandleFunc("POST "+appDevPagePrefix+"api/start-new", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			Text  string          `json:"text"`
			Agent string          `json:"agent"`
			Files []appAttachment `json:"files,omitempty"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, appMaxAttachments/3*4+64<<10)).Decode(&request); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		config, err := loadTeam(b.team)
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		ticket, branch, workspace, err := appDevStartNew(config, person.Name, request.Text, request.Agent, request.Files)
		appDevStartedUser(b.team, branch, person.User)
		if err != nil && branch == "" {
			failure(w, http.StatusBadRequest, err)
			return
		}
		answer := map[string]any{"ticket": ticket.Number, "branch": branch, "workspace": workspace}
		if err != nil {
			answer["warning"] = err.Error()
		}
		reply(w, http.StatusOK, answer)
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/focus", func(w http.ResponseWriter, r *http.Request) {
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
		if err := appDevHerdr(b.team, &focused, "workspace", "focus", request.Workspace); err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/public", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("POST "+appDevPagePrefix+"api/ticket/state", func(w http.ResponseWriter, r *http.Request) {
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
		if _, err := appTicketDo(b.team, appTicketRequest{Action: action, Number: request.Number, Body: body, Author: person.Name}); err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{"text": fmt.Sprintf("Task #%d is %s.", request.Number, map[bool]string{true: "closed", false: "open again"}[request.Close])})
	})
	// Show dev: dev as it is on the preview again, after a branch was tried
	// there.
	mux.HandleFunc("POST "+appDevPagePrefix+"api/preview-dev", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		state, err := b.preview(appPreviewRequest{})
		if err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{"text": "Dev is on the preview again: " + firstNonEmpty(state.URL, "(no address yet)")})
	})
	// The Preview's frame was sent on to an origin the page doesn't frame
	// (a sign-in in front of the app, most likely): kept for hi dev web.
	mux.HandleFunc("POST "+appDevPagePrefix+"api/preview/blocked", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		var request struct {
			Slot string `json:"slot"`
			To   string `json:"to"`
		}
		if !decode(w, r, &request) {
			return
		}
		if err := b.devFrameBlocked(request.Slot, request.To); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	// Ship and Preview, beside a piece of work: what hi dev ship and hi dev
	// preview do in the box, by an owner's click.
	mux.HandleFunc("POST "+appDevPagePrefix+"api/ship", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			Workspace string `json:"workspace"`
			Title     string `json:"title,omitempty"`
			Preview   bool   `json:"preview,omitempty"` // only the preview
			// TakeOut is the change dev had last, taken out for this one, in
			// one step: undone if this one doesn't go in either.
			TakeOut string `json:"take_out,omitempty"`
		}
		if !decode(w, r, &request) {
			return
		}
		space, err := appDevWorkspace(b.team, request.Workspace)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		if space.Ahead == 0 && (request.Preview || appDevTakenOut(loadTeamTasks(b.team), space.Branch) == nil) {
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
		result, err := b.shipDevBranch(appDevShipRequest{Branch: space.Branch, Title: request.Title, TakeOut: request.TakeOut, By: person.Name})
		if answer, ok := b.devConflictAnswer(err); ok {
			reply(w, http.StatusConflict, answer)
			return
		}
		if err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		// In the page's words: the preview is the next version, dev.
		text := fmt.Sprintf("“%s” is in the next version (%s).", result.Task.Title, result.Task.ID)
		if result.Preview.URL != "" {
			text = fmt.Sprintf("“%s” is on the preview, in the next version (%s).", result.Task.Title, result.Task.ID)
		}
		if request.TakeOut != "" {
			text += " The change before it is out of the preview; its workspace keeps it, to add again."
		}
		for _, line := range []string{result.Warning, result.Task.Error} {
			if line != "" {
				text += " " + line
			}
		}
		reply(w, http.StatusOK, map[string]string{"text": text + left})
	})
	// Try on Preview and Add to Preview, beside a branch pushed to the app's
	// GitHub or GitLab project: as for a workspace's.
	mux.HandleFunc("POST "+appDevPagePrefix+"api/pushed", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			Branch  string `json:"branch"`
			Title   string `json:"title,omitempty"`
			Preview bool   `json:"preview,omitempty"`
			TakeOut string `json:"take_out,omitempty"`
			Fetch   bool   `json:"fetch,omitempty"` // only fetch again
			// Start: work on it in the dev box, in a workspace of its own,
			// with Agent.
			Start bool   `json:"start,omitempty"`
			Agent string `json:"agent,omitempty"`
		}
		if !decode(w, r, &request) {
			return
		}
		config, err := loadTeam(b.team)
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		if request.Fetch {
			if _, _, err := fetchTeamPushed(config, true); err != nil {
				failure(w, http.StatusBadGateway, err)
				return
			}
			reply(w, http.StatusOK, map[string]string{"text": "Fetched from " + appForgeName(config.Remote) + "."})
			return
		}
		branch, err := appPushedFind(config, request.Branch)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		if request.Start {
			name, workspace, err := appDevStartPushed(config, branch, request.Agent, person.Name)
			appDevStartedUser(b.team, name, person.User)
			if err != nil && name == "" {
				failure(w, http.StatusBadRequest, err)
				return
			}
			answer := map[string]string{"branch": name, "workspace": workspace}
			if err != nil {
				answer["warning"] = err.Error()
			}
			reply(w, http.StatusOK, answer)
			return
		}
		if branch.Ahead == 0 && (request.Preview || !branch.Out) {
			failure(w, http.StatusBadRequest, fmt.Errorf("dev has everything on %s already", branch.Name))
			return
		}
		if request.Preview {
			state, err := b.previewPushedBranch(branch.Name)
			if err != nil {
				failure(w, http.StatusBadGateway, err)
				return
			}
			reply(w, http.StatusOK, map[string]string{"text": fmt.Sprintf("%s is on the preview: %s", branch.Name, state.URL)})
			return
		}
		result, err := b.shipPushedBranch(appDevShipRequest{Branch: branch.Name, Title: request.Title, TakeOut: request.TakeOut, By: person.Name})
		if answer, ok := b.devConflictAnswer(err); ok {
			reply(w, http.StatusConflict, answer)
			return
		}
		if err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		text := fmt.Sprintf("“%s” is in the next version (%s).", result.Task.Title, result.Task.ID)
		if result.Preview.URL != "" {
			text = fmt.Sprintf("“%s” is on the preview, in the next version (%s).", result.Task.Title, result.Task.ID)
		}
		for _, line := range []string{result.Warning, result.Task.Error} {
			if line != "" {
				text += " " + line
			}
		}
		reply(w, http.StatusOK, map[string]string{"text": text})
	})
	// Help: someone stuck on the dev page asks the app's developers (else its
	// owners) in the app's channel, with what went wrong.
	mux.HandleFunc("POST "+appDevPagePrefix+"api/help", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request struct {
			Workspace string `json:"workspace"`
			What      string `json:"what"`  // what they tried, in the page's words
			Error     string `json:"error"` // what hi said
		}
		if !decode(w, r, &request) {
			return
		}
		config, err := loadTeam(b.team)
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		if b.channel == nil || config.Channel == "" {
			failure(w, http.StatusConflict, errors.New("hi isn't connected to the app's channel in Slack: ask the app's developers another way, with the details below"))
			return
		}
		// The other developers, not who asks; the owners when there's none.
		who := slices.DeleteFunc(slices.Clone(config.Developers), func(id string) bool { return id == person.User })
		if len(who) == 0 {
			who = config.Owners
		}
		text := fmt.Sprintf(":sos: %s, %s needs a hand on the dev page: %s", appPeople(who), person.Name, appClipStart(strings.TrimSpace(request.What), 300))
		if spaces, err := appDevSpaces(b.team); err == nil {
			for _, space := range spaces {
				if space.ID == request.Workspace && space.Branch != "" {
					text += fmt.Sprintf(" (branch `%s`)", space.Branch)
				}
			}
		}
		if detail := appClipStart(strings.TrimSpace(request.Error), 1500); detail != "" {
			text += "\n> " + strings.ReplaceAll(detail, "\n", "\n> ")
		}
		if _, err := b.channel.post(config.Channel, text); err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/finish", func(w http.ResponseWriter, r *http.Request) {
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
		text, err := appDevFinish(config, request.Workspace, person.Name+" on the dev page", request.Discard)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{"text": text})
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/settings", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		var request appDevPageSetting
		// A file setting is up to appFileSettingMax, as base64.
		if err := json.NewDecoder(io.LimitReader(r.Body, appFileSettingMax/3*4+64<<10)).Decode(&request); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		if err := b.devPageSetting(request); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/release", func(w http.ResponseWriter, r *http.Request) {
		person, level, err := b.devPagePerson(r)
		if err != nil {
			failure(w, http.StatusUnauthorized, err)
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
		// Production's data is the owners': a developer releases the code.
		if request.PreviewData && level != appLevelOwner {
			failure(w, http.StatusForbidden, errors.New("only the app's owners start production with the preview's data; publish without it, or ask an owner to publish"))
			return
		}
		// The builder's click is the yes, logged with their name.
		release := appReleaseRequest{Notes: request.Notes, host: person.Name + " on the dev page"}
		if request.PreviewData {
			release.DataFrom = appSlotPreview
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
	byOwner := func(person appPersonKey) string { return person.Name + " on the dev page" }
	mux.HandleFunc("POST "+appDevPagePrefix+"api/rollback", func(w http.ResponseWriter, r *http.Request) {
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
		result, err := b.release(appReleaseRequest{Version: request.Version, Data: request.Data, host: byOwner(person)})
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, result)
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/dev/reset", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("POST "+appDevPagePrefix+"api/data/copy", func(w http.ResponseWriter, r *http.Request) {
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
		copy := appDataCopyRequest{From: appSlotPreview, To: appSlotProduction, owner: byOwner(person)}
		if request.To == appSlotPreview {
			copy.From, copy.To = appSlotProduction, appSlotPreview
		}
		result, err := b.copyData(copy)
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		result.Kept = ""
		reply(w, http.StatusOK, result)
	})
	mux.HandleFunc("GET "+appDevPagePrefix+"api/data", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); ok {
			reply(w, http.StatusOK, b.devPageData())
		}
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/data/snapshot", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		snapshot, err := b.snapshot(appSnapshotManual, "")
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, snapshot)
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/data/preview", func(w http.ResponseWriter, r *http.Request) {
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
		if err := appPreviewDataMode(b.team, mode, io.Discard); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/helper", func(w http.ResponseWriter, r *http.Request) {
		person, ok := builder(w, r)
		if !ok {
			return
		}
		var request appHelperRequest
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
	mux.HandleFunc("POST "+appDevPagePrefix+"api/incident", func(w http.ResponseWriter, r *http.Request) {
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
			_, err = b.restartApp(appJobsRequest{Incident: request.ID, Preview: request.Preview, Because: because, owner: byOwner(person)})
		case "close":
			_, err = b.incidentAction("close", appIncidentRequest{ID: request.ID, Because: because, owner: byOwner(person)})
		default:
			err = fmt.Errorf("no incident action %q", request.Action)
		}
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/rule", func(w http.ResponseWriter, r *http.Request) {
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
			_, err = b.addRule(appIncidentRequest{Job: request.Job, Text: request.Rule, ID: request.Incident, owner: byOwner(person)})
		}
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	mux.HandleFunc("GET "+appDevPagePrefix+"api/presence", func(w http.ResponseWriter, r *http.Request) {
		person, _, err := b.devPagePerson(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		devPresenceOf(b.team).serve(w, r, firstNonEmpty(person.User, person.Key), firstNonEmpty(person.Name, person.User), func(r *http.Request) bool { return appDevSameOrigin(b.team, r) })
	})
	mux.HandleFunc("GET "+appDevPagePrefix+"api/term", func(w http.ResponseWriter, r *http.Request) {
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
		case level != appLevelOwner && !appDevDeveloperRoutes[pattern]:
			return http.StatusForbidden, errors.New("only the app's owners do that; developers build and release, and owners look after production")
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
			appDevSocketsOpen.Add(1)
			defer appDevSocketsOpen.Done()
			// A terminal: it ends when its person may no longer have it,
			// not only at their next click.
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			r = r.WithContext(ctx)
			go func() {
				tick := time.NewTicker(appDevAccessCheck)
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
		// A change comes from the page itself. The person's cookie goes with
		// a request from the app's preview or production too, on the same
		// machine at another port or a sibling name, where code from the dev
		// box runs: that one is refused.
		if !appDevFromPage(b.team, r) {
			failure(w, http.StatusForbidden, errors.New("that came from another page than the dev page; open the dev page itself"))
			return
		}
		record := &appDevPageRecorder{ResponseWriter: w, status: http.StatusOK}
		mux.ServeHTTP(record, r)
		who := "someone"
		if person, _, err := b.devPagePerson(r); err == nil {
			who = person.Name
		}
		line := fmt.Sprintf("hi: the dev page: %s %s: %d", who, strings.TrimPrefix(r.URL.Path, appDevPagePrefix), record.status)
		if record.status != http.StatusOK && record.failure != "" {
			line += " " + record.failure
		}
		fmt.Fprintln(os.Stderr, line)
	})
}

// appDevPageRecorder keeps an answer's status, and its error, for the log.
type appDevPageRecorder struct {
	http.ResponseWriter
	status  int
	failure string
}

func (r *appDevPageRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *appDevPageRecorder) Write(data []byte) (int, error) {
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

// appDevTermCommand is the herdr client the page's terminal runs in the
// box; tests replace it.
var appDevTermCommand = func(team string) []string {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return nil
	}
	return []string{engine.bin, "exec", "-it", "-e", "TERM=xterm-256color", "-e", "COLORTERM=truecolor", appDevContainer(team), "herdr"}
}

// appDevSocketsOpen counts the dev page's terminals open, so a test waits for them to end before it puts its settings back.
var appDevSocketsOpen sync.WaitGroup

// appDevFromPage says a change comes from the dev page: a browser says
// where a request comes from, in Sec-Fetch-Site and Origin, and both must
// be the page. A request with neither is no browser's (hi's tests, curl),
// and needs the person's key all the same.
func appDevFromPage(team string, r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" {
		fmt.Fprintf(os.Stderr, "hi: the dev page refused %s from a %s page\n", r.URL.Path, site)
		return false
	}
	if r.Header.Get("Origin") == "" {
		return true
	}
	return appDevSameOrigin(team, r)
}

// appDevSameOrigin says a WebSocket comes from the page itself: its
// Origin is the page's address, as hi exposed it, or the host the request
// came for. NetBird's proxy needn't pass on the host the browser asked
// for, so the exposed address is the one to trust.
func appDevSameOrigin(team string, r *http.Request) bool {
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
	fmt.Fprintf(os.Stderr, "hi: the dev page refused %s from %s (the request was for %s)\n", r.URL.Path, origin.Host, strings.Join(hosts, ", "))
	return false
}

// devTerminal is one browser's terminal: a herdr client under a terminal
// of its own on this machine, its output to the page, and the page's keys
// to it. Binary messages are the terminal's bytes; a text message is
// {"cols":..,"rows":..} when the page's size changes.
func (b *appBroker) devTerminal(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return appDevSameOrigin(b.team, r) }}
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
	command := appDevTermCommand(b.team)
	if len(command) == 0 {
		fail("hi found no box engine on the machine")
		return
	}
	master, slave, err := appOpenPTY()
	if err != nil {
		fail("no terminal: " + err.Error())
		return
	}
	defer master.Close()
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	appResizePTY(master, cols, rows)
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
				appResizePTY(master, size.Cols, size.Rows)
			}
			continue
		}
		if _, err := master.Write(data); err != nil {
			return
		}
	}
}

// appOpenPTY opens a pseudo-terminal: its master, and its other end for
// the process.
func appOpenPTY() (*os.File, *os.File, error) {
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

func appResizePTY(master *os.File, cols, rows int) {
	if cols <= 0 || rows <= 0 || cols > 1000 || rows > 500 {
		return
	}
	unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(cols), Row: uint16(rows)})
}

// devConflictAnswer is what the page needs to ask what to do about a
// clash with dev: the files, the changes on the preview that touch them,
// and the last one, which can be taken out.
func (b *appBroker) devConflictAnswer(err error) (map[string]any, bool) {
	var conflict appDevConflictError
	if !errors.As(err, &conflict) {
		return nil, false
	}
	answer := map[string]any{"error": err.Error(), "conflict": map[string]any{"files": conflict.Files, "dev": conflict.Dev}}
	changes := appDevChanges(b.repo, loadTeamTasks(b.team))
	var with []map[string]string
	for i, change := range changes {
		touches := false
		for _, file := range appDevChangeFiles(b.repo, change) {
			touches = touches || slices.Contains(conflict.Files, file)
		}
		if touches {
			with = append(with, map[string]string{"id": change.ID, "title": change.Title})
			if i == len(changes)-1 {
				answer["last"] = map[string]string{"id": change.ID, "title": change.Title}
			}
		}
	}
	answer["with"] = with
	return answer, true
}
