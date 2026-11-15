package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vaultTest gives the test a vault of its own, empty until made.
func vaultTest(t *testing.T) {
	t.Helper()
	if vaultTestDir != nil {
		t.Skip("these tests make their own vault; HI_TEST_VAULT=1 gave every test one already")
	}
	t.Setenv("HI_VAULT_DIR", filepath.Join(t.TempDir(), "vault"))
	t.Setenv("HI_VAULT_KEY_DIR", filepath.Join(t.TempDir(), "vault-key"))
	vaultForget()
	t.Cleanup(vaultForget)
}

// fakeTPM seals as systemd does with the TPM (its ID first), or with the
// host's key alone when hostOnly.
func fakeTPM(t *testing.T, hostOnly bool) *bool {
	t.Helper()
	present := true
	savedReady, savedSeal, savedOpen := vaultTPMReady, vaultTPMSeal, vaultTPMOpen
	t.Cleanup(func() { vaultTPMReady, vaultTPMSeal, vaultTPMOpen = savedReady, savedSeal, savedOpen })
	guid := vaultTPMGUIDs[0]
	if hostOnly {
		guid = []byte{0x55, 0xb9, 0xed, 0x1d, 0x38, 0x59, 0x4d, 0x43, 0xa8, 0x31, 0x9d, 0x2e, 0xbb, 0x33, 0x2a, 0xc6}
	}
	vaultTPMReady = func() (bool, string) { return present, "this machine has no TPM" }
	vaultTPMSeal = func(data []byte) ([]byte, error) {
		return []byte(base64.StdEncoding.EncodeToString(append(append([]byte{}, guid...), data...)) + "\n"), nil
	}
	vaultTPMOpen = func(sealed []byte) ([]byte, error) {
		if !present {
			return nil, errors.New("no TPM")
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sealed)))
		if err != nil || len(raw) < 16 {
			return nil, errors.New("not a credential")
		}
		return raw[16:], nil
	}
	return &present
}

func vaultTestWords(t *testing.T) string {
	t.Helper()
	store, err := openVault()
	if err != nil {
		t.Fatal(err)
	}
	data, _, ok, err := store.get(vaultRecoveryPath)
	if err != nil || !ok {
		t.Fatal("no recovery key waits", err)
	}
	return data.Values[vaultRecoveryName]
}

func TestVaultInitFile(t *testing.T) {
	vaultTest(t)
	result, err := initVault("")
	if err != nil {
		t.Fatal(err)
	}
	if result.Slot != vaultSlotFile || !strings.Contains(result.Why, "no TPM") {
		t.Fatalf("%+v", result)
	}
	info, err := os.Stat(vaultKeyFile())
	if err != nil || info.Mode().Perm() != 0o400 {
		t.Fatal("the key file", err)
	}
	keys, _ := loadVaultKeys()
	if strings.Join(keys.kinds(), ",") != "file,recovery" || len(keys.RecoveryPublic) != 32 {
		t.Fatalf("%+v", keys.kinds())
	}
	// The key file is outside the store's folder, so a backup of hi's
	// state doesn't hold it.
	if strings.HasPrefix(vaultKeyFile(), vaultDir()) {
		t.Fatal("the key file is in the store's folder")
	}
	words := vaultTestWords(t)
	if len(strings.Fields(words)) != 24 {
		t.Fatal(words)
	}
	// Another process opens it with the file.
	vaultForget()
	if _, err := openVault(); err != nil {
		t.Fatal(err)
	}
	if _, err := initVault(""); err == nil {
		t.Fatal("a second vault over the first")
	}
}

func TestVaultTPM(t *testing.T) {
	vaultTest(t)
	present := fakeTPM(t, false)
	result, err := initVault("")
	if err != nil || result.Slot != vaultSlotTPM {
		t.Fatal(result, err)
	}
	keys, _ := loadVaultKeys()
	if strings.Join(keys.kinds(), ",") != "tpm,recovery" || fileExists(vaultKeyFile()) {
		t.Fatal(keys.kinds())
	}
	store, _ := openVault()
	store.put("hi/server/keys", vaultData{Values: map[string]string{"runpod": "rp-1"}}, "test")
	// Another machine: the TPM doesn't open it, and the recovery key
	// does, with a new slot of the machine it is on now.
	words := vaultTestWords(t)
	*present = false
	vaultForget()
	_, err = openVault()
	var locked vaultLockedError
	if !errors.As(err, &locked) || !strings.Contains(vaultLockedNote(), "locked") {
		t.Fatal(err)
	}
	if _, err := vaultUnlockWithRecovery(strings.Replace(words, strings.Fields(words)[0], "zoo", 1)); err == nil {
		t.Fatal("a wrong word unlocked")
	}
	kind, err := vaultUnlockWithRecovery(words)
	if err != nil || !strings.HasPrefix(kind, vaultSlotFile) {
		t.Fatal(kind, err)
	}
	keys, _ = loadVaultKeys()
	if strings.Join(keys.kinds(), ",") != "file,recovery" {
		t.Fatal(keys.kinds())
	}
	vaultForget()
	store, err = openVault()
	if err != nil {
		t.Fatal(err)
	}
	if data, _, _, _ := store.get("hi/server/keys"); data.Values["runpod"] != "rp-1" {
		t.Fatal(data)
	}
}

// systemd seals with the host's key alone when its TPM support is
// partial, without a word: never a tpm slot.
func TestVaultTPMFallbackRefused(t *testing.T) {
	vaultTest(t)
	fakeTPM(t, true)
	result, err := initVault("")
	if err != nil || result.Slot != vaultSlotFile || !strings.Contains(result.Why, "libtss2-rc0t64") {
		t.Fatal(result, err)
	}
	if _, err := initVault(vaultSlotTPM); err == nil {
		t.Fatal("--unlock tpm took a host-only credential")
	}
}

func TestVaultRecoveryWords(t *testing.T) {
	entropy := vaultRandom(32)
	words := vaultRecoveryEncode(entropy)
	back, err := vaultRecoveryDecode("  " + strings.ToUpper(words) + "\n")
	if err != nil || !bytes.Equal(back, entropy) {
		t.Fatal(err)
	}
	list := strings.Fields(words)
	list[3], list[4] = list[4], list[3]
	if list[3] != list[4] {
		if _, err := vaultRecoveryDecode(strings.Join(list, " ")); err == nil {
			t.Fatal("words out of order passed the checksum")
		}
	}
	if _, err := vaultRecoveryDecode("abandon ability"); err == nil {
		t.Fatal("two words")
	}
	// The box key derived from the words is the one whose public half
	// the store keeps.
	salt := vaultRandom(16)
	_, a, _ := vaultRecoveryBox(entropy, salt)
	_, b, _ := vaultRecoveryBox(back, salt)
	if !bytes.Equal(a, b) {
		t.Fatal("the recovery box key differs")
	}
}

func TestVaultVersions(t *testing.T) {
	vaultTest(t)
	initVault("")
	store, _ := openVault()
	path := "hi/app/payments/production"
	for _, value := range []string{"one", "two", "two", "three"} {
		if err := store.put(path, vaultData{Values: map[string]string{"KEY": value, "SAME": "s"}}, "ann"); err != nil {
			t.Fatal(err)
		}
	}
	file, _ := store.load(path)
	if len(file.Versions) != 3 || strings.Join(file.Versions[1].Changed, ",") != "KEY" {
		t.Fatalf("%+v", file.Versions)
	}
	// No value on disk in the clear.
	raw, _ := os.ReadFile(vaultPathFileName(store.dir, path))
	if bytes.Contains(raw, []byte("three")) || bytes.Contains(raw, []byte("two")) {
		t.Fatal("a value in the clear")
	}
	if err := store.rollback(path, 1, "ann"); err != nil {
		t.Fatal(err)
	}
	if data, version, _, _ := store.get(path); data.Values["KEY"] != "one" || version != 4 {
		t.Fatal(data, version)
	}
	if err := store.forget(path, 4); err == nil {
		t.Fatal("forgot the current version")
	}
	if err := store.forget(path, 2); err != nil {
		t.Fatal(err)
	}
	file, _ = store.load(path)
	if file.Versions[1].Forgotten == nil || file.Versions[1].Sealed != nil || len(file.Versions) != 4 {
		t.Fatal("forget")
	}
	if err := store.rollback(path, 2, "ann"); err == nil {
		t.Fatal("rolled back to a forgotten value")
	}
	// A version moved to another path, or changed, doesn't decrypt.
	other := "hi/app/billing/production"
	moved := vaultPathFile{Path: other, Versions: []vaultVersion{file.Versions[2]}}
	store.save(moved)
	if _, _, _, err := store.get(other); err == nil {
		t.Fatal("a version decrypted under another path")
	}
	file.Versions[3].Sealed[30] ^= 1
	store.save(file)
	if _, _, _, err := store.get(path); err == nil {
		t.Fatal("a changed byte decrypted")
	}
}

func TestVaultAppSecrets(t *testing.T) {
	newTeamKeysTest(t)
	vaultTest(t)
	if err := appEnv([]string{"payments", "WEATHER_API_KEY=w-1"}, strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := appEnv([]string{"payments", "--production", "DB_URL=postgres://prod"}, strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	cert := filepath.Join(t.TempDir(), "cert.pem")
	os.WriteFile(cert, []byte("-----CERT-----\x00\xff"), 0o600)
	if err := appEnv([]string{"payments", "--file", "MONGODB_CERT=" + cert}, strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	secrets, _ := loadTeamSecrets("payments")
	secrets.HeartbeatURL = "https://hc-ping.example/abc"
	if err := saveTeamSecrets("payments", secrets); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(appDir("payments"), "secrets.json"))

	var out bytes.Buffer
	if err := ensureVault(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "app payments's settings") {
		t.Fatal(out.String())
	}
	// The file keeps only the heartbeat's URL.
	left, _ := os.ReadFile(filepath.Join(appDir("payments"), "secrets.json"))
	if strings.Contains(string(left), "w-1") || strings.Contains(string(left), "xoxb") || !strings.Contains(string(left), "hc-ping.example") {
		t.Fatal(string(left))
	}
	after, err := readSecretFile(appSecretsSecret("payments"))
	if err != nil || !sameSecret(appSecretsSecret("payments"), before, after) {
		t.Fatalf("%s\n%s\n%v", before, after, err)
	}
	store, _ := openVault()
	for path, name := range map[string]string{"hi/app/payments/shared": "WEATHER_API_KEY", "hi/app/payments/production": "DB_URL", "hi/app/payments/hi": "slack_bot_token"} {
		if data, _, _, _ := store.get(path); data.Values[name] == "" {
			t.Fatalf("%s has no %s: %+v", path, name, data)
		}
	}
	if data, _, _, _ := store.get("hi/app/payments/shared"); !bytes.Equal(data.Files["MONGODB_CERT"], []byte("-----CERT-----\x00\xff")) {
		t.Fatal("the binary file")
	}
	// Changes go to the store; the boxes' settings come from it.
	if err := appEnv([]string{"payments", "WEATHER_API_KEY=w-2"}, strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	if secrets, _ := loadTeamSecrets("payments"); secrets.Env["WEATHER_API_KEY"] != "w-2" || secrets.EnvProduction["DB_URL"] != "postgres://prod" || secrets.HeartbeatURL == "" {
		t.Fatalf("%+v", secrets)
	}
	file, _ := store.load("hi/app/payments/shared")
	if last := file.Versions[len(file.Versions)-1]; strings.Join(last.Changed, ",") != "WEATHER_API_KEY" {
		t.Fatal(last.Changed)
	}
	values, _, _ := appSlotSettings("payments", appSlotProduction)
	if values["WEATHER_API_KEY"] != "w-2" || values["DB_URL"] != "postgres://prod" {
		t.Fatal(values)
	}
	// A move again is nothing.
	out.Reset()
	if err := ensureVault(&out); err != nil || strings.Contains(out.String(), "Moved") {
		t.Fatal(out.String(), err)
	}

	// Removed, the settings stay, read by nothing; back with the folder.
	if kept, err := vaultRemoveApp("payments"); err != nil || !kept {
		t.Fatal(kept, err)
	}
	if secrets, _ := loadTeamSecrets("payments"); secrets.Env["WEATHER_API_KEY"] != "" {
		t.Fatal("a removed app's settings are read")
	}
	if !vaultRemovedApp("payments") {
		t.Fatal("not marked removed")
	}
	vaultReviveApp("payments")
	if secrets, _ := loadTeamSecrets("payments"); secrets.Env["WEATHER_API_KEY"] != "w-2" {
		t.Fatal("not back")
	}
	// Renamed, they go with it.
	if err := vaultRenameApp("payments", "billing"); err != nil {
		t.Fatal(err)
	}
	if data, _, ok, err := store.get("hi/app/billing/shared"); !ok || err != nil || data.Values["WEATHER_API_KEY"] != "w-2" {
		t.Fatal(data, err)
	}
	if files, _ := store.paths("hi/app/payments"); len(files) != 0 {
		t.Fatal("left under the old name")
	}
	if n, err := vaultPurgeApp("billing"); err != nil || n == 0 {
		t.Fatal(n, err)
	}
	if files, _ := store.paths("hi/app/billing"); len(files) != 0 {
		t.Fatal("purged, still there")
	}
}

func TestVaultExportForMove(t *testing.T) {
	newTeamKeysTest(t)
	vaultTest(t)
	appEnv([]string{"payments", "WEATHER_API_KEY=w-1"}, strings.NewReader(""), io.Discard)
	ensureVault(io.Discard)
	done, err := vaultExportApp("payments")
	if err != nil {
		t.Fatal(err)
	}
	carried, _ := os.ReadFile(filepath.Join(appDir("payments"), "secrets.json"))
	if !strings.Contains(string(carried), "w-1") {
		t.Fatal("the copy doesn't carry the settings")
	}
	done()
	if left, _ := os.ReadFile(filepath.Join(appDir("payments"), "secrets.json")); strings.Contains(string(left), "w-1") {
		t.Fatal("the settings stay in the folder after the copy")
	}
	// The machine it went to takes them in.
	os.WriteFile(filepath.Join(appDir("payments"), "secrets.json"), carried, 0o600)
	vaultPurgeApp("payments")
	if err := vaultImportApp("payments"); err != nil {
		t.Fatal(err)
	}
	if secrets, _ := loadTeamSecrets("payments"); secrets.Env["WEATHER_API_KEY"] != "w-1" {
		t.Fatal("not taken in")
	}
	if left, _ := os.ReadFile(filepath.Join(appDir("payments"), "secrets.json")); strings.Contains(string(left), "w-1") {
		t.Fatal(string(left))
	}
}

func TestVaultServerFiles(t *testing.T) {
	vaultTest(t)
	dir, _ := orgDirectory("")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "keys.json"), []byte(`{"runpod": "rp-secret", "ai": "sk-or-x"}`+"\n"), 0o600)
	os.WriteFile(slackConfigPath(dir), []byte(`{"bot_token": "xoxb-server", "approvers": {"U1": "ann"}}`), 0o600)
	saveNetbirdAPIToken("nb-token")
	if err := ensureVault(io.Discard); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(dir, "keys.json")) || fileExists(slackConfigPath(dir)) || fileExists(netbirdTokenPath()) {
		t.Fatal("a file stayed")
	}
	if token, err := netbirdAPIToken(); err != nil || token != "nb-token" {
		t.Fatal(token, err)
	}
	config, err := loadSlackConfig(dir)
	if err != nil || config == nil || config.BotToken != "xoxb-server" || config.Approvers["U1"] != "ann" {
		t.Fatal(config, err)
	}
	data, err := readSecretFile(serverKeysSecret(dir))
	keys := map[string]string{}
	json.Unmarshal(data, &keys)
	if err != nil || keys["runpod"] != "rp-secret" {
		t.Fatal(string(data), err)
	}
	// Another folder, such as a test server's, keeps its files.
	other := t.TempDir()
	if err := saveSlackConfig(other, slackConfig{BotToken: "xoxb-other"}); err != nil || !fileExists(slackConfigPath(other)) {
		t.Fatal(err)
	}
	// Not one value on disk in the clear.
	filepath.Walk(vaultDir(), func(path string, info os.FileInfo, err error) error {
		if raw, _ := os.ReadFile(path); bytes.Contains(raw, []byte("rp-secret")) || bytes.Contains(raw, []byte("xoxb-server")) {
			t.Errorf("%s holds a value", path)
		}
		return nil
	})
	var out bytes.Buffer
	vaultListCommand(nil, &out)
	if !strings.Contains(out.String(), "hi/server/keys") || strings.Contains(out.String(), "rp-secret") || strings.Contains(out.String(), vaultRecoveryPath) {
		t.Fatal(out.String())
	}
}

func TestVaultSlots(t *testing.T) {
	vaultTest(t)
	fakeTPM(t, false)
	initVault(vaultSlotFile)
	var out bytes.Buffer
	vaultBrief(&out)
	if !strings.Contains(out.String(), "a copied disk can open it") || !strings.Contains(out.String(), "unlock-with add tpm") {
		t.Fatal(out.String())
	}
	if err := vaultUnlockWithCommand([]string{"remove", "file"}, &out); err == nil {
		t.Fatal("removed the only slot that opens by itself")
	}
	if err := vaultUnlockWithCommand([]string{"add", "tpm"}, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	vaultStatus(&out)
	if !strings.Contains(out.String(), "undoes the TPM's") {
		t.Fatal(out.String())
	}
	if err := vaultUnlockWithCommand([]string{"remove", "file"}, &out); err != nil {
		t.Fatal(err)
	}
	if fileExists(vaultKeyFile()) {
		t.Fatal("the key file stays")
	}
	vaultForget()
	if _, err := openVault(); err != nil {
		t.Fatal(err)
	}
}

func TestVaultResticKey(t *testing.T) {
	setupTeamTest(t)
	vaultTest(t)
	initVault("")
	os.MkdirAll(appSnapshotsDir("payments"), 0o700)
	writeSecretFile(appSnapshotKeySecret("payments"), []byte("snap-key\n"), 0o600)
	if fileExists(appSnapshotKey("payments")) {
		t.Fatal("the snapshot key is a file")
	}
	// A test binary isn't hi, so the test sees the key's file instead.
	if env := strings.Join(appResticEnv("payments"), " "); !strings.Contains(env, "RESTIC_PASSWORD_FILE") {
		t.Fatal(env)
	}
	var out bytes.Buffer
	if err := appSnapshotKeyCommand([]string{"payments"}, &out); err != nil || out.String() != "snap-key\n" {
		t.Fatal(out.String(), err)
	}
}

func TestHeartbeatFailsWhenLocked(t *testing.T) {
	var got []string
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+string(body))
	}))
	defer service.Close()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveMachineHeartbeat(machineHeartbeat{URL: service.URL + "/ping/abc", Every: "5m"})
	saved := machineHeartbeatTrouble
	t.Cleanup(func() { machineHeartbeatTrouble = saved })
	machineHeartbeatTrouble = func() string { return "the server's vault is locked; unlock it with hi server vault unlock" }
	heartbeatCommand([]string{"__ping"}, io.Discard)
	machineHeartbeatTrouble = func() string { return "" }
	heartbeatCommand([]string{"__ping"}, io.Discard)
	if len(got) != 2 || got[0] != "POST /ping/abc/fail the server's vault is locked; unlock it with hi server vault unlock" || !strings.HasPrefix(got[1], "GET /ping/abc ") {
		t.Fatal(got)
	}
}

// While a hi process still runs the binary hi update replaced, the files
// stay the secret, kept as the vault is, so the old hi reads them.
func TestVaultKeepsFilesForAnOlderHi(t *testing.T) {
	newTeamKeysTest(t)
	vaultTest(t)
	appEnv([]string{"payments", "WEATHER_API_KEY=w-1"}, strings.NewReader(""), io.Discard)
	saved := vaultKeepFiles
	t.Cleanup(func() { vaultKeepFiles = saved })
	vaultKeepFiles = func() bool { return true }
	var out bytes.Buffer
	if err := ensureVault(&out); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(appDir("payments"), "secrets.json")
	if data, _ := os.ReadFile(file); !strings.Contains(string(data), "w-1") || !strings.Contains(out.String(), "stay") {
		t.Fatal(out.String())
	}
	store, _ := openVault()
	if data, _, _, _ := store.get("hi/app/payments/shared"); data.Values["WEATHER_API_KEY"] != "w-1" {
		t.Fatal("not copied into the vault")
	}
	// A change by this hi goes to both.
	appEnv([]string{"payments", "WEATHER_API_KEY=w-2"}, strings.NewReader(""), io.Discard)
	if data, _ := os.ReadFile(file); !strings.Contains(string(data), "w-2") {
		t.Fatal("the file wasn't kept as the vault is")
	}
	// One by the older hi, in the file, is the one read, and moves in.
	data, _ := os.ReadFile(file)
	os.WriteFile(file, bytes.Replace(data, []byte("w-2"), []byte("w-3"), 1), 0o600)
	if secrets, _ := loadTeamSecrets("payments"); secrets.Env["WEATHER_API_KEY"] != "w-3" {
		t.Fatal(secrets.Env)
	}
	vaultKeepFiles = func() bool { return false }
	ensureVault(io.Discard)
	if data, _ := os.ReadFile(file); strings.Contains(string(data), "w-3") {
		t.Fatal("the file stays after the older hi is gone")
	}
	if secrets, _ := loadTeamSecrets("payments"); secrets.Env["WEATHER_API_KEY"] != "w-3" {
		t.Fatal(secrets.Env)
	}
}
