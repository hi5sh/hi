package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The preview's model calls have a daily limit
// (docs/specs/approved/hi_team_data_fork.md): a preview runs on a fork of
// production's data and doesn't redo production's paid work, and a
// worker left running there can't spend more than the limit a day.
// OpenRouter puts what each call cost in its answer's usage.cost, last in
// a streamed one; the preview's models socket adds it up.

// teamPreviewModelUSD is the preview's daily limit when the team set none.
const teamPreviewModelUSD = 5.0

// teamPreviewModelLimit is the preview's daily limit in dollars; 0 for
// none.
func teamPreviewModelLimit(team string) float64 {
	config, err := loadTeam(team)
	if err != nil {
		return teamPreviewModelUSD
	}
	switch {
	case config.PreviewModelUSD < 0:
		return 0
	case config.PreviewModelUSD == 0:
		return teamPreviewModelUSD
	}
	return config.PreviewModelUSD
}

// teamPreviewSpend is run/models-preview.json: what the preview's model
// calls cost on one day (UTC).
type teamPreviewSpend struct {
	Day   string  `json:"day"`
	USD   float64 `json:"usd"`
	Calls int     `json:"calls"`
}

var teamPreviewSpendMu sync.Mutex

func teamPreviewSpendPath(team string) string {
	return filepath.Join(teamDir(team), "run", "models-preview.json")
}

// loadTeamPreviewSpend is today's; a record from another day is none.
func loadTeamPreviewSpend(team string) teamPreviewSpend {
	today := time.Now().UTC().Format(time.DateOnly)
	var spend teamPreviewSpend
	data, err := os.ReadFile(teamPreviewSpendPath(team))
	if err != nil || json.Unmarshal(data, &spend) != nil || spend.Day != today {
		return teamPreviewSpend{Day: today}
	}
	return spend
}

func teamAddPreviewSpend(team string, cost float64) {
	teamPreviewSpendMu.Lock()
	defer teamPreviewSpendMu.Unlock()
	spend := loadTeamPreviewSpend(team)
	spend.USD += cost
	spend.Calls++
	data, _ := json.Marshal(spend)
	writeFileAtomic(teamPreviewSpendPath(team), append(data, '\n'), 0o600)
}

// teamModelsRefuse answers a model call hi doesn't pass on, in the shape
// OpenRouter's errors have, so the app's client shows the message.
func teamModelsRefuse(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": http.StatusTooManyRequests, "message": message}})
}

// teamCostPattern finds usage.cost in an answer, and not a "cost" in the
// model's own text, where its quotes are escaped.
var teamCostPattern = regexp.MustCompile(`(?:^|[^\\])"cost"\s*:\s*([0-9.eE+-]+)`)

// teamCostTail is how much of an answer's end is kept to find its cost:
// usage comes last, in a streamed answer too.
const teamCostTail = 64 << 10

// teamCostReader passes an answer on and, when it ends, gives done what
// it cost: the last usage.cost in it, 0 when there is none.
type teamCostReader struct {
	body io.ReadCloser
	tail []byte
	done func(float64)
	once sync.Once
}

func (r *teamCostReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	r.tail = append(r.tail, p[:n]...)
	if len(r.tail) > 2*teamCostTail {
		r.tail = append([]byte(nil), r.tail[len(r.tail)-teamCostTail:]...)
	}
	if err == io.EOF {
		r.finish()
	}
	return n, err
}

func (r *teamCostReader) Close() error {
	r.finish()
	return r.body.Close()
}

func (r *teamCostReader) finish() {
	r.once.Do(func() { r.done(teamAnswerCost(r.tail)) })
}

func teamAnswerCost(answer []byte) float64 {
	matches := teamCostPattern.FindAllSubmatch(answer, -1)
	if len(matches) == 0 {
		return 0
	}
	cost, err := strconv.ParseFloat(string(matches[len(matches)-1][1]), 64)
	if err != nil || cost < 0 {
		return 0
	}
	return cost
}

// The boxes whose memory a team may set: the dev box, and the app's
// boxes, Stage's and production's.
const (
	teamBoxDev = "dev"
	teamBoxApp = "app"
	// teamAppMemory is the app's boxes' memory unless the team has its own.
	teamAppMemory = "4g"
)

var teamMemoryPattern = regexp.MustCompile(`^([1-9][0-9]*)([mg])$`)

// teamBoxMemory is a box's memory limit: the team's own, or hi's.
func teamBoxMemory(config teamConfig, box string) string {
	if size := config.BoxMemory[box]; size != "" {
		return size
	}
	if box == teamBoxDev {
		return teamDevMemory
	}
	return teamAppMemory
}

// teamMemoryBytes is a limit such as 24g in bytes.
func teamMemoryBytes(size string) int64 {
	match := teamMemoryPattern.FindStringSubmatch(size)
	if match == nil {
		return 0
	}
	n, _ := strconv.ParseInt(match[1], 10, 64)
	if match[2] == "g" {
		return n << 30
	}
	return n << 20
}

// teamUpdateMemory gives a team's running boxes of a kind their limit now,
// without a restart, and says which; tests replace it.
var teamUpdateMemory = func(team, box, size string) ([]string, error) {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return nil, err
	}
	out, err := engine.output("ps", "--format", "{{.Names}}", "--filter", "name=hi-box-team-"+team+"-")
	if err != nil {
		return nil, err
	}
	var updated []string
	for _, name := range strings.Fields(out) {
		mine := name == teamDevContainer(team)
		if box == teamBoxApp {
			mine = strings.HasPrefix(name, teamPreviewContainer(team)+"-")
		}
		if !mine {
			continue
		}
		if _, err := engine.output("update", "--memory", size, "--memory-swap", fmt.Sprintf("%dm", 2*teamMemoryBytes(size)>>20), name); err != nil {
			return updated, fmt.Errorf("%s: %w", name, err)
		}
		updated = append(updated, name)
	}
	return updated, nil
}

// teamMemoryCommand is hi team limit <name> memory [dev|app <size>|default]:
// the boxes' memory, which a box that runs out of it is killed for.
func teamMemoryCommand(config teamConfig, args []string, stdout io.Writer) error {
	if len(args) == 2 {
		box, size := args[0], strings.ToLower(args[1])
		if box != teamBoxDev && box != teamBoxApp {
			return fmt.Errorf("the boxes are dev (the dev box) and app (Stage's and production's), not %q", box)
		}
		if size == "default" {
			delete(config.BoxMemory, box)
		} else {
			bytes := teamMemoryBytes(size)
			if bytes < 1<<30 {
				return fmt.Errorf("%q isn't a memory size of 1g or more, such as 24g or 2048m", args[1])
			}
			if total := teamMachineMemory(); total > 0 && bytes > total*3/4 {
				return fmt.Errorf("%s is more than three quarters of this machine's %d GB; leave room for the other boxes and the machine", size, total>>30)
			}
			if config.BoxMemory == nil {
				config.BoxMemory = map[string]string{}
			}
			config.BoxMemory[box] = size
		}
		if err := saveTeam(config); err != nil {
			return err
		}
		updated, err := teamUpdateMemory(config.Name, box, teamBoxMemory(config, box))
		if err != nil {
			fmt.Fprintf(stdout, "hi couldn't change a running box's limit (%v); it gets it when it starts again.\n", err)
		} else if len(updated) > 0 {
			fmt.Fprintf(stdout, "Running now with it, without a restart: %s.\n", strings.Join(updated, ", "))
		}
	} else if len(args) != 0 {
		return usageError{"usage: hi team limit <name> memory [dev|app <size>|default]"}
	}
	fmt.Fprintf(stdout, "Team %s's dev box may use %s of memory, and each of its app's boxes, Stage's and production's, %s; a box that needs more has a process killed (an incident says so).\n",
		config.Name, teamBoxMemory(config, teamBoxDev), teamBoxMemory(config, teamBoxApp))
	return nil
}

// teamMachineMemory is this machine's memory, in bytes, or 0.
var teamMachineMemory = func() int64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			kb, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(value), " kB"), 10, 64)
			return kb << 10
		}
	}
	return 0
}

// teamLimitCommand is hi team limit <name> [preview <usd> | preview off],
// what Stage's model calls may cost a day, and hi team limit <name> memory,
// the boxes' memory.
func teamLimitCommand(args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi team limit <name> [stage <dollars a day> | stage off | memory [dev|app <size>|default]]"}
	if len(args) >= 2 && args[1] == "memory" {
		config, err := loadTeam(args[0])
		if err != nil {
			return err
		}
		return teamMemoryCommand(config, args[2:], stdout)
	}
	if len(args) != 1 && (len(args) != 3 || args[1] != "preview") {
		return usage
	}
	team := args[0]
	config, err := loadTeam(team)
	if err != nil {
		return err
	}
	if len(args) == 3 {
		value := strings.TrimPrefix(args[2], "$")
		if value == "off" {
			config.PreviewModelUSD = -1
		} else if usd, err := strconv.ParseFloat(value, 64); err != nil || usd <= 0 {
			return fmt.Errorf("%q isn't an amount of dollars a day, or off", args[2])
		} else {
			config.PreviewModelUSD = usd
		}
		if err := saveTeam(config); err != nil {
			return err
		}
	}
	spend := loadTeamPreviewSpend(team)
	if limit := teamPreviewModelLimit(team); limit > 0 {
		fmt.Fprintf(stdout, "Team %s's preview may spend $%.2f a day on model calls; today it spent $%.2f in %d call(s). Production has no limit from hi.\n", team, limit, spend.USD, spend.Calls)
	} else {
		fmt.Fprintf(stdout, "Team %s's preview has no daily limit on model calls; today it spent $%.2f in %d call(s).\n", team, spend.USD, spend.Calls)
	}
	return nil
}
