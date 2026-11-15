package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// vaultTestServer serves the vault's socket routes for hold, as the server
// does, and points this process's readers at it.
func vaultTestServer(t *testing.T, hold *vaultHold) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "admin.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&orgServer{vault: hold}).vaultRoutes(mux)
	server := &http.Server{Handler: mux}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	saved, savedWait := vaultServerSocket, vaultRemoteWait
	t.Cleanup(func() { vaultServerSocket, vaultRemoteWait = saved, savedWait })
	vaultServerSocket = func() string { return socket }
	vaultRemoteWait = 0
	return socket
}

func vaultAuditText(t *testing.T) string {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(vaultDir(), "audit.jsonl"))
	return string(data)
}

// Other processes read and write through the server, which holds the key;
// each first read of a version by a reader is in the audit log, once.
func TestVaultThroughTheServer(t *testing.T) {
	vaultTest(t)
	if _, err := initVault(vaultSlotFile); err != nil {
		t.Fatal(err)
	}
	hold := newVaultHold()
	hold.start(&bytes.Buffer{})
	if hold.key() == nil {
		t.Fatal("the server didn't open it with the file slot")
	}
	vaultTestServer(t, hold)
	vaultForget()
	store, err := openVault()
	if err != nil || !store.remote || store.key != nil {
		t.Fatalf("%+v %v", store, err)
	}
	if err := store.put("hi/app/payments/shared", vaultData{Values: map[string]string{"DB_URL": "postgres://x"}}, "test"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if data, version, ok, err := store.get("hi/app/payments/shared"); err != nil || !ok || version != 1 || data.Values["DB_URL"] != "postgres://x" {
			t.Fatal(data, version, ok, err)
		}
	}
	audit := vaultAuditText(t)
	if strings.Count(audit, `"vault.read"`) != 1 || !strings.Contains(audit, "version 1: DB_URL") || strings.Contains(audit, "postgres://x") {
		t.Fatal(audit)
	}
	// Locked: a reader is refused, and doesn't open the store itself with
	// the key file, as it does where no server runs.
	hold.lock("ana")
	vaultForget()
	if _, err := openVault(); !errors.As(err, new(vaultLockedError)) || !strings.Contains(err.Error(), "locked by ana") {
		t.Fatal(err)
	}
	if !strings.Contains(vaultLockedNote(), "locked") {
		t.Fatal(vaultLockedNote())
	}
	// No server: the reader opens it itself.
	vaultServerSocket = func() string { return "" }
	vaultForget()
	if store, err := openVault(); err != nil || store.remote {
		t.Fatal(store, err)
	}
}

// A passphrase: added while open, the last slot that opens by itself
// removed only when said yes; then a start is locked until an admin
// unlocks it; five wrong tries slow it; what waited runs.
func TestVaultPassphrase(t *testing.T) {
	vaultTest(t)
	initVault(vaultSlotFile)
	key, _ := vaultOpenBySlots()
	if _, err := vaultChangeSlot(key, "add", vaultSlotPassphrase, "short", false, "ana"); err == nil {
		t.Fatal("a short passphrase was taken")
	}
	said, err := vaultChangeSlot(key, "add", vaultSlotPassphrase, "correct horse battery", false, "ana")
	if err != nil || !strings.Contains(said, "a passphrase too") {
		t.Fatal(said, err)
	}
	keys, _ := loadVaultKeys()
	if strings.Join(keys.kinds(), ",") != "file,passphrase,recovery" || !strings.Contains(strings.Join(vaultAdvice(keys), " "), "undoes the passphrase's") {
		t.Fatal(keys.kinds(), vaultAdvice(keys))
	}
	if _, err := vaultChangeSlot(key, "remove", vaultSlotFile, "", false, "ana"); !errors.Is(err, errVaultAskFirst) {
		t.Fatal(err)
	}
	if _, err := vaultChangeSlot(key, "remove", vaultSlotFile, "", true, "ana"); err != nil {
		t.Fatal(err)
	}
	if _, err := vaultChangeSlot(key, "remove", vaultSlotPassphrase, "", true, "ana"); err == nil {
		t.Fatal("removed the last way it opens besides the recovery key")
	}
	keys, _ = loadVaultKeys()
	if !strings.Contains(vaultProtects(keys.kinds()), "a stolen machine") {
		t.Fatal(vaultProtects(keys.kinds()))
	}

	hold := newVaultHold()
	var log bytes.Buffer
	hold.start(&log)
	if hold.key() != nil || !strings.Contains(log.String(), "the vault is locked") {
		t.Fatal(log.String())
	}
	ran := make(chan bool, 1)
	hold.whenOpen(func() { ran <- true })
	vaultTestServer(t, hold)
	vaultForget()
	if _, err := openVault(); !errors.As(err, new(vaultLockedError)) {
		t.Fatal(err)
	}
	for range 5 {
		if _, err := hold.unlock("wrong horse battery", "ana"); err == nil || !strings.Contains(err.Error(), "isn't the vault's passphrase") {
			t.Fatal(err)
		}
	}
	if _, err := hold.unlock("correct horse battery", "ana"); err == nil || !strings.Contains(err.Error(), "five wrong tries") {
		t.Fatal(err)
	}
	hold.mu.Lock()
	hold.next = time.Now()
	hold.mu.Unlock()
	// The command asks the server, which holds what it opens.
	var out bytes.Buffer
	if err := vaultUnlockCommand(nil, strings.NewReader("correct horse battery\n"), &out); err != nil || !strings.Contains(out.String(), "the server holds it") {
		t.Fatal(out.String(), err)
	}
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("what waited for the vault didn't run")
	}
	vaultForget()
	if store, err := openVault(); err != nil || !store.remote {
		t.Fatal(store, err)
	}
	if audit := vaultAuditText(t); strings.Count(audit, "refused") != 5 || strings.Contains(audit, "horse") {
		t.Fatal(audit)
	}
	// Where no server runs, a passphrase opens nothing.
	vaultServerSocket = func() string { return "" }
	vaultForget()
	if _, err := vaultUnlockHere("correct horse battery"); err == nil || !strings.Contains(err.Error(), "start it") {
		t.Fatal(err)
	}
}

// On another machine the TPM's slot doesn't open; the passphrase does,
// and hi seals a new tpm slot in place of the old.
func TestVaultPassphraseOnAnotherMachine(t *testing.T) {
	vaultTest(t)
	present := fakeTPM(t, false)
	initVault("")
	key, _ := vaultOpenBySlots()
	vaultChangeSlot(key, "add", vaultSlotPassphrase, "correct horse battery", false, "ana")
	*present = false
	hold := newVaultHold()
	hold.start(&bytes.Buffer{})
	if hold.key() != nil {
		t.Fatal("opened without the TPM")
	}
	// This machine's TPM: it opens what it seals, not the old one's.
	*present = true
	old, _ := loadVaultKeys()
	oldSlot, _ := old.slot(vaultSlotTPM)
	working, seal := vaultTPMOpen, vaultTPMSeal
	vaultTPMSeal = func(data []byte) ([]byte, error) {
		sealed, err := seal(data)
		return append(sealed, ' '), err
	}
	vaultTPMOpen = func(sealed []byte) ([]byte, error) {
		if bytes.Equal(sealed, oldSlot.Wrapped) {
			return nil, errors.New("another machine's TPM")
		}
		return working(sealed)
	}
	said, err := hold.unlock("correct horse battery", "ana")
	if err != nil || !strings.Contains(said, "opens by itself again with tpm") {
		t.Fatal(said, err)
	}
	keys, _ := loadVaultKeys()
	slot, _ := keys.slot(vaultSlotTPM)
	if strings.Join(keys.kinds(), ",") != "tpm,passphrase,recovery" || bytes.Equal(slot.Wrapped, oldSlot.Wrapped) {
		t.Fatal(keys.kinds())
	}
}

// The key in systemd's keeping: a start that's handed it opens with it;
// one without it opens as before.
func TestVaultSystemdKeeping(t *testing.T) {
	vaultTest(t)
	initVault(vaultSlotFile)
	key, _ := vaultOpenBySlots()
	vaultChangeSlot(key, "add", vaultSlotPassphrase, "correct horse battery", false, "ana")
	vaultChangeSlot(key, "remove", vaultSlotFile, "", true, "ana")
	var kept []byte
	savedKeep, savedTake, savedDrop := vaultSystemdKeep, vaultSystemdTake, vaultSystemdDrop
	t.Cleanup(func() { vaultSystemdKeep, vaultSystemdTake, vaultSystemdDrop = savedKeep, savedTake, savedDrop })
	vaultSystemdKeep = func(key []byte) error { kept = append([]byte{}, key...); return nil }
	vaultSystemdTake = func() []byte { return kept }
	vaultSystemdDrop = func() { kept = nil }

	first := newVaultHold()
	first.start(&bytes.Buffer{})
	if first.key() != nil {
		t.Fatal("a passphrase store opened by itself")
	}
	if _, err := first.unlock("correct horse battery", "ana"); err != nil || kept == nil {
		t.Fatal(err)
	}
	// A restart: handed the key.
	second := newVaultHold()
	second.start(&bytes.Buffer{})
	if second.key() == nil || second.how != "systemd" {
		t.Fatal("the restart didn't get the key")
	}
	// A key that isn't this store's is refused.
	kept = bytes.Repeat([]byte{1}, 32)
	if third := newVaultHold(); func() bool { third.start(&bytes.Buffer{}); return third.key() != nil }() {
		t.Fatal("took a wrong key")
	}
	// Locked: systemd forgets it too, so a reboot or restart stays locked.
	kept = key
	second.lock("ana")
	if kept != nil {
		t.Fatal("systemd still keeps it")
	}

	unit := orgUserUnitText("/usr/local/bin/hi", "/srv/org")
	for _, line := range vaultServerUnitLines {
		if !strings.Contains(unit, line) {
			t.Fatalf("%s:\n%s", line, unit)
		}
	}
	old := "[Unit]\nDescription=hi server\n\n[Service]\nExecStart=/usr/local/bin/hi server run\nRestart=always\n"
	upgraded, changed := vaultUnitWithKeeping(old)
	if !changed || !strings.Contains(upgraded, "[Service]\nNotifyAccess=main\nFileDescriptorStoreMax=1\nFileDescriptorStorePreserve=yes\nExecStart=") {
		t.Fatal(upgraded)
	}
	if _, again := vaultUnitWithKeeping(upgraded); again {
		t.Fatal("added twice")
	}
}

// A server from before release 2 has no vault routes: readers open the
// store themselves, as they did.
func TestVaultOlderServer(t *testing.T) {
	vaultTest(t)
	initVault(vaultSlotFile)
	dir, _ := os.MkdirTemp("", "hv")
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "admin.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.NotFoundHandler()}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	saved := vaultServerSocket
	t.Cleanup(func() { vaultServerSocket = saved })
	vaultServerSocket = func() string { return socket }
	vaultForget()
	if store, err := openVault(); err != nil || store.remote {
		t.Fatal(store, err)
	}
}

// An app whose vault is locked waits, its heartbeat failing with the
// reason, and starts once an admin unlocks it.
func TestAppWaitsForTheVault(t *testing.T) {
	vaultTest(t)
	initVault(vaultSlotFile)
	key, _ := vaultOpenBySlots()
	vaultChangeSlot(key, "add", vaultSlotPassphrase, "correct horse battery", false, "ana")
	vaultChangeSlot(key, "remove", vaultSlotFile, "", true, "ana")
	hold := newVaultHold()
	hold.start(&bytes.Buffer{})
	vaultTestServer(t, hold)
	vaultForget()
	service, hits := heartbeatTestService(t)
	saved := appVaultPoll
	t.Cleanup(func() { appVaultPoll = saved })
	appVaultPoll = 10 * time.Millisecond
	var log syncBuffer
	done := make(chan bool)
	go func() {
		appWaitForVault(appConfig{Name: "payments", HeartbeatURL: service.URL + "/ping/abc"}, &log)
		done <- true
	}()
	deadline := time.Now().Add(5 * time.Second)
	for hits("/ping/abc/fail") == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hits("/ping/abc/fail") != 1 || !strings.Contains(log.String(), "waits for the server's vault, locked") {
		t.Fatal(hits("/ping/abc/fail"), log.String())
	}
	if _, err := hold.unlock("correct horse battery", "ana"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the app still waits")
	}
	if !strings.Contains(log.String(), "vault is open; app payments starts") {
		t.Fatal(log.String())
	}
}

// The page's Vault: what it shows (never a value), Lock and Unlock through
// the server, a passphrase, the recovery key for the owner only, and a
// rollback.
func TestAdminVault(t *testing.T) {
	vaultTest(t)
	initVault(vaultSlotFile)
	hold := newVaultHold()
	hold.start(&bytes.Buffer{})
	vaultTestServer(t, hold)
	vaultForget()
	store, _ := openVault()
	store.put("hi/app/payments/shared", vaultData{Values: map[string]string{"DB_URL": "postgres://one"}}, "ana")
	store.put("hi/app/payments/shared", vaultData{Values: map[string]string{"DB_URL": "postgres://two"}}, "ana")
	owner := adminPerson{Name: "Ana", MachineOwner: true}
	other := adminPerson{Name: "Ben"}

	view := adminVaultViewOf(owner)
	if !view.Open || !view.Recovery || len(view.Paths) != 1 || view.Paths[0].Version != 2 || strings.Join(view.Paths[0].Names, ",") != "DB_URL" {
		t.Fatalf("%+v", view)
	}
	if data := strings.Join(view.Paths[0].Names, ""); strings.Contains(data, "postgres") {
		t.Fatal("a value on the page")
	}
	if brief := adminVaultBriefOf(); brief == nil || brief.Locked || !brief.Recovery {
		t.Fatalf("%+v", brief)
	}

	// The recovery key: the owner only, shown once, two words back.
	if _, _, err := adminVaultChange(other, "Ben", "recovery-show", "", "", "", 0, "", false, nil); err == nil {
		t.Fatal("an admin who isn't the owner saw the recovery key")
	}
	_, shown, err := adminVaultChange(owner, "Ana", "recovery-show", "", "", "", 0, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	words, ask := shown["words"].([]string), shown["ask"].([]int)
	if _, _, err := adminVaultChange(owner, "Ana", "recovery-saved", "", "", "", 0, "", false, []string{"zoo", "zoo"}); err == nil || !vaultRecoveryWaitingHere() {
		t.Fatal("wrong words were taken", err)
	}
	if _, _, err := adminVaultChange(owner, "Ana", "recovery-saved", "", "", "", 0, "", false, []string{words[ask[0]-1], words[ask[1]-1]}); err != nil || vaultRecoveryWaitingHere() {
		t.Fatal(err)
	}

	// A passphrase, then the file slot removed; Lock and Unlock.
	if _, _, err := adminVaultChange(owner, "Ana", "add", "correct horse battery", vaultSlotPassphrase, "", 0, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := adminVaultChange(owner, "Ana", "remove", "", vaultSlotFile, "", 0, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := adminVaultChange(owner, "Ana", "lock", "", "", "", 0, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if brief := adminVaultBriefOf(); brief == nil || !brief.Locked || !strings.Contains(brief.Why, "locked by Ana") {
		t.Fatalf("%+v", brief)
	}
	if view := adminVaultViewOf(owner); view.Open || len(view.Paths) != 0 {
		t.Fatalf("%+v", view)
	}
	if _, _, err := adminVaultChange(other, "Ben", "unlock", "correct horse battery", "", "", 0, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// A rollback puts version 1 back, as version 3.
	if _, _, err := adminVaultChange(owner, "Ana", "rollback", "", "", "hi/app/payments/shared", 1, "", false, nil); err != nil {
		t.Fatal(err)
	}
	vaultForget()
	store, _ = openVault()
	if data, version, _, _ := store.get("hi/app/payments/shared"); version != 3 || data.Values["DB_URL"] != "postgres://one" {
		t.Fatal(version, data)
	}
	if audit := vaultAuditText(t); strings.Contains(audit, "postgres") || strings.Contains(audit, "horse") || strings.Contains(audit, words[0]+" "+words[1]) {
		t.Fatal("a value in the audit log")
	}
}

// An older hi's resolver reads no secret file, so it doesn't keep the
// files on disk through an update; any other older hi does.
func TestVaultResolverKeepsNoFiles(t *testing.T) {
	dir := t.TempDir()
	for cmdline, none := range map[string]bool{
		"/home/hi/.local/bin/hi\x00app\x00__resolver\x00payments\x00":  true,
		"/home/hi/.local/bin/hi\x00team\x00__resolver\x00payments\x00": true,
		"/home/hi/.local/bin/hi\x00app\x00__serve\x00payments\x00":     false,
		"/home/hi/.local/bin/hi\x00server\x00run\x00":                  false,
		"/home/hi/.local/bin/hi\x00update\x00--restart\x00":            true,
	} {
		path := filepath.Join(dir, "cmdline")
		os.WriteFile(path, []byte(cmdline), 0o600)
		if vaultReadsNoSecret(path) != none {
			t.Errorf("%q", cmdline)
		}
	}
}

// While the server doesn't answer, the app's heartbeat pings with a note
// of since when; after a day its ops channel hears it once, and again
// when the server is back.
func TestAppServerGone(t *testing.T) {
	setupTeamTest(t)
	os.MkdirAll(filepath.Dir(orgUserUnitPath()), 0o755)
	os.WriteFile(orgUserUnitPath(), []byte("[Service]\n"), 0o644)
	reachable := true
	var said []string
	savedReach, savedSay, savedAfter := appServerReachable, appOpsSay, appServerGoneAfter
	t.Cleanup(func() { appServerReachable, appOpsSay, appServerGoneAfter = savedReach, savedSay, savedAfter })
	appServerReachable = func() bool { return reachable }
	appOpsSay = func(config appConfig, text string) { said = append(said, text) }
	appServerGoneAfter = 50 * time.Millisecond
	config := appConfig{Name: "payments"}
	if note := appServerGoneCheck(config); note != "" {
		t.Fatal(note)
	}
	reachable = false
	if note := appServerGoneCheck(config); !strings.HasPrefix(note, "the server isn't reachable since ") || len(said) != 0 {
		t.Fatal(note, said)
	}
	time.Sleep(60 * time.Millisecond)
	appServerGoneCheck(config)
	appServerGoneCheck(config)
	if len(said) != 1 || !strings.Contains(said[0], "hasn't reached the hi server since") {
		t.Fatal(said)
	}
	reachable = true
	if note := appServerGoneCheck(config); note != "" || len(said) != 2 || !strings.Contains(said[1], "reaches the hi server again, after") {
		t.Fatal(note, said)
	}
	// A short absence isn't told, gone or back.
	reachable = false
	appServerGoneAfter = time.Hour
	appServerGoneCheck(config)
	reachable = true
	appServerGoneCheck(config)
	if len(said) != 2 {
		t.Fatal(said)
	}
}

// While the recovery key waits, the server's owner hears of it on Slack:
// a day after it was made, then once a week, until it's saved.
func TestVaultRecoveryReminder(t *testing.T) {
	ts := newTestServer(t)
	fake := withSlack(t, ts)
	vaultTest(t)
	initVault(vaultSlotFile)
	data, _ := json.Marshal(orgPeople{Owner: &orgPerson{Name: "ana", Slack: "UANA"}})
	os.WriteFile(orgPeoplePath(ts.dir), data, 0o600)
	saved := computeNow
	t.Cleanup(func() { computeNow = saved })
	now := time.Now()
	computeNow = func() time.Time { return now }
	sent := func() int { fake.mu.Lock(); defer fake.mu.Unlock(); return len(fake.posts) }
	before := sent()
	ts.server.remindRecovery()
	if sent() != before {
		t.Fatal("reminded the day it was made")
	}
	now = now.Add(25 * time.Hour)
	ts.server.remindRecovery()
	ts.server.remindRecovery()
	if sent() != before+1 || !strings.Contains(fake.posts[len(fake.posts)-1], "recovery key waiting for you") {
		t.Fatal(fake.posts)
	}
	now = now.Add(8 * 24 * time.Hour)
	ts.server.remindRecovery()
	if sent() != before+2 {
		t.Fatal("no weekly reminder")
	}
	store, _ := openVault()
	store.purge(vaultRecoveryPath)
	now = now.Add(8 * 24 * time.Hour)
	ts.server.remindRecovery()
	if sent() != before+2 {
		t.Fatal("reminded after it was saved")
	}
}

// A store that opens with a key file moves to the TPM at the server's
// start, once hi can use the TPM; without one, it stays.
func TestVaultMovesToTheTPM(t *testing.T) {
	vaultTest(t)
	present := fakeTPM(t, false)
	*present = false
	initVault("")
	keys, _ := loadVaultKeys()
	if strings.Join(keys.kinds(), ",") != "file,recovery" {
		t.Fatal(keys.kinds())
	}
	var log bytes.Buffer
	newVaultHold().start(&log)
	if keys, _ := loadVaultKeys(); strings.Join(keys.kinds(), ",") != "file,recovery" || log.Len() != 0 {
		t.Fatal(keys.kinds(), log.String())
	}
	*present = true
	hold := newVaultHold()
	hold.start(&log)
	keys, _ = loadVaultKeys()
	if strings.Join(keys.kinds(), ",") != "tpm,recovery" || fileExists(vaultKeyFile()) || !strings.Contains(log.String(), "TPM keeps the vault now") {
		t.Fatal(keys.kinds(), log.String())
	}
	vaultForget()
	if _, err := vaultOpenBySlots(); err != nil {
		t.Fatal(err)
	}
}

// Renaming an app's paths seals each version again through the server,
// which holds the key.
func TestVaultRenameThroughTheServer(t *testing.T) {
	vaultTest(t)
	initVault(vaultSlotFile)
	hold := newVaultHold()
	hold.start(&bytes.Buffer{})
	vaultTestServer(t, hold)
	vaultForget()
	store, _ := openVault()
	if !store.remote {
		t.Fatal("not through the server")
	}
	store.put("hi/app/payments/shared", vaultData{Values: map[string]string{"A": "1"}}, "ana")
	store.put("hi/app/payments/shared", vaultData{Values: map[string]string{"A": "2"}}, "ana")
	if err := vaultRenameApp("payments", "billing"); err != nil {
		t.Fatal(err)
	}
	file, _ := store.load("hi/app/billing/shared")
	if len(file.Versions) != 2 {
		t.Fatal(file.Versions)
	}
	if data, err := store.decrypt("hi/app/billing/shared", file.Versions[0]); err != nil || data.Values["A"] != "1" {
		t.Fatal(data, err)
	}
}

// The key goes to systemd as it takes it: a datagram on NOTIFY_SOCKET with
// a memory-only file holding the key.
func TestVaultSystemdNotify(t *testing.T) {
	dir, _ := os.MkdirTemp("", "hn")
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "notify")
	listener, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("NOTIFY_SOCKET", socket)
	key := bytes.Repeat([]byte{7}, 32)
	keep := savedSystemdKeep
	if err := keep(key); err != nil {
		t.Fatal(err)
	}
	var messages []string
	var got []byte
	for range 2 {
		buffer, oob := make([]byte, 256), make([]byte, 256)
		listener.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, oobn, _, _, err := listener.ReadMsgUnix(buffer, oob)
		if err != nil {
			t.Fatal(err)
		}
		messages = append(messages, string(buffer[:n]))
		if oobn > 0 {
			controls, _ := syscall.ParseSocketControlMessage(oob[:oobn])
			fds, _ := syscall.ParseUnixRights(&controls[0])
			data := make([]byte, 64)
			m, _ := syscall.Pread(fds[0], data, 0)
			got = data[:m]
			syscall.Close(fds[0])
		}
	}
	if messages[0] != "FDSTOREREMOVE=1\nFDNAME=hi-vault-key" || messages[1] != "FDSTORE=1\nFDNAME=hi-vault-key" || !bytes.Equal(got, key) {
		t.Fatalf("%q %x", messages, got)
	}
}

// Files stay only for a hi older than the vault, which reads only them.
func TestVaultBinaryKnowsVault(t *testing.T) {
	dir := t.TempDir()
	old, recent := filepath.Join(dir, "old"), filepath.Join(dir, "new")
	os.WriteFile(old, bytes.Repeat([]byte("x"), 3<<20), 0o700)
	padding := bytes.Repeat([]byte("y"), 1<<20-5)
	os.WriteFile(recent, append(append(padding, vaultBinaryMarker...), padding...), 0o700)
	if vaultBinaryKnowsVault(old) || !vaultBinaryKnowsVault(recent) {
		t.Fatal("the marker")
	}
}
