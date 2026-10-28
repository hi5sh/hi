package main

// hi watch, release 3, on the server: rules on the network lines the
// teams send, and stopping a team. Every rule is plain code, with no
// model. A network rule opens its incident once and closes it after a day
// without a repeat. A burst of sends to a host on watch-hosts.txt freezes
// the team on its own, as does an advisory known to be exploited in the
// app's own packages; everything else that stops a team is an admin's.
//
// Spec: docs/specs/approved/hi_watch.md (release 3).

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	watchDenyWindow  = 10 * time.Minute
	watchDenyMax     = 20
	watchUploadMax   = 50 << 20
	watchBurstWindow = 10 * time.Minute
	watchBurstSends  = 5
	watchBurstBytes  = 1 << 20
	// watchNetworkQuiet is how long a network rule's incident stays open
	// without a repeat.
	watchNetworkQuiet = 24 * time.Hour
	watchNewHostAfter = 30 * 24 * time.Hour
)

// watchExempt are hosts big uploads go to every day: the model
// providers and GitHub.
var watchExempt = []string{"openrouter.ai", "anthropic.com", "github.com", "githubusercontent.com", "githubassets.com"}

// watchHostsStarter is watch-hosts.txt as the server first writes it:
// paste sites, file drops, tunnels, and request catchers, where a box has
// no reason to send anything.
const watchHostsStarter = `# Hosts a team's box has no reason to send to: paste sites, file drops,
# tunnels, and request catchers. One per line; a name covers its
# subdomains. hi server watch reads this file again when it changes.
pastebin.com
paste.ee
hastebin.com
ghostbin.co
dpaste.com
rentry.co
transfer.sh
file.io
0x0.st
temp.sh
catbox.moe
gofile.io
anonfiles.com
sendspace.com
wetransfer.com
mega.nz
ngrok.io
ngrok-free.app
ngrok.app
trycloudflare.com
loca.lt
localhost.run
serveo.net
pipedream.net
requestbin.com
webhook.site
interactsh.com
oast.fun
oast.pro
oast.live
burpcollaborator.net
dnslog.cn
`

// apiWatchDo is what the server asks a team to do: freeze, unfreeze,
// pause, or unpause.
type apiWatchDo struct {
	ID      string    `json:"id"`
	Action  string    `json:"action"`
	By      string    `json:"by"`
	Because string    `json:"because,omitempty"`
	At      time.Time `json:"at"`
}

// apiWatchStop is how a team is stopped, and the last request it carried
// out.
type apiWatchStop struct {
	Frozen bool   `json:"frozen,omitempty"`
	Paused bool   `json:"paused,omitempty"`
	Done   string `json:"done,omitempty"`
}

// apiUndeclared is what production's box has that its bill doesn't, from
// the team's weekly look.
type apiUndeclared struct {
	Checked time.Time `json:"checked"`
	Count   int       `json:"count"`
	Items   []string  `json:"items,omitempty"`
}

// watchNet is the recent network lines a team's rules count, kept in
// memory only.
type watchNet struct {
	denies  map[string][]time.Time
	uploads map[string][]watchSample
	burst   []watchSample
}

func (w *serverWatch) netState(team string) *watchNet {
	if w.net == nil {
		w.net = map[string]*watchNet{}
	}
	if w.net[team] == nil {
		w.net[team] = &watchNet{denies: map[string][]time.Time{}, uploads: map[string][]watchSample{}}
	}
	return w.net[team]
}

// watchHosts are the hosts in watch-hosts.txt, read again when it changes.
func (w *serverWatch) watchHosts() []string {
	path := filepath.Join(w.dir, "watch-hosts.txt")
	info, err := os.Stat(path)
	if err != nil {
		os.MkdirAll(w.dir, 0o700)
		os.WriteFile(path, []byte(watchHostsStarter), 0o600)
		info, err = os.Stat(path)
		if err != nil {
			return parseBoxAllowlist(watchHostsStarter)
		}
	}
	if !info.ModTime().Equal(w.hostsAt) {
		data, _ := os.ReadFile(path)
		w.hosts, w.hostsAt = parseBoxAllowlist(string(data)), info.ModTime()
	}
	return w.hosts
}

func watchHostMatches(hosts []string, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, listed := range hosts {
		if host == listed || strings.HasSuffix(host, "."+listed) {
			return true
		}
	}
	return false
}

var watchLogName = regexp.MustCompile(`^(apps|box:[a-z0-9][a-z0-9._-]{0,80})$`)

// watchWho says whose a log is.
func watchWho(log string) string {
	switch {
	case log == "":
		return "the lead's box"
	case log == "apps":
		return "the app"
	}
	return "member box " + strings.TrimPrefix(log, "box:")
}

// keepLogLocked appends a log's new lines, as keepNetworkLocked does for
// the lead's, and runs the network rules on them.
func (w *serverWatch) keepLogLocked(team *watchTeam, network apiWatchNetwork, exempt []string, now time.Time) []watchEvent {
	if network.Log != "" && !watchLogName.MatchString(network.Log) {
		return nil
	}
	if team.LogsKept == nil {
		team.LogsKept = map[string]int64{}
	}
	kept := team.NetworkKept
	file := "network.log"
	if network.Log != "" {
		kept = team.LogsKept[network.Log]
		file = strings.ReplaceAll(network.Log, ":", "-") + ".log"
	}
	if network.To < network.From || len(network.Lines) > watchNetworkLinesMax {
		return nil
	}
	note := ""
	switch {
	case network.Rotated:
		note = fmt.Sprintf("# %s the log started again from 0 (rotated or shortened)", now.UTC().Format(time.RFC3339))
		kept = 0
	case network.To <= kept && network.From < kept:
		return nil // kept already
	case network.From > kept:
		note = fmt.Sprintf("# %s bytes %d to %d of the log never arrived", now.UTC().Format(time.RFC3339), kept, network.From)
	}
	dir := filepath.Join(w.dir, "teams", team.Team)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	out, err := os.OpenFile(filepath.Join(dir, file), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	defer out.Close()
	var text strings.Builder
	if note != "" {
		text.WriteString(note + "\n")
	}
	for i, line := range network.Lines {
		line = strings.ReplaceAll(line, "\n", " ")
		if len(line) > watchLineMax {
			line = line[:watchLineMax]
		}
		network.Lines[i] = line
		text.WriteString(line + "\n")
	}
	if _, err := out.WriteString(text.String()); err != nil {
		return nil
	}
	if network.Log == "" {
		team.NetworkKept = network.To
	} else {
		team.LogsKept[network.Log] = network.To
	}
	return w.networkRulesLocked(team, network.Log, network.Lines, exempt, now)
}

// networkRulesLocked runs the network rules on a log's new lines.
func (w *serverWatch) networkRulesLocked(team *watchTeam, log string, lines []string, exempt []string, now time.Time) []watchEvent {
	state := w.netState(team.Team)
	hosts := w.watchHosts()
	who := watchWho(log)
	var events []watchEvent
	fire := func(rule string, push bool, open func() watchIncident) {
		if team.NetSeen == nil {
			team.NetSeen = map[string]time.Time{}
		}
		team.NetSeen[rule] = now
		events = append(events, w.ruleLocked(team, rule, true, now, func() watchIncident {
			incident := open()
			incident.Push = push
			return incident
		}, "")...)
	}
	exempted := func(host string) bool {
		return watchHostMatches(watchExempt, host) || watchHostMatches(exempt, host)
	}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		at, err := time.Parse(time.RFC3339, fields[0])
		if err != nil {
			at = now
		}
		decision, host := fields[1], strings.ToLower(fields[2])
		switch decision {
		case "blocked":
			// The agents' and herdr's own update checks and calls around
			// the proxy's sign-ins are refused on purpose, every few
			// seconds while an agent sits idle: not a box being steered.
			if watchHostMatches(teamDevQuietHosts, host) {
				continue
			}
			recent := append(state.denies[log], at)
			for len(recent) > 0 && at.Sub(recent[0]) > watchDenyWindow {
				recent = recent[1:]
			}
			state.denies[log] = recent
			if len(recent) > watchDenyMax {
				fire("denies:"+log, false, func() watchIncident {
					return watchIncident{Title: fmt.Sprintf("%s's %s was refused %d connections in 10 minutes", team.Team, who, len(recent)),
						Detail: "The last was to " + host + ". A box steered toward hosts it may not reach is often reading something written to steer it; the network log is in the server's watch/teams/" + team.Team + "/."}
				})
			}
		case "allowed", "resolve-production", "resolve-preview":
			if decision == "allowed" && net.ParseIP(strings.Trim(host, "[]")) != nil {
				fire("raw-ip:"+log+":"+host, false, func() watchIncident {
					return watchIncident{Title: fmt.Sprintf("%s's %s connected to a raw address, %s", team.Team, who, host),
						Detail: "Software reaches services by name; a bare address is how a program avoids being seen looking one up."}
				})
			}
			if watchHostMatches(hosts, host) {
				fire("watch-host:"+log+":"+host, false, func() watchIncident {
					verb := "connected to"
					if decision != "allowed" {
						verb = "looked up"
					}
					return watchIncident{Title: fmt.Sprintf("%s's %s %s %s, on the server's list of paste, file-drop, and tunnel hosts", team.Team, who, verb, host)}
				})
				if decision == "allowed" {
					state.burst = append(state.burst, watchSample{At: at})
				}
			}
			if decision == "resolve-production" {
				events = append(events, w.noteAppHostLocked(team, host, at)...)
			}
		case "closed":
			up := int64(0)
			if len(fields) >= 4 {
				up, _ = strconv.ParseInt(fields[3], 10, 64)
			}
			if watchHostMatches(hosts, host) {
				state.burst = append(state.burst, watchSample{At: at, Value: float64(up)})
			}
			if exempted(host) {
				continue
			}
			sent := append(state.uploads[host], watchSample{At: at, Value: float64(up)})
			for len(sent) > 0 && at.Sub(sent[0].At) > time.Hour {
				sent = sent[1:]
			}
			state.uploads[host] = sent
			total := 0.0
			for _, sample := range sent {
				total += sample.Value
			}
			if total > watchUploadMax {
				fire("upload:"+host, false, func() watchIncident {
					return watchIncident{Title: fmt.Sprintf("%s sent %.0f MB to %s in an hour", team.Team, total/(1<<20), host),
						Detail: "From " + who + ". Big uploads go to the model providers, GitHub, and the snapshots' bucket; anything else this size is worth a look."}
				})
			}
		case "allowlist-added":
			fire("allowlist:"+log+":"+host, false, func() watchIncident {
				return watchIncident{Title: fmt.Sprintf("%s may now reach %s from %s", team.Team, host, who),
					Detail: "A host was added to the box's allowlist: an owner allowed it on the dev page, someone ran hi box allow, or something wrote the file."}
			})
		}
	}
	// A burst of sends to listed hosts freezes the team on its own.
	for len(state.burst) > 0 && now.Sub(state.burst[0].At) > watchBurstWindow {
		state.burst = state.burst[1:]
	}
	sent := 0.0
	for _, sample := range state.burst {
		sent += sample.Value
	}
	if len(state.burst) >= watchBurstSends || sent >= watchBurstBytes {
		because := fmt.Sprintf("%d connections and %.1f MB to paste, file-drop, or tunnel hosts in 10 minutes", len(state.burst), sent/(1<<20))
		fire("burst", true, func() watchIncident {
			return watchIncident{Title: fmt.Sprintf("%s sent to paste, file-drop, or tunnel hosts: %s", team.Team, because)}
		})
		if w.config.AutoFreeze != "off" {
			w.wantLocked(team, "freeze", "watch", because, now)
		}
		state.burst = nil
	}
	return events
}

// noteAppHostLocked keeps when production last looked each host up; one
// it hasn't in 30 days goes in the next weekly line.
func (w *serverWatch) noteAppHostLocked(team *watchTeam, host string, at time.Time) []watchEvent {
	if team.AppHosts == nil {
		team.AppHosts = map[string]time.Time{}
		team.AppHostsSince, team.NetReported = at, at
	}
	last, seen := team.AppHosts[host]
	if (!seen || at.Sub(last) > watchNewHostAfter) && at.Sub(team.AppHostsSince) > 24*time.Hour && !slicesContains(team.NewHosts, host) {
		team.NewHosts = append(team.NewHosts, host)
	}
	team.AppHosts[host] = at
	return nil
}

func slicesContains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// networkQuietLocked closes network incidents not seen again in a day, and
// posts the week's new hosts.
func (w *serverWatch) networkQuietLocked(team *watchTeam, now time.Time) ([]watchEvent, string) {
	var events []watchEvent
	for rule, seen := range team.NetSeen {
		if now.Sub(seen) > watchNetworkQuiet {
			delete(team.NetSeen, rule)
			events = append(events, w.ruleLocked(team, rule, false, now, nil, "Not seen again in a day.")...)
		}
	}
	text := ""
	if len(team.NewHosts) > 0 && now.Sub(team.NetReported) >= advisoryReportEvery {
		sort.Strings(team.NewHosts)
		text = fmt.Sprintf(":globe_with_meridians: *%s's production looked up %d hosts this week it hadn't in 30 days*: %s", team.Team, len(team.NewHosts), strings.Join(teamClipLines(team.NewHosts, 30), ", "))
		team.NewHosts, team.NetReported = nil, now
	}
	return events, text
}

// ---------------------------------------------------------------------------
// stopping a team

// wantLocked asks the team to freeze, unfreeze, pause, or unpause, in the
// answer to its next heartbeat, until it says it has.
func (w *serverWatch) wantLocked(team *watchTeam, action, by, because string, now time.Time) apiWatchDo {
	if team.Want != nil && team.Want.Action == action && !team.WantDone {
		return *team.Want
	}
	w.state.StopNext++
	do := apiWatchDo{ID: fmt.Sprintf("s-%d", w.state.StopNext), Action: action, By: by, Because: because, At: now.UTC()}
	team.Want, team.WantDone = &do, false
	return do
}

// stopTeam is an admin's freeze, unfreeze, pause, or unpause.
func (s *hiServer) stopTeam(name, action, by, because string) (apiWatchDo, error) {
	switch action {
	case "freeze", "unfreeze", "pause", "unpause":
	default:
		return apiWatchDo{}, fmt.Errorf("%q isn't freeze, unfreeze, pause, or unpause", action)
	}
	w := s.watch
	w.mu.Lock()
	team := w.state.Teams[name]
	if team == nil {
		w.mu.Unlock()
		return apiWatchDo{}, fmt.Errorf("no team %s sends heartbeats", name)
	}
	do := w.wantLocked(team, action, by, because, computeNow())
	w.saveLocked()
	w.mu.Unlock()
	s.audit(by, "asked to "+action, name, because)
	s.watchPost("", fmt.Sprintf(":octagonal_sign: %s asked team %s to %s%s. It does with its next heartbeat, within a minute, and says so in its channel.", by, name, action, map[bool]string{true: ": " + because, false: ""}[because != ""]), false)
	return do, nil
}

// stopConfirmedLocked notes a team that carried out what was asked.
func (w *serverWatch) stopConfirmedLocked(s *hiServer, team *watchTeam, stop apiWatchStop) string {
	if team.Want == nil || team.WantDone || stop.Done != team.Want.ID {
		return ""
	}
	team.WantDone = true
	return fmt.Sprintf("Team %s did %s (%s).", team.Team, team.Want.Action, team.Want.ID)
}

func (s *hiServer) watchStopRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/watch/stop/{team}/{action}", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Because string `json:"because"`
			As      string `json:"as"`
		}
		json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&input)
		do, err := s.stopTeam(r.PathValue("team"), r.PathValue("action"), firstNonEmpty(input.As, currentUserName()), input.Because)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, do)
	})
}

// watchStopCommand is hi server watch freeze|unfreeze|pause|unpause <team>.
func watchStopCommand(dirFlag, action, team, because string, stdout io.Writer) error {
	var do apiWatchDo
	if err := adminCall(dirFlag, http.MethodPost, "/admin/watch/stop/"+team+"/"+action, map[string]string{"because": because, "as": currentUserName()}, &do); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Asked team %s to %s (%s). It does with its next heartbeat, within a minute; hi server watch shows when it has.\n", team, action, do.ID)
	return nil
}

// readWatchLines is a team's kept log, for tests and the admins.
func readWatchLines(path string) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}
