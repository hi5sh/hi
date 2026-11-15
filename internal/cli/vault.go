package cli

// The server's vault (docs/specs/approved/hi_server_vault.md): one store
// for every secret hi keeps, encrypted on disk. A random data key
// encrypts each version of each path with XChaCha20-Poly1305; the data
// key is kept once per slot (the machine's TPM, a key file, the recovery
// key), as LUKS does, and any one slot opens the store.
//
// The server holds the data key (vault_server.go): the apps' services,
// the page, and commands ask it on its socket to open or seal a version,
// and it logs each read. Only where no server runs does a process open
// the store itself, with a slot that opens by itself (tpm, file); a
// passphrase only a person types, so only the server holds what it opens.

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
)

const (
	vaultSlotTPM        = "tpm"
	vaultSlotFile       = "file"
	vaultSlotPassphrase = "passphrase"
	vaultSlotRecovery   = "recovery"

	// vaultPassphraseLeast is a passphrase's least length.
	vaultPassphraseLeast = 12

	// vaultRecoveryPath holds the recovery key's words until the owner
	// saves them; then hi forgets them.
	vaultRecoveryPath  = "hi/vault/recovery"
	vaultRecoveryName  = "words"
	vaultRecoveryWords = 24

	// vaultCredName binds the TPM's sealed copy to its use.
	vaultCredName = "hi-vault"
)

// vaultTPMGUIDs are systemd's IDs, the credential's first 16 bytes, for
// a credential sealed with the host's key and the TPM for one user
// (systemd 256 and newer). Anything else, such as the host's key alone
// (55b9ed1d-3859-4d43-a831-9d2ebb332ac6) that systemd falls back to
// without a word, is not a tpm slot.
var vaultTPMGUIDs = [][]byte{
	{0xef, 0x4a, 0xc1, 0x36, 0x79, 0xa9, 0x48, 0x0e, 0xa7, 0xdb, 0x68, 0x89, 0x7f, 0x9f, 0x16, 0x5d},
}

// vaultWordsText is BIP-0039's English word list (bitcoin/bips,
// bip-0039/english.txt, sha256 2f5eed53…dbda; MIT licensed): 2048 words,
// so each is 11 bits of the recovery key.
//
//go:embed vault_words.txt
var vaultWordsText string

var vaultWords = strings.Fields(vaultWordsText)

// vaultDir is the store: keys.json (the slots) and paths/, one file per
// path. It sits in hi's state, beside the server's own.
func vaultDir() string {
	if vaultTestDir != nil {
		return vaultTestDir()
	}
	if dir := os.Getenv("HI_VAULT_DIR"); dir != "" {
		return dir
	}
	parent, err := orgStateParent()
	if err != nil {
		return ""
	}
	return filepath.Join(parent, "vault")
}

// vaultKeyDir holds the file slot's key, in a folder of its own outside
// hi's state, so no backup of the state or snapshot takes it.
func vaultKeyDir() string {
	if vaultTestDir != nil {
		return vaultTestDir() + "-key"
	}
	if dir := os.Getenv("HI_VAULT_KEY_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(hiConfigDirectory(), "vault-key")
}

func vaultKeyFile() string { return filepath.Join(vaultKeyDir(), "key") }

func vaultKeysPath() string { return filepath.Join(vaultDir(), "keys.json") }

// vaultExists is whether this machine has a store. Until it does, every
// secret is the file it was.
func vaultExists() bool {
	dir := vaultDir()
	if dir == "" {
		return false
	}
	if vaultTestDir != nil && !fileExists(filepath.Join(dir, "keys.json")) {
		// A test's leftover goroutine may race the next test's folder.
		initVault(vaultSlotFile)
	}
	return fileExists(filepath.Join(dir, "keys.json"))
}

// vaultTestDir is set by tests that run with a vault in every test's own
// state (HI_TEST_VAULT=1), made the first time it's asked for.
var vaultTestDir func() string

// vaultKeys is keys.json: the data key, once per slot.
type vaultKeys struct {
	Version int         `json:"version"`
	Created time.Time   `json:"created"`
	Slots   []vaultSlot `json:"slots"`
	// RecoveryPublic is the X25519 public half derived from the recovery
	// key, which hi seals snapshot keys to without knowing the words;
	// RecoveryBoxSalt is its derivation's salt.
	RecoveryPublic  []byte `json:"recovery_public,omitempty"`
	RecoveryBoxSalt []byte `json:"recovery_box_salt,omitempty"`
	// Check is the data key's hash under a fixed label, so an unwrapped
	// key is known to be the right one.
	Check []byte `json:"check"`
}

type vaultSlot struct {
	Kind    string    `json:"kind"`
	Created time.Time `json:"created"`
	Machine string    `json:"machine,omitempty"`
	// Wrapped is the data key: sealed by systemd for tpm, encrypted with
	// the key file's or the recovery key's key otherwise.
	Wrapped []byte `json:"wrapped"`
	Salt    []byte `json:"salt,omitempty"`
}

func (k vaultKeys) slot(kind string) (vaultSlot, bool) {
	for _, slot := range k.Slots {
		if slot.Kind == kind {
			return slot, true
		}
	}
	return vaultSlot{}, false
}

func (k *vaultKeys) setSlot(slot vaultSlot) {
	k.Slots = slices.DeleteFunc(k.Slots, func(s vaultSlot) bool { return s.Kind == slot.Kind })
	k.Slots = append(k.Slots, slot)
	order := map[string]int{vaultSlotTPM: 0, vaultSlotFile: 1, vaultSlotPassphrase: 2, vaultSlotRecovery: 3}
	sort.SliceStable(k.Slots, func(i, j int) bool { return order[k.Slots[i].Kind] < order[k.Slots[j].Kind] })
}

func (k vaultKeys) kinds() []string {
	var kinds []string
	for _, slot := range k.Slots {
		kinds = append(kinds, slot.Kind)
	}
	return kinds
}

func loadVaultKeys() (vaultKeys, error) {
	var keys vaultKeys
	data, err := os.ReadFile(vaultKeysPath())
	if err != nil {
		return keys, err
	}
	if err := json.Unmarshal(data, &keys); err != nil {
		return keys, fmt.Errorf("the vault's keys.json: %w", err)
	}
	return keys, nil
}

func saveVaultKeys(keys vaultKeys) error {
	data, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(vaultKeysPath(), append(data, '\n'), 0o600)
}

func vaultKeyCheck(key []byte) []byte {
	sum := sha256.Sum256(append([]byte("hi-vault-check\x00"), key...))
	return sum[:]
}

// ---------------------------------------------------------------------------
// the TPM, through systemd-creds --user

// vaultTPMReady says whether hi can seal to the TPM without root: a TPM,
// and systemd's full support for it. With partial support systemd seals
// with the host's key alone, so that is "no", with what to install.
var vaultTPMReady = func() (bool, string) {
	if _, err := exec.LookPath("systemd-creds"); err != nil {
		return false, "this machine has no systemd-creds"
	}
	if !fileExists("/dev/tpmrm0") && !fileExists("/sys/class/tpm/tpm0") {
		return false, "this machine has no TPM"
	}
	out, err := exec.Command("systemd-analyze", "has-tpm2").CombinedOutput()
	if err == nil {
		return true, ""
	}
	if strings.Contains(string(out), "-libtss2-rc") || strings.Contains(string(out), "-libraries") {
		return false, "the TPM's library is missing; ask your IT team to install libtss2-rc0t64 (sudo apt install libtss2-rc0t64)"
	}
	return false, "systemd can't use the TPM (systemd-analyze has-tpm2)"
}

// vaultTPMSeal seals data for this user with the host's key and the TPM,
// bound to the chip only (no PCRs), so a firmware update doesn't lock the
// store but a copied disk can't open it.
var vaultTPMSeal = func(data []byte) ([]byte, error) {
	cmd := exec.Command("systemd-creds", "--user", "encrypt", "--with-key=host+tpm2", "--tpm2-pcrs=", "--name="+vaultCredName, "-", "-")
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("systemd-creds encrypt: %s", qFirstLine(stderr.String(), err.Error()))
	}
	return out, nil
}

var vaultTPMOpen = func(sealed []byte) ([]byte, error) {
	cmd := exec.Command("systemd-creds", "--user", "decrypt", "--name="+vaultCredName, "-", "-")
	cmd.Stdin = bytes.NewReader(sealed)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("systemd-creds decrypt: %s", qFirstLine(stderr.String(), err.Error()))
	}
	return out, nil
}

// vaultSealedWithTPM is whether systemd really used the TPM: its
// credential's first 16 bytes name how it was sealed.
func vaultSealedWithTPM(sealed []byte) bool {
	raw := sealed
	if decoded, err := base64.StdEncoding.DecodeString(string(bytes.Join(bytes.Fields(sealed), nil))); err == nil {
		raw = decoded
	}
	if len(raw) < 16 {
		return false
	}
	for _, guid := range vaultTPMGUIDs {
		if bytes.Equal(raw[:16], guid) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// encryption

func vaultSeal(key, plain, ad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize(), aead.NonceSize()+len(plain)+aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plain, ad), nil
}

func vaultOpenSealed(key, sealed, ad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < aead.NonceSize() {
		return nil, errors.New("too short")
	}
	return aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], ad)
}

func vaultSlotAD(kind string) []byte { return []byte("hi-vault-slot\x00" + kind) }

func vaultVersionAD(path string, version int) []byte {
	return []byte("hi-vault\x00" + path + "\x00" + strconv.Itoa(version))
}

// vaultStretch is Argon2id with the spec's cost: 64 MiB, 3 passes.
func vaultStretch(secret, salt []byte) []byte {
	return argon2.IDKey(secret, salt, 3, vaultArgonMemory, 4, 32)
}

// vaultArgonMemory is Argon2id's memory in KiB; tests use less.
var vaultArgonMemory uint32 = 64 * 1024

func vaultRandom(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// ---------------------------------------------------------------------------
// the recovery key: 24 words, 256 bits and an 8-bit checksum, as BIP39

func vaultRecoveryEncode(entropy []byte) string {
	sum := sha256.Sum256(entropy)
	bits := append(append([]byte{}, entropy...), sum[0])
	words := make([]string, 0, vaultRecoveryWords)
	for i := range vaultRecoveryWords {
		index := 0
		for b := range 11 {
			bit := i*11 + b
			index = index<<1 | int(bits[bit/8]>>(7-bit%8)&1)
		}
		words = append(words, vaultWords[index])
	}
	return strings.Join(words, " ")
}

// vaultRecoveryDecode reads the words back to their 256 bits; a word not
// in the list, or one wrong word, is refused.
func vaultRecoveryDecode(text string) ([]byte, error) {
	words := strings.Fields(strings.ToLower(text))
	if len(words) != vaultRecoveryWords {
		return nil, fmt.Errorf("the recovery key is %d words; that was %d", vaultRecoveryWords, len(words))
	}
	bits := make([]byte, 33)
	for i, word := range words {
		index := slices.Index(vaultWords, word)
		if index < 0 {
			return nil, fmt.Errorf("word %d, %q, isn't one of the recovery key's words", i+1, word)
		}
		for b := range 11 {
			if index>>(10-b)&1 == 1 {
				bit := i*11 + b
				bits[bit/8] |= 1 << (7 - bit%8)
			}
		}
	}
	entropy := bits[:32]
	if sum := sha256.Sum256(entropy); sum[0] != bits[32] {
		return nil, errors.New("those words aren't a recovery key: one is mistyped or out of order")
	}
	return entropy, nil
}

// vaultRecoveryBox is the X25519 key pair derived from the recovery key.
func vaultRecoveryBox(entropy, salt []byte) (private, public []byte, err error) {
	private = vaultStretch(append([]byte("hi-vault-box\x00"), entropy...), salt)
	public, err = curve25519.X25519(private, curve25519.Basepoint)
	return private, public, err
}

// ---------------------------------------------------------------------------
// the store

// vaultStore is an open store: the data key, in this process's memory,
// or, remote, the server's, which opens and seals each version for this
// process (vault_server.go).
type vaultStore struct {
	dir    string
	key    []byte
	remote bool
}

// openVersion decrypts one version's sealed bytes.
func (v *vaultStore) openVersion(path string, version int, sealed []byte) ([]byte, error) {
	if v.remote {
		return vaultRemoteOpen(path, version, sealed)
	}
	return vaultOpenSealed(v.key, sealed, vaultVersionAD(path, version))
}

// sealVersion encrypts one version's values.
func (v *vaultStore) sealVersion(path string, version int, plain []byte) ([]byte, error) {
	if v.remote {
		return vaultRemoteSeal(path, version, plain)
	}
	return vaultSeal(v.key, plain, vaultVersionAD(path, version))
}

// vaultLockedError is a store no slot opened.
type vaultLockedError struct{ why string }

func (e vaultLockedError) Error() string {
	return "the server's vault is locked (" + e.why + "); an admin unlocks it on the admin page or with hi server vault unlock"
}

var (
	vaultMu     sync.Mutex
	vaultOpened = map[string]*vaultStore{}
)

// openVault is this machine's store, once per process: the key the
// server holds, asked for on its socket; in the server, its own; where no
// server runs, opened here with the first slot that opens by itself.
func openVault() (*vaultStore, error) {
	dir := vaultDir()
	vaultMu.Lock()
	defer vaultMu.Unlock()
	if store := vaultOpened[dir]; store != nil {
		return store, nil
	}
	if vaultServing != nil {
		// The server: the key it holds, never cached, so a lock holds.
		if key := vaultServing.key(); key != nil {
			return &vaultStore{dir: dir, key: key}, nil
		}
		return nil, vaultLockedError{vaultServing.lockedWhy()}
	}
	if status, reached := vaultAskServer(); reached {
		if !status.Open {
			return nil, vaultLockedError{status.Why}
		}
		store := &vaultStore{dir: dir, remote: true}
		vaultOpened[dir] = store
		return store, nil
	}
	key, err := vaultOpenBySlots()
	if err != nil {
		return nil, err
	}
	store := &vaultStore{dir: dir, key: key}
	vaultOpened[dir] = store
	return store, nil
}

// vaultOpenBySlots unwraps the data key with the first slot that opens
// by itself: the TPM, or the key file.
func vaultOpenBySlots() ([]byte, error) {
	key, _, err := vaultOpenSlot()
	return key, err
}

// vaultOpenSlot is vaultOpenBySlots, and the slot that opened it.
func vaultOpenSlot() ([]byte, string, error) {
	keys, err := loadVaultKeys()
	if err != nil {
		return nil, "", err
	}
	var why []string
	for _, slot := range keys.Slots {
		var key []byte
		switch slot.Kind {
		case vaultSlotTPM:
			key, err = vaultTPMOpen(slot.Wrapped)
		case vaultSlotFile:
			var fileKey []byte
			if fileKey, err = readVaultKeyFile(); err == nil {
				key, err = vaultOpenSealed(fileKey, slot.Wrapped, vaultSlotAD(slot.Kind))
			}
		default:
			continue
		}
		if err == nil && !bytes.Equal(vaultKeyCheck(key), keys.Check) {
			err = errors.New("it gave the wrong key")
		}
		if err != nil {
			why = append(why, slot.Kind+": "+err.Error())
			continue
		}
		return key, slot.Kind, nil
	}
	if len(why) == 0 {
		why = append(why, "no slot opens it by itself")
	}
	return nil, "", vaultLockedError{strings.Join(why, "; ")}
}

// vaultUnwrap opens the data key with what a person typed: the recovery
// key's words, or a passphrase. kind says which it was.
func vaultUnwrap(keys vaultKeys, secret string) (key []byte, kind string, err error) {
	if entropy, err := vaultRecoveryDecode(secret); err == nil {
		slot, ok := keys.slot(vaultSlotRecovery)
		if !ok {
			return nil, "", errors.New("the vault has no recovery slot")
		}
		key, err := vaultOpenSealed(vaultStretch(entropy, slot.Salt), slot.Wrapped, vaultSlotAD(vaultSlotRecovery))
		if err != nil || !bytes.Equal(vaultKeyCheck(key), keys.Check) {
			return nil, vaultSlotRecovery, errors.New("that isn't this vault's recovery key")
		}
		return key, vaultSlotRecovery, nil
	} else if len(strings.Fields(secret)) == vaultRecoveryWords {
		return nil, vaultSlotRecovery, err
	}
	slot, ok := keys.slot(vaultSlotPassphrase)
	if !ok {
		return nil, "", errors.New("the vault has no passphrase; unlock it with the recovery key's 24 words")
	}
	key, err = vaultOpenSealed(vaultStretch([]byte(secret), slot.Salt), slot.Wrapped, vaultSlotAD(vaultSlotPassphrase))
	if err != nil || !bytes.Equal(vaultKeyCheck(key), keys.Check) {
		return nil, vaultSlotPassphrase, errors.New("that isn't the vault's passphrase")
	}
	return key, vaultSlotPassphrase, nil
}

// vaultResealHere replaces the tpm and file slots when none of them opens
// on this machine (a moved or restored server, a lost key file) with a
// new tpm slot, else a file slot, so the store opens by itself again. A
// store that had neither, a passphrase's alone, stays so. It says what it
// did, or "".
func vaultResealHere(keys *vaultKeys, dataKey []byte) (string, error) {
	had := slices.ContainsFunc(keys.Slots, func(s vaultSlot) bool { return s.Kind == vaultSlotTPM || s.Kind == vaultSlotFile })
	if !had {
		return "", nil
	}
	if key, err := vaultOpenBySlots(); err == nil && bytes.Equal(key, dataKey) {
		return "", nil
	}
	kind, why := vaultSlotTPM, ""
	next, err := vaultWrapSlot(vaultSlotTPM, dataKey, nil)
	if err != nil {
		why = err.Error()
		kind = vaultSlotFile
		if next, err = vaultWrapSlot(vaultSlotFile, dataKey, nil); err != nil {
			return "", err
		}
	}
	keys.Slots = slices.DeleteFunc(keys.Slots, func(s vaultSlot) bool { return s.Kind == vaultSlotTPM || s.Kind == vaultSlotFile })
	keys.setSlot(next)
	if why != "" {
		return kind + " (no tpm: " + why + ")", nil
	}
	return kind, nil
}

// vaultForget drops this process's open stores, for tests and lock.
func vaultForget() {
	vaultMu.Lock()
	defer vaultMu.Unlock()
	vaultOpened = map[string]*vaultStore{}
}

func readVaultKeyFile() ([]byte, error) {
	data, err := os.ReadFile(vaultKeyFile())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("its key file %s is gone", vaultKeyFile())
		}
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("its key file %s isn't a key", vaultKeyFile())
	}
	return key, nil
}

// vaultWrapSlot keeps the data key in a new slot of kind; secret is the
// recovery key's entropy or the passphrase.
func vaultWrapSlot(kind string, dataKey []byte, secret []byte) (vaultSlot, error) {
	host, _ := os.Hostname()
	slot := vaultSlot{Kind: kind, Created: computeNow().UTC(), Machine: host}
	switch kind {
	case vaultSlotTPM:
		if ok, why := vaultTPMReady(); !ok {
			return slot, errors.New(why)
		}
		sealed, err := vaultTPMSeal(dataKey)
		if err != nil {
			return slot, err
		}
		if !vaultSealedWithTPM(sealed) {
			return slot, errors.New("systemd sealed it without the TPM; ask your IT team to install libtss2-rc0t64 (sudo apt install libtss2-rc0t64)")
		}
		opened, err := vaultTPMOpen(sealed)
		if err != nil || !bytes.Equal(opened, dataKey) {
			return slot, fmt.Errorf("the TPM's copy didn't open again: %v", err)
		}
		slot.Wrapped = sealed
	case vaultSlotFile:
		fileKey, err := readVaultKeyFile()
		if err != nil {
			fileKey = vaultRandom(32)
			if err := os.MkdirAll(vaultKeyDir(), 0o700); err != nil {
				return slot, err
			}
			path := vaultKeyFile()
			os.Remove(path)
			if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(fileKey)+"\n"), 0o400); err != nil {
				return slot, err
			}
		}
		if slot.Wrapped, err = vaultSeal(fileKey, dataKey, vaultSlotAD(kind)); err != nil {
			return slot, err
		}
	case vaultSlotRecovery, vaultSlotPassphrase:
		if kind == vaultSlotPassphrase && len([]rune(string(secret))) < vaultPassphraseLeast {
			return slot, fmt.Errorf("a passphrase has at least %d characters; a few words are easiest to type", vaultPassphraseLeast)
		}
		slot.Salt = vaultRandom(16)
		wrapped, err := vaultSeal(vaultStretch(secret, slot.Salt), dataKey, vaultSlotAD(kind))
		if err != nil {
			return slot, err
		}
		slot.Wrapped = wrapped
	default:
		return slot, fmt.Errorf("no slot called %q: tpm, file, passphrase, or recovery", kind)
	}
	return slot, nil
}

// vaultLock takes the store's lock for a change, across processes.
func vaultLock() (func(), error) {
	if err := os.MkdirAll(vaultDir(), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(vaultDir(), ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
	}, nil
}

// vaultInitResult is what creating the store did.
type vaultInitResult struct {
	Slot string // tpm or file
	Why  string // why not tpm, when file
}

// initVault creates the store: the data key, the slot that opens by
// itself (tpm where hi can use it, else file, or the one asked for) or a
// passphrase, and the recovery key, which waits in the store for the
// owner.
func initVault(want string, passphrase ...string) (vaultInitResult, error) {
	var result vaultInitResult
	unlock, err := vaultLock()
	if err != nil {
		return result, err
	}
	defer unlock()
	if fileExists(vaultKeysPath()) {
		return result, errors.New("this machine has a vault already (hi server vault)")
	}
	dataKey := vaultRandom(32)
	keys := vaultKeys{Version: 1, Created: computeNow().UTC(), Check: vaultKeyCheck(dataKey)}
	switch want {
	case "", vaultSlotTPM:
		slot, err := vaultWrapSlot(vaultSlotTPM, dataKey, nil)
		if err == nil {
			keys.setSlot(slot)
			result.Slot = vaultSlotTPM
			break
		}
		if want == vaultSlotTPM {
			return result, err
		}
		result.Why = err.Error()
		fallthrough
	case vaultSlotFile:
		slot, err := vaultWrapSlot(vaultSlotFile, dataKey, nil)
		if err != nil {
			return result, err
		}
		keys.setSlot(slot)
		result.Slot = vaultSlotFile
	case vaultSlotPassphrase:
		if len(passphrase) == 0 {
			return result, errors.New("a passphrase vault needs its passphrase")
		}
		slot, err := vaultWrapSlot(vaultSlotPassphrase, dataKey, []byte(passphrase[0]))
		if err != nil {
			return result, err
		}
		keys.setSlot(slot)
		result.Slot = vaultSlotPassphrase
	default:
		return result, fmt.Errorf("a vault opens with tpm, file, or passphrase; %q isn't one", want)
	}
	words, err := vaultNewRecovery(&keys, dataKey)
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(filepath.Join(vaultDir(), "paths"), 0o700); err != nil {
		return result, err
	}
	store := &vaultStore{dir: vaultDir(), key: dataKey}
	if err := store.putLocked(vaultRecoveryPath, vaultData{Values: map[string]string{vaultRecoveryName: words}}, "hi"); err != nil {
		return result, err
	}
	if err := saveVaultKeys(keys); err != nil {
		return result, err
	}
	if vaultServing != nil {
		vaultServing.opened(dataKey, "init")
	} else if !vaultServerRuns() {
		vaultMu.Lock()
		vaultOpened[store.dir] = store
		vaultMu.Unlock()
	}
	vaultAudit("hi", "vault.init", "vault", "opens with "+strings.Join(keys.kinds(), ", "))
	return result, nil
}

// vaultNewRecovery makes a recovery key, puts its slot and public half in
// keys, and returns its words.
func vaultNewRecovery(keys *vaultKeys, dataKey []byte) (string, error) {
	entropy := vaultRandom(32)
	slot, err := vaultWrapSlot(vaultSlotRecovery, dataKey, entropy)
	if err != nil {
		return "", err
	}
	keys.setSlot(slot)
	keys.RecoveryBoxSalt = vaultRandom(16)
	_, public, err := vaultRecoveryBox(entropy, keys.RecoveryBoxSalt)
	if err != nil {
		return "", err
	}
	keys.RecoveryPublic = public
	return vaultRecoveryEncode(entropy), nil
}

// vaultUnlockHere opens the store, where no server runs, with the
// recovery key (a moved machine, a lost key file), and keeps it open: a
// new tpm slot in place of the old, else a file slot. A passphrase opens
// nothing here: only the server can hold what it opens.
func vaultUnlockHere(words string) (string, error) {
	unlock, err := vaultLock()
	if err != nil {
		return "", err
	}
	defer unlock()
	keys, err := loadVaultKeys()
	if err != nil {
		return "", err
	}
	dataKey, kind, err := vaultUnwrap(keys, words)
	if err != nil {
		vaultAudit(vaultActor(), "vault.unlock", "vault", "refused: "+err.Error())
		return "", err
	}
	if kind != vaultSlotRecovery {
		return "", errors.New("the server holds what a passphrase opens, and it isn't running: start it (systemctl --user start hi-server.service), then unlock")
	}
	resealed, err := vaultResealHere(&keys, dataKey)
	if err != nil {
		return "", err
	}
	if resealed == "" {
		next, err := vaultWrapSlot(vaultSlotTPM, dataKey, nil)
		resealed = vaultSlotTPM
		if err != nil {
			if next, err = vaultWrapSlot(vaultSlotFile, dataKey, nil); err != nil {
				return "", err
			}
			resealed = vaultSlotFile
		}
		keys.setSlot(next)
	}
	if err := saveVaultKeys(keys); err != nil {
		return "", err
	}
	vaultMu.Lock()
	vaultOpened[vaultDir()] = &vaultStore{dir: vaultDir(), key: dataKey}
	vaultMu.Unlock()
	vaultAudit(vaultActor(), "vault.unlock", "vault", "with the recovery key; now opens with "+resealed)
	return resealed, nil
}

// ---------------------------------------------------------------------------
// paths

// vaultData is one version of a path: names and string values, and
// files as bytes. JSON lists the names whose value is JSON hi keeps as
// text (a sign-in's settings, say), so the file it came from reads back
// the same.
type vaultData struct {
	Values map[string]string `json:"values,omitempty"`
	Files  map[string][]byte `json:"files,omitempty"`
	JSON   []string          `json:"json,omitempty"`
}

func (d vaultData) empty() bool { return len(d.Values) == 0 && len(d.Files) == 0 }

func (d vaultData) names() []string {
	var names []string
	for name := range d.Values {
		names = append(names, name)
	}
	for name := range d.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// changedNames are the names whose value differs between a and b.
func changedNames(a, b vaultData) []string {
	seen := map[string]bool{}
	var changed []string
	check := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		av, aok := a.Values[name]
		bv, bok := b.Values[name]
		af, afok := a.Files[name]
		bf, bfok := b.Files[name]
		if aok != bok || av != bv || afok != bfok || !bytes.Equal(af, bf) || slices.Contains(a.JSON, name) != slices.Contains(b.JSON, name) {
			changed = append(changed, name)
		}
	}
	for _, name := range a.names() {
		check(name)
	}
	for _, name := range b.names() {
		check(name)
	}
	sort.Strings(changed)
	return changed
}

// vaultPathFile is one path on disk: every version, encrypted.
type vaultPathFile struct {
	Path string `json:"path"`
	// Removed marks a removed app's path: kept, read by nothing, until
	// the app comes back or is purged.
	Removed  *time.Time     `json:"removed,omitempty"`
	Versions []vaultVersion `json:"versions"`
}

type vaultVersion struct {
	Version int       `json:"version"`
	Time    time.Time `json:"time"`
	By      string    `json:"by"`
	// Changed is which names this version changed; never a value.
	Changed []string `json:"changed,omitempty"`
	// Rollback is the version this one put back, if it did.
	Rollback  int        `json:"rollback,omitempty"`
	Sealed    []byte     `json:"sealed,omitempty"`
	Forgotten *time.Time `json:"forgotten,omitempty"`
}

func validVaultPath(path string) bool {
	if !strings.HasPrefix(path, "hi/") || strings.Contains(path, "//") || strings.HasSuffix(path, "/") {
		return false
	}
	for _, r := range path {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '/' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return !strings.Contains(path, "..")
}

func vaultPathFileName(dir, path string) string {
	return filepath.Join(dir, "paths", strings.ReplaceAll(path, "/", "+")+".json")
}

func (v *vaultStore) loadLocal(path string) (vaultPathFile, error) {
	file := vaultPathFile{Path: path}
	data, err := os.ReadFile(vaultPathFileName(v.dir, path))
	if errors.Is(err, fs.ErrNotExist) {
		return file, nil
	}
	if err != nil {
		return file, err
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return file, fmt.Errorf("the vault's %s: %w", path, err)
	}
	return file, nil
}

func (v *vaultStore) save(file vaultPathFile) error {
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(v.dir, "paths"), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(vaultPathFileName(v.dir, file.Path), append(data, '\n'), 0o600)
}

func (v *vaultStore) decryptLocal(path string, version vaultVersion) (vaultData, error) {
	var data vaultData
	if version.Forgotten != nil {
		return data, fmt.Errorf("version %d of %s was forgotten", version.Version, path)
	}
	plain, err := v.openVersion(path, version.Version, version.Sealed)
	if err != nil {
		var locked vaultLockedError
		if errors.As(err, &locked) {
			return data, err
		}
		return data, fmt.Errorf("version %d of %s doesn't decrypt: it was changed on disk", version.Version, path)
	}
	if err := json.Unmarshal(plain, &data); err != nil {
		return data, err
	}
	return data, nil
}

// get is a path's latest version; ok is false for a path that isn't
// there, is empty, or is a removed app's.
func (v *vaultStore) getLocal(path string) (vaultData, int, bool, error) {
	file, err := v.loadLocal(path)
	if err != nil || len(file.Versions) == 0 || file.Removed != nil {
		return vaultData{}, 0, false, err
	}
	latest := file.Versions[len(file.Versions)-1]
	data, err := v.decryptLocal(path, latest)
	if err != nil {
		return data, 0, false, err
	}
	return data, latest.Version, !data.empty(), nil
}

// getLocal and putLocal are the built-in store's own, even when hi's
// paths moved to a team's store: its login, and what hi read from
// connected stores.
func (v *vaultStore) putLocal(path string, data vaultData, by string) error {
	unlock, err := vaultLock()
	if err != nil {
		return err
	}
	defer unlock()
	return v.addVersionLocal(path, data, by, 0)
}

// put writes a new version of path, if it changed; an empty one is a
// version too, so removing every name stays in the history.
func (v *vaultStore) put(path string, data vaultData, by string) error {
	unlock, err := vaultLock()
	if err != nil {
		return err
	}
	defer unlock()
	return v.putLocked(path, data, by)
}

func (v *vaultStore) putLocked(path string, data vaultData, by string) error {
	return v.addVersion(path, data, by, 0)
}

func (v *vaultStore) addVersionLocal(path string, data vaultData, by string, rollback int) error {
	if !validVaultPath(path) {
		return fmt.Errorf("%q isn't a vault path", path)
	}
	file, err := v.loadLocal(path)
	if err != nil {
		return err
	}
	var before vaultData
	if n := len(file.Versions); n > 0 {
		if before, err = v.decryptLocal(path, file.Versions[n-1]); err != nil && file.Versions[n-1].Forgotten == nil {
			return err
		}
	}
	changed := changedNames(before, data)
	if len(file.Versions) > 0 && len(changed) == 0 && file.Removed == nil && rollback == 0 {
		return nil
	}
	if len(file.Versions) == 0 && data.empty() {
		return nil
	}
	file.Removed = nil
	number := 1
	if n := len(file.Versions); n > 0 {
		number = file.Versions[n-1].Version + 1
	}
	plain, err := json.Marshal(data)
	if err != nil {
		return err
	}
	sealed, err := v.sealVersion(path, number, plain)
	if err != nil {
		return err
	}
	file.Versions = append(file.Versions, vaultVersion{Version: number, Time: computeNow().UTC(), By: by, Changed: changed, Rollback: rollback, Sealed: sealed})
	return v.save(file)
}

// rollback puts version back as a new version.
func (v *vaultStore) rollbackLocal(path string, version int, by string) error {
	unlock, err := vaultLock()
	if err != nil {
		return err
	}
	defer unlock()
	file, err := v.loadLocal(path)
	if err != nil {
		return err
	}
	for _, each := range file.Versions {
		if each.Version != version {
			continue
		}
		data, err := v.decryptLocal(path, each)
		if err != nil {
			return err
		}
		return v.addVersionLocal(path, data, by, version)
	}
	return fmt.Errorf("%s has no version %d (hi server vault history %s)", path, version, path)
}

// forget destroys one version's value; its entry stays. The current
// version can't be forgotten: the secret would be gone with no value.
func (v *vaultStore) forgetLocal(path string, version int) error {
	unlock, err := vaultLock()
	if err != nil {
		return err
	}
	defer unlock()
	file, err := v.loadLocal(path)
	if err != nil {
		return err
	}
	for i := range file.Versions {
		if file.Versions[i].Version != version {
			continue
		}
		if i == len(file.Versions)-1 {
			return fmt.Errorf("version %d is %s's current one; set a new value first, then forget this one", version, path)
		}
		if file.Versions[i].Forgotten != nil {
			return nil
		}
		now := computeNow().UTC()
		file.Versions[i].Forgotten = &now
		file.Versions[i].Sealed = nil
		return v.save(file)
	}
	return fmt.Errorf("%s has no version %d (hi server vault history %s)", path, version, path)
}

// paths lists every path under prefix, removed apps' too.
func (v *vaultStore) pathsLocal(prefix string) ([]vaultPathFile, error) {
	entries, err := os.ReadDir(filepath.Join(v.dir, "paths"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var files []vaultPathFile
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok {
			continue
		}
		path := strings.ReplaceAll(name, "+", "/")
		if prefix != "" && path != prefix && !strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")+"/") {
			continue
		}
		file, err := v.loadLocal(path)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// markRemoved keeps a removed app's paths, read by nothing.
func (v *vaultStore) markRemoved(prefix string) (int, error) {
	unlock, err := vaultLock()
	if err != nil {
		return 0, err
	}
	defer unlock()
	files, err := v.pathsLocal(prefix)
	if err != nil {
		return 0, err
	}
	n := 0
	now := computeNow().UTC()
	for _, file := range files {
		if file.Removed != nil {
			continue
		}
		file.Removed = &now
		if err := v.save(file); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// revive gives a removed app's paths back to it.
func (v *vaultStore) revive(prefix string) (int, error) {
	unlock, err := vaultLock()
	if err != nil {
		return 0, err
	}
	defer unlock()
	files, err := v.pathsLocal(prefix)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, file := range files {
		if file.Removed == nil {
			continue
		}
		file.Removed = nil
		if err := v.save(file); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// purge deletes every path under prefix, versions and all.
func (v *vaultStore) purgeLocal(prefix string) (int, error) {
	unlock, err := vaultLock()
	if err != nil {
		return 0, err
	}
	defer unlock()
	files, err := v.pathsLocal(prefix)
	if err != nil {
		return 0, err
	}
	for _, file := range files {
		if err := os.Remove(vaultPathFileName(v.dir, file.Path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return 0, err
		}
	}
	return len(files), nil
}

// vaultAudit puts a line in hi's audit log, the server's where this
// machine has one, else the vault's own; never a value.
func vaultAudit(actor, action, subject, detail string) {
	path := filepath.Join(vaultDir(), "audit.jsonl")
	if dir, err := orgDirectory(""); err == nil && fileExists(filepath.Join(dir, "state.json")) {
		path = filepath.Join(dir, "audit.jsonl")
	}
	entry := auditEntry{Time: computeNow().UTC(), Actor: actor, Action: action, Subject: subject, Detail: detail}
	data, _ := json.Marshal(entry)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		file.Write(append(data, '\n'))
		file.Close()
	}
}
