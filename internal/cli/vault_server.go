package cli

// The server holds the vault's data key (hi_server_vault.md, release 2):
// opened at its start with a slot that opens by itself, or the key
// systemd kept across a restart (vault_systemd.go), or by an admin with
// the passphrase or the recovery key. Every other process of the hi
// account asks it on the admin socket to open or seal one version; the
// files, their versions, and the lock stay each process's own work. Each
// first read of a version by a reader is a line in the audit log.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

// vaultServing is the server's hold, in the server's process only.
var vaultServing *vaultHold

// vaultHold is the data key the server holds, and why it is locked when
// it holds none.
type vaultHold struct {
	mu       sync.Mutex
	dataKey  []byte
	why      string    // why it is locked
	how      string    // how it opened: tpm, file, systemd, passphrase, recovery, init
	failures int       // wrong passphrases or recovery keys in a row
	next     time.Time // no try before, after five wrong ones
	read     map[string]bool
	onOpen   []func()
}

func newVaultHold() *vaultHold {
	return &vaultHold{why: "nothing opened it yet", read: map[string]bool{}}
}

func (h *vaultHold) lockedWhy() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.why
}

// start opens the store as the server starts: with the key systemd kept
// across the restart, else a slot that opens by itself.
func (h *vaultHold) start(log io.Writer) {
	if !vaultExists() {
		return
	}
	keys, err := loadVaultKeys()
	if err != nil {
		h.setWhy(err.Error())
		return
	}
	if key := vaultSystemdTake(); key != nil && bytes.Equal(vaultKeyCheck(key), keys.Check) {
		h.opened(key, "systemd")
		return
	}
	key, kind, err := vaultOpenSlot()
	if err != nil {
		var locked vaultLockedError
		if errors.As(err, &locked) {
			h.setWhy(locked.why)
		} else {
			h.setWhy(err.Error())
		}
		fmt.Fprintf(log, "hi server: the vault is locked: every app waits until an admin unlocks it on the admin page or with hi server vault unlock (%s)\n", h.lockedWhy())
		return
	}
	h.opened(key, kind)
	vaultMoveToTPM(key, log)
}

// vaultMoveToTPM moves a store that opens with a key file to the TPM,
// once hi can use it (libtss2-rc0t64 installed since), and removes the
// key file: a copied disk opens it no more. It says so.
func vaultMoveToTPM(dataKey []byte, log io.Writer) {
	keys, err := loadVaultKeys()
	if err != nil {
		return
	}
	if _, has := keys.slot(vaultSlotTPM); has {
		return
	}
	if _, has := keys.slot(vaultSlotFile); !has {
		return
	}
	if ok, _ := vaultTPMReady(); !ok {
		return
	}
	said, err := vaultChangeSlot(dataKey, "add", vaultSlotTPM, "", false, "hi")
	if err != nil {
		fmt.Fprintf(log, "hi server: the vault stays with its key file; the TPM didn't take it: %v\n", err)
		return
	}
	if _, err := vaultChangeSlot(dataKey, "remove", vaultSlotFile, "", true, "hi"); err != nil {
		fmt.Fprintf(log, "hi server: the vault opens with the TPM now, and its key file still: %v\n", err)
		return
	}
	_ = said
	fmt.Fprintln(log, "hi server: this machine's TPM keeps the vault now, and its key file is gone: a copied disk no longer opens it.")
}

func (h *vaultHold) setWhy(why string) {
	h.mu.Lock()
	h.why = why
	h.mu.Unlock()
}

// opened holds key: in this process, and in systemd's keeping for the
// next start. What waited for it runs.
func (h *vaultHold) opened(key []byte, how string) {
	h.mu.Lock()
	h.dataKey = key
	h.why, h.how, h.failures = "", how, 0
	waiting := h.onOpen
	h.onOpen = nil
	h.mu.Unlock()
	vaultSystemdKeep(key)
	for _, run := range waiting {
		go run()
	}
}

// whenOpen runs f now if the store is open, else once it opens.
func (h *vaultHold) whenOpen(f func()) {
	if h.key() != nil {
		f()
		return
	}
	h.mu.Lock()
	h.onOpen = append(h.onOpen, f)
	h.mu.Unlock()
}

// key is the data key held, or nil.
func (h *vaultHold) key() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.dataKey
}

// lock forgets the key, here and in systemd's keeping.
func (h *vaultHold) lock(who string) {
	h.mu.Lock()
	h.dataKey = nil
	h.why = "locked by " + who
	h.mu.Unlock()
	vaultForget()
	vaultSystemdDrop()
	vaultAudit(who, "vault.lock", "vault", "")
}

// unlock opens the store with a passphrase or the recovery key. After
// five wrong ones in a row, one try a minute.
func (h *vaultHold) unlock(secret, who string) (string, error) {
	h.mu.Lock()
	if wait := time.Until(h.next); h.failures >= 5 && wait > 0 {
		h.mu.Unlock()
		return "", fmt.Errorf("five wrong tries: the next one in %d seconds", int(wait.Seconds())+1)
	}
	h.mu.Unlock()
	unlock, err := vaultLock()
	if err != nil {
		return "", err
	}
	defer unlock()
	keys, err := loadVaultKeys()
	if err != nil {
		return "", err
	}
	key, kind, err := vaultUnwrap(keys, secret)
	if err != nil {
		h.mu.Lock()
		h.failures++
		if h.failures >= 5 {
			h.next = time.Now().Add(time.Minute)
		}
		h.mu.Unlock()
		vaultAudit(who, "vault.unlock", "vault", "refused: "+err.Error())
		return "", err
	}
	said := "with the " + map[string]string{vaultSlotRecovery: "recovery key", vaultSlotPassphrase: "passphrase"}[kind]
	if resealed, err := vaultResealHere(&keys, key); err != nil {
		return "", err
	} else if resealed != "" {
		if err := saveVaultKeys(keys); err != nil {
			return "", err
		}
		said += "; it opens by itself again with " + resealed
	}
	h.opened(key, kind)
	vaultAudit(who, "vault.unlock", "vault", said)
	return said, nil
}

// readOnce says whether this reader's read of this version is the first
// since the server started: only that one goes in the audit log, so an
// app's minute-by-minute check doesn't flood it.
func (h *vaultHold) readOnce(who, path string, version int) bool {
	key := fmt.Sprintf("%s\x00%s\x00%d", who, path, version)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.read[key] {
		return false
	}
	h.read[key] = true
	return true
}

// ---------------------------------------------------------------------------
// the socket's routes

type vaultStatusReply struct {
	Exists bool     `json:"exists"`
	Open   bool     `json:"open"`
	Why    string   `json:"why,omitempty"`
	How    string   `json:"how,omitempty"`
	Kinds  []string `json:"kinds,omitempty"`
}

type vaultVersionRequest struct {
	Path    string `json:"path"`
	Version int    `json:"version"`
	Data    []byte `json:"data"`
}

type vaultSecretRequest struct {
	Secret string `json:"secret,omitempty"`
	Action string `json:"action,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Yes    bool   `json:"yes,omitempty"`
}

type vaultSaidReply struct {
	Said string `json:"said"`
}

// vaultWho is who asks, from the caller's header, for the audit log.
func vaultWho(r *http.Request) string {
	return firstNonEmpty(strings.TrimSpace(r.Header.Get("X-Hi-Who")), "hi")
}

// vaultRoutes are the admin socket's vault routes. A server that holds no
// key of its own (one run with --dir, or a test's) answers from a store
// opened here with a slot that opens by itself.
func (s *orgServer) vaultRoutes(mux *http.ServeMux) {
	hold := func() *vaultHold {
		if s.vault != nil {
			return s.vault
		}
		return vaultServing
	}
	mux.HandleFunc("GET /admin/vault", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, vaultServerStatus(hold()))
	})
	mux.HandleFunc("POST /admin/vault/open", func(w http.ResponseWriter, r *http.Request) {
		var request vaultVersionRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "bad request")
			return
		}
		key, err := vaultServerKey(hold())
		if err != nil {
			vaultWriteError(w, err)
			return
		}
		plain, err := vaultOpenSealed(key, request.Data, vaultVersionAD(request.Path, request.Version))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, fmt.Sprintf("version %d of %s doesn't decrypt", request.Version, request.Path))
			return
		}
		who := vaultWho(r)
		if h := hold(); h == nil || h.readOnce(who, request.Path, request.Version) {
			var data vaultData
			json.Unmarshal(plain, &data)
			vaultAudit(who, "vault.read", request.Path, fmt.Sprintf("version %d: %s", request.Version, strings.Join(data.names(), " ")))
		}
		writeJSON(w, http.StatusOK, vaultVersionRequest{Path: request.Path, Version: request.Version, Data: plain})
	})
	mux.HandleFunc("POST /admin/vault/seal", func(w http.ResponseWriter, r *http.Request) {
		var request vaultVersionRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "bad request")
			return
		}
		key, err := vaultServerKey(hold())
		if err != nil {
			vaultWriteError(w, err)
			return
		}
		sealed, err := vaultSeal(key, request.Data, vaultVersionAD(request.Path, request.Version))
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, vaultVersionRequest{Path: request.Path, Version: request.Version, Data: sealed})
	})
	mux.HandleFunc("POST /admin/vault/unlock", func(w http.ResponseWriter, r *http.Request) {
		var request vaultSecretRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "bad request")
			return
		}
		h := hold()
		if h == nil {
			writeAPIError(w, http.StatusBadRequest, "this server doesn't hold the machine's vault")
			return
		}
		if h.key() != nil {
			writeJSON(w, http.StatusOK, vaultSaidReply{Said: "open already"})
			return
		}
		said, err := h.unlock(request.Secret, vaultWho(r))
		if err != nil {
			writeAPIError(w, http.StatusForbidden, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, vaultSaidReply{Said: said})
	})
	mux.HandleFunc("POST /admin/vault/lock", func(w http.ResponseWriter, r *http.Request) {
		h := hold()
		if h == nil {
			writeAPIError(w, http.StatusBadRequest, "this server doesn't hold the machine's vault")
			return
		}
		h.lock(vaultWho(r))
		writeJSON(w, http.StatusOK, vaultSaidReply{Said: "locked"})
	})
	mux.HandleFunc("POST /admin/vault/slot", func(w http.ResponseWriter, r *http.Request) {
		var request vaultSecretRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "bad request")
			return
		}
		key, err := vaultServerKey(hold())
		if err != nil {
			vaultWriteError(w, err)
			return
		}
		said, err := vaultChangeSlot(key, request.Action, request.Kind, request.Secret, request.Yes, vaultWho(r))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, vaultSaidReply{Said: said})
	})
	mux.HandleFunc("POST /admin/vault/recovery", func(w http.ResponseWriter, r *http.Request) {
		key, err := vaultServerKey(hold())
		if err != nil {
			vaultWriteError(w, err)
			return
		}
		if err := vaultMakeRecovery(key, vaultWho(r)); err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, vaultSaidReply{Said: "a new recovery key waits for the owner"})
	})
}

// vaultServerStatus is the store as the server holds it.
func vaultServerStatus(h *vaultHold) vaultStatusReply {
	reply := vaultStatusReply{Exists: vaultExists()}
	if !reply.Exists {
		return reply
	}
	if keys, err := loadVaultKeys(); err == nil {
		reply.Kinds = keys.kinds()
	}
	if _, err := vaultServerKey(h); err != nil {
		var locked vaultLockedError
		reply.Why = err.Error()
		if errors.As(err, &locked) {
			reply.Why = locked.why
		}
		return reply
	}
	reply.Open = true
	if h != nil {
		h.mu.Lock()
		reply.How = h.how
		h.mu.Unlock()
	}
	return reply
}

// vaultServerKey is the data key the server answers with.
func vaultServerKey(h *vaultHold) ([]byte, error) {
	if h != nil {
		if key := h.key(); key != nil {
			return key, nil
		}
		return nil, vaultLockedError{h.lockedWhy()}
	}
	if !vaultExists() {
		return nil, errors.New("this machine has no vault")
	}
	return vaultOpenBySlots()
}

func vaultWriteError(w http.ResponseWriter, err error) {
	var locked vaultLockedError
	if errors.As(err, &locked) {
		writeJSON(w, http.StatusLocked, map[string]any{"error": locked.why, "locked": true})
		return
	}
	writeAPIError(w, http.StatusInternalServerError, err.Error())
}

// ---------------------------------------------------------------------------
// the other processes' side

// vaultServerSocket is the running server's admin socket, "" without one.
var vaultServerSocket = func() string {
	dir, err := orgDirectory("")
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "admin.sock")
}

// errVaultNoServer is no server to ask: none runs, or an older hi's,
// which holds no key.
var errVaultNoServer = errors.New("no server holds the vault")

// vaultServerCall asks the running server; errVaultNoServer when there is
// none to ask.
func vaultServerCall(method, path string, body, result any) error {
	return vaultServerCallAs(vaultReader(), method, path, body, result)
}

// vaultServerCallAs is vaultServerCall for who, as the audit log names
// them.
func vaultServerCallAs(who, method, path string, body, result any) error {
	socket := vaultServerSocket()
	if socket == "" {
		return errVaultNoServer
	}
	if _, err := os.Stat(socket); err != nil {
		return errVaultNoServer
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socket)
		}},
	}
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, "http://hi-server"+path, payload)
	if err != nil {
		return err
	}
	request.Header.Set("X-Hi-Who", who)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return errVaultNoServer
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if response.StatusCode == http.StatusNotFound {
		return errVaultNoServer
	}
	if response.StatusCode != http.StatusOK {
		var failed struct {
			Error  string `json:"error"`
			Locked bool   `json:"locked"`
		}
		json.Unmarshal(data, &failed)
		if failed.Locked {
			return vaultLockedError{failed.Error}
		}
		return errors.New(firstNonEmpty(failed.Error, response.Status))
	}
	if result != nil {
		return json.Unmarshal(data, result)
	}
	return nil
}

// vaultReader names this process for the audit log: who, and the command.
func vaultReader() string {
	command := ""
	if len(os.Args) > 1 {
		args := os.Args[1:]
		if len(args) > 3 {
			args = args[:3]
		}
		command = " (hi " + strings.Join(args, " ") + ")"
	}
	return vaultActor() + command
}

// vaultAskServer is the server's answer about the store; reached is false
// when no server holds it, and the caller opens it itself.
func vaultAskServer() (vaultStatusReply, bool) {
	var status vaultStatusReply
	if err := vaultServerCall("GET", "/admin/vault", nil, &status); err != nil || !status.Exists {
		return status, false
	}
	return status, true
}

// vaultServerRuns is whether a server that holds the store runs.
func vaultServerRuns() bool {
	_, reached := vaultAskServer()
	return reached
}

// vaultRemoteOpen and vaultRemoteSeal ask the server; when it has gone
// (a restart), they wait for it a few seconds, then open the store here
// with a slot that opens by itself.
func vaultRemoteOpen(path string, version int, sealed []byte) ([]byte, error) {
	var reply vaultVersionRequest
	err := vaultRemoteRetry("/admin/vault/open", vaultVersionRequest{Path: path, Version: version, Data: sealed}, &reply)
	if errors.Is(err, errVaultNoServer) {
		key, err := vaultOpenBySlots()
		if err != nil {
			return nil, err
		}
		return vaultOpenSealed(key, sealed, vaultVersionAD(path, version))
	}
	return reply.Data, err
}

func vaultRemoteSeal(path string, version int, plain []byte) ([]byte, error) {
	var reply vaultVersionRequest
	err := vaultRemoteRetry("/admin/vault/seal", vaultVersionRequest{Path: path, Version: version, Data: plain}, &reply)
	if errors.Is(err, errVaultNoServer) {
		key, err := vaultOpenBySlots()
		if err != nil {
			return nil, err
		}
		return vaultSeal(key, plain, vaultVersionAD(path, version))
	}
	return reply.Data, err
}

// vaultRemoteWait is how long a reader waits for a restarting server.
var vaultRemoteWait = 10 * time.Second

func vaultRemoteRetry(path string, body, result any) error {
	deadline := time.Now().Add(vaultRemoteWait)
	for {
		err := vaultServerCall("POST", path, body, result)
		if !errors.Is(err, errVaultNoServer) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// what needs the data key: slots and the recovery key, in the server or
// where no server runs

// vaultChangeSlot adds or removes a slot. Removing refuses the last way
// the store opens besides the recovery key, and asks (yes) before the
// last that opens by itself: after a reboot every app would wait.
func vaultChangeSlot(dataKey []byte, action, kind, secret string, yes bool, who string) (string, error) {
	if kind != vaultSlotTPM && kind != vaultSlotFile && kind != vaultSlotPassphrase {
		return "", errors.New("a slot is tpm, file, or passphrase")
	}
	unlock, err := vaultLock()
	if err != nil {
		return "", err
	}
	defer unlock()
	keys, err := loadVaultKeys()
	if err != nil {
		return "", err
	}
	switch action {
	case "add":
		slot, err := vaultWrapSlot(kind, dataKey, []byte(secret))
		if err != nil {
			return "", err
		}
		keys.setSlot(slot)
		if err := saveVaultKeys(keys); err != nil {
			return "", err
		}
		vaultAudit(who, "vault.slot-add", "vault", kind)
		return fmt.Sprintf("The vault opens with %s too.\n%s", vaultSlotWords(kind), vaultProtects(keys.kinds())), nil
	case "remove":
		if _, ok := keys.slot(kind); !ok {
			return "", fmt.Errorf("the vault has no %s slot", kind)
		}
		left := slices.DeleteFunc(keys.kinds(), func(k string) bool { return k == kind || k == vaultSlotRecovery })
		if len(left) == 0 {
			return "", fmt.Errorf("%s is the only way the vault opens besides the recovery key; add another first (hi server vault unlock-with add tpm|file|passphrase)", kind)
		}
		byItself := slices.ContainsFunc(left, func(k string) bool { return k == vaultSlotTPM || k == vaultSlotFile })
		if !byItself && kind != vaultSlotPassphrase && !yes {
			return "", errVaultAskFirst
		}
		keys.Slots = slices.DeleteFunc(keys.Slots, func(s vaultSlot) bool { return s.Kind == kind })
		if err := saveVaultKeys(keys); err != nil {
			return "", err
		}
		if kind == vaultSlotFile {
			os.Remove(vaultKeyFile())
			os.Remove(vaultKeyDir())
		}
		vaultAudit(who, "vault.slot-remove", "vault", kind)
		return fmt.Sprintf("The vault no longer opens with %s.\n%s", vaultSlotWords(kind), vaultProtects(keys.kinds())), nil
	}
	return "", errors.New("add or remove")
}

// errVaultAskFirst is removing the last slot that opens by itself, not
// yet confirmed.
var errVaultAskFirst = errors.New(vaultNoAutoWarning + " Say yes to go on (--yes)")

const vaultNoAutoWarning = "After a reboot, every app waits until someone unlocks the vault. TPM and a passphrase keep them running and still protect a copied disk."

// vaultMakeRecovery makes a new recovery key, which waits in the store
// for the owner; the old words stop working.
func vaultMakeRecovery(dataKey []byte, who string) error {
	locked, err := vaultLock()
	if err != nil {
		return err
	}
	once := sync.Once{}
	unlock := func() { once.Do(locked) }
	defer unlock()
	keys, err := loadVaultKeys()
	if err != nil {
		return err
	}
	words, err := vaultNewRecovery(&keys, dataKey)
	if err != nil {
		return err
	}
	store := &vaultStore{dir: vaultDir(), key: dataKey}
	if err := store.putLocked(vaultRecoveryPath, vaultData{Values: map[string]string{vaultRecoveryName: words}}, who); err != nil {
		return err
	}
	if err := saveVaultKeys(keys); err != nil {
		return err
	}
	vaultAudit(who, "vault.recovery-new", "vault", "the old recovery key stopped working")
	unlock()
	// Each app's key beside its offsite copy, sealed to the new one.
	vaultResealOffsite(os.Stderr)
	return nil
}

// vaultLocalKey is the data key where no server holds it: opened here.
func vaultLocalKey() ([]byte, error) {
	store, err := openVault()
	if err != nil {
		return nil, err
	}
	if store.remote {
		return nil, errors.New("the server holds the vault")
	}
	return store.key, nil
}

// ---------------------------------------------------------------------------
// the owner's reminder of an unsaved recovery key

// vaultRemindFile is when the owner was last reminded.
func vaultRemindFile() string { return filepath.Join(vaultDir(), "reminded.json") }

// vaultRemindEvery is how often; the first a day after the key was made.
var vaultRemindEvery = 7 * 24 * time.Hour

// vaultRemindDue says whether the owner is due a reminder now: the
// recovery key waits, a day after it was made, a week after the last.
func vaultRemindDue(now time.Time) bool {
	if !vaultExists() || !vaultRecoveryWaitingHere() {
		return false
	}
	var last struct {
		At time.Time `json:"at"`
	}
	if data, err := os.ReadFile(vaultRemindFile()); err == nil {
		json.Unmarshal(data, &last)
	}
	if !last.At.IsZero() {
		return now.Sub(last.At) >= vaultRemindEvery
	}
	info, err := os.Stat(vaultPathFileName(vaultDir(), vaultRecoveryPath))
	return err == nil && now.Sub(info.ModTime()) >= 24*time.Hour
}

func vaultReminded(now time.Time) {
	data, _ := json.Marshal(map[string]time.Time{"at": now.UTC()})
	writeFileAtomic(vaultRemindFile(), data, 0o600)
}

// remindRecovery sends the server's owner a Slack DM, once a week, while
// the vault's recovery key waits for them to save it.
func (s *orgServer) remindRecovery() {
	now := computeNow()
	if s.slack == nil || !vaultRemindDue(now) {
		return
	}
	owner := loadOrgPeople(s.dir).Owner
	if owner == nil || owner.Slack == "" {
		return
	}
	text := "The hi server's vault on " + adminMachineName() + " has a recovery key waiting for you to save it: it opens every secret there when nothing else does, after a move, a restore, or a lost disk. Save it on the admin page (Keys, The server's vault) or with hi server vault recovery on a terminal on the machine."
	if _, _, err := s.slack.api.PostMessage(owner.Slack, slack.MsgOptionText(slackEscape(text), false)); err != nil {
		fmt.Fprintf(s.log, "slack: the recovery key's reminder: %v\n", err)
		return
	}
	vaultReminded(now)
}
