package cli

// A terminal in an app's boxes (hi_admin.md, release 7): the machine's
// owner opens a shell in one of an app's running boxes from the admin
// page, xterm.js over a WebSocket as the dev page's terminal is. Not on
// the machine: the account that runs hi holds every app's keys, and the
// boxes hold none. Every terminal is said in Activity and the app's ops
// channel; what's typed isn't kept.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// adminTermBox is one of an app's boxes a terminal can open in.
type adminTermBox struct {
	ID        string `json:"id"`    // lead, production, stage, dev, memory
	Label     string `json:"label"` // as people say it: "the lead's box"
	Container string `json:"-"`
}

// adminTermView is the app's Terminal section.
type adminTermView struct {
	Name  string         `json:"name"`
	Owner string         `json:"owner"` // the machine's owner
	You   bool           `json:"you"`   // the admin is the owner
	Boxes []adminTermBox `json:"boxes"`
}

// adminTermIdle is how long a terminal stays open without a key.
var adminTermIdle = 30 * time.Minute

// adminTermCheck is how often an open terminal asks whether its admin is
// still signed in, and still the owner.
var adminTermCheck = 30 * time.Second

// adminBoxRunning says a container runs; tests replace it.
var adminBoxRunning = func(container string) bool {
	engine, _, err := detectBoxEngine()
	return err == nil && engine.state(container) == "running"
}

// adminTermBoxes are an app's boxes that run, in the order people think
// of them: the lead, production, Stage, the dev box, the memory. Not the
// box proxies or the NetBird peer: their keys and the network are the
// machine's.
func adminTermBoxes(name string) []adminTermBox {
	candidates := []adminTermBox{{ID: "lead", Label: "the lead's box", Container: "hi-box-app-" + name}}
	if state, ok := loadTeamSlot(name, appSlotProduction); ok && state.Container != "" {
		candidates = append(candidates, adminTermBox{ID: "production", Label: "production's box", Container: state.Container})
	}
	if state, ok := loadTeamSlot(name, appSlotPreview); ok && state.Container != "" {
		candidates = append(candidates, adminTermBox{ID: "stage", Label: "Stage's box", Container: state.Container})
	}
	candidates = append(candidates,
		adminTermBox{ID: "dev", Label: "the dev box", Container: appDevContainer(name)},
		adminTermBox{ID: "memory", Label: "the memory's box", Container: appMemoryContainer(name)})
	boxes := []adminTermBox{}
	for _, box := range candidates {
		if adminBoxRunning(box.Container) {
			boxes = append(boxes, box)
		}
	}
	return boxes
}

func adminTermViewOf(name string, admin adminPerson) (adminTermView, error) {
	if !appNamePattern.MatchString(name) {
		return adminTermView{}, fmt.Errorf("no app %q on this machine", name)
	}
	if _, err := loadTeam(name); err != nil {
		return adminTermView{}, fmt.Errorf("no app %q on this machine", name)
	}
	config, _ := loadAdminConfig()
	view := adminTermView{Name: name, Owner: config.machineOwner(), Boxes: adminTermBoxes(name)}
	view.You = admin.Machine && view.Owner != "" && admin.Name == view.Owner
	return view, nil
}

// adminTermCommand is the shell a terminal runs in a box: bash where the
// box has it, else sh, as the box's own user. HI_ADMIN_TERM marks it, so
// its processes go when the terminal closes. Tests replace it.
var adminTermCommand = func(container, mark string) []string {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return nil
	}
	return []string{engine.bin, "exec", "-it", "-e", "TERM=xterm-256color", "-e", "COLORTERM=truecolor", "-e", "HI_ADMIN_TERM=" + mark,
		container, "sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash -l; else exec sh -l; fi"}
}

// adminTermEnd ends what a terminal left running in its box: what the
// shell started outlives the engine's exec client. Tests replace it.
var adminTermEnd = func(container, mark string) {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return
	}
	script := `for p in /proc/[0-9]*; do tr '\0' '\n' < "$p/environ" 2>/dev/null | grep -qx "HI_ADMIN_TERM=` + mark + `" && kill -HUP "${p#/proc/}" 2>/dev/null; done; true`
	exec.Command(engine.bin, "exec", container, "sh", "-c", script).Run()
}

// adminTermTell says in Activity and the app's ops channel that a
// terminal opened or closed.
func adminTermTell(admin adminPerson, app, what string) {
	adminRecord(admin.Name, app, what)
	if err := adminTell(app, false, fmt.Sprintf(":computer: %s %s, from the admin page.", admin.Name, what)); err != nil {
		fmt.Fprintf(os.Stderr, "hi: telling app %s's channel: %v\n", app, err)
	}
}

// terminal is one browser's terminal in a box: a shell under a terminal
// of its own on this machine, its output to the page, and the page's keys
// to it. Binary messages are the terminal's bytes; a text message is
// {"cols":..,"rows":..} when the page's size changes.
func (s *adminServer) terminal(w http.ResponseWriter, r *http.Request, admin adminPerson) {
	query := r.URL.Query()
	view, err := adminTermViewOf(query.Get("name"), admin)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if !view.You {
		http.Error(w, "only the machine's owner, "+firstNonEmpty(view.Owner, "nobody yet")+", opens a terminal in an app's boxes", http.StatusForbidden)
		return
	}
	var box adminTermBox
	for _, candidate := range view.Boxes {
		if candidate.ID == query.Get("box") {
			box = candidate
		}
	}
	if box.ID == "" {
		http.Error(w, "that box of "+view.Name+" doesn't run", http.StatusNotFound)
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: adminSameOrigin}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hi: the admin page's terminal: %v\n", err)
		return
	}
	defer conn.Close()
	var writeMu sync.Mutex
	closeWith := func(reason string) {
		writeMu.Lock()
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, reason), time.Now().Add(time.Second))
		writeMu.Unlock()
		conn.Close()
	}
	fail := func(text string) {
		writeMu.Lock()
		conn.WriteMessage(websocket.BinaryMessage, []byte("\r\n\x1b[31m"+text+"\x1b[0m\r\n"))
		writeMu.Unlock()
	}
	mark, err := adminNewKey()
	if err != nil {
		fail("no terminal: " + err.Error())
		return
	}
	command := adminTermCommand(box.Container, mark)
	if len(command) == 0 {
		fail("hi found no box engine on the machine")
		return
	}
	master, slave, err := appOpenPTY()
	if err != nil {
		fail("no terminal: " + err.Error())
		return
	}
	defer master.Close()
	cols, _ := strconv.Atoi(query.Get("cols"))
	rows, _ := strconv.Atoi(query.Get("rows"))
	appResizePTY(master, cols, rows)
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		slave.Close()
		fail("the shell didn't start: " + err.Error())
		return
	}
	slave.Close()
	opened := time.Now()
	adminTermTell(admin, view.Name, "opened a terminal in "+view.Name+"'s "+trimThe(box.Label))
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()
	defer func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP)
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		adminTermEnd(box.Container, mark)
		minutes := int(time.Since(opened).Round(time.Minute).Minutes())
		adminTermTell(admin, view.Name, fmt.Sprintf("closed the terminal in %s's %s, after %d min", view.Name, trimThe(box.Label), minutes))
	}()

	// The shell's output, to the page; its end closes the terminal.
	go func() {
		buffer := make([]byte, 32<<10)
		for {
			n, err := master.Read(buffer)
			if n > 0 {
				writeMu.Lock()
				werr := conn.WriteMessage(websocket.BinaryMessage, buffer[:n])
				writeMu.Unlock()
				if werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		closeWith("the shell ended")
	}()

	// It closes after a while without a key, and when its admin's sign-in
	// ends or they aren't the owner any more.
	input := make(chan struct{}, 1)
	go func() {
		idle := time.NewTimer(adminTermIdle)
		check := time.NewTicker(adminTermCheck)
		defer idle.Stop()
		defer check.Stop()
		for {
			select {
			case <-exited:
				return
			case <-input:
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(adminTermIdle)
			case <-idle.C:
				closeWith(fmt.Sprintf("no key for %d minutes", int(adminTermIdle.Minutes())))
				return
			case <-check.C:
				now, ok := s.admin(r)
				if view, err := adminTermViewOf(view.Name, now); !ok || err != nil || !view.You {
					closeWith("you're no longer signed in as the machine's owner")
					return
				}
			}
		}
	}()

	conn.SetReadLimit(1 << 20)
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if kind == websocket.TextMessage {
			var size struct{ Cols, Rows int }
			if json.Unmarshal(data, &size) == nil {
				appResizePTY(master, size.Cols, size.Rows)
			}
			continue
		}
		select {
		case input <- struct{}{}:
		default:
		}
		if _, err := master.Write(data); err != nil {
			return
		}
	}
}

// trimThe is a box's label without its "the", after an app's name:
// "demo's lead's box".
func trimThe(label string) string {
	if len(label) > 4 && label[:4] == "the " {
		return label[4:]
	}
	return label
}
