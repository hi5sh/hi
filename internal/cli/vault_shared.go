package cli

// Shared keys (hi_server_vault.md, Shared keys): a key several apps use,
// such as one OpenRouter key, at hi/shared/<name>, never copied into an
// app's path. Admins set it, give it to apps, and remove it; an app's
// owners use one given to their app by reference (hi app env <app> NAME
// --from shared/<name>), which app.json keeps, not the value. Which apps
// may use each is shared.json beside the store: no secret.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

var vaultSharedPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

func vaultSharedPath(name string) string { return "hi/shared/" + name }

// vaultSharedGrant is which apps may use a shared key.
type vaultSharedGrant struct {
	Apps    []string  `json:"apps"`
	By      string    `json:"by,omitempty"`
	Created time.Time `json:"created"`
}

func vaultSharedFile() string { return filepath.Join(vaultDir(), "shared.json") }

func loadVaultShared() map[string]*vaultSharedGrant {
	grants := map[string]*vaultSharedGrant{}
	if data, err := os.ReadFile(vaultSharedFile()); err == nil {
		json.Unmarshal(data, &grants)
	}
	return grants
}

func saveVaultShared(grants map[string]*vaultSharedGrant) error {
	data, err := json.MarshalIndent(grants, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(vaultSharedFile(), append(data, '\n'), 0o600)
}

// updateVaultShared changes the grants under the store's lock.
func updateVaultShared(change func(map[string]*vaultSharedGrant) error) error {
	unlock, err := vaultLock()
	if err != nil {
		return err
	}
	defer unlock()
	grants := loadVaultShared()
	if err := change(grants); err != nil {
		return err
	}
	return saveVaultShared(grants)
}

// appEnvSource is where a setting's value comes from, "shared/<name>".
func appSharedName(source string) (string, bool) {
	name, ok := strings.CutPrefix(source, "shared/")
	return name, ok && vaultSharedPattern.MatchString(name)
}

// appEnvFromScope is the key in app.json's EnvFrom for a slot: "" is the
// preview and production, as hi app env without a slot.
func appEnvFromScope(slot string) string { return slot }

// appSharedUsers is each shared key's apps that use it, by reference.
func appSharedUsers() map[string][]string {
	users := map[string][]string{}
	for _, app := range machineAppNames() {
		config, err := loadTeam(app)
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, from := range []string{config.OpenRouterFrom, config.ClaudeFrom} {
			if name, ok := appSharedName(from); ok && !seen[name] {
				seen[name] = true
				users[name] = append(users[name], app)
			}
		}
		for _, refs := range config.EnvFrom {
			for _, source := range refs {
				if name, ok := appSharedName(source); ok && !seen[name] {
					seen[name] = true
					users[name] = append(users[name], app)
				}
			}
		}
	}
	return users
}

// appSharedScope is the values one scope's references give: each from
// its shared key while the app may use it. missing says what it couldn't.
func appSharedScope(config appConfig, scope string) (values map[string]string, missing []string) {
	values = map[string]string{}
	refs := config.EnvFrom[scope]
	if len(refs) == 0 {
		return values, nil
	}
	grants := loadVaultShared()
	store, storeErr := openVault()
	for key, source := range refs {
		name, ok := appSharedName(source)
		if !ok {
			continue
		}
		grant := grants[name]
		if grant == nil || !slices.Contains(grant.Apps, config.Name) {
			missing = append(missing, key+" (shared/"+name+" isn't given to the app)")
			continue
		}
		if storeErr != nil {
			missing = append(missing, key+" ("+storeErr.Error()+")")
			continue
		}
		data, _, found, err := store.get(vaultSharedPath(name))
		if err != nil || !found {
			missing = append(missing, key+" (shared/"+name+" has no value)")
			continue
		}
		values[key] = data.Values["value"]
	}
	sort.Strings(missing)
	return values, missing
}

// appEnvFrom sets a setting of the app's to come from a shared key.
func appEnvFrom(config *appConfig, secrets *appSecrets, slot, key, source string) error {
	name, ok := appSharedName(source)
	if !ok {
		return fmt.Errorf("--from takes a shared key, shared/<name> (hi server vault shared); %q isn't one", source)
	}
	if !vaultExists() {
		return errors.New("shared keys are in the server's vault, and this machine has none")
	}
	grant := loadVaultShared()[name]
	if grant == nil {
		return fmt.Errorf("the server has no shared key %s (hi server vault shared)", name)
	}
	if !slices.Contains(grant.Apps, config.Name) {
		return fmt.Errorf("shared/%s isn't given to app %s; an admin gives it: hi server vault shared give %s %s", name, config.Name, name, config.Name)
	}
	// The name's own value in this slot goes: one source a name.
	appEnvUnset(secrets, slot, key)
	appFileUnset(secrets, slot, key)
	if config.EnvFrom == nil {
		config.EnvFrom = map[string]map[string]string{}
	}
	scope := appEnvFromScope(slot)
	if config.EnvFrom[scope] == nil {
		config.EnvFrom[scope] = map[string]string{}
	}
	config.EnvFrom[scope][key] = "shared/" + name
	return nil
}

// appEnvFromUnset drops a reference; it says whether there was one.
func appEnvFromUnset(config *appConfig, slot, key string) bool {
	scope := appEnvFromScope(slot)
	if _, ok := config.EnvFrom[scope][key]; !ok {
		return false
	}
	delete(config.EnvFrom[scope], key)
	if len(config.EnvFrom[scope]) == 0 {
		delete(config.EnvFrom, scope)
	}
	return true
}

// ---------------------------------------------------------------------------
// hi server vault shared

const vaultSharedUsage = `usage:
  hi server vault shared                       Each shared key, the apps given it, and the apps that use it
  hi server vault shared set <name>            Set its value, asked without echo
  hi server vault shared give <name> <app>... [--take] [--coming]
                                               Let these apps use it, or take it back; --coming
                                               for an app about to move here (hi app move)
  hi server vault shared rm <name>             Remove it; refused while an app uses it`

func vaultSharedCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "ls" {
		return vaultSharedList(stdout)
	}
	if !vaultExists() {
		return errors.New("this machine has no vault: hi server vault init")
	}
	switch args[0] {
	case "set":
		if len(args) != 2 {
			return usageError{vaultSharedUsage}
		}
		value, err := readSecretLine(stdin, stdout, "shared/"+args[1]+"'s value")
		if err != nil {
			return err
		}
		said, err := vaultSharedSet(args[1], value, vaultActor())
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, said)
		return nil
	case "give":
		take, coming := false, false
		var rest []string
		for _, arg := range args[1:] {
			switch arg {
			case "--take":
				take = true
				continue
			case "--coming":
				coming = true
				continue
			}
			rest = append(rest, arg)
		}
		if len(rest) < 2 {
			return usageError{vaultSharedUsage}
		}
		said, err := vaultSharedGrantApps(rest[0], rest[1:], take, coming, vaultActor())
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, said)
		return nil
	case "rm":
		if len(args) != 2 {
			return usageError{vaultSharedUsage}
		}
		said, err := vaultSharedRemove(args[1], vaultActor())
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, said)
		return nil
	}
	return usageError{vaultSharedUsage}
}

// vaultSharedRow is a shared key as hi lists it.
type vaultSharedRow struct {
	Name    string    `json:"name"`
	Version int       `json:"version"`
	Changed time.Time `json:"changed"`
	Given   []string  `json:"given"`
	Using   []string  `json:"using"`
	// Spent is what each app's model calls cost on it this month, by its
	// fingerprint in their call lines (model_calls.go); Outside the last
	// time it spent outside hi's apps.
	Spent   map[string]float64 `json:"spent,omitempty"`
	Outside *sharedOutside     `json:"outside,omitempty"`
}

func vaultSharedRows() ([]vaultSharedRow, error) {
	grants := loadVaultShared()
	users := appSharedUsers()
	var rows []vaultSharedRow
	store, err := openVault()
	if err != nil {
		return nil, err
	}
	for name, grant := range grants {
		row := vaultSharedRow{Name: name, Given: append([]string{}, grant.Apps...), Using: users[name]}
		if file, err := store.load(vaultSharedPath(name)); err == nil && len(file.Versions) > 0 {
			latest := file.Versions[len(file.Versions)-1]
			row.Version, row.Changed = latest.Version, latest.Time
		}
		if row.Using == nil {
			row.Using = []string{}
		}
		if data, _, found, err := store.get(vaultSharedPath(name)); err == nil && found {
			row.Spent = appSpentWithKey(callKeyFingerprint(data.Values["value"]))
		}
		if last, ok := loadSharedOutside()[name]; ok && time.Since(last.At) < 7*24*time.Hour {
			row.Outside = &last
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

func vaultSharedList(stdout io.Writer) error {
	if !vaultExists() {
		fmt.Fprintln(stdout, "No shared keys: this machine has no vault.")
		return nil
	}
	rows, err := vaultSharedRows()
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintln(stdout, "No shared keys yet. hi server vault shared set <name> makes one, such as openrouter.")
		return nil
	}
	for _, row := range rows {
		given, using := "no app", "none"
		if len(row.Given) > 0 {
			given = strings.Join(row.Given, ", ")
		}
		if len(row.Using) > 0 {
			using = strings.Join(row.Using, ", ")
		}
		fmt.Fprintf(stdout, "shared/%-20s v%-3d given to %s; used by %s\n", row.Name, row.Version, given, using)
	}
	return nil
}

func vaultSharedSet(name, value, who string) (string, error) {
	if !vaultSharedPattern.MatchString(name) {
		return "", fmt.Errorf("%q isn't a shared key's name: lowercase letters, digits, and dashes", name)
	}
	if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("a shared key's value is one line, not empty")
	}
	store, err := openVault()
	if err != nil {
		return "", err
	}
	if err := store.put(vaultSharedPath(name), vaultData{Values: map[string]string{"value": value}}, who); err != nil {
		return "", err
	}
	if err := updateVaultShared(func(grants map[string]*vaultSharedGrant) error {
		if grants[name] == nil {
			grants[name] = &vaultSharedGrant{Apps: []string{}, By: who, Created: time.Now().UTC()}
		}
		return nil
	}); err != nil {
		return "", err
	}
	vaultAudit(who, "vault.shared-set", vaultSharedPath(name), "")
	users := appSharedUsers()[name]
	if len(users) > 0 {
		return fmt.Sprintf("shared/%s has its new value; %s get it within a minute, as any change.", name, strings.Join(users, ", ")), nil
	}
	return fmt.Sprintf("shared/%s is set. hi server vault shared give %s <app> lets an app use it; its owners then: hi app env <app> NAME --from shared/%s", name, name, name), nil
}

func vaultSharedGive(name string, apps []string, take bool, who string) (string, error) {
	return vaultSharedGrantApps(name, apps, take, false, who)
}

// vaultSharedGrantApps is vaultSharedGive, which with coming also gives a
// key to an app that isn't on this machine yet: one about to move here,
// whose move would otherwise find the key not given (hi app move). An
// app being received is one too.
func vaultSharedGrantApps(name string, apps []string, take, coming bool, who string) (string, error) {
	if !vaultSharedPattern.MatchString(name) {
		return "", fmt.Errorf("%q isn't a shared key's name", name)
	}
	for _, app := range apps {
		if !appNamePattern.MatchString(app) {
			return "", fmt.Errorf("%q isn't an app's name", app)
		}
		if _, err := loadTeam(app); err != nil && !take && !coming && !fileExists(appMoveIncoming(app)) {
			return "", fmt.Errorf("no app %s on this machine (for one about to move here: --coming)", app)
		}
	}
	err := updateVaultShared(func(grants map[string]*vaultSharedGrant) error {
		grant := grants[name]
		if grant == nil {
			return fmt.Errorf("no shared key %s: hi server vault shared set %s", name, name)
		}
		for _, app := range apps {
			if take {
				grant.Apps = slices.DeleteFunc(grant.Apps, func(a string) bool { return a == app })
			} else if !slices.Contains(grant.Apps, app) {
				grant.Apps = append(grant.Apps, app)
			}
		}
		sort.Strings(grant.Apps)
		return nil
	})
	if err != nil {
		return "", err
	}
	if take {
		vaultAudit(who, "vault.shared-take", vaultSharedPath(name), strings.Join(apps, " "))
		var using []string
		for _, app := range apps {
			if slices.Contains(appSharedUsers()[name], app) {
				using = append(using, app)
			}
		}
		if len(using) > 0 {
			return fmt.Sprintf("%s can't use shared/%s now; their boxes no longer get it within a minute (%s).", strings.Join(apps, ", "), name, strings.Join(using, ", ")), nil
		}
		return fmt.Sprintf("%s can't use shared/%s now.", strings.Join(apps, ", "), name), nil
	}
	vaultAudit(who, "vault.shared-give", vaultSharedPath(name), strings.Join(apps, " "))
	return fmt.Sprintf("%s may use shared/%s: hi app env <app> NAME --from shared/%s", strings.Join(apps, ", "), name, name), nil
}

func vaultSharedRemove(name, who string) (string, error) {
	if users := appSharedUsers()[name]; len(users) > 0 {
		return "", fmt.Errorf("%s use shared/%s: they unset it first (hi app env <app> --unset NAME)", strings.Join(users, ", "), name)
	}
	store, err := openVault()
	if err != nil {
		return "", err
	}
	known := false
	if err := updateVaultShared(func(grants map[string]*vaultSharedGrant) error {
		_, known = grants[name]
		delete(grants, name)
		return nil
	}); err != nil {
		return "", err
	}
	n, err := store.purge(vaultSharedPath(name))
	if err != nil {
		return "", err
	}
	if !known && n == 0 {
		return "", fmt.Errorf("no shared key %s", name)
	}
	vaultAudit(who, "vault.shared-rm", vaultSharedPath(name), "")
	return fmt.Sprintf("shared/%s is removed, with its versions; the history of who changed it stays in the audit log.", name), nil
}

// vaultSharedRenameApp gives a renamed app's grants to its new name.
func vaultSharedRenameApp(old, name string) {
	if !vaultExists() {
		return
	}
	updateVaultShared(func(grants map[string]*vaultSharedGrant) error {
		for _, grant := range grants {
			for i, app := range grant.Apps {
				if app == old {
					grant.Apps[i] = name
				}
			}
			sort.Strings(grant.Apps)
		}
		return nil
	})
}

// vaultSharedPurgeApp takes a purged app's grants back.
func vaultSharedPurgeApp(name string) {
	if !vaultExists() {
		return
	}
	updateVaultShared(func(grants map[string]*vaultSharedGrant) error {
		for _, grant := range grants {
			grant.Apps = slices.DeleteFunc(grant.Apps, func(a string) bool { return a == name })
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// hi app move: the shared keys an app uses go with it

// appSharedExportFile holds them in the app's folder for the move only.
func appSharedExportFile(name string) string {
	return filepath.Join(appDir(name), "shared-keys.json")
}

// vaultExportShared writes the shared keys the app uses beside its
// settings, for the move's connection; done removes the file.
func vaultExportShared(name string) (func(), error) {
	path := appSharedExportFile(name)
	done := func() { os.Remove(path) }
	config, err := loadTeam(name)
	if err != nil || (len(config.EnvFrom) == 0 && config.OpenRouterFrom == "" && config.ClaudeFrom == "") || !vaultExists() {
		return done, nil
	}
	store, err := openVault()
	if err != nil {
		return done, err
	}
	values := map[string]string{}
	for _, from := range []string{config.OpenRouterFrom, config.ClaudeFrom} {
		if shared, ok := appSharedName(from); ok {
			if data, _, found, err := store.get(vaultSharedPath(shared)); err == nil && found {
				values[shared] = data.Values["value"]
			}
		}
	}
	for _, refs := range config.EnvFrom {
		for _, source := range refs {
			if shared, ok := appSharedName(source); ok {
				if data, _, found, err := store.get(vaultSharedPath(shared)); err == nil && found {
					values[shared] = data.Values["value"]
				}
			}
		}
	}
	if len(values) == 0 {
		return done, nil
	}
	data, _ := json.Marshal(values)
	return done, writeFileAtomic(path, data, 0o600)
}

// appSharedNamesOf are the shared keys an app uses, by name, each once.
func appSharedNamesOf(config appConfig) []string {
	var names []string
	add := func(from string) {
		if shared, ok := appSharedName(from); ok && !slices.Contains(names, shared) {
			names = append(names, shared)
		}
	}
	add(config.OpenRouterFrom)
	add(config.ClaudeFrom)
	for _, refs := range sortedKeys(config.EnvFrom) {
		for _, source := range config.EnvFrom[refs] {
			add(source)
		}
	}
	return names
}

// vaultImportShared takes a moved app's shared keys: one this machine
// doesn't have is copied and given to the app; one it has is given only
// if an admin had, so its admins decide. It says what it did.
func vaultImportShared(name string, stdout io.Writer) error {
	path := appSharedExportFile(name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	defer os.Remove(path)
	var values map[string]string
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	grants := loadVaultShared()
	for shared, value := range values {
		if grants[shared] != nil {
			if !slices.Contains(grants[shared].Apps, name) {
				fmt.Fprintf(stdout, "App %s uses shared/%s, which this machine has but hasn't given it: an admin does, hi server vault shared give %s %s.\n", name, shared, shared, name)
			}
			continue
		}
		if _, err := vaultSharedSet(shared, value, "hi app move"); err != nil {
			return err
		}
		if _, err := vaultSharedGive(shared, []string{name}, false, "hi app move"); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Copied shared/%s, which app %s uses, into this machine's vault, given to it alone. Admins: hi server vault shared.\n", shared, name)
	}
	return nil
}

// vaultSharedSetApps gives a shared key to exactly these apps: the ones
// not in the list are taken back, refused for one that uses it.
func vaultSharedSetApps(name string, apps []string, who string) (string, error) {
	grant := loadVaultShared()[name]
	if grant == nil {
		return "", fmt.Errorf("no shared key %s", name)
	}
	var give, take []string
	for _, app := range apps {
		if !slices.Contains(grant.Apps, app) {
			give = append(give, app)
		}
	}
	using := appSharedUsers()[name]
	for _, app := range grant.Apps {
		if !slices.Contains(apps, app) {
			if slices.Contains(using, app) {
				return "", fmt.Errorf("%s uses shared/%s: it stops first (hi app env %s --unset NAME)", app, name, app)
			}
			take = append(take, app)
		}
	}
	if len(give) > 0 {
		if _, err := vaultSharedGive(name, give, false, who); err != nil {
			return "", err
		}
	}
	if len(take) > 0 {
		if _, err := vaultSharedGive(name, take, true, who); err != nil {
			return "", err
		}
	}
	switch {
	case len(apps) == 0:
		return fmt.Sprintf("No app may use shared/%s now.", name), nil
	case len(give) == 0 && len(take) == 0:
		return fmt.Sprintf("shared/%s is given to %s, as it was.", name, strings.Join(apps, ", ")), nil
	}
	return fmt.Sprintf("shared/%s is given to %s. Each uses it by naming the setting: hi app env <app> NAME --from shared/%s", name, strings.Join(apps, ", "), name), nil
}

// appSharedKeyNames are the server's shared keys, by name.
func appSharedKeyNames() []string {
	if !vaultExists() {
		return nil
	}
	return sortedKeys(loadVaultShared())
}

// appCheckSharedOpenRouter checks a shared key as an OpenRouter key, with
// OpenRouter.
func appCheckSharedOpenRouter(name string) error {
	store, err := openVault()
	if err != nil {
		return err
	}
	data, _, found, err := store.get(vaultSharedPath(name))
	if err != nil || !found || data.Values["value"] == "" {
		return fmt.Errorf("shared/%s has no value", name)
	}
	if err := appCheckOpenRouterKey(data.Values["value"]); err != nil {
		return fmt.Errorf("shared/%s as an OpenRouter key: %w", name, err)
	}
	return nil
}

// appCheckSharedClaude checks a shared key looks like a Claude token.
func appCheckSharedClaude(name string) error {
	store, err := openVault()
	if err != nil {
		return err
	}
	data, _, found, err := store.get(vaultSharedPath(name))
	if err != nil || !found || data.Values["value"] == "" {
		return fmt.Errorf("shared/%s has no value", name)
	}
	if err := appCheckClaudeToken(data.Values["value"]); err != nil {
		return fmt.Errorf("shared/%s as a Claude token: %w", name, err)
	}
	return nil
}

// appSharedTo are the shared keys shared to an app, by their reference.
func appSharedTo(app string) []string {
	var names []string
	for _, name := range sortedKeys(loadVaultShared()) {
		if slices.Contains(loadVaultShared()[name].Apps, app) {
			names = append(names, "shared/"+name)
		}
	}
	return names
}

// appEnvRefNames are an app's settings that take a shared key's value, as
// the settings list says them.
func appEnvRefNames(config appConfig) []string {
	var names []string
	for _, scope := range sortedKeys(config.EnvFrom) {
		where := map[string]string{"": "", appSlotProduction: ", production only", appSlotPreview: ", the preview only", appSlotDev: ", the dev box only"}[scope]
		for _, key := range sortedKeys(config.EnvFrom[scope]) {
			names = append(names, fmt.Sprintf("%s (from %s%s)", key, config.EnvFrom[scope][key], where))
		}
	}
	return names
}
