package cli

// The data key in systemd's keeping across the server's restarts
// (hi_server_vault.md, Restarts don't lock it): once unlocked, the server
// hands systemd a memory-only file holding the key (FDSTORE), and
// systemd gives it to the server's next start (LISTEN_FDS). So hi
// update, a crash, and the page's Restart leave the store unlocked; a
// reboot, or the user's manager stopping, locks it. hi-server.service
// says NotifyAccess=main, FileDescriptorStoreMax=1, and
// FileDescriptorStorePreserve=yes (systemd 254 or newer).

import (
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const vaultFDName = "hi-vault-key"

// vaultServerUnitLines are what hi-server.service needs for it.
var vaultServerUnitLines = []string{"NotifyAccess=main", "FileDescriptorStoreMax=1", "FileDescriptorStorePreserve=yes"}

// vaultSystemdKeep, vaultSystemdTake, and vaultSystemdDrop are systemd's
// keeping; tests replace them.
var vaultSystemdKeep = func(key []byte) {
	fd, err := unix.MemfdCreate(vaultFDName, unix.MFD_CLOEXEC)
	if err != nil {
		return
	}
	defer unix.Close(fd)
	if _, err := unix.Write(fd, key); err != nil {
		return
	}
	vaultNotify("FDSTOREREMOVE=1\nFDNAME="+vaultFDName, -1)
	vaultNotify("FDSTORE=1\nFDNAME="+vaultFDName, fd)
}

var vaultSystemdTake = func() []byte {
	if os.Getenv("LISTEN_PID") != strconv.Itoa(os.Getpid()) {
		return nil
	}
	count, _ := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	names := strings.Split(os.Getenv("LISTEN_FDNAMES"), ":")
	for _, name := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
		os.Unsetenv(name)
	}
	var key []byte
	for i := 0; i < count; i++ {
		fd := 3 + i
		if i < len(names) && names[i] == vaultFDName && key == nil {
			buffer := make([]byte, 64)
			if n, err := unix.Pread(fd, buffer, 0); err == nil && n == 32 {
				key = buffer[:n]
			}
		}
		// Every fd systemd gave is closed, so none reaches a child.
		unix.Close(fd)
	}
	return key
}

var vaultSystemdDrop = func() {
	vaultNotify("FDSTOREREMOVE=1\nFDNAME="+vaultFDName, -1)
}

// vaultNotify sends one message to systemd, with fd when it is one.
func vaultNotify(message string, fd int) error {
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return fmt.Errorf("no NOTIFY_SOCKET")
	}
	if strings.HasPrefix(socket, "@") {
		socket = "\x00" + socket[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer conn.Close()
	var rights []byte
	if fd >= 0 {
		rights = syscall.UnixRights(fd)
	}
	_, _, err = conn.WriteMsgUnix([]byte(message), rights, nil)
	return err
}

// vaultUpgradeServerUnit adds what systemd's keeping needs to
// hi-server.service, for its next start.
func vaultUpgradeServerUnit(log io.Writer) {
	path := orgUserUnitPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	unit, changed := vaultUnitWithKeeping(string(data))
	if !changed {
		return
	}
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		fmt.Fprintf(log, "hi server: %s can't keep the vault's key across restarts: %v\n", orgUnit, err)
		return
	}
	appSystemctl("daemon-reload")
	fmt.Fprintf(log, "hi server: %s keeps the vault's key across restarts from its next start\n", orgUnit)
}

// vaultUnitWithKeeping is unit with vaultServerUnitLines in [Service].
func vaultUnitWithKeeping(unit string) (string, bool) {
	var missing []string
	for _, line := range vaultServerUnitLines {
		key := strings.SplitN(line, "=", 2)[0] + "="
		if !strings.Contains(unit, "\n"+key) {
			missing = append(missing, line)
		}
	}
	if len(missing) == 0 || !strings.Contains(unit, "[Service]\n") {
		return unit, false
	}
	return strings.Replace(unit, "[Service]\n", "[Service]\n"+strings.Join(missing, "\n")+"\n", 1), true
}
