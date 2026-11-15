package cli

// Each secret hi kept as a file maps to paths in the vault. Code that
// read or wrote the file reads and writes through readSecretFile and
// writeSecretFile, which give and take the same bytes the file held, so
// nothing that parses them changes. Without a vault on the machine they
// are the file, as before.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type secretKind int

const (
	secretJSON secretKind = iota // a JSON object: each key a name
	secretRaw                    // one value: the file's text
	secretApp                    // an app's secrets.json, split by slot
)

// secretFile is a secret's file and its place in the vault.
type secretFile struct {
	disk string
	path string
	kind secretKind
	// what names it, for messages
	what string
}

// The secrets hi keeps. A path empty means the file stays a file (a
// folder that isn't this machine's own, such as a test's).
func serverKeysSecret(dir string) secretFile {
	return secretFile{disk: filepath.Join(dir, "keys.json"), path: machineServerPath(dir, "hi/server/keys"), kind: secretJSON, what: "the server's keys"}
}

func serverSlackSecret(dir string) secretFile {
	return secretFile{disk: slackConfigPath(dir), path: machineServerPath(dir, "hi/server/slack"), kind: secretJSON, what: "the server's Slack tokens"}
}

func appSecretsSecret(name string) secretFile {
	path := ""
	if appNamePattern.MatchString(name) {
		path = "hi/app/" + name
	}
	return secretFile{disk: filepath.Join(appDir(name), "secrets.json"), path: path, kind: secretApp, what: "app " + name + "'s settings"}
}

func appSnapshotKeySecret(name string) secretFile {
	path := ""
	if appNamePattern.MatchString(name) {
		path = "hi/app/" + name + "/snapshots"
	}
	return secretFile{disk: appSnapshotKey(name), path: path, kind: secretRaw, what: "app " + name + "'s snapshot key"}
}

func mailSecret() secretFile {
	return secretFile{disk: mailSettingsPath(), path: "hi/machine/mail", kind: secretJSON, what: "the mail settings"}
}

func hiDomainSecret() secretFile {
	return secretFile{disk: hiDomainPath(), path: "hi/machine/dns", kind: secretJSON, what: "the domain's DNS settings"}
}

func netbirdTokenSecret() secretFile {
	return secretFile{disk: netbirdTokenPath(), path: "hi/machine/netbird", kind: secretRaw, what: "NetBird's API token"}
}

// machineServerPath is path for the machine's own server folder, and ""
// for any other, which keeps its files.
func machineServerPath(dir, path string) string {
	own, err := orgDirectory("")
	if err != nil {
		return ""
	}
	a, _ := filepath.Abs(dir)
	b, _ := filepath.Abs(own)
	if a != b {
		return ""
	}
	return path
}

func (f secretFile) inVault() bool { return f.path != "" && vaultExists() }

// appSecretSlots splits secrets.json: its settings by slot, the offsite
// bucket's keys, and the rest (tokens and sign-ins) in hi.
var appSecretSlots = []struct {
	suffix string
	values string
	files  string
}{
	{"shared", "env", "files"},
	{"production", "env_production", "files_production"},
	{"stage", "env_preview", "files_preview"},
	{"dev", "env_dev", "files_dev"},
}

var appSecretOffsite = []string{"backup_key_id", "backup_secret"}

// appSecretDisk stays in secrets.json: the heartbeat's URL hi before
// v2.6.154 kept there, until moveAppHeartbeat moves it to app.json.
var appSecretDisk = []string{"heartbeat_url"}

func (f secretFile) paths() []string {
	if f.kind != secretApp {
		return []string{f.path}
	}
	var paths []string
	for _, slot := range appSecretSlots {
		paths = append(paths, f.path+"/"+slot.suffix)
	}
	return append(paths, f.path+"/offsite", f.path+"/hi")
}

func notExist(path string) error {
	return &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
}

// readSecretFile is the secret's bytes, as its file held them; a secret
// that isn't there is an error os.IsNotExist knows.
func readSecretFile(f secretFile) ([]byte, error) {
	if !f.inVault() {
		return os.ReadFile(f.disk)
	}
	// While its file holds it, the file is the secret: an older hi still
	// running reads and writes it there (vaultKeepFiles).
	if disk, err := os.ReadFile(f.disk); err == nil && diskHoldsSecret(f, disk) {
		return disk, nil
	}
	store, err := openVault()
	if err != nil {
		return nil, err
	}
	return readVaultSecret(store, f)
}

// readVaultSecret is the secret as the vault has it, with what stays on
// disk.
func readVaultSecret(store *vaultStore, f secretFile) ([]byte, error) {
	found := false
	parts := map[string]vaultData{}
	for _, path := range f.paths() {
		data, _, ok, err := store.get(path)
		if err != nil {
			return nil, err
		}
		if ok {
			parts[path] = data
			found = true
		}
	}
	disk, diskErr := os.ReadFile(f.disk)
	if diskErr != nil && !errors.Is(diskErr, fs.ErrNotExist) {
		return nil, diskErr
	}
	if !found {
		if diskErr != nil {
			return nil, notExist(f.disk)
		}
		return disk, nil
	}
	return joinSecret(f, parts, disk)
}

// writeSecretFile keeps data, the file's bytes, in the vault (and the
// names that stay on disk in the file), or in the file without one.
func writeSecretFile(f secretFile, data []byte, mode os.FileMode) error {
	if !f.inVault() {
		if err := os.MkdirAll(filepath.Dir(f.disk), 0o700); err != nil {
			return err
		}
		return writeFileAtomic(f.disk, data, mode)
	}
	store, err := openVault()
	if err != nil {
		return err
	}
	parts, disk, err := splitSecret(f, data)
	if err != nil {
		return err
	}
	unlock, err := vaultLock()
	if err != nil {
		return err
	}
	defer unlock()
	for _, path := range f.paths() {
		if err := store.putLocked(path, parts[path], vaultActor()); err != nil {
			return err
		}
	}
	if vaultKeepFiles() {
		return writeFileAtomic(f.disk, data, mode)
	}
	if disk != nil {
		return writeFileAtomic(f.disk, disk, 0o600)
	}
	if err := os.Remove(f.disk); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// diskHoldsSecret is whether a secret's file holds it, not only what
// stays on disk (an app's heartbeat URL).
func diskHoldsSecret(f secretFile, data []byte) bool {
	if f.kind != secretApp {
		return true
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil {
		return true
	}
	for name := range object {
		if !containsString(appSecretDisk, name) {
			return true
		}
	}
	return false
}

// vaultKeepFiles is whether a hi process still runs a binary hi update
// replaced since: it reads the secrets' files, so they stay, kept as the
// vault is, until it restarts on this hi.
var vaultKeepFiles = func() bool {
	exe, err := os.Executable()
	if err != nil {
		return true
	}
	exe = strings.TrimSuffix(exe, " (deleted)")
	entries, err := os.ReadDir(updateProcRoot)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.Name() == fmt.Sprint(os.Getpid()) || entry.Name()[0] < '0' || entry.Name()[0] > '9' {
			continue
		}
		if target, err := os.Readlink(filepath.Join(updateProcRoot, entry.Name(), "exe")); err == nil && target == exe+" (deleted)" {
			if vaultReadsNoSecret(filepath.Join(updateProcRoot, entry.Name(), "cmdline")) {
				continue
			}
			if vaultBinaryKnowsVault(filepath.Join(updateProcRoot, entry.Name(), "exe")) {
				continue // it reads the vault itself
			}
			return true
		}
	}
	return false
}

// vaultBinaryMarker is in every hi binary that reads the vault itself
// (v2.6.153 and newer): the recovery key's path, a string long enough
// that Go keeps it as data, not in instructions as it does short ones.
var vaultBinaryMarker = []byte(vaultRecoveryPath)

var vaultBinaryKnown = struct {
	sync.Mutex
	seen map[string]bool
}{seen: map[string]bool{}}

// vaultBinaryKnowsVault is whether the binary a process runs, replaced
// since, has the vault's code: such a hi reads the vault when a secret's
// file is gone, so no file stays for it. One older reads only the files.
func vaultBinaryKnowsVault(exe string) bool {
	info, err := os.Stat(exe)
	if err != nil {
		return false
	}
	key := fmt.Sprintf("%s %d %d", exe, info.Size(), info.ModTime().UnixNano())
	vaultBinaryKnown.Lock()
	known, ok := vaultBinaryKnown.seen[key]
	vaultBinaryKnown.Unlock()
	if ok {
		return known
	}
	file, err := os.Open(exe)
	if err != nil {
		return false
	}
	defer file.Close()
	buffer := make([]byte, 1<<20)
	carry := []byte{}
	for {
		n, err := file.Read(buffer)
		chunk := append(carry, buffer[:n]...)
		if bytes.Contains(chunk, vaultBinaryMarker) {
			known = true
			break
		}
		if keep := len(vaultBinaryMarker) - 1; len(chunk) > keep {
			carry = append([]byte{}, chunk[len(chunk)-keep:]...)
		}
		if err != nil {
			break
		}
	}
	vaultBinaryKnown.Lock()
	vaultBinaryKnown.seen[key] = known
	vaultBinaryKnown.Unlock()
	return known
}

// vaultReadsNoSecret is whether a process, by its command line, is one of
// hi's that never reads a secret file: an app's resolver, which an update
// restarts only with its app, a while after the rest; and hi update
// itself, which runs the binary it replaced while it restarts the rest.
func vaultReadsNoSecret(cmdline string) bool {
	data, err := os.ReadFile(cmdline)
	if err != nil {
		return false
	}
	args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	if len(args) >= 2 && args[1] == "update" {
		return true
	}
	return len(args) >= 3 && (args[1] == "app" || args[1] == "team") && args[2] == "__resolver"
}

// removeSecretFile removes the secret: an empty version in the vault,
// so its history stays.
func removeSecretFile(f secretFile) error {
	if f.inVault() {
		store, err := openVault()
		if err != nil {
			return err
		}
		for _, path := range f.paths() {
			if err := store.put(path, vaultData{}, vaultActor()); err != nil {
				return err
			}
		}
	}
	if err := os.Remove(f.disk); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func secretFileExists(f secretFile) bool {
	_, err := readSecretFile(f)
	return err == nil
}

// vaultActor is who a change is by: the command's or service's user.
func vaultActor() string {
	if name := firstNonEmpty(os.Getenv("HI_ACTOR"), os.Getenv("USER")); name != "" {
		return name
	}
	return "hi"
}

func splitSecret(f secretFile, data []byte) (map[string]vaultData, []byte, error) {
	parts := map[string]vaultData{}
	switch f.kind {
	case secretRaw:
		parts[f.path] = vaultData{Values: map[string]string{"value": string(data)}}
		return parts, nil, nil
	case secretJSON:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", f.what, err)
		}
		parts[f.path] = jsonToVault(object)
		return parts, nil, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", f.what, err)
	}
	for _, slot := range appSecretSlots {
		var values map[string]string
		var files map[string]string
		if raw, ok := object[slot.values]; ok {
			if err := json.Unmarshal(raw, &values); err != nil {
				return nil, nil, fmt.Errorf("%s: %s: %w", f.what, slot.values, err)
			}
		}
		if raw, ok := object[slot.files]; ok {
			if err := json.Unmarshal(raw, &files); err != nil {
				return nil, nil, fmt.Errorf("%s: %s: %w", f.what, slot.files, err)
			}
		}
		delete(object, slot.values)
		delete(object, slot.files)
		part := vaultData{}
		if len(values) > 0 {
			part.Values = values
		}
		for name, encoded := range files {
			content, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: the file %s isn't base64", f.what, name)
			}
			if part.Files == nil {
				part.Files = map[string][]byte{}
			}
			part.Files[name] = content
		}
		parts[f.path+"/"+slot.suffix] = part
	}
	offsite := map[string]json.RawMessage{}
	for _, name := range appSecretOffsite {
		if raw, ok := object[name]; ok {
			offsite[name] = raw
			delete(object, name)
		}
	}
	parts[f.path+"/offsite"] = jsonToVault(offsite)
	disk := map[string]json.RawMessage{}
	for _, name := range appSecretDisk {
		if raw, ok := object[name]; ok {
			disk[name] = raw
			delete(object, name)
		}
	}
	parts[f.path+"/hi"] = jsonToVault(object)
	var diskData []byte
	if len(disk) > 0 {
		encoded, err := json.MarshalIndent(disk, "", "  ")
		if err != nil {
			return nil, nil, err
		}
		diskData = append(encoded, '\n')
	}
	return parts, diskData, nil
}

func joinSecret(f secretFile, parts map[string]vaultData, disk []byte) ([]byte, error) {
	switch f.kind {
	case secretRaw:
		return []byte(parts[f.path].Values["value"]), nil
	case secretJSON:
		return marshalSecretObject(vaultToJSON(parts[f.path]))
	}
	object := vaultToJSON(parts[f.path+"/hi"])
	for name, raw := range vaultToJSON(parts[f.path+"/offsite"]) {
		object[name] = raw
	}
	for _, slot := range appSecretSlots {
		part := parts[f.path+"/"+slot.suffix]
		if len(part.Values) > 0 {
			raw, _ := json.Marshal(part.Values)
			object[slot.values] = raw
		}
		if len(part.Files) > 0 {
			files := map[string]string{}
			for name, content := range part.Files {
				files[name] = base64.StdEncoding.EncodeToString(content)
			}
			raw, _ := json.Marshal(files)
			object[slot.files] = raw
		}
	}
	if len(disk) > 0 {
		var kept map[string]json.RawMessage
		if json.Unmarshal(disk, &kept) == nil {
			for _, name := range appSecretDisk {
				if raw, ok := kept[name]; ok {
					object[name] = raw
				}
			}
		}
	}
	return marshalSecretObject(object)
}

func marshalSecretObject(object map[string]json.RawMessage) ([]byte, error) {
	data, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// jsonToVault keeps a JSON object's strings as values, and anything else
// as its JSON text, marked.
func jsonToVault(object map[string]json.RawMessage) vaultData {
	data := vaultData{}
	for name, raw := range object {
		if data.Values == nil {
			data.Values = map[string]string{}
		}
		var text string
		if bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)) && json.Unmarshal(raw, &text) == nil {
			data.Values[name] = text
			continue
		}
		var compact bytes.Buffer
		if json.Compact(&compact, raw) == nil {
			raw = compact.Bytes()
		}
		data.Values[name] = string(raw)
		data.JSON = append(data.JSON, name)
	}
	sort.Strings(data.JSON)
	return data
}

func vaultToJSON(data vaultData) map[string]json.RawMessage {
	object := map[string]json.RawMessage{}
	for name, value := range data.Values {
		if containsString(data.JSON, name) {
			object[name] = json.RawMessage(value)
			continue
		}
		raw, _ := json.Marshal(value)
		object[name] = raw
	}
	return object
}

// sameSecret is whether two readings of a secret hold the same thing,
// whatever the JSON's spacing or order.
func sameSecret(f secretFile, a, b []byte) bool {
	if f.kind == secretRaw {
		return bytes.Equal(a, b)
	}
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return bytes.Equal(ja, jb)
}

// moveSecretFile moves a secret's file into the vault: written, read
// back, compared, and only then the file removed. A file that fails
// stays as it is.
func moveSecretFile(f secretFile) (bool, error) {
	if !f.inVault() {
		return false, nil
	}
	data, err := os.ReadFile(f.disk)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !diskHoldsSecret(f, data) {
		return false, nil // only an older hi's heartbeat URL left: moved already
	}
	store, err := openVault()
	if err != nil {
		return false, err
	}
	parts, disk, err := splitSecret(f, data)
	if err != nil {
		return false, err
	}
	unlock, err := vaultLock()
	if err != nil {
		return false, err
	}
	for _, path := range f.paths() {
		if err := store.putLocked(path, parts[path], "hi"); err != nil {
			unlock()
			return false, err
		}
	}
	unlock()
	back, err := readVaultSecret(store, f)
	if err != nil {
		return false, err
	}
	if !sameSecret(f, data, back) {
		return false, fmt.Errorf("%s read back differently from the vault; its file stays", f.what)
	}
	if vaultKeepFiles() {
		return false, nil // an older hi still reads it there
	}
	if disk != nil {
		return true, writeFileAtomic(f.disk, disk, 0o600)
	}
	if err := os.Remove(f.disk); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return true, err
	}
	return true, nil
}

// machineSecretFiles are every secret file this machine has, for the move.
func machineSecretFiles() []secretFile {
	var files []secretFile
	if dir, err := orgDirectory(""); err == nil {
		files = append(files, serverKeysSecret(dir), serverSlackSecret(dir))
	}
	files = append(files, mailSecret(), netbirdTokenSecret(), hiDomainSecret())
	for _, name := range machineAppNames() {
		files = append(files, appSecretsSecret(name), appSnapshotKeySecret(name))
	}
	return files
}

// machineAppNames are the apps this machine has, by their links (or
// folders) that hold an app.json.
func machineAppNames() []string {
	entries, err := os.ReadDir(appsDir())
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if appNamePattern.MatchString(entry.Name()) && (entry.Type()&fs.ModeSymlink != 0 || entry.IsDir()) && fileExists(filepath.Join(appDir(entry.Name()), appConfigFile)) {
			names = append(names, entry.Name())
		}
	}
	return names
}

// vaultFinishMove moves what is still a file into the vault every five
// minutes, until nothing is: files stay while a service runs an older hi.
func vaultFinishMove(log io.Writer) {
	for vaultExists() {
		left := false
		for _, f := range machineSecretFiles() {
			if data, err := os.ReadFile(f.disk); err == nil && f.path != "" && diskHoldsSecret(f, data) {
				left = true
				break
			}
		}
		for _, name := range machineAppNames() {
			left = left || appLegacyHeartbeat(name) != ""
		}
		if !left {
			return
		}
		time.Sleep(5 * time.Minute)
		var said strings.Builder
		if err := ensureVault(&said); err != nil {
			fmt.Fprintf(log, "hi: %v\n", err)
		}
		for _, line := range strings.Split(said.String(), "\n") {
			if strings.HasPrefix(line, "Moved into") {
				fmt.Fprintln(log, line)
			}
		}
	}
}

// ensureVault creates this machine's store where it has secrets hi keeps
// and none yet, and moves every secret file into it. It says what it did,
// by name, and never a value.
func ensureVault(stdout io.Writer) error {
	if vaultDir() == "" {
		return nil
	}
	if !vaultExists() {
		have := false
		for _, f := range machineSecretFiles() {
			if f.path != "" && fileExists(f.disk) {
				have = true
				break
			}
		}
		if !have && !machineRunsHiServices() {
			return nil
		}
		result, err := initVault("")
		if err != nil {
			return fmt.Errorf("creating the server's vault: %w", err)
		}
		switch {
		case result.Slot == vaultSlotTPM:
			fmt.Fprintln(stdout, "The server's vault is made: every secret hi keeps is encrypted on disk, and it opens by itself with this machine's TPM.")
		case result.Why != "":
			fmt.Fprintf(stdout, "The server's vault is made: every secret hi keeps is encrypted on disk. It opens by itself with a key file, so a copied disk can open it (%s).\n", result.Why)
		default:
			fmt.Fprintln(stdout, "The server's vault is made, with a key file: every secret hi keeps is encrypted on disk.")
		}
		fmt.Fprintln(stdout, "Its recovery key waits for the owner: hi server vault recovery, on a terminal on this machine.")
	}
	var moved, failed []string
	for _, f := range machineSecretFiles() {
		ok, err := moveSecretFile(f)
		if err != nil {
			failed = append(failed, f.what+": "+err.Error())
			continue
		}
		if ok {
			moved = append(moved, f.what)
		}
	}
	for _, name := range machineAppNames() {
		if err := moveAppHeartbeat(name); err != nil {
			failed = append(failed, "app "+name+"'s heartbeat: "+err.Error())
		}
	}
	if len(moved) > 0 {
		fmt.Fprintf(stdout, "Moved into the vault: %s.\n", strings.Join(moved, "; "))
	}
	if vaultKeepFiles() {
		fmt.Fprintln(stdout, "The secrets' files stay, kept as the vault is, while a hi service still runs the old hi; they go once it restarts.")
	}
	if len(failed) > 0 {
		return errors.New("these stay files: " + strings.Join(failed, "; "))
	}
	return nil
}
