package cli

// What the machine and its apps use, for the admin page's Home: the
// machine's CPU, memory, swap, and disks, and each app's CPU and memory
// (its lead's service and its boxes together) and its folder on disk.
// The page samples every few seconds while someone has it open, and
// stops a little after the last one closes it.

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// adminLoad is the machine's, and its apps', as Home shows them.
type adminLoad struct {
	At        time.Time                 `json:"at"`
	Cores     int                       `json:"cores"`
	CPU       *float64                  `json:"cpu,omitempty"` // the share of all cores busy, 0 to 1; none until two samples
	Load      [3]float64                `json:"load"`
	MemTotal  uint64                    `json:"mem_total"`
	MemUsed   uint64                    `json:"mem_used"`
	SwapTotal uint64                    `json:"swap_total"`
	SwapUsed  uint64                    `json:"swap_used"`
	Disks     []adminDisk               `json:"disks"`
	Uptime    float64                   `json:"uptime"` // seconds
	Apps      map[string]*adminAppUsage `json:"apps"`
}

type adminDisk struct {
	Mount string `json:"mount"`
	Used  uint64 `json:"used"`
	Total uint64 `json:"total"`
}

type adminAppUsage struct {
	CPU    *float64  `json:"cpu,omitempty"` // its share of the machine's cores, 0 to 1
	Memory uint64    `json:"memory"`
	Boxes  int       `json:"boxes"`
	Disk   int64     `json:"disk"` // its folder's bytes; -1 until measured
	DiskAt time.Time `json:"disk_at,omitzero"`
	// PerBox is each running box's own, by container name.
	PerBox map[string]*adminBoxUsage `json:"per_box,omitempty"`
}

// adminBoxUsage is one box's CPU and memory now.
type adminBoxUsage struct {
	CPU    *float64 `json:"cpu,omitempty"` // its share of the machine's cores, 0 to 1
	Memory uint64   `json:"memory"`
}

// These are replaced in tests.
var (
	// adminProcRoot holds proc/ and sys/fs/cgroup/.
	adminProcRoot = "/"
	// adminBoxCgroups is each running box's cgroup, by container name,
	// relative to /sys/fs/cgroup.
	adminBoxCgroups = adminPodmanCgroups
	// adminFolderSize is a folder's size in bytes, -1 if it can't tell.
	adminFolderSize = adminDu
	adminUsageEvery = 5 * time.Second
	adminDiskEvery  = 10 * time.Minute
)

// adminSampler keeps the latest sample and what the next one needs.
type adminSampler struct {
	mu       sync.Mutex
	latest   *adminLoad
	asked    time.Time
	running  bool
	cpuTotal uint64
	cpuIdle  uint64
	apps     map[string]uint64 // each app's CPU time so far, microseconds
	perBox   map[string]uint64 // each box's, by container name
	at       time.Time
	disk     map[string]int64
	diskAt   time.Time
	measured bool // a folder measuring is under way
	boxes    map[string]string
	boxesAt  time.Time
}

// usage is the latest sample, and keeps sampling for a while: the page
// asks every few seconds while it's open.
func (s *adminServer) usage() *adminLoad {
	sampler := &s.sampler
	sampler.mu.Lock()
	sampler.asked = time.Now()
	start := !sampler.running
	sampler.running = true
	latest := sampler.latest
	sampler.mu.Unlock()
	if start {
		if latest == nil {
			s.sample()
			sampler.mu.Lock()
			latest = sampler.latest
			sampler.mu.Unlock()
		}
		go s.keepSampling(adminUsageEvery) // read now: tests put it back after
	}
	return latest
}

func (s *adminServer) keepSampling(every time.Duration) {
	sampler := &s.sampler
	for {
		time.Sleep(every)
		sampler.mu.Lock()
		if time.Since(sampler.asked) > 2*time.Minute {
			sampler.running = false
			sampler.mu.Unlock()
			return
		}
		sampler.mu.Unlock()
		s.sample()
	}
}

// sample reads the machine and its apps once.
func (s *adminServer) sample() {
	sampler := &s.sampler
	now := time.Now()
	usage := &adminLoad{At: now.UTC(), Cores: runtime.NumCPU(), Apps: map[string]*adminAppUsage{}}
	total, idle := adminReadCPU()
	usage.Load = adminReadLoad()
	usage.MemTotal, usage.MemUsed, usage.SwapTotal, usage.SwapUsed = adminReadMemory()
	usage.Uptime = adminReadUptime()

	names := adminAppNames()
	usage.Disks = adminDisks(names)

	// Each app: its service's cgroup and its boxes'.
	sampler.mu.Lock()
	if sampler.boxes == nil || now.Sub(sampler.boxesAt) > 30*time.Second {
		sampler.mu.Unlock()
		boxes := adminBoxCgroups()
		sampler.mu.Lock()
		sampler.boxes, sampler.boxesAt = boxes, now
	}
	boxes := sampler.boxes
	sampler.mu.Unlock()
	base := adminUserManagerCgroup()
	cpuTimes := map[string]uint64{}
	boxTimes := map[string]uint64{}
	for _, name := range names {
		var groups []string
		if base != "" {
			for _, unit := range []string{appUnitName(name), legacyAppUnit(name)} {
				group := filepath.Join(base, "app.slice", unit)
				if fileExists(adminCgroupPath(group)) {
					groups = append(groups, group)
					break
				}
			}
		}
		app := &adminAppUsage{Disk: -1, PerBox: map[string]*adminBoxUsage{}}
		var cpu uint64
		for _, group := range groups { // its lead's service
			app.Memory += adminReadUint(adminCgroupPath(group, "memory.current"))
			cpu += adminReadCgroupCPU(group)
		}
		for box, group := range boxes {
			if adminBoxApp(box, names) != name {
				continue
			}
			app.Boxes++
			memory, boxCPU := adminReadUint(adminCgroupPath(group, "memory.current")), adminReadCgroupCPU(group)
			app.PerBox[box] = &adminBoxUsage{Memory: memory}
			app.Memory += memory
			cpu += boxCPU
			boxTimes[box] = boxCPU
		}
		cpuTimes[name] = cpu
		usage.Apps[name] = app
	}

	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	if !sampler.at.IsZero() {
		if dt := total - sampler.cpuTotal; total > sampler.cpuTotal {
			busy := 1 - float64(idle-sampler.cpuIdle)/float64(dt)
			busy = min(max(busy, 0), 1)
			usage.CPU = &busy
		}
		wall := now.Sub(sampler.at).Microseconds() * int64(usage.Cores)
		for name, cpu := range cpuTimes {
			if before, ok := sampler.apps[name]; ok && wall > 0 && cpu >= before {
				share := min(float64(cpu-before)/float64(wall), 1)
				usage.Apps[name].CPU = &share
			}
		}
		for _, app := range usage.Apps {
			for box, use := range app.PerBox {
				if before, ok := sampler.perBox[box]; ok && wall > 0 && boxTimes[box] >= before {
					share := min(float64(boxTimes[box]-before)/float64(wall), 1)
					use.CPU = &share
				}
			}
		}
	}
	sampler.cpuTotal, sampler.cpuIdle, sampler.apps, sampler.perBox, sampler.at = total, idle, cpuTimes, boxTimes, now
	for name, app := range usage.Apps {
		if size, ok := sampler.disk[name]; ok {
			app.Disk, app.DiskAt = size, sampler.diskAt
		}
	}
	sampler.latest = usage
	if !sampler.measured && (sampler.diskAt.IsZero() || now.Sub(sampler.diskAt) > adminDiskEvery) {
		sampler.measured = true
		go s.measureFolders(names, adminFolderSize)
	}
}

// measureFolders measures each app's folder, which can take a while.
func (s *adminServer) measureFolders(names []string, folderSize func(string) int64) {
	sizes := map[string]int64{}
	for _, name := range names {
		sizes[name] = folderSize(appDir(name))
	}
	sampler := &s.sampler
	sampler.mu.Lock()
	sampler.disk, sampler.diskAt, sampler.measured = sizes, time.Now().UTC(), false
	if sampler.latest != nil {
		// A new sample, never the one handed out: the page encodes that
		// without the lock.
		latest := *sampler.latest
		latest.Apps = make(map[string]*adminAppUsage, len(sampler.latest.Apps))
		for name, app := range sampler.latest.Apps {
			copied := *app
			if size, ok := sizes[name]; ok {
				copied.Disk, copied.DiskAt = size, sampler.diskAt
			}
			latest.Apps[name] = &copied
		}
		sampler.latest = &latest
	}
	sampler.mu.Unlock()
}

func adminAppNames() []string {
	entries, _ := os.ReadDir(appsDir())
	var names []string
	for _, entry := range entries {
		if appNamePattern.MatchString(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	return names
}

// adminBoxApp is the app a box belongs to: hi-box-app-<app>, or
// hi-box-app-<app>-<what>, the longest app name that fits.
func adminBoxApp(box string, apps []string) string {
	rest, ok := strings.CutPrefix(box, appBoxPrefix)
	if !ok {
		return ""
	}
	best := ""
	for _, app := range apps {
		if (rest == app || strings.HasPrefix(rest, app+"-")) && len(app) > len(best) {
			best = app
		}
	}
	return best
}

// appBoxPrefix starts every app's box's name.
const appBoxPrefix = "hi-box-app-"

func adminCgroupPath(group string, file ...string) string {
	return filepath.Join(append([]string{adminProcRoot, "sys", "fs", "cgroup", group}, file...)...)
}

// adminUserManagerCgroup is the account's systemd --user manager's cgroup,
// such as /user.slice/user-1000.slice/user@1000.service, from this
// process's own: the admin page runs as one of its services.
func adminUserManagerCgroup() string {
	data, err := os.ReadFile(filepath.Join(adminProcRoot, "proc", "self", "cgroup"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		segments := strings.Split(parts[2], "/")
		for i, segment := range segments {
			if strings.HasPrefix(segment, "user@") && strings.HasSuffix(segment, ".service") {
				return strings.Join(segments[:i+1], "/")
			}
		}
	}
	return ""
}

func adminReadUint(path string) uint64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	value, _ := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	return value
}

func adminReadCgroupCPU(group string) uint64 {
	data, err := os.ReadFile(adminCgroupPath(group, "cpu.stat"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "usage_usec "); ok {
			usec, _ := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			return usec
		}
	}
	return 0
}

// adminReadCPU is the machine's CPU time so far, all of it and idle, from
// /proc/stat's first line.
func adminReadCPU() (total, idle uint64) {
	file, err := os.Open(filepath.Join(adminProcRoot, "proc", "stat"))
	if err != nil {
		return 0, 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0, 0
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0
	}
	for i, field := range fields[1:] {
		if i >= 8 { // guest time is counted in user already
			break
		}
		value, _ := strconv.ParseUint(field, 10, 64)
		total += value
		if i == 3 || i == 4 { // idle, iowait
			idle += value
		}
	}
	return total, idle
}

func adminReadLoad() [3]float64 {
	var load [3]float64
	data, err := os.ReadFile(filepath.Join(adminProcRoot, "proc", "loadavg"))
	if err != nil {
		return load
	}
	fields := strings.Fields(string(data))
	for i := 0; i < 3 && i < len(fields); i++ {
		load[i], _ = strconv.ParseFloat(fields[i], 64)
	}
	return load
}

// adminReadMemory is memory and swap, total and used, in bytes: used is
// what isn't available, as free counts it.
func adminReadMemory() (memTotal, memUsed, swapTotal, swapUsed uint64) {
	data, err := os.ReadFile(filepath.Join(adminProcRoot, "proc", "meminfo"))
	if err != nil {
		return
	}
	values := map[string]uint64{}
	for _, line := range strings.Split(string(data), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		kb, _ := strconv.ParseUint(fields[0], 10, 64)
		values[name] = kb * 1024
	}
	memTotal, swapTotal = values["MemTotal"], values["SwapTotal"]
	if available, ok := values["MemAvailable"]; ok && available <= memTotal {
		memUsed = memTotal - available
	}
	if free := values["SwapFree"]; free <= swapTotal {
		swapUsed = swapTotal - free
	}
	return
}

func adminReadUptime() float64 {
	data, err := os.ReadFile(filepath.Join(adminProcRoot, "proc", "uptime"))
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	seconds, _ := strconv.ParseFloat(fields[0], 64)
	return seconds
}

// adminDisks are the filesystems that hold the system, the account's
// home (and so its boxes), and each app's folder, once each, by mount
// point.
func adminDisks(apps []string) []adminDisk {
	home, _ := os.UserHomeDir()
	paths := []string{"/", home}
	for _, app := range apps {
		paths = append(paths, appDir(app))
	}
	mounts := adminMounts()
	seen := map[string]bool{}
	disks := []adminDisk{}
	for _, path := range paths {
		if path == "" {
			continue
		}
		mount := adminMountOf(path, mounts)
		if seen[mount] {
			continue
		}
		seen[mount] = true
		var stat syscall.Statfs_t
		if syscall.Statfs(mount, &stat) != nil || stat.Blocks == 0 {
			continue
		}
		total := stat.Blocks * uint64(stat.Bsize)
		free := stat.Bavail * uint64(stat.Bsize)
		used := total - min(free, total)
		disks = append(disks, adminDisk{Mount: mount, Used: used, Total: total})
	}
	sort.Slice(disks, func(i, j int) bool { return disks[i].Mount < disks[j].Mount })
	return disks
}

// adminMounts are the mount points, from /proc/self/mounts.
func adminMounts() []string {
	data, err := os.ReadFile(filepath.Join(adminProcRoot, "proc", "self", "mounts"))
	if err != nil {
		return []string{"/"}
	}
	var mounts []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			mounts = append(mounts, strings.ReplaceAll(fields[1], `\040`, " "))
		}
	}
	return mounts
}

// adminMountOf is the mount point a path is on: the longest that holds it.
func adminMountOf(path string, mounts []string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	best := "/"
	for _, mount := range mounts {
		if (path == mount || strings.HasPrefix(path, strings.TrimSuffix(mount, "/")+"/")) && len(mount) > len(best) {
			best = mount
		}
	}
	return best
}

// adminPodmanCgroups asks podman for its running containers' cgroups.
func adminPodmanCgroups() map[string]string {
	groups := map[string]string{}
	b := &appBroker{}
	out, err := b.engineOutput("ps", "-q", "--no-trunc")
	if err != nil {
		return groups
	}
	ids := strings.Fields(out)
	if len(ids) == 0 {
		return groups
	}
	out, err = b.engineOutput(append([]string{"inspect", "--format", "{{.Name}}\t{{.State.CgroupPath}}"}, ids...)...)
	if err != nil {
		return groups
	}
	for _, line := range strings.Split(out, "\n") {
		name, group, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok && group != "" {
			groups[strings.TrimPrefix(name, "/")] = group
		}
	}
	return groups
}

// adminDu is a folder's size, as du counts it on its own filesystem. A
// box's files the account can't read are left out.
func adminDu(dir string) int64 {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "du", "-sxb", dir).Output()
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return -1
	}
	size, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return -1
	}
	return size
}
