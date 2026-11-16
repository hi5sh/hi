package cli

// The page's Vault (hi_server_vault.md, release 2), in Keys: whether the
// server's vault is open, Unlock and Lock, the ways it opens, the
// recovery key for the owner, each path's versions with Rollback and
// Forget, and the removed apps' settings with Purge. A passphrase goes
// from the browser to the page and from the page to the server on its
// socket; the page keeps none. No value is ever in an answer.

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// adminVaultBrief is the vault in the page's state, for its banner.
type adminVaultBrief struct {
	Locked bool   `json:"locked,omitempty"`
	Why    string `json:"why,omitempty"`
	// Recovery says the recovery key waits for the owner to save it.
	Recovery bool `json:"recovery,omitempty"`
}

func adminVaultBriefOf() *adminVaultBrief {
	if !vaultExists() {
		return nil
	}
	brief := &adminVaultBrief{Recovery: vaultRecoveryWaitingHere()}
	if _, err := openVault(); err != nil {
		brief.Locked, brief.Why = true, err.Error()
		var locked vaultLockedError
		if errors.As(err, &locked) {
			brief.Why = locked.why
		}
	}
	return brief
}

// vaultRecoveryWaitingHere is whether the recovery key's words wait in the
// store, from its files alone: no value is read.
func vaultRecoveryWaitingHere() bool {
	return fileExists(vaultPathFileName(vaultDir(), vaultRecoveryPath))
}

type adminVaultPath struct {
	Path    string    `json:"path"`
	Version int       `json:"version"`
	Time    time.Time `json:"time"`
	By      string    `json:"by"`
	Names   []string  `json:"names"`
	Removed bool      `json:"removed,omitempty"`
}

type adminVaultView struct {
	Exists   bool             `json:"exists"`
	Open     bool             `json:"open"`
	Why      string           `json:"why,omitempty"`
	Kinds    []string         `json:"kinds"`
	Protects string           `json:"protects"`
	Advice   []string         `json:"advice"`
	Recovery bool             `json:"recovery"`
	Owner    bool             `json:"owner"`
	OwnerIs  string           `json:"owner_is,omitempty"`
	Paths    []adminVaultPath `json:"paths"`
	// Removed are removed apps whose settings the vault keeps.
	Removed   []string `json:"removed"`
	Heartbeat bool     `json:"heartbeat"`
	TPM       string   `json:"tpm,omitempty"` // why not, when the TPM can't keep it
	Warning   string   `json:"warning"`       // before the last way it opens by itself goes
	// Shared are the shared keys, and Apps the apps they can be given to.
	Shared []vaultSharedRow `json:"shared"`
	Apps   []string         `json:"apps"`
	// Stores are the team stores connected; Sources the paths apps take
	// from them; Moved where hi's own paths are, when not here.
	Stores  []adminVaultStore  `json:"stores"`
	Sources []adminVaultSource `json:"sources"`
	Moved   string             `json:"moved,omitempty"`
}

type adminVaultStore struct {
	Prefix  string `json:"prefix"`
	Address string `json:"address"`
	Where   string `json:"where"`
	Login   string `json:"login"`
	Role    string `json:"role"`
	Handoff string `json:"handoff"`
	Ends    string `json:"ends,omitempty"` // an AppRole secret ID that ends soon
}

type adminVaultSource struct {
	From    string            `json:"from"`
	Apps    []string          `json:"apps"`
	Version int               `json:"version"`
	Read    time.Time         `json:"read,omitzero"`
	Names   []string          `json:"names"`
	Left    map[string]string `json:"left,omitempty"`
	Error   string            `json:"error,omitempty"`
}

func adminVaultViewOf(admin adminPerson) adminVaultView {
	view := adminVaultView{Exists: vaultExists(), Kinds: []string{}, Advice: []string{}, Paths: []adminVaultPath{}, Removed: []string{}, Shared: []vaultSharedRow{}, Apps: machineAppNames(),
		Owner: admin.MachineOwner, Warning: vaultNoAutoWarning}
	_, view.Heartbeat = loadMachineHeartbeat()
	if config, ok := loadAdminConfig(); ok {
		view.OwnerIs = config.machineOwner()
	}
	if !view.Exists {
		return view
	}
	keys, err := loadVaultKeys()
	if err != nil {
		view.Why = err.Error()
		return view
	}
	view.Kinds, view.Protects, view.Advice = keys.kinds(), vaultProtects(keys.kinds()), append(view.Advice, vaultAdvice(keys)...)
	view.Recovery = vaultRecoveryWaitingHere()
	if ok, why := vaultTPMReady(); !ok {
		view.TPM = why
	}
	store, err := openVault()
	if err != nil {
		view.Why = err.Error()
		var locked vaultLockedError
		if errors.As(err, &locked) {
			view.Why = locked.why
		}
		return view
	}
	view.Open = true
	files, _ := store.paths("")
	removed := map[string]bool{}
	for _, file := range files {
		if file.Path == vaultRecoveryPath || strings.HasPrefix(file.Path, "hi/connected/") || len(file.Versions) == 0 {
			continue
		}
		latest := file.Versions[len(file.Versions)-1]
		row := adminVaultPath{Path: file.Path, Version: latest.Version, Time: latest.Time, By: latest.By, Names: []string{}, Removed: file.Removed != nil}
		if data, err := store.decrypt(file.Path, latest); err == nil {
			row.Names = data.names()
		}
		view.Paths = append(view.Paths, row)
		if app, ok := strings.CutPrefix(file.Path, "hi/app/"); ok && file.Removed != nil {
			removed[strings.SplitN(app, "/", 2)[0]] = true
		}
	}
	for name := range removed {
		view.Removed = append(view.Removed, name)
	}
	sort.Strings(view.Removed)
	if rows, err := vaultSharedRows(); err == nil && rows != nil {
		view.Shared = rows
	}
	view.Stores, view.Sources, view.Moved = []adminVaultStore{}, []adminVaultSource{}, vaultMovedLine()
	if conns, err := loadVaultConns(); err == nil {
		for _, conn := range conns.Conns {
			row := adminVaultStore{Prefix: conn.Prefix, Address: conn.Address, Where: vaultConnWhere(conn), Login: conn.Login, Role: conn.Role, Handoff: vaultHandoff(conn, false)}
			if !conn.SecretIDEnds.IsZero() && time.Until(conn.SecretIDEnds) < 14*24*time.Hour {
				row.Ends = conn.SecretIDEnds.Local().Format("Jan 2")
			}
			view.Stores = append(view.Stores, row)
		}
	}
	states := loadVaultSources()
	used := appSourcesUsed()
	for _, from := range sortedKeys(used) {
		row := adminVaultSource{From: from, Apps: used[from], Names: []string{}}
		if state := states[from]; state != nil {
			row.Version, row.Read, row.Left, row.Error = state.Version, state.Read, state.Left, state.Error
			row.Names = append(row.Names, state.Names...)
		}
		view.Sources = append(view.Sources, row)
	}
	if view.Apps == nil {
		view.Apps = []string{}
	}
	return view
}

type adminVaultVersion struct {
	Version   int        `json:"version"`
	Time      time.Time  `json:"time"`
	By        string     `json:"by"`
	Changed   []string   `json:"changed"`
	Rollback  int        `json:"rollback,omitempty"`
	Forgotten *time.Time `json:"forgotten,omitempty"`
}

// adminVaultAsked is the two words a recovery key's owner was asked back,
// by who was shown it.
var adminVaultAsked = struct {
	sync.Mutex
	positions map[string][]int
}{positions: map[string][]int{}}

func (s *adminServer) vaultRoutes(mux *http.ServeMux, reply func(http.ResponseWriter, int, any),
	decode func(http.ResponseWriter, *http.Request, any) bool,
	signedIn func(func(http.ResponseWriter, *http.Request, adminPerson)) http.HandlerFunc) {
	failure := func(w http.ResponseWriter, status int, err error) {
		reply(w, status, map[string]string{"error": err.Error()})
	}
	mux.HandleFunc("GET /api/vault", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		reply(w, http.StatusOK, adminVaultViewOf(admin))
	}))
	mux.HandleFunc("GET /api/vault/history", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		path := r.URL.Query().Get("path")
		if !validVaultPath(path) || path == vaultRecoveryPath {
			failure(w, http.StatusBadRequest, errors.New("no such path"))
			return
		}
		store, err := openVault()
		if err != nil {
			failure(w, http.StatusConflict, err)
			return
		}
		file, err := store.load(path)
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		versions := []adminVaultVersion{}
		for i := len(file.Versions) - 1; i >= 0; i-- {
			v := file.Versions[i]
			versions = append(versions, adminVaultVersion{Version: v.Version, Time: v.Time, By: v.By, Changed: append([]string{}, v.Changed...), Rollback: v.Rollback, Forgotten: v.Forgotten})
		}
		answer := map[string]any{"path": path, "versions": versions, "names": []map[string]any{}, "how": adminVaultPathHow(path)}
		if _, ok := adminVaultSettingsPath(path); ok {
			answer["editable"] = true
		}
		if data, _, found, err := store.get(path); err == nil && found {
			names := []map[string]any{}
			for _, name := range data.names() {
				_, file := data.Files[name]
				names = append(names, map[string]any{"name": name, "file": file})
			}
			answer["names"] = names
		}
		reply(w, http.StatusOK, answer)
	}))
	mux.HandleFunc("POST /api/vault", signedIn(func(w http.ResponseWriter, r *http.Request, admin adminPerson) {
		var request struct {
			Action  string              `json:"action"`
			Secret  string              `json:"secret"`
			Kind    string              `json:"kind"`
			Path    string              `json:"path"`
			Version int                 `json:"version"`
			App     string              `json:"app"`
			Yes     bool                `json:"yes"`
			Words   []string            `json:"words"`
			Name    string              `json:"name"`
			Store   adminVaultStoreForm `json:"store"`
			Apps    []string            `json:"apps"`
		}
		if !decode(w, r, &request) {
			return
		}
		who := admin.orgName()
		var text string
		var extra map[string]any
		var err error
		switch request.Action {
		case "shared-set":
			text, err = vaultSharedSet(request.Name, request.Secret, who)
		case "shared-give", "shared-take":
			text, err = vaultSharedGive(request.Name, []string{request.App}, request.Action == "shared-take", who)
		case "shared-apps":
			text, err = vaultSharedSetApps(request.Name, request.Apps, who)
		case "shared-rm":
			text, err = vaultSharedRemove(request.Name, who)
		case "store-connect", "store-disconnect", "store-login", "store-new-key":
			text, err = adminVaultStoreChange(request.Action, request.Store, who)
		default:
			text, extra, err = adminVaultChange(admin, who, request.Action, request.Secret, request.Kind, request.Path, request.Version, request.App, request.Yes, request.Words)
		}
		if err == nil && strings.HasPrefix(request.Action, "shared-") {
			adminRecord(admin.Name, "", strings.TrimPrefix(request.Action, "shared-")+" the shared key "+request.Name)
		}
		if err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		answer := map[string]any{"text": text}
		for k, v := range extra {
			answer[k] = v
		}
		reply(w, http.StatusOK, answer)
	}))
}

// adminVaultChange is one of the page's changes to the vault.
func adminVaultChange(admin adminPerson, who, action, secret, kind, path string, version int, app string, yes bool, words []string) (string, map[string]any, error) {
	server := func(route string, body any) (string, error) {
		var said vaultSaidReply
		err := vaultServerCallAs(who+" (the admin page)", "POST", route, body, &said)
		if errors.Is(err, errVaultNoServer) {
			return "", errors.New("the server isn't running, and it holds the vault: start it (systemctl --user start hi-server.service)")
		}
		return said.Said, err
	}
	switch action {
	case "unlock":
		said, err := server("/admin/vault/unlock", vaultSecretRequest{Secret: secret})
		if err != nil {
			return "", nil, err
		}
		vaultForget()
		adminRecord(admin.Name, "", "unlocked the server's vault")
		return "The vault is open (" + said + "). The apps that waited start within a minute.", nil, nil
	case "lock":
		if _, err := server("/admin/vault/lock", nil); err != nil {
			return "", nil, err
		}
		vaultForget()
		adminRecord(admin.Name, "", "locked the server's vault")
		return "The server forgot the vault's key. Running boxes keep running; one that starts waits until someone unlocks.", nil, nil
	case "add", "remove":
		said, err := server("/admin/vault/slot", vaultSecretRequest{Action: action, Kind: kind, Secret: secret, Yes: yes})
		if err != nil {
			return "", nil, err
		}
		adminRecord(admin.Name, "", map[string]string{"add": "added", "remove": "removed"}[action]+" the vault's "+kind+" slot")
		return said, nil, nil
	case "recovery-new", "recovery-show", "recovery-saved":
		if !admin.MachineOwner {
			return "", nil, errors.New("only the server's owner sees and makes the recovery key")
		}
		return adminVaultRecovery(admin, who, action, words, server)
	case "rollback", "forget":
		if !validVaultPath(path) || path == vaultRecoveryPath || version < 1 {
			return "", nil, errors.New("no such version")
		}
		store, err := openVault()
		if err != nil {
			return "", nil, err
		}
		if action == "rollback" {
			if err := store.rollback(path, version, who); err != nil {
				return "", nil, err
			}
			vaultAudit(who, "vault.rollback", path, fmt.Sprintf("to version %d", version))
			adminRecord(admin.Name, "", fmt.Sprintf("put version %d of %s back", version, path))
			return fmt.Sprintf("%s has version %d's values again, as a new version. A box gets them within a minute.", path, version), nil, nil
		}
		if err := store.forget(path, version); err != nil {
			return "", nil, err
		}
		vaultAudit(who, "vault.forget", path, fmt.Sprintf("version %d's value", version))
		adminRecord(admin.Name, "", fmt.Sprintf("forgot version %d of %s", version, path))
		return fmt.Sprintf("Version %d of %s is forgotten; its entry stays. Backups of hi's state taken before still hold it, so change the secret at its source too.", version, path), nil, nil
	case "path-set", "path-remove":
		// A name in an app's settings path, through hi app env, so a
		// setting changed here is checked and reaches the boxes as any.
		target, ok := adminVaultSettingsPath(path)
		if !ok {
			return "", nil, fmt.Errorf("%s isn't changed here: %s", path, adminVaultPathHow(path))
		}
		if !appEnvName.MatchString(kind) {
			return "", nil, fmt.Errorf("%q isn't a setting's name: letters, digits, and _, such as STRIPE_KEY", kind)
		}
		args := append([]string{target.app}, target.flags...)
		if action == "path-remove" {
			args = append(args, "--unset", kind)
		} else {
			if secret == "" || strings.ContainsAny(secret, "\r\n") {
				return "", nil, errors.New("a setting's value is one line, not empty")
			}
			args = append(args, kind+"="+secret)
		}
		var out strings.Builder
		os.Setenv("HI_ACTOR", who)
		err := appEnv(args, strings.NewReader(""), &out)
		os.Unsetenv("HI_ACTOR")
		if err != nil {
			return "", nil, err
		}
		adminRecord(admin.Name, target.app, map[string]string{"path-set": "set ", "path-remove": "removed "}[action]+kind+" in "+path)
		return strings.SplitN(strings.TrimSpace(out.String()), "\n", 2)[0], nil, nil
	case "purge":
		if !appNamePattern.MatchString(app) || !vaultRemovedApp(app) {
			return "", nil, errors.New("no removed app's settings by that name")
		}
		if _, err := os.Lstat(filepath.Join(appsDir(), app)); err == nil {
			return "", nil, fmt.Errorf("app %s is on this machine again", app)
		}
		n, err := vaultPurgeApp(app)
		if err != nil {
			return "", nil, err
		}
		if err := os.RemoveAll(appSnapshotsDir(app)); err != nil {
			return "", nil, fmt.Errorf("its settings are deleted, but its snapshots stay: %w", err)
		}
		adminRecord(admin.Name, app, "purged the removed app's settings and snapshots")
		return fmt.Sprintf("Removed app %s's settings (%d paths), its snapshot key, and its local snapshots are deleted. An offsite copy stays, and so does the history of who changed what.", app, n), nil, nil
	}
	return "", nil, fmt.Errorf("no such change: %q", action)
}

// adminVaultRecovery is the recovery key on the page, for the owner:
// shown once, two words asked back, then forgotten; or a new one.
func adminVaultRecovery(admin adminPerson, who, action string, words []string, server func(string, any) (string, error)) (string, map[string]any, error) {
	store, err := openVault()
	if err != nil {
		return "", nil, err
	}
	switch action {
	case "recovery-new":
		if _, err := server("/admin/vault/recovery", nil); err != nil {
			return "", nil, err
		}
		adminRecord(admin.Name, "", "made a new recovery key for the vault")
		return "A new recovery key waits for you: the old words stop working on this machine. Backups of hi's state taken before still open with the old words.", nil, nil
	case "recovery-show":
		data, _, ok, err := store.get(vaultRecoveryPath)
		if err != nil {
			return "", nil, err
		}
		if !ok {
			return "", nil, errors.New("the recovery key was saved already, and hi forgot it; a new one replaces it")
		}
		all := strings.Fields(data.Values[vaultRecoveryName])
		asked := vaultTwoPositions(len(all))
		adminVaultAsked.Lock()
		adminVaultAsked.positions[admin.Name] = asked
		adminVaultAsked.Unlock()
		vaultAudit(who, "vault.recovery-shown", "vault", "on the admin page")
		return "", map[string]any{"words": all, "ask": []int{asked[0] + 1, asked[1] + 1}}, nil
	}
	adminVaultAsked.Lock()
	asked := adminVaultAsked.positions[admin.Name]
	adminVaultAsked.Unlock()
	if len(asked) != 2 || len(words) != 2 {
		return "", nil, errors.New("show the recovery key first")
	}
	data, _, ok, err := store.get(vaultRecoveryPath)
	if err != nil || !ok {
		return "", nil, errors.New("the recovery key was saved already")
	}
	all := strings.Fields(data.Values[vaultRecoveryName])
	for i, position := range asked {
		if strings.ToLower(strings.TrimSpace(words[i])) != all[position] {
			return "", nil, fmt.Errorf("word %d isn't that; the recovery key still waits", position+1)
		}
	}
	if _, err := store.purge(vaultRecoveryPath); err != nil {
		return "", nil, err
	}
	adminVaultAsked.Lock()
	delete(adminVaultAsked.positions, admin.Name)
	adminVaultAsked.Unlock()
	vaultAudit(who, "vault.recovery-saved", "vault", "hi forgot the words")
	adminRecord(admin.Name, "", "saved the vault's recovery key")
	return "Saved, and hi has forgotten it. With it, the vault opens on any machine.", nil, nil
}

// adminVaultStoreForm is the page's form for a team store.
type adminVaultStoreForm struct {
	Prefix    string `json:"prefix"`
	Address   string `json:"address"`
	Where     string `json:"where"` // vault://<mount>[/<path>]
	Login     string `json:"login"`
	Namespace string `json:"namespace"`
	RoleID    string `json:"role_id"`
	SecretID  string `json:"secret_id"`
}

// adminVaultStoreChange runs the command's own code for a team store,
// and gives what it said.
func adminVaultStoreChange(action string, form adminVaultStoreForm, who string) (string, error) {
	var out strings.Builder
	var err error
	os.Setenv("HI_ACTOR", who)
	defer os.Unsetenv("HI_ACTOR")
	switch action {
	case "store-connect":
		args := []string{form.Prefix, form.Where, "--address", form.Address, "--login", firstNonEmpty(form.Login, "jwt")}
		if form.Namespace != "" {
			args = append(args, "--namespace", form.Namespace)
		}
		err = vaultConnectCommand(args, strings.NewReader(form.RoleID+"\n"+form.SecretID+"\n"), &out)
	case "store-disconnect":
		err = vaultDisconnectCommand([]string{form.Prefix}, &out)
	case "store-login":
		err = vaultLoginCommand([]string{form.Prefix}, &out)
	case "store-new-key":
		err = vaultLoginCommand([]string{form.Prefix, "--new-key"}, &out)
	}
	return strings.TrimSpace(out.String()), err
}

// adminVaultSettings is an app's settings path: the app, and hi app env's
// flag for its slot.
type adminVaultSettings struct {
	app   string
	flags []string
}

// adminVaultSettingsPath says whether a path is one of an app's settings,
// which the page changes name by name.
func adminVaultSettingsPath(path string) (adminVaultSettings, bool) {
	rest, ok := strings.CutPrefix(path, "hi/app/")
	app, slot, found := strings.Cut(rest, "/")
	if !ok || !found || !appNamePattern.MatchString(app) {
		return adminVaultSettings{}, false
	}
	flags, ok := map[string][]string{"shared": nil, "production": {"--production"}, "stage": {"--preview"}, "dev": {"--dev"}}[slot]
	if !ok {
		return adminVaultSettings{}, false
	}
	if _, err := loadTeam(app); err != nil {
		return adminVaultSettings{}, false
	}
	return adminVaultSettings{app: app, flags: flags}, true
}

// adminVaultPathHow says where a path's names are changed, for one the
// page doesn't change name by name.
func adminVaultPathHow(path string) string {
	parts := strings.Split(path, "/")
	switch {
	case len(parts) == 4 && parts[1] == "app":
		app := parts[2]
		switch parts[3] {
		case "shared", "production", "stage", "dev":
			return "the app's Settings, here, or on its dev page; a file setting with hi app env " + app + " --file NAME=<path>"
		case "hi":
			return "hi's own for the app: its Slack tokens (the app's Slack), its OpenRouter key (hi app key " + app + " openrouter), and its sign-ins (the app's Agents)"
		case "offsite":
			return "hi app backups " + app + " --offsite"
		case "snapshots":
			return "never by hand: it opens the app's snapshots"
		}
	case len(parts) == 3 && parts[1] == "shared":
		return "Shared keys, below: Change value"
	case path == "hi/server/keys":
		return "the server's Keys (its providers, AI, data, and templates)"
	case path == "hi/server/slack":
		return "the server's Slack"
	case path == "hi/machine/dns":
		return "hi net domain"
	case path == "hi/machine/netbird":
		return "hi app domain <domain> --new-token (NetBird's API token)"
	case path == "hi/machine/mail":
		return "Keys (left panel), Email"
	case path == vaultConnPath:
		return "Team stores, below"
	case path == openRouterManagementPath:
		return "Keys (left panel), OpenRouter management key"
	}
	return "the command that set it"
}
