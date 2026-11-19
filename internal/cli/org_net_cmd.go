package cli

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// hi server net: the server's NetBird peer (org_peer.go).
func orgNetCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags, dirFlag := orgFlags("net", stderr)
	keyFile := flags.String("setup-key-file", "", "the peer's NetBird setup key, in a file only you can read")
	management := flags.String("management-url", "", "the network's management server, when it isn't NetBird's own")
	name := flags.String("name", "", "the peer's name on the network (default <machine>-server)")
	as := flags.String("as", currentUserName(), "who is acting")
	positional, err := flags.parse(args)
	if err != nil {
		return err
	}
	dir, err := orgDirectory(*dirFlag)
	if err != nil {
		return err
	}
	usage := usageError{"usage: hi server net [join [--setup-key-file F] [--management-url URL] [--name N] | forget-old]"}
	if len(positional) > 1 {
		return usage
	}
	command := "status"
	if len(positional) == 1 {
		command = positional[0]
	}
	switch command {
	case "status":
		view, err := serverNetViewOf(dir)
		if err != nil {
			return err
		}
		printServerNet(view, stdout)
		return nil
	case "join":
		config, err := loadServerConfig(dir)
		if err != nil {
			return err
		}
		if config.OldPeer != nil {
			return fmt.Errorf("the server is still moving from %s: once its devices have followed, or now with hi server net forget-old, it can move again", config.OldPeer.Name)
		}
		key, err := readNetBirdSetupKey(*keyFile, stdin, stdout)
		if err != nil {
			return err
		}
		defer clear(key)
		status, err := serverNetJoin(dir, serverNetJoinOptions{Key: key, ManagementURL: *management, Name: *name}, *as, stdout)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "The server is on NetBird as %s (%s).\n", serverJoinAddress(status), status.Address)
		fmt.Fprintf(stdout, "Devices join with: hi server join %s\n", serverJoinAddress(status))
		switch {
		case config.Peer != nil:
			fmt.Fprintf(stdout, "Its peer from before, %s, answers beside it, and tells each device that calls the new address; devices that reach it follow. It goes once none has called for a day, in 14 days at the latest, or now: hi server net forget-old\n", config.Peer.Name)
		case config.Listen != "":
			fmt.Fprintf(stdout, "It answers at %s too, this machine's NetBird address, and tells each device that calls there the new address; devices that reach it follow. hi server net forget-old stops that once they have.\n", config.Listen)
		}
		if serverPeerCarriesPage(dir) {
			fmt.Fprintf(stdout, "The admin page is there too, for its admins: http://%s:%d/\n", serverJoinAddress(status), adminPort)
			fmt.Fprintf(stdout, "Ask your IT team for NetBird policies that let devices reach %s on TCP %d, and the admins on TCP %d.\n", serverJoinAddress(status), serverDefaultPort, adminPort)
		} else {
			fmt.Fprintf(stdout, "Ask your IT team for a NetBird policy that lets devices reach %s on TCP %d.\n", serverJoinAddress(status), serverDefaultPort)
		}
		if !fileExists(filepath.Join(dir, "admin.sock")) {
			fmt.Fprintf(stdout, "The server isn't running; it answers there once it starts: %s\n", orgRestartLine())
		}
		return nil
	case "forget-old":
		view, _ := serverNetViewOf(dir)
		_, gone, err := serverForgetOld(dir)
		if err != nil {
			return err
		}
		if len(gone) == 0 {
			fmt.Fprintln(stdout, "The server has no old address: it answers only on its peer.")
			return nil
		}
		serverAuditOffline(dir, *as, "stopped answering at its old addresses", strings.Join(gone, ", "), "")
		serverReloadNet(dir, *as)
		fmt.Fprintf(stdout, "The server no longer answers at %s.\n", strings.Join(gone, " or "))
		if recent := serverRecentOld(view.OldDevices, time.Now()); len(recent) > 0 {
			fmt.Fprintf(stdout, "These devices called there in the last day; each joins again at the new address, as the same device: hi server join %s\n", view.Join)
			for _, device := range recent {
				fmt.Fprintf(stdout, "  %s on %s, %s\n", device.User, device.Hostname, appDevAgo(time.Now(), device.Last))
			}
		}
		return nil
	}
	return usage
}

// serverNetViewOf is the running server's view of its addresses, or one
// made from config.json and the peer when it isn't running.
func serverNetViewOf(dir string) (serverNetView, error) {
	var view serverNetView
	if fileExists(filepath.Join(dir, "admin.sock")) {
		if err := adminCall(dir, http.MethodGet, "/admin/net", nil, &view); err == nil {
			return view, nil
		}
	}
	config, err := loadServerConfig(dir)
	if err != nil {
		return view, err
	}
	view.Listen = config.Listen
	if config.Peer != nil {
		status, err := serverPeerStatusOf(config.Peer.ID)
		problem := ""
		if err != nil {
			problem = "its container doesn't answer: " + err.Error()
		}
		view.Peer = serverPeerViewOf(config.Peer, status, problem)
		view.Join = serverJoinAddress(status)
		if view.Join == "" {
			view.Join = config.Peer.Name
		}
		view.Page = serverPeerCarriesPage(dir)
	} else {
		view.Join = config.Listen
	}
	if config.OldPeer != nil {
		status, _ := serverPeerStatusOf(config.OldPeer.ID)
		view.Old = serverPeerViewOf(config.OldPeer, status, "")
	}
	return view, nil
}

// serverRecentOld are the devices that called at an old address in the
// last day.
func serverRecentOld(devices []serverOldDevice, now time.Time) []serverOldDevice {
	var recent []serverOldDevice
	for _, device := range devices {
		if now.Sub(device.Last) < serverOldQuietFor {
			recent = append(recent, device)
		}
	}
	return recent
}

func printServerNet(view serverNetView, stdout io.Writer) {
	if view.Peer == nil {
		if view.Listen != "" {
			fmt.Fprintf(stdout, "The server listens on this machine's NetBird address, %s: it moves when the machine does.\n", view.Listen)
		}
		fmt.Fprintln(stdout, "A NetBird peer of its own moves it to another network without the machine: hi server net join")
		return
	}
	p := view.Peer
	state := "on its network"
	if !p.Connected {
		state = "not on its network"
		if p.Problem != "" {
			state += " (" + p.Problem + ")"
		}
	}
	address := ""
	if p.Address != "" {
		address = " (" + p.Address + ")"
	}
	fmt.Fprintf(stdout, "The server's NetBird peer: %s%s, %s, at %s.\n", firstNonEmpty(p.FQDN, p.Name), address, state, p.Network)
	if view.Join != "" {
		fmt.Fprintf(stdout, "Devices join with: hi server join %s\n", view.Join)
		if view.Page {
			fmt.Fprintf(stdout, "The admin page is there too: http://%s:%d/\n", view.Join, adminPort)
		}
	}
	if o := view.Old; o != nil {
		fmt.Fprintf(stdout, "It still answers as %s, its peer from before the move, until devices have followed (by %s at the latest); hi server net forget-old ends that now.\n",
			firstNonEmpty(o.FQDN, o.Name), o.Until.Local().Format("2 January"))
	}
	if view.Listen != "" {
		fmt.Fprintf(stdout, "It still answers at %s, this machine's NetBird address from before its peer; hi server net forget-old ends that.\n", view.Listen)
	}
	if recent := serverRecentOld(view.OldDevices, time.Now()); len(recent) > 0 {
		fmt.Fprintln(stdout, "Devices that called at an old address in the last day:")
		for _, device := range recent {
			fmt.Fprintf(stdout, "  %s on %s, %s\n", device.User, device.Hostname, appDevAgo(time.Now(), device.Last))
		}
	}
	if view.Local != "" {
		fmt.Fprintf(stdout, "This machine's own hi reaches it at %s.\n", view.Local)
	}
}

// orgInitPeerKey is the setup key for the server's NetBird peer: from a
// file, from HI_NETBIRD_SETUP_KEY, or asked at a terminal, where Enter
// has the server listen on the machine's NetBird address instead. None,
// without a terminal, is that too.
func orgInitPeerKey(path string, stdin io.Reader, stdout io.Writer) ([]byte, error) {
	if path != "" {
		return readNetBirdSetupKey(path, stdin, stdout)
	}
	if value, ok := os.LookupEnv(netBirdSetupKeyEnv); ok && strings.TrimSpace(value) != "" {
		return []byte(strings.TrimSpace(value)), nil
	}
	file, ok := stdin.(*os.File)
	if !ok || !isTerminal(stdin) {
		return nil, nil
	}
	if _, err := serverPeerEngine(); err != nil {
		return nil, nil
	}
	fmt.Fprintln(stdout, "The server can have a NetBird peer of its own, so it moves to another network without the machine.")
	fmt.Fprint(stdout, "Its setup key, from the network's dashboard (Enter: listen on this machine's NetBird address instead): ")
	key, err := readSecret(file, stdout)
	fmt.Fprintln(stdout)
	if err != nil {
		return nil, err
	}
	if key = bytes.TrimSpace(key); len(key) == 0 {
		return nil, nil
	}
	return key, nil
}
