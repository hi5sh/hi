package cli

// hi server vault: the store's state, its slots, the recovery key, and
// each path's versions. No command prints a value.

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"slices"
	"strconv"
	"strings"
)

const vaultUsage = `usage:
  hi server vault                        Locked or open, how it opens, what it protects, counts
  hi server vault init [--unlock tpm|file|passphrase]
                                         Create it (hi update does); the recovery key waits for the owner
  hi server vault unlock                 Open it with the passphrase or the recovery key
  hi server vault lock                   The server forgets the key now; running boxes keep running
  hi server vault unlock-with add|remove tpm|file|passphrase [--yes]
                                         Add or remove a way it opens
  hi server vault recovery [--new]       The owner, on a terminal: save the recovery key; --new makes another
  hi server vault ls [<prefix>]          Paths, versions, when changed, and names; never values
  hi server vault history <path>         A path's versions: who changed which names, when
  hi server vault rollback <path> <version>
                                         Put a version back, as a new version
  hi server vault forget <path> --version <n>
                                         Destroy one version's value (it leaked); its entry stays
  hi server vault shared [set|give|rm]   Keys several apps use, such as one OpenRouter key
  hi server vault connect <prefix>/ vault://<mount>[/<path>] --address https://… [--login jwt|approle]
                                         Read a team's OpenBao or Vault: apps take paths there (hi app env --from)
  hi server vault disconnect <prefix>    Stop reading it; refused while an app takes from it
  hi server vault login [<prefix>] [--new-key]
                                         Whether hi logs in, and what to hand the store's owners`

func vaultCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return vaultStatus(stdout)
	}
	command, rest := args[0], args[1:]
	switch command {
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, vaultUsage)
		return nil
	case "status":
		return vaultStatus(stdout)
	case "init":
		want := ""
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--unlock" && i+1 < len(rest):
				want = rest[i+1]
				i++
			case strings.HasPrefix(rest[i], "--unlock="):
				want = strings.TrimPrefix(rest[i], "--unlock=")
			default:
				return usageError{"usage: hi server vault init [--unlock tpm|file|passphrase]"}
			}
		}
		if vaultExists() {
			return errors.New("this machine has a vault already (hi server vault)")
		}
		if want == "" {
			return ensureVault(stdout)
		}
		var passphrase []string
		if want == vaultSlotPassphrase {
			phrase, err := vaultAskPassphrase(stdin, stdout)
			if err != nil {
				return err
			}
			passphrase = append(passphrase, phrase)
		}
		result, err := initVault(want, passphrase...)
		if err != nil {
			return err
		}
		if want == vaultSlotPassphrase {
			// The server holds what a passphrase opens.
			if err := vaultServerCall("POST", "/admin/vault/unlock", vaultSecretRequest{Secret: passphrase[0]}, nil); err == nil {
				fmt.Fprintln(stdout, "The server's vault is made; it opens with the passphrase, and the server holds it open. After a reboot an admin unlocks it.")
			} else {
				fmt.Fprintln(stdout, "The server's vault is made; it opens with the passphrase. Unlock it once the server runs: hi server vault unlock.")
			}
			fmt.Fprintln(stdout, "Its recovery key waits for the owner: hi server vault recovery.")
			return ensureVault(stdout)
		}
		fmt.Fprintf(stdout, "The server's vault is made; it opens by itself with %s. Its recovery key waits for the owner: hi server vault recovery.\n", vaultSlotWords(result.Slot))
		return ensureVault(stdout)
	case "unlock":
		return vaultUnlockCommand(rest, stdin, stdout)
	case "lock":
		return vaultLockCommand(rest, stdout)
	case "unlock-with":
		return vaultUnlockWithCommand(rest, stdin, stdout)
	case "recovery":
		return vaultRecoveryCommand(rest, stdin, stdout)
	case "ls":
		return vaultListCommand(rest, stdout)
	case "history":
		return vaultHistoryCommand(rest, stdout)
	case "rollback":
		return vaultRollbackCommand(rest, stdout)
	case "forget":
		return vaultForgetCommand(rest, stdout)
	case "shared":
		return vaultSharedCommand(rest, stdin, stdout)
	case "connect":
		return vaultConnectCommand(rest, stdin, stdout)
	case "disconnect":
		return vaultDisconnectCommand(rest, stdout)
	case "login":
		return vaultLoginCommand(rest, stdout)
	case "move":
		return vaultMoveCommand(rest, stdin, stdout)
	}
	return usageError{vaultUsage}
}

func vaultSlotWords(kind string) string {
	switch kind {
	case vaultSlotTPM:
		return "this machine's TPM"
	case vaultSlotFile:
		return "a key file (" + vaultKeyFile() + ")"
	case vaultSlotPassphrase:
		return "a passphrase"
	}
	return kind
}

// vaultProtects says in one line what the slots protect against
// together: the weakest one sets it.
func vaultProtects(kinds []string) string {
	switch {
	case slices.Contains(kinds, vaultSlotFile):
		return "Protects backups and copies of hi's state; a copied disk can open it (file slot)."
	case slices.Contains(kinds, vaultSlotTPM):
		return "Protects backups, copies of hi's state, and a stolen or copied disk; it opens by itself on this machine only."
	case slices.Contains(kinds, vaultSlotPassphrase):
		return "Protects backups, copies of hi's state, a stolen or copied disk, and a stolen machine; after a reboot an admin unlocks it."
	}
	return "Nothing opens it by itself: every app waits until someone unlocks it."
}

// vaultAdvice is what the store's slots should change, if anything.
func vaultAdvice(keys vaultKeys) []string {
	kinds := keys.kinds()
	var advice []string
	if slices.Contains(kinds, vaultSlotFile) && (slices.Contains(kinds, vaultSlotTPM) || slices.Contains(kinds, vaultSlotPassphrase)) {
		stronger := map[bool]string{true: "TPM's", false: "passphrase's"}[slices.Contains(kinds, vaultSlotTPM)]
		advice = append(advice, "The file slot undoes the "+stronger+": a copied disk opens the store. Remove it? hi server vault unlock-with remove file")
	}
	if slices.Contains(kinds, vaultSlotTPM) && !slices.Contains(kinds, vaultSlotPassphrase) && !slices.Contains(kinds, vaultSlotFile) {
		advice = append(advice, "Add a passphrase, so moving the server doesn't need the recovery key: hi server vault unlock-with add passphrase")
	}
	if !slices.Contains(kinds, vaultSlotTPM) {
		if ok, why := vaultTPMReady(); ok {
			advice = append(advice, "This machine's TPM can keep the store: hi server vault unlock-with add tpm, then remove file.")
		} else if strings.Contains(why, "libtss2") {
			advice = append(advice, "This machine has a TPM; "+why+", and hi moves the store to it.")
		} else if strings.Contains(why, "no TPM") && slices.Contains(kinds, vaultSlotFile) {
			advice = append(advice, "No TPM: a copied disk opens this store. Ask your IT team for a virtual TPM, or add a passphrase.")
		}
	}
	return advice
}

func vaultRecoveryWaiting(store *vaultStore) bool {
	_, _, ok, err := store.get(vaultRecoveryPath)
	return err == nil && ok
}

// vaultBrief is the vault's few lines for hi server status: locked or
// not, what it protects, and what nobody would hear.
func vaultBrief(stdout io.Writer) {
	if !vaultExists() {
		fmt.Fprintln(stdout, "Vault: none yet; secrets are files (hi server vault).")
	} else if keys, err := loadVaultKeys(); err != nil {
		fmt.Fprintf(stdout, "Vault: %v\n", err)
	} else {
		store, err := openVault()
		if err != nil {
			fmt.Fprintf(stdout, "Vault: locked. %v\n", err)
		} else {
			fmt.Fprintf(stdout, "Vault: open, with %s. %s\n", strings.Join(keys.kinds(), ", "), vaultProtects(keys.kinds()))
			if vaultRecoveryWaiting(store) {
				fmt.Fprintln(stdout, "  The recovery key waits for the owner: hi server vault recovery, on a terminal here.")
			}
		}
		for _, line := range vaultAdvice(keys) {
			fmt.Fprintln(stdout, "  "+line)
		}
	}
	if _, set := loadMachineHeartbeat(); !set && orgUserUnitHere() {
		fmt.Fprintln(stdout, "  Nobody would hear that the server is locked or down: set a heartbeat (hi heartbeat <url>).")
	}
}

func vaultStatus(stdout io.Writer) error {
	if !vaultExists() {
		fmt.Fprintln(stdout, "This machine has no vault yet: hi keeps its secrets as files, 0600. hi update makes one where a server or apps run; hi server vault init makes it now.")
		return nil
	}
	keys, err := loadVaultKeys()
	if err != nil {
		return err
	}
	store, openErr := openVault()
	switch {
	case openErr != nil:
		fmt.Fprintf(stdout, "The server's vault is locked: %v\n", openErr)
	case store.remote:
		fmt.Fprintln(stdout, "The server's vault is open; the server holds its key.")
	default:
		fmt.Fprintln(stdout, "The server's vault is open.")
	}
	var ways []string
	for _, slot := range keys.Slots {
		ways = append(ways, slot.Kind)
	}
	fmt.Fprintf(stdout, "Opens with: %s\n", strings.Join(ways, ", "))
	if line := vaultMovedLine(); line != "" {
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintln(stdout, vaultProtects(keys.kinds()))
	for _, line := range vaultAdvice(keys) {
		fmt.Fprintln(stdout, line)
	}
	if store == nil {
		return nil
	}
	files, err := store.paths("")
	if err != nil {
		return err
	}
	paths, versions, removed := 0, 0, 0
	for _, file := range files {
		if file.Path == vaultRecoveryPath || strings.HasPrefix(file.Path, "hi/connected/") {
			continue
		}
		paths++
		versions += len(file.Versions)
		if file.Removed != nil {
			removed++
		}
	}
	line := fmt.Sprintf("%d paths, %d versions", paths, versions)
	if removed > 0 {
		line += fmt.Sprintf(", %d of removed apps", removed)
	}
	fmt.Fprintln(stdout, line+" (hi server vault ls)")
	if vaultRecoveryWaiting(store) {
		fmt.Fprintln(stdout, "The recovery key waits for the owner: hi server vault recovery, on a terminal on this machine.")
	}
	return nil
}

func vaultUnlockCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) > 0 {
		return usageError{"usage: hi server vault unlock"}
	}
	if !vaultExists() {
		return errors.New("this machine has no vault")
	}
	vaultForget()
	if _, err := openVault(); err == nil {
		fmt.Fprintln(stdout, "The vault is open already.")
		return nil
	}
	secret, err := readSecretLine(stdin, stdout, "The passphrase, or the recovery key's 24 words")
	if err != nil {
		return err
	}
	var reply vaultSaidReply
	err = vaultServerCall("POST", "/admin/vault/unlock", vaultSecretRequest{Secret: secret}, &reply)
	if errors.Is(err, errVaultNoServer) {
		// No server holds it: the recovery key opens it here, and seals a
		// slot that opens by itself.
		kind, err := vaultUnlockHere(secret)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "The vault is open, and opens by itself again with %s. The apps that waited start within a minute.\n", kind)
		return nil
	}
	if err != nil {
		return err
	}
	vaultForget()
	fmt.Fprintf(stdout, "The vault is open (%s); the server holds it. The apps that waited start within a minute.\n", reply.Said)
	return nil
}

func vaultLockCommand(args []string, stdout io.Writer) error {
	if len(args) > 0 {
		return usageError{"usage: hi server vault lock"}
	}
	if !vaultExists() {
		return errors.New("this machine has no vault")
	}
	if err := vaultServerCall("POST", "/admin/vault/lock", nil, nil); err != nil {
		if errors.Is(err, errVaultNoServer) {
			return errors.New("no server holds the vault to lock: it runs as hi-server.service")
		}
		return err
	}
	vaultForget()
	keys, _ := loadVaultKeys()
	fmt.Fprintln(stdout, "The server forgot the vault's key. Running boxes keep running; a box that starts waits until an admin unlocks it (hi server vault unlock).")
	if slices.ContainsFunc(keys.kinds(), func(k string) bool { return k == vaultSlotTPM || k == vaultSlotFile }) {
		fmt.Fprintf(stdout, "It opens by itself again at the server's next start (%s).\n", strings.Join(keys.kinds(), ", "))
	}
	return nil
}

// vaultAskPassphrase asks a new passphrase twice, without echo.
func vaultAskPassphrase(stdin io.Reader, stdout io.Writer) (string, error) {
	both, err := readSecretLines(stdin, stdout, fmt.Sprintf("A passphrase (at least %d characters; a few words are easiest)", vaultPassphraseLeast), "Again")
	if err != nil {
		return "", err
	}
	first, again := both[0], both[1]
	if len([]rune(first)) < vaultPassphraseLeast {
		return "", fmt.Errorf("a passphrase has at least %d characters", vaultPassphraseLeast)
	}
	if again != first {
		return "", errors.New("the two weren't the same; nothing changed")
	}
	return first, nil
}

func vaultUnlockWithCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	usage := usageError{"usage: hi server vault unlock-with add|remove tpm|file|passphrase [--yes]"}
	yes := false
	var positional []string
	for _, arg := range args {
		if arg == "--yes" {
			yes = true
			continue
		}
		positional = append(positional, arg)
	}
	if len(positional) != 2 || (positional[0] != "add" && positional[0] != "remove") {
		return usage
	}
	action, kind := positional[0], positional[1]
	if kind != vaultSlotTPM && kind != vaultSlotFile && kind != vaultSlotPassphrase {
		return usage
	}
	secret := ""
	if action == "add" && kind == vaultSlotPassphrase {
		phrase, err := vaultAskPassphrase(stdin, stdout)
		if err != nil {
			return err
		}
		secret = phrase
	}
	change := func(yes bool) (string, error) {
		var reply vaultSaidReply
		err := vaultServerCall("POST", "/admin/vault/slot", vaultSecretRequest{Action: action, Kind: kind, Secret: secret, Yes: yes}, &reply)
		if errors.Is(err, errVaultNoServer) {
			key, err := vaultLocalKey()
			if err != nil {
				return "", err
			}
			return vaultChangeSlot(key, action, kind, secret, yes, vaultActor())
		}
		return reply.Said, err
	}
	said, err := change(yes)
	if err != nil && strings.Contains(err.Error(), vaultNoAutoWarning) {
		file, ok := stdin.(*os.File)
		if !ok || !isTerminal(file) {
			return err
		}
		fmt.Fprintf(stdout, "%s\nRemove %s anyway? [y/N] ", vaultNoAutoWarning, kind)
		answer, _ := readLine(stdin)
		if answer = strings.ToLower(strings.TrimSpace(answer)); answer != "y" && answer != "yes" {
			fmt.Fprintln(stdout, "Nothing changed.")
			return nil
		}
		said, err = change(true)
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, said)
	return nil
}

// vaultRecoveryCommand shows the recovery key once, on a terminal, asks
// two of its words back, and then forgets it.
func vaultRecoveryCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	fresh := false
	for _, arg := range args {
		if arg != "--new" {
			return usageError{"usage: hi server vault recovery [--new]"}
		}
		fresh = true
	}
	file, ok := stdin.(*os.File)
	if !ok || !isTerminal(file) {
		return errors.New("the recovery key is shown only on a terminal on this machine: run hi server vault recovery there")
	}
	store, err := openVault()
	if err != nil {
		return err
	}
	if fresh {
		err := vaultServerCall("POST", "/admin/vault/recovery", nil, nil)
		if errors.Is(err, errVaultNoServer) {
			var key []byte
			if key, err = vaultLocalKey(); err == nil {
				err = vaultMakeRecovery(key, vaultActor())
			}
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, "A new recovery key: the old words stop working on this machine. Backups of hi's state taken before still open with the old words.")
	}
	data, _, ok, err := store.get(vaultRecoveryPath)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("the recovery key was saved already, and hi forgot it. Lost it? hi server vault recovery --new makes another; the old one stops working")
	}
	words := strings.Fields(data.Values[vaultRecoveryName])
	fmt.Fprintln(stdout, "\nThe vault's recovery key. Write it down, or keep it in a password manager, away from this machine.")
	fmt.Fprintln(stdout, "It opens every secret on this machine, and is shown only now: hi forgets it once you've saved it.")
	fmt.Fprintln(stdout, "Never send it by Slack or mail.")
	fmt.Fprintln(stdout)
	for i := 0; i < len(words); i += 6 {
		var row []string
		for j := i; j < i+6 && j < len(words); j++ {
			row = append(row, fmt.Sprintf("%2d %-9s", j+1, words[j]))
		}
		fmt.Fprintln(stdout, "  "+strings.Join(row, " "))
	}
	fmt.Fprintln(stdout)
	fmt.Fprint(stdout, "Saved it? Press Enter, and the screen is cleared.")
	readLine(stdin)
	fmt.Fprint(stdout, "\033[2J\033[3J\033[H")
	asked := vaultTwoPositions(len(words))
	for _, position := range asked {
		fmt.Fprintf(stdout, "Word %d: ", position+1)
		answer, _ := readLine(stdin)
		if strings.ToLower(strings.TrimSpace(answer)) != words[position] {
			return errors.New("that isn't the word; the recovery key still waits: run hi server vault recovery again")
		}
	}
	if _, err := store.purge(vaultRecoveryPath); err != nil {
		return err
	}
	vaultAudit(vaultActor(), "vault.recovery-saved", "vault", "hi forgot the words")
	fmt.Fprintln(stdout, "Saved, and hi has forgotten it. With it, the vault opens on any machine: hi server vault unlock.")
	return nil
}

func vaultTwoPositions(n int) []int {
	pick := func() int {
		i, _ := rand.Int(rand.Reader, big.NewInt(int64(n)))
		return int(i.Int64())
	}
	a := pick()
	b := pick()
	for b == a {
		b = pick()
	}
	if b < a {
		a, b = b, a
	}
	return []int{a, b}
}

func vaultListCommand(args []string, stdout io.Writer) error {
	if len(args) > 1 {
		return usageError{"usage: hi server vault ls [<prefix>]"}
	}
	store, err := openVault()
	if err != nil {
		return err
	}
	prefix := ""
	if len(args) == 1 {
		prefix = strings.TrimSuffix(args[0], "/")
	}
	files, err := store.paths(prefix)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Fprintln(stdout, "Nothing there.")
		return nil
	}
	for _, file := range files {
		if file.Path == vaultRecoveryPath || strings.HasPrefix(file.Path, "hi/connected/") || len(file.Versions) == 0 {
			continue
		}
		latest := file.Versions[len(file.Versions)-1]
		names := "empty"
		if data, err := store.decrypt(file.Path, latest); err == nil && !data.empty() {
			names = strings.Join(data.names(), " ")
		}
		note := ""
		if file.Removed != nil {
			note = "  (a removed app's, kept)"
		}
		fmt.Fprintf(stdout, "%-34s v%-3d %s  %s%s\n", file.Path, latest.Version, latest.Time.Local().Format("2006-01-02 15:04"), names, note)
	}
	vaultConnectedList(stdout)
	return nil
}

func vaultHistoryCommand(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return usageError{"usage: hi server vault history <path>"}
	}
	store, err := openVault()
	if err != nil {
		return err
	}
	file, err := store.load(args[0])
	if err != nil {
		return err
	}
	if len(file.Versions) == 0 || args[0] == vaultRecoveryPath {
		return fmt.Errorf("the vault has no %s (hi server vault ls)", args[0])
	}
	for _, version := range file.Versions {
		what := "changed " + strings.Join(version.Changed, " ")
		if len(version.Changed) == 0 {
			what = "no change"
		}
		if version.Rollback > 0 {
			what = fmt.Sprintf("put version %d back (%s)", version.Rollback, what)
		}
		if version.Forgotten != nil {
			what += ", value forgotten " + version.Forgotten.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(stdout, "v%-3d %s  %-12s %s\n", version.Version, version.Time.Local().Format("2006-01-02 15:04"), version.By, what)
	}
	return nil
}

func vaultRollbackCommand(args []string, stdout io.Writer) error {
	if len(args) != 2 {
		return usageError{"usage: hi server vault rollback <path> <version>"}
	}
	version, err := strconv.Atoi(strings.TrimPrefix(args[1], "v"))
	if err != nil || version < 1 {
		return usageError{"usage: hi server vault rollback <path> <version>"}
	}
	store, err := openVault()
	if err != nil {
		return err
	}
	if err := store.rollback(args[0], version, vaultActor()); err != nil {
		return err
	}
	vaultAudit(vaultActor(), "vault.rollback", args[0], fmt.Sprintf("to version %d", version))
	fmt.Fprintf(stdout, "%s has version %d's values again, as a new version. A box gets them within a minute; it reads them as its app says (on_settings_change).\n", args[0], version)
	return nil
}

func vaultForgetCommand(args []string, stdout io.Writer) error {
	var path string
	version := 0
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--version" && i+1 < len(args):
			n, err := strconv.Atoi(strings.TrimPrefix(args[i+1], "v"))
			if err != nil || n < 1 {
				return usageError{"usage: hi server vault forget <path> --version <n>"}
			}
			version = n
			i++
		case path == "" && !strings.HasPrefix(args[i], "-"):
			path = args[i]
		default:
			return usageError{"usage: hi server vault forget <path> --version <n>"}
		}
	}
	if path == "" || version == 0 {
		return usageError{"usage: hi server vault forget <path> --version <n>"}
	}
	store, err := openVault()
	if err != nil {
		return err
	}
	if err := store.forget(path, version); err != nil {
		return err
	}
	vaultAudit(vaultActor(), "vault.forget", path, fmt.Sprintf("version %d's value", version))
	fmt.Fprintf(stdout, "Version %d of %s is forgotten; its entry stays. Backups of hi's state taken before still hold it, so change the secret at its source too.\n", version, path)
	return nil
}

// vaultLockedNote is the line a heartbeat or a status gives for the
// store, "" when it opens.
func vaultLockedNote() string {
	if !vaultExists() {
		return ""
	}
	if _, err := openVault(); err != nil {
		var locked vaultLockedError
		if errors.As(err, &locked) {
			return "the server's vault is locked; unlock it with hi server vault unlock"
		}
		return "the server's vault: " + err.Error()
	}
	return ""
}
