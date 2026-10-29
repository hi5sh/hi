package main

// hi watch, on the team's machine: every minute __serve makes a heartbeat,
// with each part's state, the disk, OOM kills, the last snapshot, what
// model calls cost through hi and what OpenRouter counts on the key, and
// the network logs from where the watcher left off, and its own watcher
// runs the rules on it (team_watch_local.go). Until hi 2.6.32 it went to
// the team's hi server, which watched from outside.
//
// Spec: docs/specs/approved/hi_watch.md, and hi_team_ops.md (release 3).

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	teamWatchEvery = time.Minute
	// teamWatchCatchUp is how soon the next heartbeat goes while more of
	// the network log waits than one carries.
	teamWatchCatchUp = 5 * time.Second
	teamWatchWindow  = time.Hour
)

func teamWatchStatePath(team string) string { return filepath.Join(teamDir(team), "run", "watch.json") }
func teamSpendLogPath(team string) string   { return filepath.Join(teamDir(team), "run", "spend.log") }
func teamServeStartsPath(team string) string {
	return filepath.Join(teamDir(team), "run", "serve-starts.log")
}
func teamNetworkLogPath(team string) string {
	return filepath.Join(teamDir(team), "run", "proxy", "network.log")
}

// teamWatchState is run/watch.json.
type teamWatchState struct {
	Server  string    `json:"server,omitempty"`
	Sent    time.Time `json:"sent,omitzero"`
	Error   string    `json:"error,omitempty"`
	ErrorAt time.Time `json:"error_at,omitzero"`
	Kept    int64     `json:"network_kept"`
	// Told maps the server's incidents to the ones opened here.
	Told map[string]string `json:"told,omitempty"`
	// KeyUsage is what OpenRouter counted on the key, sampled each
	// heartbeat, for the last two hours.
	KeyUsage []watchSample `json:"key_usage,omitempty"`
	// OOM is each box's oom_kill count when last read, and Kills the kills
	// of the last day.
	OOM   map[string]int `json:"oom,omitempty"`
	Kills []time.Time    `json:"kills,omitempty"`
	// Advisories are what the server last matched against production's
	// bill, and Reported the last weekly report posted.
	Advisories     []apiAdvisory      `json:"advisories,omitempty"`
	AdvisoryCounts []apiAdvisoryCount `json:"advisory_counts,omitempty"`
	AdvisoriesAt   time.Time          `json:"advisories_at,omitzero"`
	Reported       string             `json:"reported,omitempty"`
	// KeyHash is the server-made key the team uses (release 4).
	KeyHash string `json:"key_hash,omitempty"`
	// Undeclared is the weekly look at production's box against its bill.
	Undeclared *apiUndeclared `json:"undeclared,omitempty"`
	// LogsKept is how far the server has each log besides the lead's.
	LogsKept map[string]int64 `json:"logs_kept,omitempty"`
}

var teamWatchStateMu sync.Mutex

func loadTeamWatchState(team string) teamWatchState {
	var state teamWatchState
	if data, err := os.ReadFile(teamWatchStatePath(team)); err == nil {
		json.Unmarshal(data, &state)
	}
	return state
}

func saveTeamWatchState(team string, state teamWatchState) error {
	if err := os.MkdirAll(filepath.Dir(teamWatchStatePath(team)), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	return writeFileAtomic(teamWatchStatePath(team), append(data, '\n'), 0o600)
}

var teamSpendMu sync.Mutex

// teamRecordSpend notes what one model call through the app's proxy cost.
func teamRecordSpend(team, slot string, cost float64) {
	if cost <= 0 {
		return
	}
	teamSpendMu.Lock()
	defer teamSpendMu.Unlock()
	file, err := os.OpenFile(teamSpendLogPath(team), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintf(file, "%s %s %s\n", time.Now().UTC().Format(time.RFC3339), slot, strconv.FormatFloat(cost, 'f', -1, 64))
}

// teamNoteServeStart notes a start of __serve, for how often systemd
// starts it again.
func teamNoteServeStart(team string) {
	path := teamServeStartsPath(team)
	var kept []string
	for _, line := range boxReadLines(path, 200) {
		if at, err := time.Parse(time.RFC3339, line); err == nil && time.Since(at) < 7*24*time.Hour {
			kept = append(kept, line)
		}
	}
	kept = append(kept, time.Now().UTC().Format(time.RFC3339))
	writeFileAtomic(path, []byte(strings.Join(kept, "\n")+"\n"), 0o600)
}

// teamSpendSince adds up what model calls cost through hi since from: the
// app's proxy (run/spend.log) and the lead's (spend lines in the network
// log, which Hindsight's calls go through too).
func teamSpendSince(team string, from time.Time) float64 {
	total := 0.0
	// marker is the second field a line must have: the network log has
	// every decision, and its spend lines say "spend".
	add := func(path, marker string) {
		file, err := os.Open(path)
		if err != nil {
			return
		}
		defer file.Close()
		// The network log only grows; its last few megabytes cover an hour.
		if info, err := file.Stat(); err == nil && info.Size() > 8<<20 {
			file.Seek(info.Size()-8<<20, io.SeekStart)
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 3 || marker != "" && fields[1] != marker {
				continue
			}
			at, err := time.Parse(time.RFC3339, fields[0])
			if err != nil || at.Before(from) {
				continue
			}
			if cost, err := strconv.ParseFloat(fields[2], 64); err == nil && cost > 0 {
				total += cost
			}
		}
	}
	add(teamSpendLogPath(team), "")
	add(teamNetworkLogPath(team), "spend")
	// Members' calls that fell back to OpenRouter (box_fallback.go).
	if entries, err := os.ReadDir(teamEventsDir(team)); err == nil {
		for _, entry := range entries {
			add(filepath.Join(teamEventsDir(team), entry.Name()), "spend")
		}
	}
	return total
}

// teamReadNetwork reads the network log's whole lines from offset from, at
// most max of them, and where they end. A log shorter than from was
// rotated, and starts again from 0.
func teamReadNetwork(path string, from int64, max int) (apiWatchNetwork, bool) {
	network := apiWatchNetwork{From: from, To: from, Lines: []string{}}
	file, err := os.Open(path)
	if err != nil {
		return network, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return network, false
	}
	if info.Size() < from {
		network.From, network.To, network.Rotated, from = 0, 0, true, 0
	}
	if _, err := file.Seek(from, io.SeekStart); err != nil {
		return network, false
	}
	reader := bufio.NewReaderSize(file, 64<<10)
	for len(network.Lines) < max {
		line, err := reader.ReadString('\n')
		if err != nil {
			break // a line not finished yet waits for the next heartbeat
		}
		network.To += int64(len(line))
		network.Lines = append(network.Lines, strings.TrimSuffix(line, "\n"))
	}
	more := network.To < info.Size()
	return network, more
}

// ---------------------------------------------------------------------------
// what the heartbeat says

// teamWatchProbe reads the machine; tests replace it.
type teamWatchProbe struct {
	state    func(container string) string // "running", "exited", or "" when there is none
	statfs   func(path string) (used, inodes float64, err error)
	graph    func() string
	oom      func(team string) map[string]int
	keyUsage func(team string) (float64, bool)
}

var teamWatchMachine = teamWatchProbe{
	state: func(container string) string {
		out, err := (&teamBroker{}).engineOutput("inspect", "-f", "{{.State.Status}}", container)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(out)
	},
	statfs: teamStatfs,
	graph: func() string {
		out, _ := (&teamBroker{}).engineOutput("info", "--format", "{{.Store.GraphRoot}}")
		return strings.TrimSpace(out)
	},
	oom:      teamOOMCounts,
	keyUsage: teamKeyUsage,
}

func teamStatfs(path string) (float64, float64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	used, inodes := 0.0, 0.0
	if stat.Blocks > 0 {
		used = 1 - float64(stat.Bavail)/float64(stat.Blocks)
	}
	if stat.Files > 0 {
		inodes = 1 - float64(stat.Ffree)/float64(stat.Files)
	}
	return used, inodes, nil
}

// teamOOMCounts is each of the team's running boxes' oom_kill count, from
// its cgroup's memory.events.
func teamOOMCounts(team string) map[string]int {
	counts := map[string]int{}
	b := &teamBroker{}
	out, err := b.engineOutput("ps", "--format", "{{.Names}}", "--filter", "name=hi-box-team-"+team)
	if err != nil {
		return counts
	}
	for _, name := range strings.Fields(out) {
		path, err := b.engineOutput("inspect", "-f", "{{.State.CgroupPath}}", name)
		if err != nil || strings.TrimSpace(path) == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/sys/fs/cgroup", strings.TrimSpace(path), "memory.events"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if value, ok := strings.CutPrefix(line, "oom_kill "); ok {
				counts[name], _ = strconv.Atoi(value)
			}
		}
	}
	return counts
}

// teamKeyUsage is what OpenRouter has counted on the team's own key, in
// dollars, all time. A team on this machine's key shares it with whatever
// else uses that key, so it has none of its own to compare.
func teamKeyUsage(team string) (float64, bool) {
	secrets, err := loadTeamSecrets(team)
	if err != nil || secrets.OpenRouterAPIKey == "" {
		return 0, false
	}
	request, _ := http.NewRequest("GET", teamOpenRouterUpstream+"/api/v1/key", nil)
	request.Header.Set("Authorization", "Bearer "+secrets.OpenRouterAPIKey)
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		return 0, false
	}
	defer response.Body.Close()
	var answer struct {
		Data struct {
			Usage *float64 `json:"usage"`
		} `json:"data"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&answer) != nil || answer.Data.Usage == nil {
		return 0, false
	}
	return *answer.Data.Usage, true
}

// heartbeat is what the team says now. It updates the watch state's
// samples (key usage, OOM kills), which the caller saves.
func (b *teamBroker) heartbeat(state *teamWatchState, probe teamWatchProbe, now time.Time) apiHeartbeat {
	beat := apiHeartbeat{Team: b.team, At: now.UTC(), Hi: version, Parts: map[string]apiWatchPart{}}
	if versions, err := loadTeamVersions(b.team); err == nil && versions.Production > 0 {
		beat.Production = fmt.Sprintf("v%d", versions.Production)
	}
	part := func(container string) apiWatchPart {
		switch probe.state(container) {
		case "running":
			return apiWatchPart{State: "up"}
		case "":
			return apiWatchPart{State: "down", Detail: "no box"}
		default:
			return apiWatchPart{State: "down", Detail: "its box isn't running"}
		}
	}
	beat.Parts["broker"] = apiWatchPart{State: "up"}
	beat.Parts["lead"] = part("hi-box-team-" + b.team)
	beat.Parts["proxy"] = part("hi-box-team-" + b.team + "-proxy")
	beat.Parts["gateway"] = teamGatewayPart(b.team, now)
	if memory := probe.state(teamMemoryContainer(b.team)); memory == "" {
		beat.Parts["memory"] = apiWatchPart{State: "off"}
	} else {
		beat.Parts["memory"] = part(teamMemoryContainer(b.team))
	}
	if slot, ok := loadTeamSlot(b.team, teamSlotProduction); ok {
		production := part(slot.Container)
		if production.State == "up" {
			jobs := loadTeamJobsState(b.team)
			if !jobs.Silent.IsZero() && now.Sub(jobs.Silent) >= teamAppSilentAfter {
				production = apiWatchPart{State: "down", Detail: "its box runs but hasn't answered since " + jobs.Silent.UTC().Format("15:04 UTC")}
			}
		}
		for _, incident := range loadTeamIncidents(b.team) {
			if incident.Kind == "app" && strings.HasPrefix(incident.Title, "Production's app stopped") {
				if now.Sub(incident.Opened) < time.Hour {
					production.Restarts1h++
				}
				if now.Sub(incident.Opened) < 24*time.Hour {
					production.Restarts24h++
				}
			}
		}
		beat.Parts["production"] = production
	}
	for _, line := range boxReadLines(teamServeStartsPath(b.team), 200) {
		if at, err := time.Parse(time.RFC3339, line); err == nil && now.Sub(at) < 24*time.Hour {
			beat.Restarts++
		}
	}
	beat.Restarts = max(beat.Restarts-1, 0) // the start that runs now isn't a restart
	if used, inodes, err := probe.statfs(teamDir(b.team)); err == nil {
		beat.Disk.Team, beat.Disk.Inodes = used, inodes
	}
	if graph := probe.graph(); graph != "" {
		if used, _, err := probe.statfs(graph); err == nil {
			beat.Disk.Containers = used
		}
	}
	// OOM kills: what each box's count went up by since the last look.
	counts := probe.oom(b.team)
	if state.OOM == nil {
		state.OOM = map[string]int{}
	}
	for name, count := range counts {
		if seen, ok := state.OOM[name]; ok && count > seen {
			for range count - seen {
				state.Kills = append(state.Kills, now.UTC())
			}
		}
	}
	state.OOM = map[string]int{}
	for name, count := range counts {
		state.OOM[name] = count
	}
	var kills []time.Time
	for _, at := range state.Kills {
		if now.Sub(at) < 24*time.Hour {
			kills = append(kills, at)
		}
	}
	state.Kills, beat.OOMKills = kills, len(kills)
	snapshots := loadTeamSnapshotStatus(b.team)
	if snapshots.Last != nil {
		beat.Snapshot.Last = snapshots.Last.Time
	}
	if config, err := loadTeam(b.team); err == nil && config.Backups != nil && config.Backups.Offsite != "" {
		beat.Snapshot.OffsiteSet, beat.Snapshot.Offsite = true, snapshots.Copied
	}
	for _, incident := range loadTeamIncidents(b.team) {
		if incident.Status != "closed" {
			beat.Incidents++
		}
	}
	// Spend through hi, and on the key, over the same window: up to an
	// hour, from the oldest sample of the key's usage within it.
	from := now.Add(-teamWatchWindow)
	if usage, ok := probe.keyUsage(b.team); ok {
		state.KeyUsage = append(state.KeyUsage, watchSample{At: now.UTC(), Value: usage})
		var kept []watchSample
		for _, sample := range state.KeyUsage {
			if now.Sub(sample.At) <= 2*teamWatchWindow {
				kept = append(kept, sample)
			}
		}
		state.KeyUsage = kept
		first := kept[len(kept)-1]
		for _, sample := range kept {
			if now.Sub(sample.At) <= teamWatchWindow {
				first = sample
				break
			}
		}
		if first.At.Before(now) {
			from = first.At
			spent := usage - first.Value
			beat.KeyUsage = &spent
		}
	} else {
		state.KeyUsage = nil
	}
	beat.Spend = teamSpendSince(b.team, from)
	beat.Window = int(now.Sub(from).Round(time.Minute) / time.Minute)
	return beat
}

// teamGatewayPart is the lead's gateway, from the state Hermes writes every
// few seconds: running, connected to Slack, and recent.
func teamGatewayPart(team string, now time.Time) apiWatchPart {
	var state struct {
		Gateway   string    `json:"gateway_state"`
		Updated   time.Time `json:"updated_at"`
		Platforms map[string]struct {
			State   string    `json:"state"`
			Error   string    `json:"error_message"`
			Updated time.Time `json:"updated_at"`
		} `json:"platforms"`
	}
	data, err := os.ReadFile(filepath.Join(teamDir(team), "lead", "gateway_state.json"))
	if err != nil || json.Unmarshal(data, &state) != nil {
		return apiWatchPart{State: "down", Detail: "no gateway state"}
	}
	part := apiWatchPart{State: "up", LastEvent: state.Updated}
	slackState := state.Platforms["slack"]
	switch {
	case state.Gateway != "running":
		part.State, part.Detail = "down", "the gateway is "+firstNonEmpty(state.Gateway, "not running")
	case now.Sub(state.Updated) > 2*time.Minute:
		part.State, part.Detail = "down", "the gateway hasn't written its state since "+state.Updated.UTC().Format("15:04 UTC")
	case slackState.State != "connected":
		part.State, part.Detail = "down", "Slack is "+firstNonEmpty(slackState.State, "not connected")
		if slackState.Error != "" {
			part.Detail += ": " + slackState.Error
		}
	}
	return part
}

// ---------------------------------------------------------------------------
// sending

// teamWatchClient is the hi server this machine is connected to, or nil.
// Tests replace it.
var teamWatchClient = func() (*serverClient, string) {
	connection, err := loadServerConnection()
	if err != nil || connection == nil || connection.URL == "" {
		return nil, ""
	}
	key, err := loadDeviceKey(false)
	if err != nil {
		return nil, ""
	}
	return newServerClient(connection.URL, key), connection.URL
}

// sendHeartbeat sends one heartbeat, with the network log from where the
// server has it, and opens or closes the incidents its answer tells of.
// It says whether more of the network log waits.
func (b *teamBroker) sendHeartbeat(stopping string) (bool, error) {
	if stopping != "" {
		// Nothing outside waits for a last word: a stopped team is the
		// outside heartbeat's to notice (team_heartbeat.go).
		return false, nil
	}
	teamWatchStateMu.Lock()
	state := loadTeamWatchState(b.team)
	now := time.Now()
	beat := b.heartbeat(&state, teamWatchMachine, now)
	beat.Stopping = stopping
	bomPath := ""
	if stopping == "" {
		// Production's bill, made the first time after a release.
		if path, version, err := teamProductionBom(b.team, teamBomTools); err == nil {
			bomPath, beat.Bom, beat.BomVersion = path, teamBomDigest(path), version
		}
		beat.Ignored = loadTeamAdvisoriesIgnored(b.team)
		beat.Undeclared = state.Undeclared
		beat.KeyHash = state.KeyHash
	}
	stop := loadTeamWatchStop(b.team)
	beat.Stop = apiWatchStop{Frozen: stop.Frozen, Paused: stop.Paused, Done: stop.Done}
	network, more := teamReadNetwork(teamNetworkLogPath(b.team), state.Kept, watchNetworkLinesMax)
	beat.Network = &network
	budget := watchNetworkLinesMax - len(network.Lines)
	for _, log := range teamWatchLogs(b.team) {
		if budget <= 0 {
			more = true
			break
		}
		lines, rest := teamReadNetwork(log[1], state.LogsKept[log[0]], budget)
		lines.Log = log[0]
		if len(lines.Lines) > 0 || lines.Rotated {
			beat.Logs = append(beat.Logs, lines)
			budget -= len(lines.Lines)
		}
		more = more || rest
	}
	beat.Exempt = teamWatchExempt(b.team)
	saveTeamWatchState(b.team, state)
	teamWatchStateMu.Unlock()

	reply, events, posts := b.localBeat(beat, bomPath, now)

	teamWatchStateMu.Lock()
	defer teamWatchStateMu.Unlock()
	state = loadTeamWatchState(b.team)
	if state.Server != "" {
		// The first check here after the hi server watched: what it told is
		// checked here now, and opens again if it still holds.
		for _, local := range state.Told {
			if b.incidentOpen(local) {
				b.recovered(local, "The team watches itself now, on its own machine; this opens again here if it still holds.")
			}
		}
		state.Told = nil
	}
	state.Server = ""
	state.Sent, state.Error, state.ErrorAt, state.Kept = now.UTC(), "", time.Time{}, reply.NetworkKept
	if state.LogsKept == nil {
		state.LogsKept = map[string]int64{}
	}
	for name, kept := range reply.LogsKept {
		state.LogsKept[name] = kept
	}
	if stopping == "" {
		b.tellLocal(&state, events)
		if b.channel != nil && len(posts) > 0 {
			if config, err := loadTeam(b.team); err == nil {
				for _, text := range posts {
					b.channel.post(config.opsChannel(), text)
				}
			}
		}
		state.Advisories, state.AdvisoryCounts, state.AdvisoriesAt = reply.Advisories, reply.Counts, now.UTC()
		if reply.Report != nil && reply.Report.ID != state.Reported && b.channel != nil {
			if config, err := loadTeam(b.team); err == nil {
				if _, err := b.channel.post(config.opsChannel(), reply.Report.Text); err == nil {
					state.Reported = reply.Report.ID
				}
			}
		}
	}
	saveTeamWatchState(b.team, state)
	if reply.Do != nil {
		// Confirmed in the next heartbeat.
		if err := b.carryOut(*reply.Do); err != nil {
			return false, fmt.Errorf("doing what was asked (%s): %w", reply.Do.Action, err)
		}
		more = true
	}
	return more && reply.NetworkKept == network.To, nil
}

// teamWatchLogs are the logs a heartbeat carries besides the lead's: the
// app boxes' lookups, and the dev box's network log.
func teamWatchLogs(team string) [][2]string {
	logs := [][2]string{}
	if fileExists(teamResolveLogPath(team)) {
		logs = append(logs, [2]string{"apps", teamResolveLogPath(team)})
	}
	if path := filepath.Join(teamDevDir(team), "net", "network.log"); fileExists(path) {
		logs = append(logs, [2]string{"box:" + teamDevBox(team), path})
	}
	return logs
}

// teamWatchExempt are the hosts big uploads may go to besides the model
// providers and GitHub: the snapshots' offsite bucket.
func teamWatchExempt(team string) []string {
	config, err := loadTeam(team)
	if err != nil || config.Backups == nil || config.Backups.Offsite == "" {
		return nil
	}
	if parsed, err := url.Parse(strings.TrimPrefix(config.Backups.Offsite, "s3:")); err == nil && parsed.Hostname() != "" {
		return []string{parsed.Hostname()}
	}
	return nil
}

// lookAtProduction compares production's box with its bill once a week.
func (b *teamBroker) lookAtProduction() {
	teamWatchStateMu.Lock()
	state := loadTeamWatchState(b.team)
	teamWatchStateMu.Unlock()
	if state.Undeclared != nil && time.Since(state.Undeclared.Checked) < 7*24*time.Hour {
		return
	}
	undeclared, err := teamUndeclared(b.team)
	if err != nil {
		return
	}
	teamWatchStateMu.Lock()
	defer teamWatchStateMu.Unlock()
	state = loadTeamWatchState(b.team)
	state.Undeclared = undeclared
	saveTeamWatchState(b.team, state)
}

var errTeamNotWatched = errors.New("this machine isn't connected to a hi server (hi connect), so nothing watches the team from outside")

// tellWatched opens an incident here for each of the server's the lead can
// act on, once, and closes it when the server has.
func (b *teamBroker) tellWatched(state *teamWatchState, tell []apiWatchIncident) {
	if state.Told == nil {
		state.Told = map[string]string{}
	}
	open := map[string]bool{}
	for _, incident := range tell {
		open[incident.ID] = true
		if _, told := state.Told[incident.ID]; told {
			continue
		}
		local, err := b.openIncident(teamIncident{Kind: "watch", Slot: teamSlotProduction, Title: incident.Title, Detail: incident.Detail, Opened: time.Now().UTC()},
			incident.Title+".\nThe team's watcher noticed it; the lead looks into it here.", "", teamIncidentPrompt)
		if err != nil {
			continue
		}
		state.Told[incident.ID] = local.ID
	}
	for id, local := range state.Told {
		if open[id] {
			continue
		}
		if b.incidentOpen(local) {
			b.recovered(local, "The hi server says it is resolved.")
		}
		delete(state.Told, id)
	}
}

// keepWatched sends a heartbeat every minute until done, sooner while the
// network log has more than one carries.
func (b *teamBroker) keepWatched(done <-chan struct{}, stderr io.Writer) {
	failing := false
	for {
		more, err := b.sendHeartbeat("")
		switch {
		case errors.Is(err, errTeamNotWatched):
		case err != nil && !failing:
			fmt.Fprintf(stderr, "hi: the team's heartbeat to its hi server: %v\n", err)
			failing = true
		case err == nil && failing:
			fmt.Fprintln(stderr, "hi: the team's heartbeat reaches its hi server again")
			failing = false
		}
		b.lookAtProduction()
		wait := teamWatchEvery
		if more {
			wait = teamWatchCatchUp
		}
		select {
		case <-done:
			return
		case <-time.After(wait):
		}
	}
}

// teamWatchLine is what hi team status says about the watcher.
func teamWatchLine(team string) string {
	state := loadTeamWatchState(team)
	if state.Sent.IsZero() {
		return "Watcher: on this machine, from the team's next start; it checks every minute."
	}
	return fmt.Sprintf("Watcher: on this machine, last checked %s ago.", teamAgo(time.Since(state.Sent)))
}
