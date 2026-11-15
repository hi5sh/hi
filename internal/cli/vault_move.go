package cli

// hi's own paths in a team's OpenBao or Vault (hi_server_vault.md,
// release 3: Instead): hi server vault move vault://kv copies every path
// to kv/hi/..., reads each back, and switches. The built-in store keeps
// hi's login to the team's store, the recovery key, and what hi read from
// connected stores; and the copy of every path, as a fallback while the
// team's store is away, until hi server vault move --forget.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// vaultLocalOnly are the paths that stay in the built-in store.
func vaultLocalOnly(path string) bool {
	return path == vaultConnPath || strings.HasPrefix(path, "hi/vault/") || strings.HasPrefix(path, "hi/connected/")
}

// The names that tell a file and a JSON value apart in a team's store,
// whose values are strings: not variable names, so no app takes them.
const (
	vaultKVFilesName = "__hi_files"
	vaultKVJSONName  = "__hi_json"
)

func vaultDataToKV(data vaultData) map[string]string {
	out := map[string]string{}
	for name, value := range data.Values {
		out[name] = value
	}
	var files []string
	for name, content := range data.Files {
		out[name] = base64.StdEncoding.EncodeToString(content)
		files = append(files, name)
	}
	if len(files) > 0 {
		sort.Strings(files)
		out[vaultKVFilesName] = strings.Join(files, ",")
	}
	if len(data.JSON) > 0 {
		out[vaultKVJSONName] = strings.Join(data.JSON, ",")
	}
	return out
}

func vaultDataFromKV(values map[string]any) vaultData {
	data := vaultData{}
	files := map[string]bool{}
	if list, ok := values[vaultKVFilesName].(string); ok {
		for _, name := range strings.Split(list, ",") {
			files[name] = true
		}
	}
	if list, ok := values[vaultKVJSONName].(string); ok && list != "" {
		data.JSON = strings.Split(list, ",")
	}
	for name, value := range values {
		text, ok := value.(string)
		if !ok || name == vaultKVFilesName || name == vaultKVJSONName {
			continue
		}
		if files[name] {
			if content, err := base64.StdEncoding.DecodeString(text); err == nil {
				if data.Files == nil {
					data.Files = map[string][]byte{}
				}
				data.Files[name] = content
			}
			continue
		}
		if data.Values == nil {
			data.Values = map[string]string{}
		}
		data.Values[name] = text
	}
	return data
}

// vaultMovedState is where hi's paths are, cached a little in each
// process.
var vaultMovedState = struct {
	sync.Mutex
	conn *vaultConn
	at   time.Time
}{}

// movedKV is the team store's client for path, when hi's paths moved
// there and path isn't one that stays here; else nil.
func (v *vaultStore) movedKV(path string) (*vaultKV, *vaultConn) {
	if vaultLocalOnly(path) {
		return nil, nil
	}
	vaultMovedState.Lock()
	if time.Since(vaultMovedState.at) > 30*time.Second {
		vaultMovedState.conn = nil
		if data, _, ok, err := v.getLocal(vaultConnPath); err == nil && ok {
			var conns vaultConns
			if json.Unmarshal([]byte(data.Values["config"]), &conns) == nil && conns.Moved != nil {
				vaultMovedState.conn = conns.Moved
			}
		}
		vaultMovedState.at = time.Now()
	}
	conn := vaultMovedState.conn
	vaultMovedState.Unlock()
	if conn == nil {
		return nil, nil
	}
	kv, err := vaultKVFor(*conn)
	if err != nil {
		return nil, nil
	}
	return kv, conn
}

// vaultMovedForget drops the cache, after a move.
func vaultMovedForget() {
	vaultMovedState.Lock()
	vaultMovedState.at = time.Time{}
	vaultMovedState.Unlock()
}

// vaultAway is whether err is the team's store being away (down, sealed,
// unreachable), when the built-in copy stands in.
func vaultAway(err error) bool {
	if err == nil {
		return false
	}
	var kv *vaultKVError
	if errors.As(err, &kv) {
		return kv.Status >= 500
	}
	return true
}

func (v *vaultStore) get(path string) (vaultData, int, bool, error) {
	kv, conn := v.movedKV(path)
	if kv == nil {
		return v.getLocal(path)
	}
	secret, err := kv.read(path, 0)
	if vaultKVMissing(err) {
		return vaultData{}, 0, false, nil
	}
	if err != nil {
		if vaultAway(err) && !conn.Forgot {
			return v.getLocal(path)
		}
		return vaultData{}, 0, false, err
	}
	if secret.Data == nil {
		return vaultData{}, 0, false, nil
	}
	if file, _ := v.loadLocal(path); file.Removed != nil {
		return vaultData{}, 0, false, nil
	}
	data := vaultDataFromKV(secret.Data)
	return data, secret.Version, !data.empty(), nil
}

func (v *vaultStore) addVersion(path string, data vaultData, by string, rollback int) error {
	kv, conn := v.movedKV(path)
	if kv == nil {
		return v.addVersionLocal(path, data, by, rollback)
	}
	if !validVaultPath(path) {
		return fmt.Errorf("%q isn't a vault path", path)
	}
	before, _, _, err := v.get(path)
	if err != nil {
		return err
	}
	if len(changedNames(before, data)) == 0 && rollback == 0 {
		if file, _ := v.loadLocal(path); file.Removed == nil {
			return nil
		}
	}
	if _, err := kv.write(path, vaultDataToKV(data)); err != nil {
		return err
	}
	// The copy here: the value until --forget, then who changed which
	// names alone.
	if conn.Forgot {
		return v.addEntryLocal(path, data, by, rollback, before)
	}
	return v.addVersionLocal(path, data, by, rollback)
}

// addEntryLocal records a version in the built-in store without its
// value: the history after --forget.
func (v *vaultStore) addEntryLocal(path string, data vaultData, by string, rollback int, before vaultData) error {
	file, err := v.loadLocal(path)
	if err != nil {
		return err
	}
	number := 1
	if n := len(file.Versions); n > 0 {
		number = file.Versions[n-1].Version + 1
	}
	file.Removed = nil
	file.Versions = append(file.Versions, vaultVersion{Version: number, Time: computeNow().UTC(), By: by, Changed: changedNames(before, data), Rollback: rollback})
	return v.save(file)
}

// load is a path's history: hi's own entries, here, with the team
// store's versions of it.
func (v *vaultStore) load(path string) (vaultPathFile, error) {
	kv, _ := v.movedKV(path)
	local, err := v.loadLocal(path)
	if kv == nil || err != nil {
		return local, err
	}
	current, versions, err := kv.current(path)
	if err != nil {
		if vaultAway(err) {
			return local, nil
		}
		if vaultKVMissing(err) {
			return vaultPathFile{Path: path, Removed: local.Removed}, nil
		}
		return local, err
	}
	file := vaultPathFile{Path: path, Removed: local.Removed}
	for n := 1; n <= current; n++ {
		entry := vaultVersion{Version: n, By: "the team's store"}
		if remote, ok := versions[n]; ok {
			entry.Time = remote.Created
			if remote.Destroyed {
				t := remote.Created
				entry.Forgotten = &t
			}
		}
		// hi's own record of who and what, where it has one: matched by
		// order from the end, since both count the same changes.
		if i := len(local.Versions) - (current - n) - 1; i >= 0 && i < len(local.Versions) {
			entry.By, entry.Changed, entry.Rollback = local.Versions[i].By, local.Versions[i].Changed, local.Versions[i].Rollback
		}
		if versions[n].Deleted && entry.Forgotten == nil {
			continue
		}
		file.Versions = append(file.Versions, entry)
	}
	return file, nil
}

func (v *vaultStore) decrypt(path string, version vaultVersion) (vaultData, error) {
	kv, _ := v.movedKV(path)
	if kv == nil {
		return v.decryptLocal(path, version)
	}
	if version.Forgotten != nil {
		return vaultData{}, fmt.Errorf("version %d of %s was forgotten", version.Version, path)
	}
	secret, err := kv.read(path, version.Version)
	if err != nil {
		return vaultData{}, err
	}
	return vaultDataFromKV(secret.Data), nil
}

func (v *vaultStore) rollback(path string, version int, by string) error {
	kv, _ := v.movedKV(path)
	if kv == nil {
		return v.rollbackLocal(path, version, by)
	}
	secret, err := kv.read(path, version)
	if err != nil || secret.Data == nil {
		return fmt.Errorf("%s has no version %d to put back (hi server vault history %s)", path, version, path)
	}
	unlock, err := vaultLock()
	if err != nil {
		return err
	}
	defer unlock()
	return v.addVersion(path, vaultDataFromKV(secret.Data), by, version)
}

func (v *vaultStore) forget(path string, version int) error {
	kv, _ := v.movedKV(path)
	if kv == nil {
		return v.forgetLocal(path, version)
	}
	current, _, err := kv.current(path)
	if err != nil {
		return err
	}
	if version == current {
		return fmt.Errorf("version %d is %s's current one; set a new value first, then forget this one", version, path)
	}
	return kv.destroy(path, version)
}

// paths is every path under prefix: hi's in the team's store, with the
// ones that stay here.
func (v *vaultStore) paths(prefix string) ([]vaultPathFile, error) {
	kv, _ := v.movedKV("hi/app")
	local, err := v.pathsLocal(prefix)
	if kv == nil || err != nil {
		return local, err
	}
	listed, err := kv.list(strings.TrimSuffix(firstNonEmpty(prefix, "hi"), "/"))
	if err != nil {
		if vaultAway(err) {
			return local, nil
		}
		return nil, err
	}
	var out []vaultPathFile
	for _, file := range local {
		if vaultLocalOnly(file.Path) {
			out = append(out, file)
		}
	}
	for _, path := range listed {
		if !validVaultPath(path) || vaultLocalOnly(path) {
			continue
		}
		if prefix != "" && path != prefix && !strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")+"/") {
			continue
		}
		file, err := v.load(path)
		if err != nil {
			return nil, err
		}
		out = append(out, file)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (v *vaultStore) purge(prefix string) (int, error) {
	kv, _ := v.movedKV(prefix + "/x")
	if kv == nil {
		return v.purgeLocal(prefix)
	}
	files, err := v.paths(prefix)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, file := range files {
		if vaultLocalOnly(file.Path) {
			continue
		}
		if err := kv.remove(file.Path); err != nil {
			return n, err
		}
		n++
	}
	v.purgeLocal(prefix)
	return n, nil
}

// movePrefix moves paths in the team's store: each latest version copied
// to its new path, then the old removed; hi's history goes along here.
func (v *vaultStore) movePrefix(old, new string) error {
	kv, _ := v.movedKV(old + "/x")
	if kv == nil {
		return v.movePrefixLocal(old, new)
	}
	files, err := v.paths(old)
	if err != nil {
		return err
	}
	for _, file := range files {
		data, _, ok, err := v.get(file.Path)
		if err != nil {
			return err
		}
		if ok {
			if _, err := kv.write(new+strings.TrimPrefix(file.Path, old), vaultDataToKV(data)); err != nil {
				return err
			}
		}
	}
	for _, file := range files {
		if err := kv.remove(file.Path); err != nil {
			return err
		}
	}
	return v.movePrefixLocal(old, new)
}

// ---------------------------------------------------------------------------
// hi server vault move

func vaultMoveCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	usage := usageError{"usage: hi server vault move vault://<mount> --address https://<host>:8200 [--login jwt|approle] [--role <name>] [--auth-mount <path>] [--namespace <ns>] [--ca <file>]\n       hi server vault move --forget [--yes]"}
	forget, yes := false, false
	var rest []string
	for _, arg := range args {
		switch arg {
		case "--forget":
			forget = true
		case "--yes":
			yes = true
		default:
			rest = append(rest, arg)
		}
	}
	if !vaultExists() {
		return errors.New("this machine has no vault")
	}
	store, err := openVault()
	if err != nil {
		return err
	}
	conns, err := loadVaultConns()
	if err != nil {
		return err
	}
	if forget {
		if conns.Moved == nil {
			return errors.New("hi's paths are here, in the built-in store: nothing to forget")
		}
		if err := confirm(stdin, stdout, yes, "Forget the built-in store's copy of hi's paths? While "+conns.Moved.Address+" is away, nothing stands in for it."); err != nil {
			return err
		}
		return vaultForgetMoved(store, conns, stdout)
	}
	if conns.Moved != nil {
		return fmt.Errorf("hi's paths are in %s already", conns.Moved.Address)
	}
	conn := vaultConn{Prefix: "hi", Login: "jwt", Created: time.Now().UTC()}
	caFile := ""
	var positional []string
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		value := func() string {
			if _, after, ok := strings.Cut(arg, "="); ok && strings.HasPrefix(arg, "--") {
				return after
			}
			if i+1 < len(rest) {
				i++
				return rest[i]
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
		default:
			if strings.HasPrefix(arg, "-") {
				return usage
			}
			positional = append(positional, arg)
		}
	}
	// A move set up before, waiting for the store's owners, keeps its key.
	if conns.Pending != nil {
		conn = *conns.Pending
	} else {
		if len(positional) != 1 {
			return usage
		}
		mount, path, err := parseVaultURL(positional[0])
		if err != nil {
			return err
		}
		if path != "" {
			return errors.New("hi's paths go to the mount's hi/: vault://<mount>, such as vault://kv")
		}
		conn.Mount = mount
		if conn.Address == "" {
			conn.Address = firstNonEmpty(os.Getenv("BAO_ADDR"), os.Getenv("VAULT_ADDR"))
		}
		if err := vaultCheckAddress(conn.Address); err != nil {
			return err
		}
		if conn.CA, err = vaultReadCA(caFile); err != nil {
			return err
		}
		if err := vaultSetupLogin(&conn, "", stdin, stdout); err != nil {
			return err
		}
	}
	kv, err := vaultKVFresh(conn)
	if err == nil {
		_, err = kv.login()
	}
	if err != nil {
		conns.Pending = &conn
		if err := saveVaultConns(conns, vaultActor()); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "hi can't log in to %s yet (%v). For the store's owners, once:\n\n%s\nThen run hi server vault move again: it keeps this key.\n", conn.Address, err, vaultHandoff(conn, true))
		return nil
	}
	conn.KV = kv.kind()
	if conn.KV != 2 {
		return errors.New("hi's paths need a KV v2 mount, which keeps versions")
	}
	files, err := store.pathsLocal("")
	if err != nil {
		return err
	}
	moved := 0
	for _, file := range files {
		if vaultLocalOnly(file.Path) || len(file.Versions) == 0 {
			continue
		}
		data, _, ok, err := store.getLocal(file.Path)
		if err != nil {
			return fmt.Errorf("%s: %w; nothing switched", file.Path, err)
		}
		if !ok {
			continue
		}
		if _, err := kv.write(file.Path, vaultDataToKV(data)); err != nil {
			return fmt.Errorf("copying %s: %w; nothing switched, and what was copied stays there", file.Path, err)
		}
		back, err := kv.read(file.Path, 0)
		if err != nil || len(changedNames(data, vaultDataFromKV(back.Data))) > 0 {
			return fmt.Errorf("%s read back differently from %s; nothing switched", file.Path, conn.Address)
		}
		moved++
	}
	conns.Moved, conns.Pending = &conn, nil
	if err := saveVaultConns(conns, vaultActor()); err != nil {
		return err
	}
	vaultMovedForget()
	vaultAudit(vaultActor(), "vault.move", "hi/", fmt.Sprintf("%d paths to %s %s/hi", moved, conn.Address, conn.Mount))
	fmt.Fprintf(stdout, "hi's %d paths are in %s now, at %s/hi/, each read back. hi reads and writes there; the built-in store keeps a copy that stands in while it's away, until hi server vault move --forget.\n", moved, conn.Address, conn.Mount)
	fmt.Fprintln(stdout, "The built-in store keeps hi's login to it, the recovery key, and what hi reads from connected stores.")
	return nil
}

// vaultForgetMoved destroys the built-in copy of hi's moved paths' values,
// keeping who changed what.
func vaultForgetMoved(store *vaultStore, conns vaultConns, stdout io.Writer) error {
	unlock, err := vaultLock()
	if err != nil {
		return err
	}
	files, err := store.pathsLocal("")
	if err != nil {
		unlock()
		return err
	}
	n := 0
	now := computeNow().UTC()
	for _, file := range files {
		if vaultLocalOnly(file.Path) {
			continue
		}
		for i := range file.Versions {
			if file.Versions[i].Sealed != nil {
				file.Versions[i].Sealed = nil
				file.Versions[i].Forgotten = &now
			}
		}
		if err := store.save(file); err != nil {
			unlock()
			return err
		}
		n++
	}
	unlock()
	conns.Moved.Forgot = true
	if err := saveVaultConns(conns, vaultActor()); err != nil {
		return err
	}
	vaultMovedForget()
	vaultAudit(vaultActor(), "vault.move-forget", "hi/", fmt.Sprintf("%d paths' copies", n))
	fmt.Fprintf(stdout, "The built-in store no longer keeps %d paths' values: they are in %s alone. Backups of hi's state taken before still hold them.\n", n, conns.Moved.Address)
	return nil
}

// vaultMovedLine is hi server vault's line about where hi's paths are.
func vaultMovedLine() string {
	conns, err := loadVaultConns()
	if err != nil {
		return ""
	}
	if conns.Pending != nil {
		return "A move to " + conns.Pending.Address + " waits for its owners to set up hi's login: hi server vault move, again, once they have."
	}
	if conns.Moved == nil {
		return ""
	}
	line := "hi's paths are in " + conns.Moved.Address + ", at " + conns.Moved.Mount + "/hi/"
	if conns.Moved.Forgot {
		return line + "; the built-in store keeps who changed what, not the values."
	}
	return line + "; the built-in store keeps a copy until hi server vault move --forget."
}
