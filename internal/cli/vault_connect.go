package cli

// A team's OpenBao or Vault beside the built-in store (hi_server_vault.md,
// release 3): hi server vault connect corp/ vault://kv/corp keeps
// everything under corp/ in the team's store, read only, and an app takes
// a path's names as its settings: hi app env payments --from
// corp/payments/prod. The server reads each such path's metadata every
// minute; a new version is a change of the app's settings, and the
// values are kept, encrypted with the built-in store's key, so a box
// that starts while the team's store is away uses that copy.

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const vaultConnPath = "hi/machine/vault"

var vaultPrefixPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// vaultConns is hi/machine/vault: the team stores hi reads, and the one
// hi's own paths moved to.
type vaultConns struct {
	Conns []vaultConn `json:"conns,omitempty"`
	Moved *vaultConn  `json:"moved,omitempty"`
	// Pending is a move waiting for the store's owners to set up hi's
	// login; it keeps its key pair.
	Pending *vaultConn `json:"pending,omitempty"`
	Saved   time.Time  `json:"saved,omitzero"`
}

func loadVaultConns() (vaultConns, error) {
	var conns vaultConns
	if !vaultExists() {
		return conns, nil
	}
	store, err := openVault()
	if err != nil {
		return conns, err
	}
	data, _, ok, err := store.getLocal(vaultConnPath)
	if err != nil || !ok {
		return conns, err
	}
	err = json.Unmarshal([]byte(data.Values["config"]), &conns)
	return conns, err
}

func saveVaultConns(conns vaultConns, who string) error {
	store, err := openVault()
	if err != nil {
		return err
	}
	conns.Saved = time.Now().UTC()
	data, err := json.Marshal(conns)
	if err != nil {
		return err
	}
	return store.putLocal(vaultConnPath, vaultData{Values: map[string]string{"config": string(data)}}, who)
}

func (c vaultConns) find(prefix string) (vaultConn, bool) {
	for _, conn := range c.Conns {
		if conn.Prefix == prefix {
			return conn, true
		}
	}
	return vaultConn{}, false
}

// vaultSource is a team store's path as an app names it:
// corp/payments/prod, or corp/payments/prod?version=12, pinned.
type vaultSource struct {
	Prefix string
	Path   string // under the connection
	Pin    int
}

func parseVaultSource(text string) (vaultSource, error) {
	base, query, _ := strings.Cut(text, "?")
	prefix, path, ok := strings.Cut(strings.Trim(base, "/"), "/")
	if !ok || !vaultPrefixPattern.MatchString(prefix) || path == "" || strings.Contains(path, "..") || prefix == "shared" {
		return vaultSource{}, fmt.Errorf("%q isn't a path in a connected store, such as corp/payments/prod (hi server vault connect)", text)
	}
	source := vaultSource{Prefix: prefix, Path: path}
	if query != "" {
		version, ok := strings.CutPrefix(query, "version=")
		n, err := strconv.Atoi(version)
		if !ok || err != nil || n < 1 {
			return vaultSource{}, fmt.Errorf("%q: a pin is ?version=<n>", text)
		}
		source.Pin = n
	}
	return source, nil
}

func (s vaultSource) String() string {
	text := s.Prefix + "/" + s.Path
	if s.Pin > 0 {
		text += "?version=" + strconv.Itoa(s.Pin)
	}
	return text
}

// cachePath is where the built-in store keeps the source's last read.
func (s vaultSource) cachePath() string {
	sum := sha256.Sum256([]byte(s.String()))
	return "hi/connected/" + hex.EncodeToString(sum[:8])
}

// vaultSourceState is what hi knows of a source between reads; no value.
type vaultSourceState struct {
	Version int               `json:"version,omitempty"`
	Hash    string            `json:"hash,omitempty"` // KV v1: what was read, hashed
	Read    time.Time         `json:"read,omitzero"`
	Names   []string          `json:"names,omitempty"`
	Left    map[string]string `json:"left,omitempty"` // names left out, and why
	Error   string            `json:"error,omitempty"`
	Since   time.Time         `json:"since,omitzero"` // failing since
	Refused bool              `json:"refused,omitempty"`
	Told    bool              `json:"told,omitempty"` // the outage told
}

func vaultSourcesFile() string { return filepath.Join(vaultDir(), "connected.json") }

var vaultSourcesMu sync.Mutex

func loadVaultSources() map[string]*vaultSourceState {
	states := map[string]*vaultSourceState{}
	if data, err := os.ReadFile(vaultSourcesFile()); err == nil {
		json.Unmarshal(data, &states)
	}
	return states
}

func updateVaultSources(change func(map[string]*vaultSourceState)) {
	vaultSourcesMu.Lock()
	defer vaultSourcesMu.Unlock()
	states := loadVaultSources()
	change(states)
	if data, err := json.MarshalIndent(states, "", "  "); err == nil {
		writeFileAtomic(vaultSourcesFile(), append(data, '\n'), 0o600)
	}
}

// ---------------------------------------------------------------------------
// reading a source

// vaultReadSource reads a source from the team's store and keeps it: the
// values in the built-in store, what hi knows of it in connected.json.
// changed are the names whose values changed since the last read.
func vaultReadSource(source vaultSource) (state vaultSourceState, changed []string, err error) {
	conns, err := loadVaultConns()
	if err != nil {
		return state, nil, err
	}
	conn, ok := conns.find(source.Prefix)
	if !ok {
		return state, nil, fmt.Errorf("no store is connected as %s/ (hi server vault connect)", source.Prefix)
	}
	kv, err := vaultKVFor(conn)
	if err != nil {
		return state, nil, err
	}
	secret, err := kv.read(source.Path, source.Pin)
	if err != nil {
		return state, nil, err
	}
	values, left := vaultKVStrings(secret.Data)
	for name := range values {
		if why := appTeamNameProblem(name); why != "" {
			left[name] = why
			delete(values, name)
		}
	}
	store, err := openVault()
	if err != nil {
		return state, nil, err
	}
	before, _, _, _ := store.getLocal(source.cachePath())
	changed = changedNames(before, vaultData{Values: values})
	if err := store.putLocal(source.cachePath(), vaultData{Values: values}, "hi"); err != nil {
		return state, nil, err
	}
	state = vaultSourceState{Version: secret.Version, Read: time.Now().UTC(), Names: sortedKeys(values), Left: left}
	if secret.Version == 0 {
		state.Hash = vaultValuesHash(values)
	}
	key := source.String()
	updateVaultSources(func(states map[string]*vaultSourceState) {
		copied := state
		states[key] = &copied
	})
	return state, changed, nil
}

func vaultValuesHash(values map[string]string) string {
	sum := sha256.New()
	for _, name := range sortedKeys(values) {
		fmt.Fprintf(sum, "%s=%q\n", name, values[name])
	}
	return hex.EncodeToString(sum.Sum(nil))[:16]
}

// appTeamNameProblem says why a name from a team's store isn't a setting,
// or "".
func appTeamNameProblem(name string) string {
	switch {
	case !appEnvName.MatchString(name):
		return "it isn't a variable name"
	case appBoxEnvReserved[name]:
		return "hi sets it in the app's box itself"
	}
	return ""
}

// vaultSourceValues are a source's values as last read, from the
// built-in store; no network. An error says it was never read.
func vaultSourceValues(source vaultSource) (map[string]string, error) {
	store, err := openVault()
	if err != nil {
		return nil, err
	}
	data, _, ok, err := store.getLocal(source.cachePath())
	if err != nil {
		return nil, err
	}
	if !ok {
		state := loadVaultSources()[source.String()]
		why := "it hasn't been read yet"
		if state != nil && state.Error != "" {
			why = state.Error
		}
		return nil, fmt.Errorf("%s isn't read from the team's store: %s", source, why)
	}
	return data.Values, nil
}

// ---------------------------------------------------------------------------
// an app's sources

// appEnvSource is a team store's path an app takes settings from, with
// the names it leaves out.
type appEnvSource struct {
	From string   `json:"from"`
	Skip []string `json:"skip,omitempty"`
}

// appTeamScope is one scope's values from its sources, first source first:
// a name a later source also gives keeps the first's value (clashes are
// told). It fails when a source was never read.
func appTeamScope(config appConfig, scope string) (map[string]string, map[string]string, error) {
	values, from := map[string]string{}, map[string]string{}
	for _, src := range config.EnvSources[scope] {
		source, err := parseVaultSource(src.From)
		if err != nil {
			return nil, nil, err
		}
		got, err := vaultSourceValues(source)
		if err != nil {
			return nil, nil, err
		}
		for name, value := range got {
			if slices.Contains(src.Skip, name) {
				continue
			}
			if _, taken := values[name]; taken {
				continue
			}
			values[name], from[name] = value, src.From
		}
	}
	return values, from, nil
}

// appSourcesUsed is every source the machine's apps use, and which apps.
func appSourcesUsed() map[string][]string {
	used := map[string][]string{}
	for _, name := range machineAppNames() {
		config, err := loadTeam(name)
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, list := range config.EnvSources {
			for _, src := range list {
				if !seen[src.From] {
					seen[src.From] = true
					used[src.From] = append(used[src.From], name)
				}
			}
		}
	}
	return used
}

// appOwnNames are the names the app sets itself, or by a shared key, that
// a slot of scope sees.
func appOwnNames(config appConfig, secrets appSecrets, scope string) map[string]string {
	names := map[string]string{}
	add := func(set map[string]string, what string) {
		for name := range set {
			names[name] = what
		}
	}
	maps := appEnvMaps(&secrets)
	files := appFileMaps(&secrets)
	switch scope {
	case appSlotDev:
		add(*maps.dev, "the app (dev)")
		add(*files.dev, "the app (dev)")
	case "":
		add(*maps.both, "the app")
		add(*files.both, "the app")
		add(*maps.production, "the app (production)")
		add(*maps.preview, "the app (Stage)")
	default:
		add(*maps.both, "the app")
		add(*files.both, "the app")
		if scope == appSlotProduction {
			add(*maps.production, "the app (production)")
		} else {
			add(*maps.preview, "the app (Stage)")
		}
	}
	for _, s := range []string{"", scope} {
		for name, source := range config.EnvFrom[s] {
			names[name] = source
		}
	}
	return names
}

// appScopesSeen are the scopes whose sources a slot of scope sees, and
// that see it.
func appScopesSeen(scope string) []string {
	switch scope {
	case appSlotDev:
		return []string{appSlotDev}
	case "":
		return []string{"", appSlotProduction, appSlotPreview}
	}
	return []string{"", scope}
}

// appAddSource adds a team store's path to an app's settings in scope,
// after reading it: a name that another source or the app itself gives
// is refused, unless it's skipped.
func appAddSource(config *appConfig, secrets appSecrets, scope, from string, skip []string, stdout io.Writer) error {
	source, err := parseVaultSource(from)
	if err != nil {
		return err
	}
	for _, src := range config.EnvSources[scope] {
		if src.From == source.String() {
			return fmt.Errorf("app %s takes %s already", config.Name, source)
		}
	}
	state, _, err := vaultReadSource(source)
	if err != nil {
		if vaultKVRefused(err) {
			return fmt.Errorf("%w: ask its owners for read on that path (hi server vault login prints what to hand them)", err)
		}
		return err
	}
	var clashes []string
	own := appOwnNames(*config, secrets, scope)
	for _, name := range state.Names {
		if slices.Contains(skip, name) {
			continue
		}
		if who, ok := own[name]; ok {
			clashes = append(clashes, fmt.Sprintf("%s (set by %s)", name, who))
			continue
		}
		for _, s := range appScopesSeen(scope) {
			for _, other := range config.EnvSources[s] {
				if slices.Contains(other.Skip, name) {
					continue
				}
				if otherState := loadVaultSources()[other.From]; otherState != nil && slices.Contains(otherState.Names, name) {
					clashes = append(clashes, fmt.Sprintf("%s (from %s)", name, other.From))
				}
			}
		}
	}
	if len(clashes) > 0 {
		sort.Strings(clashes)
		return fmt.Errorf("%s gives names the app has from elsewhere: %s. Leave each out of one (--skip NAME), or unset it here first", source, strings.Join(slices.Compact(clashes), ", "))
	}
	if config.EnvSources == nil {
		config.EnvSources = map[string][]appEnvSource{}
	}
	config.EnvSources[scope] = append(config.EnvSources[scope], appEnvSource{From: source.String(), Skip: skip})
	version := "version " + strconv.Itoa(state.Version)
	if state.Version == 0 {
		version = "a KV v1 path"
	}
	fmt.Fprintf(stdout, "App %s takes %s (%s): %s.\n", config.Name, source, version, strings.Join(slices.DeleteFunc(append([]string{}, state.Names...), func(n string) bool { return slices.Contains(skip, n) }), ", "))
	for _, name := range sortedKeys(state.Left) {
		fmt.Fprintf(stdout, "  %s is left out: %s.\n", name, state.Left[name])
	}
	if source.Pin == 0 {
		fmt.Fprintln(stdout, "A new version there reaches the app within a minute, as any change does; ?version=<n> pins one.")
	}
	return nil
}

// appRemoveSource drops a source from scope; it says whether there was one.
func appRemoveSource(config *appConfig, scope, from string) bool {
	list := config.EnvSources[scope]
	n := len(list)
	list = slices.DeleteFunc(list, func(s appEnvSource) bool { return s.From == from })
	if len(list) == n {
		return false
	}
	if len(list) == 0 {
		delete(config.EnvSources, scope)
	} else {
		config.EnvSources[scope] = list
	}
	return true
}

// appSourceGiving is the source that gives name to a slot of scope, or "".
func appSourceGiving(config appConfig, scope, name string) string {
	for _, s := range appScopesSeen(scope) {
		if scope != "" && s != "" && s != scope {
			continue
		}
		for _, src := range config.EnvSources[s] {
			if slices.Contains(src.Skip, name) {
				continue
			}
			if state := loadVaultSources()[src.From]; state != nil && slices.Contains(state.Names, name) {
				return src.From
			}
		}
	}
	return ""
}

func appOverrideMark(config *appConfig, scope, name string, on bool) {
	if on {
		if config.EnvOverride == nil {
			config.EnvOverride = map[string][]string{}
		}
		if !slices.Contains(config.EnvOverride[scope], name) {
			config.EnvOverride[scope] = append(config.EnvOverride[scope], name)
		}
		return
	}
	config.EnvOverride[scope] = slices.DeleteFunc(config.EnvOverride[scope], func(n string) bool { return n == name })
	if len(config.EnvOverride[scope]) == 0 {
		delete(config.EnvOverride, scope)
	}
}

// ---------------------------------------------------------------------------
// the server's minute

// vaultPollSources reads each source the apps use when its version moved,
// and tells the apps' ops channels what changed, by name, and when the
// team's store is away or refuses.
func vaultPollSources() {
	if !vaultExists() {
		return
	}
	conns, err := loadVaultConns()
	if err != nil || len(conns.Conns) == 0 {
		return
	}
	// Each app hears one message a minute at most, for all its sources.
	told := map[string][]string{}
	tell := func(apps []string, text string) {
		for _, app := range apps {
			if !slices.Contains(told[app], text) {
				told[app] = append(told[app], text)
			}
		}
	}
	used := appSourcesUsed()
	for _, from := range sortedKeys(used) {
		source, err := parseVaultSource(from)
		if err != nil {
			continue
		}
		vaultPollSource(conns, source, used[from], tell)
	}
	for _, app := range sortedKeys(told) {
		if config, err := loadTeam(app); err == nil {
			appOpsSay(config, strings.Join(told[app], "\n"))
		}
	}
}

var vaultSourceAwayAfter = 2 * time.Minute

// vaultSourcesEvery is how often the server looks at each source.
var vaultSourcesEvery = time.Minute

func vaultPollSource(conns vaultConns, source vaultSource, apps []string, tell func([]string, string)) {
	key := source.String()
	before := loadVaultSources()[key]
	_, cached := vaultSourceValuesQuiet(source)
	conn, ok := conns.find(source.Prefix)
	if !ok {
		return
	}
	fail := func(err error) {
		now := time.Now().UTC()
		var told string
		updateVaultSources(func(states map[string]*vaultSourceState) {
			state := states[key]
			if state == nil {
				state = &vaultSourceState{}
				states[key] = state
			}
			if state.Since.IsZero() {
				state.Since = now
			}
			state.Error, state.Refused = err.Error(), vaultKVRefused(err)
			if state.Told || (!state.Refused && now.Sub(state.Since) < vaultSourceAwayAfter) {
				return
			}
			state.Told = true
			switch {
			case state.Refused:
				told = fmt.Sprintf("The team's store refused hi's read of %s: ask its owners for read on that path; hi server vault login on the machine prints what to hand them.", key)
			case cached:
				told = fmt.Sprintf("The team's store didn't answer (%s); the app keeps %s version %d from %s, and a box that starts uses it.", err, key, state.Version, state.Read.Local().Format("Jan 2 15:04"))
			default:
				told = fmt.Sprintf("The team's store didn't answer (%s), and hi has no copy of %s yet: a box that starts can't get its settings until it answers.", err, key)
			}
		})
		if told != "" {
			tell(apps, ":warning: "+told)
		}
	}
	kv, err := vaultKVFor(conn)
	if err != nil {
		fail(err)
		return
	}
	if source.Pin > 0 && cached && before != nil && before.Version == source.Pin {
		vaultSourceGood(key, before, apps, tell)
		return
	}
	if source.Pin == 0 && cached && before != nil && before.Version > 0 {
		current, _, err := kv.current(source.Path)
		if err != nil {
			fail(err)
			return
		}
		if current == before.Version {
			vaultSourceGood(key, before, apps, tell)
			return
		}
	}
	state, changed, err := vaultReadSource(source)
	if err != nil {
		fail(err)
		return
	}
	vaultSourceGood(key, before, apps, tell)
	if before == nil || before.Read.IsZero() || len(changed) == 0 {
		return
	}
	version := fmt.Sprintf("version %d", state.Version)
	if state.Version == 0 {
		version = "a new value"
	}
	for _, app := range apps {
		config, err := loadTeam(app)
		if err != nil {
			continue
		}
		text := fmt.Sprintf(":arrows_counterclockwise: %s is %s now (changed %s); the app's boxes get it within a minute.", key, version, strings.Join(changed, ", "))
		if clashes := appLaterClashes(config, key, changed); len(clashes) > 0 {
			text += " " + strings.Join(clashes, " ")
		}
		tell([]string{app}, text)
	}
}

// appLaterClashes are what a source's new names clash with, told once:
// the first source keeps its value.
func appLaterClashes(config appConfig, key string, changed []string) []string {
	secrets, _ := loadTeamSecrets(config.Name)
	var out []string
	for scope, list := range config.EnvSources {
		for _, src := range list {
			if src.From != key {
				continue
			}
			own := appOwnNames(config, secrets, scope)
			for _, name := range changed {
				if slices.Contains(src.Skip, name) {
					continue
				}
				if slices.Contains(config.EnvOverride[scope], name) {
					out = append(out, fmt.Sprintf("%s overrides %s here and keeps its value.", strings.TrimSpace(map[string]string{"": "The app", appSlotProduction: "Production", appSlotPreview: "Stage", appSlotDev: "The dev box"}[scope]), name))
					continue
				}
				if who, ok := own[name]; ok {
					out = append(out, fmt.Sprintf("%s comes from %s too, which had it first and keeps it: leave it out of %s (--skip %s) or unset it there.", name, who, key, name))
				}
			}
		}
	}
	return slices.Compact(out)
}

// vaultSourceGood clears a failure, and tells the apps it answers again.
func vaultSourceGood(key string, before *vaultSourceState, apps []string, tell func([]string, string)) {
	if before == nil || before.Error == "" {
		return
	}
	told := before.Told
	updateVaultSources(func(states map[string]*vaultSourceState) {
		if state := states[key]; state != nil {
			state.Error, state.Since, state.Refused, state.Told = "", time.Time{}, false, false
		}
	})
	if told {
		tell(apps, ":white_check_mark: The team's store answers again; "+key+" is current.")
	}
}

func vaultSourceValuesQuiet(source vaultSource) (map[string]string, bool) {
	values, err := vaultSourceValues(source)
	return values, err == nil
}

// ---------------------------------------------------------------------------
// hi server vault connect, disconnect, login

func vaultConnectCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	usage := usageError{"usage: hi server vault connect <prefix>/ vault://<mount>[/<path>] --address https://<host>:8200 [--login jwt|approle] [--role <name>] [--auth-mount <path>] [--namespace <ns>] [--ca <file>] [--secret-id-ttl 30d]"}
	var positional []string
	conn := vaultConn{Login: "jwt", Created: time.Now().UTC()}
	caFile, ttl := "", ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func() string {
			if _, after, ok := strings.Cut(arg, "="); ok && strings.HasPrefix(arg, "--") {
				return after
			}
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		name, _, _ := strings.Cut(arg, "=")
		switch name {
		case "--address":
			conn.Address = value()
		case "--login":
			conn.Login = value()
		case "--role":
			conn.Role = value()
		case "--auth-mount":
			conn.AuthMount = value()
		case "--namespace":
			conn.Namespace = value()
		case "--ca":
			caFile = value()
		case "--secret-id-ttl":
			ttl = value()
		default:
			if strings.HasPrefix(arg, "-") {
				return usage
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) != 2 {
		return usage
	}
	conn.Prefix = strings.Trim(positional[0], "/")
	if !vaultPrefixPattern.MatchString(conn.Prefix) || conn.Prefix == "shared" || conn.Prefix == "hi" {
		return fmt.Errorf("%q isn't a prefix: lowercase letters, digits, and dashes, and not hi or shared", positional[0])
	}
	if conn.Address == "" {
		conn.Address = firstNonEmpty(os.Getenv("BAO_ADDR"), os.Getenv("VAULT_ADDR"))
	}
	mount, path, err := parseVaultURL(positional[1])
	if err != nil {
		return err
	}
	conn.Mount, conn.Path = mount, path
	if err := vaultCheckAddress(conn.Address); err != nil {
		return err
	}
	if conn.CA, err = vaultReadCA(caFile); err != nil {
		return err
	}
	if err := vaultSetupLogin(&conn, ttl, stdin, stdout); err != nil {
		return err
	}
	conns, err := loadVaultConns()
	if err != nil {
		return err
	}
	if _, ok := conns.find(conn.Prefix); ok {
		return fmt.Errorf("%s/ is connected already: hi server vault disconnect %s first", conn.Prefix, conn.Prefix)
	}
	tried := vaultConnTry(&conn)
	conns.Conns = append(conns.Conns, conn)
	if err := saveVaultConns(conns, vaultActor()); err != nil {
		return err
	}
	vaultAudit(vaultActor(), "vault.connect", conn.Prefix+"/", conn.Address+" "+conn.Mount+"/"+conn.Path)
	fmt.Fprintf(stdout, "%s/ is %s in %s.\n", conn.Prefix, vaultConnWhere(conn), conn.Address)
	fmt.Fprintln(stdout, tried)
	fmt.Fprintf(stdout, "\nFor the store's owners, once:\n\n%s\n", vaultHandoff(conn, false))
	return nil
}

// vaultSetupLogin makes the jwt key pair, or asks for the AppRole's IDs.
func vaultSetupLogin(conn *vaultConn, ttl string, stdin io.Reader, stdout io.Writer) error {
	if conn.Role == "" {
		conn.Role = "hi-server-" + vaultServerName()
	}
	switch conn.Login {
	case "jwt":
		conn.AuthMount = firstNonEmpty(conn.AuthMount, "jwt")
		conn.Seed = make([]byte, ed25519.SeedSize)
		rand.Read(conn.Seed)
	case "approle":
		conn.AuthMount = firstNonEmpty(conn.AuthMount, "approle")
		ids, err := readSecretLines(stdin, stdout, "The AppRole's role ID", "Its secret ID")
		if err != nil {
			return err
		}
		conn.RoleID, conn.SecretID = ids[0], ids[1]
		if conn.RoleID == "" || conn.SecretID == "" {
			return errors.New("an AppRole login needs its role ID and its secret ID")
		}
		if ttl != "" {
			d, err := parseDays(ttl)
			if err != nil {
				return err
			}
			conn.SecretIDEnds = time.Now().Add(d).UTC()
		}
	default:
		return fmt.Errorf("--login is jwt or approle, not %q", conn.Login)
	}
	return nil
}

// parseDays reads 30d, 12h, or a Go duration.
func parseDays(text string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(text, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 1 {
			return 0, fmt.Errorf("%q isn't a duration, such as 30d", text)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(text)
}

// parseVaultURL reads vault://<mount>[/<path>].
func parseVaultURL(text string) (string, string, error) {
	rest, ok := strings.CutPrefix(text, "vault://")
	mount, path, _ := strings.Cut(strings.Trim(rest, "/"), "/")
	if !ok || mount == "" {
		return "", "", fmt.Errorf("%q isn't a place in a store: vault://<mount>[/<path>], such as vault://kv/corp", text)
	}
	return mount, path, nil
}

func vaultConnWhere(conn vaultConn) string {
	return strings.TrimRight(conn.Mount+"/"+conn.Path, "/")
}

// vaultConnTry logs in, learns the mount's KV version, and says how it
// went.
func vaultConnTry(conn *vaultConn) string {
	kv, err := vaultKVFresh(*conn)
	if err == nil {
		_, err = kv.login()
	}
	if err != nil {
		return "hi can't log in yet (" + err.Error() + "): once its owners have set it up, hi server vault login checks it."
	}
	conn.KV = kv.kind()
	return "hi logs in to it."
}

// vaultHandoff is what the store's owners run, once: the auth mount, the
// policy, the role bound to this server's name, and the public key.
func vaultHandoff(conn vaultConn, write bool) string {
	where := vaultConnWhere(conn)
	policy := conn.Role
	var b strings.Builder
	caps := `["read"]`
	list := ""
	if write {
		caps = `["create", "read", "update", "delete", "list"]`
		list = fmt.Sprintf("path \"%s/destroy/%s/*\" { capabilities = [\"update\"] }\n", conn.Mount, strings.Trim(conn.Path, "/"))
	}
	inner := strings.Trim(conn.Path, "/")
	dataPath, metaPath := conn.Mount+"/data/"+inner+"/*", conn.Mount+"/metadata/"+inner+"/*"
	if inner == "" {
		dataPath, metaPath = conn.Mount+"/data/*", conn.Mount+"/metadata/*"
	}
	if conn.KV == 1 {
		fmt.Fprintf(&b, "bao policy write %s - <<'EOF'\npath \"%s/*\" { capabilities = %s }\nEOF\n", policy, strings.TrimRight(where, "/"), caps)
	} else {
		fmt.Fprintf(&b, "bao policy write %s - <<'EOF'\npath \"%s\"     { capabilities = %s }\npath \"%s\" { capabilities = %s }\n%sEOF\n", policy, dataPath, caps, metaPath, caps, list)
	}
	if conn.Namespace != "" {
		b.WriteString("# in the namespace " + conn.Namespace + " (bao -namespace=" + conn.Namespace + " …)\n")
	}
	switch conn.Login {
	case "jwt":
		fmt.Fprintf(&b, "bao auth enable -path=%s jwt   # once, if it isn't\n", conn.AuthMount)
		fmt.Fprintf(&b, "bao write auth/%s/config jwt_validation_pubkeys=@%s.pub   # with the store's other keys, if it has any\n", conn.AuthMount, conn.Role)
		fmt.Fprintf(&b, "bao write auth/%s/role/%s role_type=jwt \\\n    bound_subject=%s bound_audiences=hi \\\n    user_claim=sub token_policies=%s token_ttl=15m\n", conn.AuthMount, conn.Role, vaultConnSubject(), policy)
		fmt.Fprintf(&b, "\n%s.pub:\n%s", conn.Role, vaultConnPublicPEM(conn.Seed))
	case "approle":
		fmt.Fprintf(&b, "bao auth enable -path=%s approle   # once, if it isn't\n", conn.AuthMount)
		fmt.Fprintf(&b, "bao write auth/%s/role/%s token_policies=%s token_ttl=15m secret_id_ttl=90d\n", conn.AuthMount, conn.Role, policy)
		fmt.Fprintf(&b, "bao read auth/%s/role/%s/role-id\nbao write -f auth/%s/role/%s/secret-id   # both go to hi server vault connect, asked without echo\n", conn.AuthMount, conn.Role, conn.AuthMount, conn.Role)
	}
	return b.String()
}

func vaultDisconnectCommand(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return usageError{"usage: hi server vault disconnect <prefix>"}
	}
	prefix := strings.Trim(args[0], "/")
	var users []string
	for from, apps := range appSourcesUsed() {
		if strings.HasPrefix(from, prefix+"/") {
			users = append(users, apps...)
		}
	}
	if len(users) > 0 {
		sort.Strings(users)
		return fmt.Errorf("%s take settings from %s/: they drop it first (hi app env <app> --unset-from <path>)", strings.Join(slices.Compact(users), ", "), prefix)
	}
	conns, err := loadVaultConns()
	if err != nil {
		return err
	}
	n := len(conns.Conns)
	conns.Conns = slices.DeleteFunc(conns.Conns, func(c vaultConn) bool { return c.Prefix == prefix })
	if len(conns.Conns) == n {
		return fmt.Errorf("no store is connected as %s/", prefix)
	}
	if err := saveVaultConns(conns, vaultActor()); err != nil {
		return err
	}
	vaultAudit(vaultActor(), "vault.disconnect", prefix+"/", "")
	fmt.Fprintf(stdout, "%s/ isn't connected now; hi's login to it is forgotten. Its owners can remove the role.\n", prefix)
	return nil
}

func vaultLoginCommand(args []string, stdout io.Writer) error {
	fresh := false
	var prefixes []string
	for _, arg := range args {
		if arg == "--new-key" {
			fresh = true
			continue
		}
		prefixes = append(prefixes, strings.Trim(arg, "/"))
	}
	conns, err := loadVaultConns()
	if err != nil {
		return err
	}
	all := append([]vaultConn{}, conns.Conns...)
	if conns.Moved != nil {
		all = append(all, *conns.Moved)
	}
	if len(all) == 0 {
		fmt.Fprintln(stdout, "No team store is connected: hi server vault connect, or move.")
		return nil
	}
	changed := false
	for i := range all {
		conn := &all[i]
		if len(prefixes) > 0 && !slices.Contains(prefixes, conn.Prefix) {
			continue
		}
		if fresh {
			if conn.Login != "jwt" {
				return fmt.Errorf("%s/ logs in with an AppRole: a new secret ID comes from its owners", conn.Prefix)
			}
			conn.OldSeed = conn.Seed
			conn.Seed = make([]byte, ed25519.SeedSize)
			rand.Read(conn.Seed)
			changed = true
			vaultAudit(vaultActor(), "vault.login-new-key", conn.Prefix+"/", "")
			fmt.Fprintf(stdout, "%s/: a new key pair. The old one logs in until the store's owners swap in the new public key:\n\n%s\n", conn.Prefix, vaultHandoff(*conn, conn.Prefix == "hi"))
			continue
		}
		kv, err := vaultKVFor(*conn)
		if err == nil {
			_, err = kv.login()
		}
		state := "logs in"
		if err != nil {
			state = "can't log in: " + err.Error()
		} else if len(conn.OldSeed) > 0 {
			// The new key works: the old one goes.
			test := *conn
			test.OldSeed = nil
			if kvNew, err := vaultKVFresh(test); err == nil {
				if _, err := kvNew.login(); err == nil {
					conn.OldSeed, changed = nil, true
					state += " with the new key; the old one is forgotten"
				}
			}
		}
		fmt.Fprintf(stdout, "%s/ (%s in %s, %s as %s): %s.\n", conn.Prefix, vaultConnWhere(*conn), conn.Address, conn.Login, conn.Role, state)
		if !conn.SecretIDEnds.IsZero() && time.Until(conn.SecretIDEnds) < 14*24*time.Hour {
			fmt.Fprintf(stdout, "  Its secret ID ends %s: ask its owners for a new one.\n", conn.SecretIDEnds.Local().Format("Jan 2"))
		}
		if err != nil {
			fmt.Fprintf(stdout, "\nFor the store's owners:\n\n%s\n", vaultHandoff(*conn, conn.Prefix == "hi"))
		}
	}
	if changed {
		var kept vaultConns = conns
		kept.Conns = nil
		for _, conn := range all {
			if conns.Moved != nil && conn.Prefix == "hi" {
				moved := conn
				kept.Moved = &moved
				continue
			}
			kept.Conns = append(kept.Conns, conn)
		}
		return saveVaultConns(kept, vaultActor())
	}
	return nil
}

// vaultConnectedList is hi server vault ls's part for connected stores:
// each source an app uses, its version, names, and what's left out.
func vaultConnectedList(stdout io.Writer) {
	used := appSourcesUsed()
	if len(used) == 0 {
		return
	}
	states := loadVaultSources()
	fmt.Fprintln(stdout, "\nFrom team stores:")
	for _, from := range sortedKeys(used) {
		state := states[from]
		line := fmt.Sprintf("  %-32s", from)
		if state != nil && !state.Read.IsZero() {
			line += fmt.Sprintf(" v%-3d read %s  %s", state.Version, state.Read.Local().Format("2006-01-02 15:04"), strings.Join(state.Names, " "))
		} else {
			line += " not read yet"
		}
		fmt.Fprintf(stdout, "%s  (%s)\n", line, strings.Join(used[from], ", "))
		if state != nil {
			for _, name := range sortedKeys(state.Left) {
				fmt.Fprintf(stdout, "    %s left out: %s\n", name, state.Left[name])
			}
			if state.Error != "" {
				fmt.Fprintf(stdout, "    since %s: %s\n", state.Since.Local().Format("Jan 2 15:04"), state.Error)
			}
		}
	}
}

// vaultImportSources reads a moved app's team-store paths on this machine,
// and says which stores it needs connected here first.
func vaultImportSources(name string, stdout io.Writer) {
	config, err := loadTeam(name)
	if err != nil || len(config.EnvSources) == 0 {
		return
	}
	conns, _ := loadVaultConns()
	needed := map[string]bool{}
	for _, list := range config.EnvSources {
		for _, src := range list {
			source, err := parseVaultSource(src.From)
			if err != nil {
				continue
			}
			if _, ok := conns.find(source.Prefix); !ok {
				needed[source.Prefix] = true
				continue
			}
			if _, _, err := vaultReadSource(source); err != nil {
				fmt.Fprintf(stdout, "App %s takes %s, which this machine can't read yet: %v\n", name, src.From, err)
			}
		}
	}
	for _, prefix := range sortedKeys(needed) {
		fmt.Fprintf(stdout, "App %s takes settings from %s/, which isn't connected on this machine: hi server vault connect %s/ vault://<mount>/<path> --address https://…, as on the machine it came from. Until then its boxes can't get those settings.\n", name, prefix, prefix)
	}
}
