package cli

// An app's boxes, for its Overview on the admin page: each container the
// app has on this machine, running or not, what it's for, its state,
// image, and limits. Its CPU and memory now come with the page's usage
// (admin_usage.go), every few seconds; this is what changes less often.

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// adminBox is one of an app's boxes, as Overview shows it.
type adminBox struct {
	Name     string    `json:"name"`           // the container's
	What     string    `json:"what"`           // as people say it: "production's box"
	Does     string    `json:"does,omitempty"` // what it's for
	Term     string    `json:"term,omitempty"` // its box in the Terminal section, when it has one
	State    string    `json:"state"`          // the engine's: running, exited, created, …
	Started  time.Time `json:"started,omitzero"`
	Finished time.Time `json:"finished,omitzero"` // when it stopped, for one that doesn't run
	ExitCode int       `json:"exit_code,omitempty"`
	Image    string    `json:"image,omitempty"`
	Restarts int       `json:"restarts,omitempty"`
	// MemoryMax is its memory limit, 0 for none; MemoryPeak the most it
	// used since it started, where the kernel says.
	MemoryMax  uint64  `json:"memory_max,omitempty"`
	MemoryPeak uint64  `json:"memory_peak,omitempty"`
	CPUs       float64 `json:"cpus,omitempty"` // its CPU limit in cores, 0 for none
	PIDs       uint64  `json:"pids,omitempty"`
	OOMKills   int     `json:"oom_kills,omitempty"`
	// Production's and Stage's: the version, its commit, and the command
	// that serves it on its port.
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Command string `json:"command,omitempty"`
	Port    int    `json:"port,omitempty"`
}

// adminInspected is what the box engine's inspect says of a container,
// Podman's and Docker's alike (Docker has no CgroupPath).
type adminInspected struct {
	Name         string
	RestartCount int
	Config       struct{ Image string }
	State        struct {
		Status     string
		StartedAt  time.Time
		FinishedAt time.Time
		ExitCode   int
		CgroupPath string
	}
	HostConfig struct {
		Memory   int64
		NanoCpus int64
	}
}

// adminInspectBoxes inspects the containers whose names pass keep, all of
// them, running or not. Tests replace it.
var adminInspectBoxes = func(keep func(string) bool) []adminInspected {
	b := &appBroker{}
	out, err := b.engineOutput("ps", "-a", "--format", "{{.Names}}")
	if err != nil {
		return nil
	}
	var names []string
	for _, name := range strings.Fields(out) {
		if keep(strings.TrimPrefix(name, "/")) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	out, err = b.engineOutput(append([]string{"inspect", "--type", "container"}, names...)...)
	if err != nil {
		return nil
	}
	var inspected []adminInspected
	if json.Unmarshal([]byte(out), &inspected) != nil {
		return nil
	}
	return inspected
}

// adminBoxesCache keeps each app's boxes a few seconds: Overview asks
// every three, and inspect isn't free.
var adminBoxesCache = struct {
	sync.Mutex
	at    map[string]time.Time
	boxes map[string][]adminBox
}{at: map[string]time.Time{}, boxes: map[string][]adminBox{}}

const adminBoxesFresh = 10 * time.Second

// adminBoxesForget empties the cache, for tests.
func adminBoxesForget() {
	adminBoxesCache.Lock()
	adminBoxesCache.at, adminBoxesCache.boxes = map[string]time.Time{}, map[string][]adminBox{}
	adminBoxesCache.Unlock()
}

// adminAppBoxes are an app's boxes, the ones people think of first first:
// the lead, production, Stage, the dev box, then what helps them.
func adminAppBoxes(app string) []adminBox {
	adminBoxesCache.Lock()
	if at, ok := adminBoxesCache.at[app]; ok && time.Since(at) < adminBoxesFresh {
		boxes := adminBoxesCache.boxes[app]
		adminBoxesCache.Unlock()
		return boxes
	}
	adminBoxesCache.Unlock()

	apps := adminAppNames()
	tasks := "hi-box-" + app + "-t-"
	inspected := adminInspectBoxes(func(name string) bool {
		return adminBoxApp(name, apps) == app || strings.HasPrefix(name, tasks)
	})
	production, _ := loadTeamSlot(app, appSlotProduction)
	stage, _ := loadTeamSlot(app, appSlotPreview)
	boxes := []adminBox{}
	ranks := map[string]int{}
	for _, c := range inspected {
		name := strings.TrimPrefix(c.Name, "/")
		box := adminBox{Name: name, State: c.State.Status, Image: c.Config.Image, Restarts: c.RestartCount, ExitCode: c.State.ExitCode}
		if !c.State.StartedAt.IsZero() && c.State.StartedAt.Year() > 1 {
			box.Started = c.State.StartedAt.UTC()
		}
		if box.State != "running" && c.State.FinishedAt.Year() > 1 {
			box.Finished = c.State.FinishedAt.UTC()
		}
		if c.HostConfig.Memory > 0 {
			box.MemoryMax = uint64(c.HostConfig.Memory)
		}
		if c.HostConfig.NanoCpus > 0 {
			box.CPUs = float64(c.HostConfig.NanoCpus) / 1e9
		}
		if box.State == "running" && c.State.CgroupPath != "" {
			box.MemoryPeak = adminReadUint(adminCgroupPath(c.State.CgroupPath, "memory.peak"))
			box.PIDs = adminReadUint(adminCgroupPath(c.State.CgroupPath, "pids.current"))
			box.OOMKills = adminReadOOMKills(c.State.CgroupPath)
			if box.MemoryMax == 0 {
				box.MemoryMax = adminReadUint(adminCgroupPath(c.State.CgroupPath, "memory.max")) // "max" reads as 0
			}
		}
		ranks[name] = adminDescribeBox(app, &box, production, stage)
		boxes = append(boxes, box)
	}
	sort.SliceStable(boxes, func(i, j int) bool {
		if ranks[boxes[i].Name] != ranks[boxes[j].Name] {
			return ranks[boxes[i].Name] < ranks[boxes[j].Name]
		}
		return boxes[i].Name < boxes[j].Name
	})

	adminBoxesCache.Lock()
	adminBoxesCache.at[app], adminBoxesCache.boxes[app] = time.Now(), boxes
	adminBoxesCache.Unlock()
	return boxes
}

// adminDescribeBox says what a box of app's is, and where it goes in the
// list.
func adminDescribeBox(app string, box *adminBox, production, stage appPreview) int {
	lead := appBoxPrefix + app
	slot := func(slot appPreview) {
		box.Version, box.Commit, box.Command, box.Port = slot.Label, shortCommit(slot.Commit), slot.Command, slot.Port
	}
	switch name := box.Name; {
	case name == lead:
		box.What, box.Term, box.Does = "the lead's box", "lead", "The lead: it answers in the app's channel and hands the work on"
		return 0
	case production.Container != "" && name == production.Container:
		box.What, box.Term, box.Does = "production's box", "production", "Production: what the app's people use"
		slot(production)
		return 1
	case stage.Container != "" && name == stage.Container:
		box.What, box.Term, box.Does = "Stage's box", "stage", "Stage: the next version, before it's released"
		slot(stage)
		return 2
	case appSlotContainerPattern.MatchString(name) && strings.Contains(name, "-app-production-"):
		box.What, box.Does = "an earlier production box", "Production as it ran before; hi no longer uses it"
		return 3
	case appSlotContainerPattern.MatchString(name):
		box.What, box.Does = "an earlier Stage box", "Stage as it ran before; hi no longer uses it"
		return 3
	case name == appDevContainer(app):
		box.What, box.Term, box.Does = "the dev box", "dev", "The dev page's chat, terminal, and files"
		return 4
	case name == appMemoryContainer(app):
		box.What, box.Term, box.Does = "the memory's box", "memory", "Hindsight, the app's long-term memory"
		return 5
	case name == lead+"-proxy":
		box.What, box.Does = "the lead's proxy", "The lead's way out: only to the hosts on its list"
		return 6
	case name == appDevContainer(app)+"-proxy":
		box.What, box.Does = "the dev box's proxy", "The dev box's way out: only to the hosts on its list"
		return 7
	case name == appDevNetContainer(app):
		box.What, box.Does = "the NetBird peer", "The app on NetBird: its addresses and the dev page go through it"
		return 8
	case strings.HasPrefix(name, "hi-box-"+app+"-t-") && strings.HasSuffix(name, "-proxy"):
		box.What, box.Does = "a task box's proxy", "A task box's way out"
		return 10
	case strings.HasPrefix(name, "hi-box-"+app+"-t-"):
		box.What, box.Does = "a task box", "An agent's work on a task, apart from the rest"
		return 9
	}
	box.What = box.Name
	return 11
}

// adminReadOOMKills is how many processes a cgroup's memory limit killed.
func adminReadOOMKills(group string) int {
	data, err := os.ReadFile(adminCgroupPath(group, "memory.events"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "oom_kill "); ok {
			n, _ := strconv.Atoi(strings.TrimSpace(value))
			return n
		}
	}
	return 0
}
