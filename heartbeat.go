package main

// Heartbeats (docs/specs/approved/hi_team_ops.md, release 2): hi sends a
// GET to an outside service, such as healthchecks.io, which alerts when
// they stop. A team's comes from its service (team_heartbeat.go); this
// machine's, for a machine that runs hi's services, from a systemd user
// timer. hi only sends: who hears of a stop, and how, is set up at the
// service. The URL is the secret, so it is never printed or logged, only
// its host.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// heartbeatClient sends heartbeats; tests replace it.
var heartbeatClient = &http.Client{Timeout: 10 * time.Second}

// heartbeatPing sends one heartbeat, and says how it went by the
// service's host, never the URL.
func heartbeatPing(target string) error {
	response, err := heartbeatClient.Get(target)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("%s: %v", heartbeatHost(target), err)
	}
	io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	response.Body.Close()
	if response.StatusCode >= 300 {
		return fmt.Errorf("%s answered %s", heartbeatHost(target), response.Status)
	}
	return nil
}

// heartbeatHost names a heartbeat's service without its secret path.
func heartbeatHost(target string) string {
	if parsed, err := url.Parse(target); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return "the heartbeat's service"
}

// checkHeartbeatURL is an http(s) address with a host.
func checkHeartbeatURL(target string) error {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return errors.New("a heartbeat URL is an address such as https://hc-ping.com/<uuid>, from the service's check")
	}
	return nil
}

// parseHeartbeatEvery reads an interval such as 1m, between least and most.
func parseHeartbeatEvery(text string, least, most time.Duration) (time.Duration, error) {
	every, err := time.ParseDuration(strings.TrimSpace(text))
	if err != nil || every < least || every > most {
		return 0, fmt.Errorf("the interval is a duration between %s and %s, such as 1m or 5m, not %q", heartbeatDuration(least), heartbeatDuration(most), text)
	}
	return every, nil
}

// heartbeatDuration writes 5m0s as 5m and 1h0m0s as 1h.
func heartbeatDuration(d time.Duration) string {
	text := d.String()
	if strings.HasSuffix(text, "m0s") {
		text = strings.TrimSuffix(text, "0s")
	}
	if strings.HasSuffix(text, "h0m") {
		text = strings.TrimSuffix(text, "0m")
	}
	return text
}

// heartbeatLast is the last heartbeat sent, and how it went.
type heartbeatLast struct {
	At    time.Time `json:"at"`
	Error string    `json:"error,omitempty"`
}

func (last heartbeatLast) String() string {
	if last.At.IsZero() {
		return "none sent yet"
	}
	if last.Error != "" {
		return fmt.Sprintf("failed %s: %s", last.At.Local().Format("Jan 2 15:04:05"), last.Error)
	}
	return "sent " + last.At.Local().Format("Jan 2 15:04:05")
}

// ---------------------------------------------------------------------------
// the machine's heartbeat

const (
	machineHeartbeatUnit    = "hi-heartbeat"
	machineHeartbeatDefault = 5 * time.Minute
)

// machineHeartbeat is ~/.config/hi/heartbeat.json.
type machineHeartbeat struct {
	URL   string        `json:"url"`
	Every string        `json:"every"`
	Last  heartbeatLast `json:"last"`
}

func machineHeartbeatPath() string { return filepath.Join(hiConfigDirectory(), "heartbeat.json") }

func loadMachineHeartbeat() (machineHeartbeat, bool) {
	var beat machineHeartbeat
	data, err := os.ReadFile(machineHeartbeatPath())
	if err != nil || json.Unmarshal(data, &beat) != nil || beat.URL == "" {
		return machineHeartbeat{}, false
	}
	return beat, true
}

func saveMachineHeartbeat(beat machineHeartbeat) error {
	if err := os.MkdirAll(filepath.Dir(machineHeartbeatPath()), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(beat, "", "  ")
	return writeFileAtomic(machineHeartbeatPath(), append(data, '\n'), 0o600)
}

// runHeartbeat is hi heartbeat [<url> [--every 5m] | off | test].
func runHeartbeat(args []string, stdout, stderr io.Writer) int {
	return exitCode(heartbeatCommand(args, stdout), stderr)
}

func heartbeatCommand(args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi heartbeat [<url> [--every 5m] | off | test]"}
	beat, set := loadMachineHeartbeat()
	switch {
	case len(args) == 0:
		if !set {
			fmt.Fprintln(stdout, "This machine sends no heartbeat, so nobody would hear that it stopped. hi heartbeat <url> sends one, to a service such as healthchecks.io.")
			machineHeartbeatNote(stdout)
			return nil
		}
		fmt.Fprintf(stdout, "This machine sends a heartbeat to %s every %s; the last was %s.\n", heartbeatHost(beat.URL), beat.Every, beat.Last)
		return nil
	case len(args) == 1 && args[0] == "__ping":
		// The timer's: once, recorded.
		if !set {
			return nil
		}
		err := heartbeatPing(beat.URL)
		beat.Last = heartbeatLast{At: time.Now().UTC()}
		if err != nil {
			beat.Last.Error = err.Error()
		}
		saveMachineHeartbeat(beat)
		return err
	case len(args) == 1 && args[0] == "test":
		if !set {
			return errors.New("this machine has no heartbeat; hi heartbeat <url> sets one")
		}
		if err := heartbeatPing(beat.URL); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s took the heartbeat.\n", heartbeatHost(beat.URL))
		return nil
	case len(args) == 1 && args[0] == "off":
		teamSystemctl("disable", "--now", machineHeartbeatUnit+".timer")
		dir := filepath.Dir(teamUnitPath("x"))
		os.Remove(filepath.Join(dir, machineHeartbeatUnit+".timer"))
		os.Remove(filepath.Join(dir, machineHeartbeatUnit+".service"))
		teamSystemctl("daemon-reload")
		os.Remove(machineHeartbeatPath())
		fmt.Fprintln(stdout, "This machine sends no heartbeat now. Pause or remove its check at the service, or it will alert.")
		return nil
	}
	target, every := "", machineHeartbeatDefault
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--every" && i+1 < len(args):
			i++
			var err error
			if every, err = parseHeartbeatEvery(args[i], time.Minute, time.Hour); err != nil {
				return err
			}
		case strings.HasPrefix(args[i], "-") || target != "":
			return usage
		default:
			target = args[i]
		}
	}
	if target == "" {
		return usage
	}
	return setMachineHeartbeat(target, every, stdout)
}

// setMachineHeartbeat checks the URL with a first ping, keeps it, and
// starts the timer.
func setMachineHeartbeat(target string, every time.Duration, stdout io.Writer) error {
	target = strings.TrimSpace(target)
	if err := checkHeartbeatURL(target); err != nil {
		return err
	}
	if err := heartbeatPing(target); err != nil {
		return fmt.Errorf("the first heartbeat didn't arrive: %w", err)
	}
	beat := machineHeartbeat{URL: target, Every: heartbeatDuration(every), Last: heartbeatLast{At: time.Now().UTC()}}
	if err := saveMachineHeartbeat(beat); err != nil {
		return err
	}
	if err := installMachineHeartbeatTimer(every); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "This machine sends a heartbeat to %s every %s; the first arrived. Set the check's period there to %s, and who it alerts.\n",
		heartbeatHost(target), beat.Every, beat.Every)
	machineHeartbeatNote(stdout)
	if !teamLingers() {
		fmt.Fprintf(stdout, "It stops when you log out, and doesn't start at boot, until you run:\n  sudo loginctl enable-linger %s\n", currentUserName())
	}
	return nil
}

// machineHeartbeatNote says what the machine's heartbeat is for, when the
// machine runs none of hi's services.
func machineHeartbeatNote(stdout io.Writer) {
	if !machineRunsHiServices() {
		fmt.Fprintln(stdout, "It means most on a machine that runs hi's services all the time: teams, or a hi server. This one runs neither.")
	}
}

// machineRunsHiServices is whether this machine has a team or a hi server.
func machineRunsHiServices() bool {
	if entries, err := os.ReadDir(teamsDir()); err == nil && len(entries) > 0 {
		return true
	}
	return fileExists("/etc/systemd/system/hi-server.service")
}

// installMachineHeartbeatTimer writes and starts the timer and its
// service, which runs hi heartbeat __ping.
func installMachineHeartbeatTimer(every time.Duration) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	dir := filepath.Dir(teamUnitPath("x"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	service := fmt.Sprintf(`[Unit]
Description=hi: this machine's heartbeat

[Service]
Type=oneshot
Environment=HOME=%s
ExecStart=%s heartbeat __ping
`, os.Getenv("HOME"), executable)
	timer := fmt.Sprintf(`[Unit]
Description=hi: this machine's heartbeat, every %[1]s

[Timer]
OnActiveSec=1min
OnUnitActiveSec=%[1]s
AccuracySec=5s

[Install]
WantedBy=timers.target
`, heartbeatDuration(every))
	if err := os.WriteFile(filepath.Join(dir, machineHeartbeatUnit+".service"), []byte(service), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, machineHeartbeatUnit+".timer"), []byte(timer), 0o644); err != nil {
		return err
	}
	if out, err := teamSystemctl("daemon-reload"); err != nil {
		return fmt.Errorf("systemctl --user daemon-reload: %s", qFirstLine(out, err.Error()))
	}
	if out, err := teamSystemctl("enable", "--now", machineHeartbeatUnit+".timer"); err != nil {
		return fmt.Errorf("systemctl --user enable --now %s.timer: %s", machineHeartbeatUnit, qFirstLine(out, err.Error()))
	}
	// A changed interval: start counting from now.
	teamSystemctl("restart", machineHeartbeatUnit+".timer")
	return nil
}
