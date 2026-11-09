package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// hi serve llm --on local serves a model on this machine, in a box with
// no way out, behind a front that is its only way in, as systemd user
// units written as Quadlet files (hi_serve.md, How a served box runs).

const (
	serveHeadroom  = 8 << 30
	serveBoxPort   = 8080 // where llama-server listens in the box, and the front
	serveFirstPort = 8080
)

// These are replaced in tests.
var (
	serveSysRoot   = "" // a fake / with /sys, /dev, and /proc
	serveSystemctl = func(args ...string) (string, error) {
		command := exec.Command("systemctl", append([]string{"--user"}, args...)...)
		command.Env = userBusEnv()
		out, err := command.CombinedOutput()
		if err != nil {
			return string(out), fmt.Errorf("systemctl --user %s: %s", strings.Join(args, " "), qFirstLine(string(out), err.Error()))
		}
		return string(out), nil
	}
	serveJournal = func(stdout, stderr io.Writer, args ...string) error {
		command := exec.Command("journalctl", append([]string{"--user"}, args...)...)
		command.Env = userBusEnv()
		command.Stdout, command.Stderr = stdout, stderr
		return command.Run()
	}
	serveLingers    = func() bool { return appLingers() }
	serveGroupNames = func() []string {
		ids, _ := os.Getgroups()
		var names []string
		for _, id := range ids {
			if group, err := user.LookupGroupId(strconv.Itoa(id)); err == nil {
				names = append(names, group.Name)
			}
		}
		return names
	}
	servePortFree = func(host string, port int) bool {
		listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			return false
		}
		listener.Close()
		return true
	}
	serveHealthEvery = 2 * time.Second
	serveExecutable  = func() (string, error) {
		executable, err := os.Executable()
		if err != nil {
			return "", err
		}
		if resolved, err := filepath.EvalSymlinks(executable); err == nil {
			executable = resolved
		}
		return executable, nil
	}
)

func servePath(path string) string { return filepath.Join(serveSysRoot, path) }

// userBusEnv is the environment for systemctl --user: some SSH sessions,
// such as a rented machine's, start without XDG_RUNTIME_DIR, and systemd's
// user bus is then not found though the account lingers.
func userBusEnv() []string {
	env := os.Environ()
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		if dir := fmt.Sprintf("/run/user/%d", os.Getuid()); fileExists(dir) {
			env = append(env, "XDG_RUNTIME_DIR="+dir)
		}
	}
	return env
}

// ---------------------------------------------------------------------------
// detect

// serveMachine is what this machine can serve on, from evidence.
type serveMachine struct {
	backend       string
	gpu           string // such as gfx1151, from the kernel's KFD topology
	driver        string // the NVIDIA kernel module's version
	nvidia        nvidiaGPUs
	devices       bool // the backend's devices: /dev/kfd and /dev/dri, /dev/dri, or NVIDIA's CDI spec
	missingGroups []string
	engine        boxEngine
	podman        string // its version
	runtime       string
	lingers       bool
	memTotal      int64
	memAvailable  int64
	appsReserve   int64 // what apps' boxes may still take, under their limits
	appBoxes      int
	problems      []string // each says what is missing and what adds it
}

// room is the memory a model may take: what is available, less what
// teams' boxes may still take and a headroom.
func (m serveMachine) room() int64 {
	return max(0, m.memAvailable-m.appsReserve-serveHeadroom)
}

func detectServeMachine(backend string) serveMachine {
	m := serveMachine{backend: backend}
	switch backend {
	case backendROCm, backendVulkan:
		m.detectAMD()
	case backendCUDA:
		m.detectNVIDIA()
	case backendCPU:
	case "":
		m.problems = append(m.problems, "No GPU was found: no AMD GPU in the kernel's KFD, and no NVIDIA driver. On a Strix Halo, hi install strix sets it up; --backend cpu serves a recipe with a CPU profile on the CPU.")
	default:
		m.problems = append(m.problems, fmt.Sprintf("%q isn't a backend; hi serve knows %s.", backend, strings.Join(serveBackends, ", ")))
	}
	if path, err := boxLookPath("podman"); err == nil {
		m.engine = boxEngine{name: "podman", bin: path}
		if out, err := m.engine.output("info", "--format", "{{.Version.Version}} {{.Host.OCIRuntime.Name}}"); err == nil {
			if fields := strings.Fields(out); len(fields) == 2 {
				m.podman, m.runtime = fields[0], fields[1]
			}
		}
		if m.podman == "" {
			m.problems = append(m.problems, "Podman doesn't answer (podman info failed); check it with podman info.")
		} else if m.runtime != "crun" && (backend == backendROCm || backend == backendVulkan) {
			m.problems = append(m.problems, fmt.Sprintf("Podman runs containers with %s, and passing the GPU's groups in needs crun: install crun, and set runtime = \"crun\" under [engine] in ~/.config/containers/containers.conf.", m.runtime))
		} else if compareVersions(m.podman, "4.9") < 0 {
			m.problems = append(m.problems, fmt.Sprintf("Podman %s is too old for hi serve's units; it needs 4.9 or later.", m.podman))
		}
	} else {
		m.problems = append(m.problems, "Podman isn't installed; hi install podman adds it. (Docker isn't used: its daemon runs boxes as root.)")
	}
	m.lingers = serveLingers()
	m.memTotal, m.memAvailable = memInfo()
	if m.engine.bin != "" && m.podman != "" {
		m.appsReserve, m.appBoxes = appsReserve(m.engine)
	}
	return m
}

// kfdGPU is the first GPU in the kernel's KFD topology, as gfxNNNN.
func kfdGPU() string {
	nodes, _ := filepath.Glob(servePath("/sys/class/kfd/kfd/topology/nodes/*/properties"))
	for _, node := range nodes {
		data, err := os.ReadFile(node)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if value, ok := strings.CutPrefix(line, "gfx_target_version "); ok {
				if version, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && version > 0 {
					return fmt.Sprintf("gfx%d%x%x", version/10000, version/100%100, version%100)
				}
			}
		}
	}
	return ""
}

func memInfo() (total, available int64) {
	data, err := os.ReadFile(servePath("/proc/meminfo"))
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kb, _ := strconv.ParseInt(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			total = kb << 10
		case "MemAvailable:":
			available = kb << 10
		}
	}
	return total, available
}

// appsReserve is what the running boxes that hi serve didn't start may
// still take: each one's memory limit less what it uses now, which is
// already out of what is available.
func appsReserve(engine boxEngine) (int64, int) {
	out, err := engine.output("ps", "-q")
	if err != nil || strings.TrimSpace(out) == "" {
		return 0, 0
	}
	args := append([]string{"inspect", "--format", `{{.HostConfig.Memory}}|{{.State.CgroupPath}}|{{index .Config.Labels "hi.serve"}}`}, strings.Fields(out)...)
	out, err = engine.output(args...)
	if err != nil {
		return 0, 0
	}
	var reserve int64
	boxes := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.Split(line, "|")
		if len(parts) != 3 || (parts[2] != "" && parts[2] != "<no value>") {
			continue
		}
		limit, _ := strconv.ParseInt(parts[0], 10, 64)
		if limit <= 0 {
			continue
		}
		boxes++
		used := int64(0)
		if data, err := os.ReadFile(servePath(filepath.Join("/sys/fs/cgroup", parts[1], "memory.current"))); err == nil {
			used, _ = strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		}
		reserve += max(0, limit-used)
	}
	return reserve, boxes
}

func (m serveMachine) describe() string {
	parts := []string{}
	switch {
	case m.gpu == "gfx1151":
		parts = append(parts, "Strix Halo (gfx1151)")
	case m.gpu != "":
		parts = append(parts, m.gpu)
	case len(m.nvidia.names) > 0:
		parts = append(parts, fmt.Sprintf("%s, driver %s for CUDA %s, %s of GPU memory free", strings.Join(m.nvidia.names, " and "), m.nvidia.driver, m.nvidia.cuda, sizeText(m.nvidia.free)))
	}
	switch {
	case !m.devices:
	case m.backend == backendROCm:
		parts = append(parts, "/dev/kfd and /dev/dri")
	case m.backend == backendVulkan:
		parts = append(parts, "/dev/dri")
	case m.backend == backendCUDA:
		parts = append(parts, "its CDI devices")
	}
	if m.backend == backendCPU {
		parts = append(parts, "the CPU")
	}
	if m.podman != "" {
		parts = append(parts, "Podman "+m.podman+" with "+m.runtime)
	}
	return strings.Join(parts, ", ")
}

func serveDetectCommand(args []string, stdout, stderr io.Writer) error {
	flags := newComputeFlags("serve detect", stderr)
	backend := flags.String("backend", "", "rocm, vulkan, cuda, or cpu (default: what the GPU points to)")
	positional, err := parseInterspersedFlags(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 0 {
		return usageError{"usage: hi serve detect [--backend rocm|vulkan|cuda|cpu]"}
	}
	if *backend == "" {
		*backend = defaultServeBackend()
	} else if !isServeBackend(*backend) {
		return usageError{fmt.Sprintf("--backend %s: hi serve knows %s", *backend, strings.Join(serveBackends, ", "))}
	}
	m := detectServeMachine(*backend)
	yes := func(ok bool, text string) string {
		if ok {
			return text
		}
		return "missing"
	}
	gpu := "none found"
	switch {
	case m.gpu != "":
		gpu = m.gpu
		if m.gpu == "gfx1151" {
			gpu += " (Strix Halo)"
		}
	case len(m.nvidia.names) > 0:
		gpu = strings.Join(m.nvidia.names, " and ")
	case *backend == backendCPU:
		gpu = "not used (--backend cpu)"
	}
	fmt.Fprintf(stdout, "GPU:        %s\n", gpu)
	switch *backend {
	case backendROCm, backendVulkan:
		fmt.Fprintf(stdout, "Devices:    %s\n", yes(m.devices, map[bool]string{true: "/dev/kfd and /dev/dri", false: "/dev/dri"}[*backend == backendROCm]))
		if len(m.missingGroups) == 0 {
			fmt.Fprintln(stdout, "Groups:     render and video")
		} else {
			fmt.Fprintf(stdout, "Groups:     not in %s\n", strings.Join(m.missingGroups, " and "))
		}
	case backendCUDA:
		if m.driver != "" {
			driver := m.driver
			if m.nvidia.cuda != "" {
				driver += ", for CUDA " + m.nvidia.cuda + " and older"
			}
			fmt.Fprintf(stdout, "Driver:     %s\n", driver)
		}
		if len(m.nvidia.names) > 0 {
			fmt.Fprintf(stdout, "GPU memory: %s, %s free\n", sizeText(m.nvidia.total), sizeText(m.nvidia.free))
			fmt.Fprintf(stdout, "Devices:    %s\n", yes(m.devices, "nvidia.com/gpu, through CDI"))
		}
	}
	fmt.Fprintf(stdout, "Backend:    %s\n", backendText(*backend))
	if m.podman != "" {
		fmt.Fprintf(stdout, "Podman:     %s with %s\n", m.podman, m.runtime)
	} else {
		fmt.Fprintln(stdout, "Podman:     missing")
	}
	fmt.Fprintf(stdout, "Lingering:  %s\n", map[bool]string{true: "on: served things run without a login, and start at boot", false: "off: served things stop when you log out"}[m.lingers])
	fmt.Fprintf(stdout, "Memory:     %s, %s available\n", sizeText(m.memTotal), sizeText(m.memAvailable))
	fmt.Fprintf(stdout, "Apps:       %d %s, which may still take %s\n", m.appBoxes, map[bool]string{true: "box", false: "boxes"}[m.appBoxes == 1], sizeText(m.appsReserve))
	if *backend == backendCUDA {
		fmt.Fprintf(stdout, "For models: %s of GPU memory and %s on the host (after %s and %s of headroom)\n", sizeText(m.vramRoom()), sizeText(m.room()), sizeText(serveVRAMHeadroom), sizeText(serveHeadroom))
	} else {
		fmt.Fprintf(stdout, "For models: %s (after %s of headroom)\n", sizeText(m.room()), sizeText(serveHeadroom))
	}
	if dir, err := serveModelsDir(); err == nil {
		if free, err := freeDisk(dir); err == nil {
			fmt.Fprintf(stdout, "Disk:       %s free for %s\n", sizeText(free), dir)
		}
	}
	if len(m.problems) == 0 {
		fmt.Fprintln(stdout, "\nThis machine can serve models: hi serve recipes llm shows which fit.")
	} else {
		fmt.Fprintln(stdout, "\nThis machine can't serve models yet:")
		for _, problem := range m.problems {
			fmt.Fprintf(stdout, "  - %s\n", problem)
		}
	}
	if !m.lingers {
		fmt.Fprintf(stdout, "\nTo keep served things running after you log out, and start them at boot:\n  sudo loginctl enable-linger %s\n", currentUserName())
	}
	return nil
}

// ---------------------------------------------------------------------------
// serving

var serveNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// serveNameFor is the default name for a recipe: qwen3.8-flash-next is
// qwen3-8-flash-next.
func serveNameFor(recipe string) string {
	var name strings.Builder
	dash := false
	for _, r := range strings.ToLower(recipe) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			name.WriteRune(r)
			dash = false
		} else if !dash && name.Len() > 0 {
			name.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(name.String(), "-")
}

func checkServeName(name string) error {
	if !serveNamePattern.MatchString(name) || strings.HasSuffix(name, "-front") || strings.HasSuffix(name, "-") {
		return usageError{fmt.Sprintf("%q can't name a served thing: use lowercase letters, digits, and dashes, up to 40", name)}
	}
	if isServeWord(name) {
		return usageError{fmt.Sprintf("%q is one of hi serve's own words; choose another --name", name)}
	}
	return nil
}

// localRequest is hi serve llm --on local's options.
type localRequest struct {
	argument, quant, alias, extra, name, memory, backend string
	context, port                                        int
	portSet                                              bool
	max                                                  time.Duration
	yes, dryRun                                          bool
	share, org                                           bool
	reason                                               string
}

// localPlan is what serving a model here takes.
type localPlan struct {
	backend string
	recipe  serveRecipe
	profile localProfile
	model   hubModel
	need    int64 // on the host, and on the GPU where its memory is the host's
	vram    int64 // cuda: on the GPU
	limit   int64
	args    []string
	adhoc   bool
}

// planLocal resolves a recipe or an ad hoc model for this machine, and
// asks the Hub for its files at the pinned revision.
func planLocal(argument, quant string, contextSize int, alias, extra, backend, memory string) (localPlan, error) {
	recipe, err := resolveServeRecipe(argument, quant, contextSize, alias, "")
	if err != nil {
		return localPlan{}, err
	}
	plan := localPlan{recipe: recipe, backend: backend}
	_, builtin := serveRecipes[strings.ToLower(argument)]
	profile, ok := recipe.local[backend]
	switch {
	case ok:
		plan.profile = profile
	case builtin:
		return localPlan{}, fmt.Errorf("the recipe %s has no %s profile; it runs on %s", recipe.name, backend, strings.Join(recipe.places(), ", "))
	case backend == backendCPU:
		return localPlan{}, errors.New("a model without a recipe isn't served on the CPU, which only a recipe's tested profile is for; serve it on a GPU")
	default:
		plan.adhoc = true
		plan.profile = localProfile{Image: serveDefaultImages[backend]}
		if backend == backendCUDA {
			plan.profile.CUDA = serveDefaultCUDA
		}
	}
	if extra != "" {
		extraArgs := strings.Fields(extra)
		if err := checkRecipeArgs(extraArgs); err != nil {
			return localPlan{}, usageError{err.Error()}
		}
		plan.args = extraArgs
	}
	plan.model, err = hubModelFiles(recipe.repo, recipe.revision, recipe.quant)
	if err != nil {
		return localPlan{}, err
	}
	switch {
	case plan.adhoc && backend == backendCUDA:
		// No measurement: the files and an estimate for the context's
		// cache on the GPU, and a little on the host.
		plan.vram = plan.model.size() + plan.model.size()*15/100 + 1<<30
		// The limit holds the model file's page cache as it loads: a
		// smaller one evicts it as it is read, and loading takes minutes.
		plan.need, plan.limit = 4<<30, max(16<<30, plan.model.size()+4<<30)
	case plan.adhoc:
		plan.need = plan.model.size() + plan.model.size()*15/100 + 1<<30
		plan.limit = plan.need
	default:
		plan.need, _ = parseSize(profile.Need)
		plan.limit, _ = parseSize(profile.Limit)
		if profile.VRAM != "" {
			plan.vram, _ = parseSize(profile.VRAM)
		}
	}
	if memory != "" {
		if plan.limit, err = parseSize(memory); err != nil {
			return localPlan{}, usageError{"--memory: " + err.Error()}
		}
	}
	return plan, nil
}

// serverArgs are llama-server's arguments in the box: hi's own, then the
// backend's, the recipe's, and --args.
func (p localPlan) serverArgs() []string {
	args := []string{"--host", "0.0.0.0", "--port", strconv.Itoa(serveBoxPort),
		"-m", "/models/" + p.model.first(), "-ngl", map[bool]string{true: "999", false: "0"}[usesGPU(p.backend)], "-c", strconv.Itoa(p.recipe.context), "--alias", p.recipe.alias}
	args = append(args, p.profile.Args...)
	args = append(args, p.recipe.common...)
	return append(args, p.args...)
}

// checkFits refuses a model that doesn't fit in memory or on the disk:
// the GPU's memory where it has its own, and the host's.
func checkFits(m serveMachine, need, vram, download int64, modelsDir string, stdout io.Writer, estimated bool) error {
	room := m.room()
	measured := "with its context"
	if estimated {
		measured = "by estimate (the files and 15% for the context)"
	}
	if vram > 0 && vram > m.vramRoom() {
		return fmt.Errorf("it needs %s of GPU memory %s, and the GPU has %s free, less %s of headroom. It is %s short, so nothing was started",
			sizeText(vram), measured, sizeText(m.nvidia.free), sizeText(serveVRAMHeadroom), sizeText(vram-m.vramRoom()))
	}
	host := ""
	if vram > 0 {
		host, measured = " on the host", "besides"
		if estimated {
			measured = "besides, by estimate"
		}
	}
	if need > room {
		return fmt.Errorf("it needs %s%s %s, and this machine has %s for it: %s available, less %s that apps' boxes may still take and %s of headroom. It is %s short, so nothing was started",
			sizeText(need), host, measured, sizeText(room), sizeText(m.memAvailable), sizeText(m.appsReserve), sizeText(serveHeadroom), sizeText(need-room))
	}
	free, err := freeDisk(modelsDir)
	if err != nil {
		return err
	}
	if download > 0 && free < download+download/10 {
		return fmt.Errorf("it needs %s to download, with 10%% to spare, and %s has %s free: %s short, so nothing was started",
			sizeText(download+download/10), modelsDir, sizeText(free), sizeText(download+download/10-free))
	}
	disk := "the model is here"
	if download > 0 {
		disk = fmt.Sprintf("%s to download, %s free", sizeText(download), sizeText(free))
	}
	if vram > 0 {
		fmt.Fprintf(stdout, "  check      it needs %s of GPU memory and %s on the host: fits, %s and %s left; %s\n",
			sizeText(vram), sizeText(need), sizeText(m.vramRoom()-vram), sizeText(room-need), disk)
		return nil
	}
	fmt.Fprintf(stdout, "  check      it needs %s %s: fits, %s left; %s\n", sizeText(need), measured, sizeText(room-need), disk)
	return nil
}

func serveLocalCommand(request localRequest, stdin io.Reader, stdout, stderr io.Writer) error {
	if request.backend == "" {
		request.backend = defaultServeBackend()
	} else if !isServeBackend(request.backend) {
		return usageError{fmt.Sprintf("--backend %s: hi serve knows %s", request.backend, strings.Join(serveBackends, ", "))}
	}
	served, err := loadServed()
	if err != nil {
		return err
	}
	name := request.name
	if name == "" {
		recipe, known := serveRecipes[strings.ToLower(request.argument)]
		if known {
			name = serveNameFor(recipe.name)
		} else {
			_, repo, _ := strings.Cut(request.argument, "/")
			name = serveNameFor(strings.TrimSuffix(strings.TrimSuffix(repo, "-GGUF"), "-gguf"))
		}
	}
	if err := checkServeName(name); err != nil {
		return err
	}
	if existing, ok := served[name]; ok {
		if existing.Provider == "local" {
			return fmt.Errorf("%s is already served here (%s); hi serve start %s starts it again, or choose another --name", name, localState(existing).text, name)
		}
		return fmt.Errorf("%s is already served on %s; choose another --name", name, existing.Provider)
	}

	address := ""
	if request.share || request.org {
		if address, err = shareAddress(); err != nil {
			return err
		}
	}
	if request.org {
		if _, err := serveOrgClient(); err != nil {
			return err
		}
	}

	m := detectServeMachine(request.backend)
	if len(m.problems) > 0 {
		return fmt.Errorf("this machine can't serve it yet:\n  - %s", strings.Join(m.problems, "\n  - "))
	}
	fmt.Fprintf(stdout, "  detect     %s; %s available, apps' boxes may still take %s\n", m.describe(), sizeText(m.memAvailable), sizeText(m.appsReserve))

	plan, err := planLocal(request.argument, request.quant, request.context, request.alias, request.extra, request.backend, request.memory)
	if err != nil {
		return err
	}
	modelsDir, err := serveModelsDir()
	if err != nil {
		return err
	}
	if err := checkCUDA(m, plan.profile.CUDA); err != nil {
		return err
	}
	modelDir := filepath.Join(modelsDir, modelDirName(plan.recipe.repo))
	download := toDownload(modelDir, plan.model)
	if err := checkFits(m, plan.need, plan.vram, download, modelsDir, stdout, plan.adhoc); err != nil {
		return err
	}
	port := request.port
	if request.portSet {
		if !portsFree(port, address) || portTaken(served, port) {
			return fmt.Errorf("port %d on %s is in use; choose another --port", port, portPlaces(address))
		}
	} else if port, err = freeServePort(served, address); err != nil {
		return err
	}
	record := servedModel{
		Name: name, Provider: "local", Kind: "llm", Recipe: plan.recipe.name, Model: plan.recipe.alias,
		Repo: plan.recipe.repo, Quant: plan.recipe.quant, Revision: plan.model.Revision, Backend: request.backend,
		Image: plan.profile.Image, Need: plan.need, VRAM: plan.vram, CUDA: plan.profile.CUDA, Memory: plan.limit, Context: plan.recipe.context,
		Args: plan.serverArgs(), ModelDir: modelDirName(plan.recipe.repo), Port: port,
		URL: fmt.Sprintf("http://127.0.0.1:%d", port), Started: computeNow(),
		Shared: request.share, Org: request.org, Address: address,
	}
	if request.max != noLimit {
		record.Deadline = computeNow().Add(request.max)
	}
	if plan.profile.Tested != "" {
		fmt.Fprintf(stdout, "Recipe %s, tested on %s.\n", plan.recipe.name, plan.profile.Tested)
	}
	describeDownload(plan.model, modelDir, download, stdout)

	if request.dryRun {
		executable, _ := serveExecutable()
		fmt.Fprintf(stdout, "\nWould pull %s, and write these units to %s:\n", plan.profile.Image, serveQuadletDir())
		for _, unit := range serveUnits(record, executable, modelsDir, true) {
			fmt.Fprintf(stdout, "\n--- %s\n%s", unit.name, unit.content)
		}
		if !record.Deadline.IsZero() {
			fmt.Fprintf(stdout, "\nand a timer that runs hi serve stop %s at %s.\n", name, record.Deadline.Format("2006-01-02 15:04"))
		}
		fmt.Fprintf(stdout, "\nThe API would be http://127.0.0.1:%d/v1, model %s.", port, plan.recipe.alias)
		if record.servesShared() {
			fmt.Fprintf(stdout, " Also %s, with a key.", record.sharedURL())
		}
		if record.Org {
			fmt.Fprintf(stdout, " The org server would be asked to add it to hi org ai as %s.", orgModelID(name))
		}
		fmt.Fprintln(stdout, " Nothing was changed.")
		return nil
	}
	if download > 0 {
		if err := confirm(stdin, stdout, request.yes, fmt.Sprintf("Download %s and serve it?", sizeText(download))); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "  download   %s:%s at %s into %s\n", plan.recipe.repo, plan.recipe.quant, shortCommit(plan.model.Revision), modelDir)
	if err := downloadModel(modelDir, plan.model, stdout); err != nil {
		return err
	}
	if err := pullServeImage(m.engine, plan.profile.Image, request.backend, stdout, stderr); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "  start      hi-serve-%s: a box with %s, no way out, the model read-only, behind its front on %s:%d\n", name,
		map[bool]string{true: "the GPU", false: "the CPU only"}[usesGPU(request.backend)], portPlaces(address), port)
	if err := ensureServeKeys(record); err != nil {
		return err
	}
	served[name] = record
	if err := writeServed(served); err != nil {
		return err
	}
	if err := startLocal(record, stdout); err != nil {
		return err
	}
	if err := finishLocalStart(record, m, stdout); err != nil {
		return err
	}
	if record.Shared {
		printShareHandOver(record, readServeKey(name, serveSharedKey), stdout)
	}
	if record.Org {
		named, err := nameToOrg(record, request.reason, stdout)
		if err != nil {
			return fmt.Errorf("%w\nIt runs here; hi serve rm %s, then serve it again with --org, to ask again", err, name)
		}
		return updateServed(named)
	}
	return nil
}

// updateServed writes one record back.
func updateServed(record servedModel) error {
	served, err := loadServed()
	if err != nil {
		return err
	}
	served[record.Name] = record
	return writeServed(served)
}

// portsFree reports a port free on 127.0.0.1 and, for a shared server,
// on the NetBird address.
func portsFree(port int, address string) bool {
	return servePortFree("127.0.0.1", port) && (address == "" || servePortFree(address, port))
}

func portPlaces(address string) string {
	if address == "" {
		return "127.0.0.1"
	}
	return "127.0.0.1 and " + address
}

func describeDownload(model hubModel, dir string, download int64, stdout io.Writer) {
	if download == 0 {
		return
	}
	fmt.Fprintf(stdout, "\nFrom the Hub: %s at %s, %s in %d %s, into %s\n", model.Repo, model.Revision, sizeText(model.size()),
		len(model.Files), map[bool]string{true: "file", false: "files"}[len(model.Files) == 1], dir)
	for _, file := range model.Files {
		fmt.Fprintf(stdout, "  %s  %s\n", file.Path, sizeText(file.Size))
	}
	license := "see the model's page"
	if model.License != "" {
		license = model.License
	}
	fmt.Fprintf(stdout, "License: %s, https://huggingface.co/%s\n\n", license, model.Repo)
}

func portTaken(served map[string]servedModel, port int) bool {
	for _, record := range served {
		if record.Provider == "local" && record.Port == port {
			return true
		}
	}
	return false
}

func freeServePort(served map[string]servedModel, address string) (int, error) {
	for port := serveFirstPort; port < serveFirstPort+100; port++ {
		if !portTaken(served, port) && portsFree(port, address) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port from 8080 to 8179 on %s; choose one with --port", portPlaces(address))
}

func pullServeImage(engine boxEngine, image, backend string, stdout, stderr io.Writer) error {
	if engine.exists("image", image) {
		return nil
	}
	fmt.Fprintf(stdout, "  pull       %s (once; %s)\n", image, serveImageSizes[backend])
	if err := engine.interactive(nil, stdout, stderr, "pull", image); err != nil {
		return fmt.Errorf("podman pull %s: %w", image, err)
	}
	return nil
}

// startLocal writes a served thing's units and starts them, then waits
// until it is healthy. If it doesn't come up, it is left stopped.
func startLocal(record servedModel, stdout io.Writer) error {
	if err := writeServeUnits(record, true); err != nil {
		return err
	}
	if _, err := serveSystemctl("daemon-reload"); err != nil {
		return err
	}
	box, front := "hi-serve-"+record.Name+".service", "hi-serve-"+record.Name+"-front.service"
	if _, err := serveSystemctl("start", box, front); err != nil {
		stopLocalUnits(record)
		return fmt.Errorf("%w\nPodman makes the units from the files in %s; it is kept, stopped: hi serve logs %s", err, serveQuadletDir(), record.Name)
	}
	if !record.Deadline.IsZero() {
		if err := writeMaxTimer(record); err != nil {
			return err
		}
	}
	if err := waitForLocal(record, stdout); err != nil {
		var tail strings.Builder
		serveJournal(&tail, io.Discard, "-u", box, "-n", "20", "-o", "cat", "--no-pager")
		stopLocalUnits(record)
		if tail.Len() > 0 {
			fmt.Fprintf(stdout, "\nThe end of its log:\n%s\n", strings.TrimRight(tail.String(), "\n"))
		}
		return fmt.Errorf("%w; it is kept, stopped: hi serve logs %s, then hi serve start %s or hi serve rm %s", err, record.Name, record.Name, record.Name)
	}
	return nil
}

func finishLocalStart(record servedModel, m serveMachine, stdout io.Writer) error {
	fmt.Fprintf(stdout, "  ready      OpenAI-compatible API: http://127.0.0.1:%d/v1   model: %s\n", record.Port, record.Model)
	if !record.Deadline.IsZero() {
		fmt.Fprintf(stdout, "\nIt stops at %s (--max).\n", record.Deadline.Format("2006-01-02 15:04"))
	} else {
		fmt.Fprintln(stdout, "\nIt keeps running, and comes back after a reboot, until you stop it.")
	}
	fmt.Fprintf(stdout, "  hi serve status %s   its address, health, and memory\n  hi serve logs %s     its log\n  hi serve stop %s     stop it, and keep it to start again\n", record.Name, record.Name, record.Name)
	if !m.lingers {
		fmt.Fprintf(stdout, "\nIt stops when you log out, and doesn't start at boot, until you run:\n  sudo loginctl enable-linger %s\n", currentUserName())
	}
	return nil
}

// waitForLocal polls the server's health through its front until it
// answers, within the same 45 minutes as on rented hardware.
func waitForLocal(record servedModel, stdout io.Writer) error {
	started := time.Now()
	deadline := computeNow().Add(serveReadyWithin)
	last, lastPrinted := "", time.Now()
	client := &http.Client{Timeout: 5 * time.Second}
	health := fmt.Sprintf("http://127.0.0.1:%d/health", record.Port)
	for computeNow().Before(deadline) {
		unit := localUnit(record.Name)
		if unit.failed() || (unit.active == "inactive" && time.Since(started) > 5*time.Second) {
			if unit.status == "137" {
				return errors.New("the kernel killed it for memory while it loaded")
			}
			return fmt.Errorf("it stopped while starting (%s)", unit.describe())
		}
		state := "starting the box"
		if response, err := client.Get(health); err == nil {
			response.Body.Close()
			switch response.StatusCode {
			case http.StatusOK:
				fmt.Fprintf(stdout, "  [%s] ready after %s\n", computeNow().Format("15:04:05"), formatDuration(time.Since(started).Round(time.Second)))
				return nil
			case http.StatusServiceUnavailable:
				state = "loading the model"
			}
		}
		if state != last || time.Since(lastPrinted) > time.Minute {
			fmt.Fprintf(stdout, "  [%s] %s\n", computeNow().Format("15:04:05"), state)
			last, lastPrinted = state, time.Now()
		}
		time.Sleep(serveHealthEvery)
	}
	return fmt.Errorf("it was not ready after %s", formatDuration(serveReadyWithin))
}

// unitState is what systemd says of a unit.
type unitState struct {
	load, active, sub, result, status, since string
}

func (u unitState) failed() bool { return u.active == "failed" }

func (u unitState) describe() string {
	text := u.active
	if u.result != "" && u.result != "success" {
		text += ", " + u.result
	}
	if u.status != "" && u.status != "0" {
		text += ", exit status " + u.status
	}
	return text
}

func localUnit(name string) unitState {
	out, _ := serveSystemctl("show", "hi-serve-"+name+".service", "--property=LoadState,ActiveState,SubState,Result,ExecMainStatus,ActiveEnterTimestamp")
	var state unitState
	for _, line := range strings.Split(out, "\n") {
		key, value, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch key {
		case "LoadState":
			state.load = value
		case "ActiveState":
			state.active = value
		case "SubState":
			state.sub = value
		case "Result":
			state.result = value
		case "ExecMainStatus":
			state.status = value
		case "ActiveEnterTimestamp":
			state.since = value
		}
	}
	return state
}

// localStatus is a local server's state, as hi serve ls shows it.
type localStatus struct {
	text    string // starting, running, stopped, stopped: out of memory, or failed
	running bool
}

func localState(record servedModel) localStatus {
	unit := localUnit(record.Name)
	switch {
	case unit.active == "active" || unit.active == "reloading":
		client := &http.Client{Timeout: 2 * time.Second}
		if response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", record.Port)); err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return localStatus{"running", true}
			}
		}
		return localStatus{"starting", true}
	case unit.active == "activating":
		return localStatus{"starting", true}
	case unit.status == "137":
		return localStatus{"stopped: out of memory", false}
	case unit.failed() && unit.status == "0":
		// The server exited cleanly, and Podman's cleanup after it failed:
		// on Ubuntu 26.04, AppArmor's profile for pasta refuses Podman's
		// signal when the last container stops (hi_serve.md, Findings 6).
		return localStatus{"stopped", false}
	case unit.failed():
		return localStatus{"failed", false}
	default:
		return localStatus{"stopped", false}
	}
}

// ---------------------------------------------------------------------------
// units

func serveQuadletDir() string {
	return filepath.Join(hiUserConfigBase(), "containers", "systemd")
}

func serveUserUnitDir() string {
	return filepath.Join(hiUserConfigBase(), "systemd", "user")
}

func hiUserConfigBase() string {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return base
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}

type serveUnit struct{ name, content string }

// quadletValue quotes one word of a unit's Exec= line, as systemd reads
// it, and escapes its specifiers.
func quadletValue(word string) string {
	escaped := strings.ReplaceAll(word, "%", "%%")
	escaped = strings.ReplaceAll(escaped, "$", "$$")
	if word != "" && !strings.ContainsAny(word, " \t\"'\\;") {
		return escaped
	}
	escaped = strings.ReplaceAll(escaped, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

func quadletPath(path string) string {
	return strings.ReplaceAll(path, "%", "%%")
}

// serveUnits are a served thing's Quadlet files. Without autostart they
// have no [Install], so a stopped one stays stopped after a reboot.
func serveUnits(record servedModel, executable, modelsDir string, autostart bool) []serveUnit {
	name := "hi-serve-" + record.Name
	label := "Label=hi.serve=" + record.Name + "\n"
	install := ""
	if autostart {
		install = "\n[Install]\nWantedBy=default.target\n"
	}
	header := "# Written by hi serve for " + record.Name + "; hi serve rm removes it.\n"
	exec := make([]string, len(record.Args))
	for i, arg := range record.Args {
		exec[i] = quadletValue(arg)
	}
	devices, boxArgs := backendUnitLines(backendOf(record))
	box := header + "[Unit]\nDescription=hi serve " + record.Name + ": " + record.Model + "\n\n[Container]\n" +
		"ContainerName=" + name + "\n" +
		"Image=" + record.Image + "\n" +
		"Pull=never\n" +
		"Exec=" + strings.Join(exec, " ") + "\n" +
		"Volume=" + quadletPath(filepath.Join(modelsDir, record.ModelDir)) + ":/models:ro\n" +
		"Network=" + name + ".network\n" +
		devices +
		"UserNS=keep-id\nSecurityLabelDisable=true\n" +
		"Tmpfs=/tmp\nDropCapability=ALL\nNoNewPrivileges=true\nPidsLimit=4096\n" +
		fmt.Sprintf("PodmanArgs=--memory=%dm --oom-score-adj=1000%s\n", record.Memory>>20, boxArgs) +
		label +
		"\n[Service]\nRestart=on-failure\n# The kernel killed it for memory: it stays stopped, beside the app it was killed for.\nRestartPreventExitStatus=137\nTimeoutStartSec=300\n" +
		install
	frontExec := fmt.Sprintf("Exec=serve __front :%d http://%s:%d", serveBoxPort, name, serveBoxPort)
	publish := fmt.Sprintf("PublishPort=127.0.0.1:%d:%d\n", record.Port, serveBoxPort)
	keys, retry := "", ""
	if record.servesShared() {
		frontExec += fmt.Sprintf(" --shared :%d --keys %s", serveFrontPort, serveFrontKeys)
		publish += fmt.Sprintf("PublishPort=%s:%d:%d\n", record.Address, record.Port, serveFrontPort)
		keys = "Volume=" + quadletPath(serveKeysDir(record.Name)) + ":" + serveFrontKeys + ":ro\n"
		// At boot the NetBird address may come up after it: try again until then.
		retry = "RestartSec=10\n"
	}
	// BindsTo: a box that stopped on its own, killed for memory, takes its
	// front with it, which Requires didn't, and the front held the port.
	front := header + "[Unit]\nDescription=hi serve " + record.Name + ": its front\nBindsTo=" + name + ".service\nAfter=" + name + ".service\nStartLimitIntervalSec=0\n\n[Container]\n" +
		"ContainerName=" + name + "-front\n" +
		"Image=" + record.Image + "\n" +
		"Pull=never\n" +
		frontExec + "\n" +
		"Volume=" + quadletPath(executable) + ":/usr/local/bin/hi:ro\n" +
		keys +
		"Network=" + name + ".network\n" +
		"Network=" + name + "-front.network\n" +
		publish +
		"UserNS=keep-id\nSecurityLabelDisable=true\nReadOnly=true\nDropCapability=ALL\nNoNewPrivileges=true\nPidsLimit=256\n" +
		// --entrypoint, not Entrypoint=, which Quadlet only has from Podman 5.
		"PodmanArgs=--memory=256m --entrypoint=/usr/local/bin/hi\n" +
		label +
		"\n[Service]\nRestart=on-failure\n" + retry +
		install
	return []serveUnit{
		{name + ".network", header + "[Network]\nNetworkName=" + name + "\nInternal=true\n" + label},
		{name + "-front.network", header + "[Network]\nNetworkName=" + name + "-front\n" + label},
		{name + ".container", box},
		{name + "-front.container", front},
	}
}

func writeServeUnits(record servedModel, autostart bool) error {
	executable, err := serveExecutable()
	if err != nil {
		return err
	}
	modelsDir, err := serveModelsDir()
	if err != nil {
		return err
	}
	dir := serveQuadletDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, unit := range serveUnits(record, executable, modelsDir, autostart) {
		if err := os.WriteFile(filepath.Join(dir, unit.name), []byte(unit.content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ownsUnits reports whether a served thing's unit files are hi serve's,
// by their label.
func ownsUnits(name string) bool {
	data, err := os.ReadFile(filepath.Join(serveQuadletDir(), "hi-serve-"+name+".container"))
	return err == nil && strings.Contains(string(data), "\nLabel=hi.serve="+name+"\n")
}

// writeMaxTimer stops a served thing at its --max, also when the machine
// was off then: the timer is persistent.
func writeMaxTimer(record servedModel) error {
	executable, err := serveExecutable()
	if err != nil {
		return err
	}
	dir := serveUserUnitDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := "hi-serve-" + record.Name + "-max"
	header := "# Written by hi serve for " + record.Name + "; hi serve stop and rm remove it.\n"
	timer := header + "[Unit]\nDescription=hi serve " + record.Name + ": stop at its --max\n\n[Timer]\n" +
		"OnCalendar=" + record.Deadline.UTC().Format("2006-01-02 15:04:05") + " UTC\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n"
	service := header + "[Unit]\nDescription=hi serve " + record.Name + ": stop at its --max\n\n[Service]\nType=oneshot\n" +
		"ExecStart=" + quadletValue(executable) + " serve stop " + record.Name + "\n"
	if err := os.WriteFile(filepath.Join(dir, name+".timer"), []byte(timer), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, name+".service"), []byte(service), 0o644); err != nil {
		return err
	}
	if _, err := serveSystemctl("daemon-reload"); err != nil {
		return err
	}
	_, err = serveSystemctl("enable", "--now", name+".timer")
	return err
}

func removeMaxTimer(name string) {
	unit := "hi-serve-" + name + "-max"
	dir := serveUserUnitDir()
	if !fileExists(filepath.Join(dir, unit+".timer")) {
		return
	}
	serveSystemctl("disable", "--now", unit+".timer")
	os.Remove(filepath.Join(dir, unit+".timer"))
	os.Remove(filepath.Join(dir, unit+".service"))
}

// stopLocalUnits stops a served thing and keeps it from starting at boot.
func stopLocalUnits(record servedModel) error {
	removeMaxTimer(record.Name)
	if err := writeServeUnits(record, false); err != nil {
		return err
	}
	serveSystemctl("daemon-reload")
	units := []string{"hi-serve-" + record.Name + "-front.service", "hi-serve-" + record.Name + ".service"}
	_, err := serveSystemctl(append([]string{"stop"}, units...)...)
	// A cleanup that failed after a clean exit leaves the units failed.
	serveSystemctl(append([]string{"reset-failed"}, units...)...)
	return err
}

// ---------------------------------------------------------------------------
// stop, start, rm, status, logs

func localStop(record servedModel, stdout, stderr io.Writer) error {
	if !ownsUnits(record.Name) {
		return fmt.Errorf("the units of %s aren't labeled as hi serve's, so hi leaves them alone", record.Name)
	}
	if err := stopLocalUnits(record); err != nil {
		return err
	}
	served, err := loadServed()
	if err != nil {
		return err
	}
	if current, ok := served[record.Name]; ok {
		current.Deadline = time.Time{}
		served[record.Name] = current
		if err := writeServed(served); err != nil {
			return err
		}
	}
	tellOrg(record, "down", stderr)
	fmt.Fprintf(stdout, "Stopped %s, and kept it: hi serve start %s starts it again on port %d, and hi serve rm %s forgets it.\n",
		record.Name, record.Name, record.Port, record.Name)
	return nil
}

func serveStartCommand(args []string, stdout, stderr io.Writer) error {
	flags := newComputeFlags("serve start", stderr)
	maxLifetime := &lifetimeFlag{value: noLimit}
	flags.Var(maxLifetime, "max", "stop after this long")
	positional, err := parseInterspersedFlags(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return usageError{"usage: hi serve start <name> [--max <duration>]"}
	}
	served, err := loadServed()
	if err != nil {
		return err
	}
	record, ok := served[positional[0]]
	if !ok || record.Provider != "local" {
		return fmt.Errorf("%s isn't served on this machine; see hi serve ls", positional[0])
	}
	if !ownsUnits(record.Name) {
		return fmt.Errorf("the units of %s aren't labeled as hi serve's, so hi leaves them alone", record.Name)
	}
	if localState(record).running {
		return fmt.Errorf("%s is already running", record.Name)
	}
	m := detectServeMachine(backendOf(record))
	if len(m.problems) > 0 {
		return fmt.Errorf("this machine can't serve it now:\n  - %s", strings.Join(m.problems, "\n  - "))
	}
	if err := checkCUDA(m, record.CUDA); err != nil {
		return err
	}
	modelsDir, err := serveModelsDir()
	if err != nil {
		return err
	}
	if err := checkFits(m, record.Need, record.VRAM, 0, modelsDir, stdout, false); err != nil {
		return err
	}
	if record.servesShared() {
		// NetBird may have given this machine a new address since.
		if record.Address, err = shareAddress(); err != nil {
			return err
		}
		if err := ensureServeKeys(record); err != nil {
			return err
		}
	}
	// A front left from before, by a box that ended on its own, holds the
	// port: it goes first.
	serveSystemctl("stop", "hi-serve-"+record.Name+"-front.service")
	if !portsFree(record.Port, record.Address) {
		return fmt.Errorf("port %d on %s, where %s is served, is in use by something else", record.Port, portPlaces(record.Address), record.Name)
	}
	record.Deadline = time.Time{}
	if maxLifetime.value != noLimit {
		record.Deadline = computeNow().Add(maxLifetime.value)
	}
	record.Started = computeNow()
	served[record.Name] = record
	if err := writeServed(served); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "  start      hi-serve-%s on %s:%d\n", record.Name, portPlaces(record.Address), record.Port)
	if err := startLocal(record, stdout); err != nil {
		return err
	}
	if err := finishLocalStart(record, m, stdout); err != nil {
		return err
	}
	if record.Shared {
		fmt.Fprintf(stdout, "\nShared, with the same key, at %s.\n", record.sharedURL())
	}
	tellOrg(record, "up", stderr)
	return nil
}

func serveRemoveCommand(args []string, stdout, stderr io.Writer) error {
	flags := newComputeFlags("serve rm", stderr)
	positional, err := parseInterspersedFlags(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return usageError{"usage: hi serve rm <name>"}
	}
	served, err := loadServed()
	if err != nil {
		return err
	}
	record, ok := served[positional[0]]
	if !ok {
		return fmt.Errorf("%s isn't served; see hi serve ls", positional[0])
	}
	if record.Provider != "local" {
		return fmt.Errorf("%s is on %s, where stopping it ends it: hi serve stop %s", record.Name, record.Provider, record.Name)
	}
	if fileExists(filepath.Join(serveQuadletDir(), "hi-serve-"+record.Name+".container")) {
		if !ownsUnits(record.Name) {
			return fmt.Errorf("the units of %s aren't labeled as hi serve's, so hi leaves them alone", record.Name)
		}
		stopLocalUnits(record)
		// Quadlet's network units stay active once they made the network:
		// stopped here, so serving the name again makes it again.
		networks := []string{"hi-serve-" + record.Name + "-network.service", "hi-serve-" + record.Name + "-front-network.service"}
		serveSystemctl(append([]string{"stop"}, networks...)...)
		serveSystemctl(append([]string{"reset-failed"}, networks...)...)
		for _, unit := range []string{".network", "-front.network", ".container", "-front.container"} {
			os.Remove(filepath.Join(serveQuadletDir(), "hi-serve-"+record.Name+unit))
		}
		serveSystemctl("daemon-reload")
	}
	if path, err := boxLookPath("podman"); err == nil {
		engine := boxEngine{name: "podman", bin: path}
		filter := "label=hi.serve=" + record.Name
		if out, err := engine.output("ps", "-aq", "--filter", filter); err == nil && strings.TrimSpace(out) != "" {
			engine.output(append([]string{"rm", "-f"}, strings.Fields(out)...)...)
		}
		if out, err := engine.output("network", "ls", "-q", "--filter", filter); err == nil && strings.TrimSpace(out) != "" {
			engine.output(append([]string{"network", "rm"}, strings.Fields(out)...)...)
		}
	}
	tellOrg(record, "rm", stderr)
	os.RemoveAll(serveKeysDir(record.Name))
	delete(served, record.Name)
	if err := writeServed(served); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Removed %s. Its model stays in %s; hi serve models rm %s deletes it.\n", record.Name,
		filepath.Join("~/.local/share/hi/models", record.ModelDir), record.Repo)
	return nil
}

func serveStatusCommand(args []string, stdout, stderr io.Writer) error {
	flags := newComputeFlags("serve status", stderr)
	positional, err := parseInterspersedFlags(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return usageError{"usage: hi serve status <name>"}
	}
	served, err := loadServed()
	if err != nil {
		return err
	}
	record, ok := served[instanceName(positional[0])]
	if !ok {
		return fmt.Errorf("%s isn't served; see hi serve ls", positional[0])
	}
	if record.Provider != "local" {
		fmt.Fprintf(stdout, "Name:     %s\nWhere:    %s\nModel:    %s (%s:%s)\nSince:    %s\n", record.Name, record.Provider, record.Model, record.Repo, record.Quant, record.Started.Format("2006-01-02 15:04"))
		fmt.Fprintf(stdout, "\nThe machine under it: hi compute status %s\n", record.Name)
		return nil
	}
	state := localState(record)
	unit := localUnit(record.Name)
	fmt.Fprintf(stdout, "Name:     %s\nKind:     %s\nWhere:    local (%s)\n", record.Name, record.kind(), backendOf(record))
	fmt.Fprintf(stdout, "State:    %s\n", state.text)
	if state.running && unit.since != "" {
		fmt.Fprintf(stdout, "Since:    %s\n", unit.since)
	}
	fmt.Fprintf(stdout, "Model:    %s (%s:%s at %s)\n", record.Model, record.Repo, record.Quant, shortCommit(record.Revision))
	if record.Recipe != "" {
		fmt.Fprintf(stdout, "Recipe:   %s\n", record.Recipe)
	}
	fmt.Fprintf(stdout, "Address:  http://127.0.0.1:%d/v1\n", record.Port)
	if record.servesShared() {
		fmt.Fprintf(stdout, "Shared:   %s, with a key (%s)\n", record.sharedURL(), sharedKeysText(record))
	}
	if record.Org {
		fmt.Fprintf(stdout, "Org:      %s\n", orgUpstreamState(record))
	}
	memory := fmt.Sprintf("needs %s in all; the box's host side is limited to %s", sizeText(record.Need), sizeText(record.Memory))
	switch {
	case record.VRAM > 0:
		memory = fmt.Sprintf("needs %s of GPU memory and %s on the host, where the box is limited to %s", sizeText(record.VRAM), sizeText(record.Need), sizeText(record.Memory))
	case !usesGPU(backendOf(record)):
		memory = fmt.Sprintf("needs %s, on the CPU; the box is limited to %s", sizeText(record.Need), sizeText(record.Memory))
	}
	if used := serveBoxMemory(record.Name); used > 0 {
		memory += fmt.Sprintf(", and uses %s of it now", sizeText(used))
	}
	fmt.Fprintf(stdout, "Memory:   %s\n", memory)
	fmt.Fprintf(stdout, "Image:    %s\n", record.Image)
	if !record.Deadline.IsZero() {
		fmt.Fprintf(stdout, "Stops:    %s (--max)\n", record.Deadline.Format("2006-01-02 15:04"))
	}
	fmt.Fprintf(stdout, "Units:    hi-serve-%s.service, hi-serve-%s-front.service\n", record.Name, record.Name)
	fmt.Fprintf(stdout, "\nConnect:  OPENAI_BASE_URL=http://127.0.0.1:%d/v1 with any API key, model %s\n", record.Port, record.Model)
	if record.Shared {
		fmt.Fprintf(stdout, "Shared:   OPENAI_BASE_URL=%s OPENAI_API_KEY=$(hi serve key %s)\n", record.sharedURL(), record.Name)
	}
	if record.Org {
		fmt.Fprintf(stdout, "Org:      hi q --model %s, on a device that joined the org\n", orgModelID(record.Name))
	}
	if state.text == "stopped: out of memory" {
		fmt.Fprintf(stdout, "\nThe kernel killed it for memory, so it stays stopped; hi serve start %s checks memory again first.\n", record.Name)
	}
	return nil
}

// serveBoxMemory is what a served box's cgroup holds now, or 0.
func serveBoxMemory(name string) int64 {
	path, err := boxLookPath("podman")
	if err != nil {
		return 0
	}
	engine := boxEngine{name: "podman", bin: path}
	out, err := engine.output("inspect", "--format", "{{.State.CgroupPath}}", "hi-serve-"+name)
	if err != nil {
		return 0
	}
	data, err := os.ReadFile(servePath(filepath.Join("/sys/fs/cgroup", strings.TrimSpace(out), "memory.current")))
	if err != nil {
		return 0
	}
	used, _ := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	return used
}

func localLogs(record servedModel, args []string, stdout, stderr io.Writer) error {
	flags := newComputeFlags("serve logs", stderr)
	follow := flags.Bool("follow", false, "keep printing new lines")
	lines := flags.Int("n", 40, "lines")
	if _, err := parseInterspersedFlags(flags, args); err != nil {
		return err
	}
	journal := []string{"-u", "hi-serve-" + record.Name + ".service", "-u", "hi-serve-" + record.Name + "-front.service",
		"-n", strconv.Itoa(*lines), "-o", "cat", "--no-pager"}
	if *follow {
		journal = append(journal, "-f")
	}
	return serveJournal(stdout, stderr, journal...)
}

// ---------------------------------------------------------------------------
// recipes, pull, models

func serveRecipesCommand(args []string, stdout, stderr io.Writer) error {
	flags := newComputeFlags("serve recipes", stderr)
	backendFlag := flags.String("backend", "", "rocm, vulkan, cuda, or cpu (default: what the GPU points to)")
	positional, err := parseInterspersedFlags(flags, args)
	if err != nil {
		return err
	}
	if len(positional) > 1 || (len(positional) == 1 && positional[0] != "llm") {
		if len(positional) == 1 && isServeKind(positional[0]) {
			return fmt.Errorf("hi serve %s isn't built yet; hi serve llm is", positional[0])
		}
		return usageError{"usage: hi serve recipes [llm] [--backend rocm|vulkan|cuda|cpu]"}
	}
	backend := *backendFlag
	if backend == "" {
		backend = defaultServeBackend()
	} else if !isServeBackend(backend) {
		return usageError{fmt.Sprintf("--backend %s: hi serve knows %s", backend, strings.Join(serveBackends, ", "))}
	}
	m := detectServeMachine(backend)
	table := newServeTable(stdout)
	fmt.Fprintln(table, "RECIPE\tKIND\tMODEL\tRUNS ON\tHERE")
	for _, name := range serveRecipeNames() {
		recipe := serveRecipes[name]
		here := "no local profile"
		if backend != "" {
			here = "no " + backend + " profile"
		}
		if profile, ok := recipe.local[backend]; ok {
			need, _ := parseSize(profile.Need)
			vram, _ := parseSize(profile.VRAM)
			switch {
			case len(m.problems) > 0:
				here = "can't serve here (hi serve detect)"
			case vram > m.vramRoom():
				here = fmt.Sprintf("too big: needs %s of GPU memory, %s free", sizeText(vram), sizeText(m.vramRoom()))
			case need > m.room():
				here = fmt.Sprintf("too big: needs %s, %s free", sizeText(need), sizeText(m.room()))
			case vram > 0:
				here = fmt.Sprintf("fits on %s: needs %s of %s of GPU memory", backend, sizeText(vram), sizeText(m.vramRoom()))
			default:
				here = fmt.Sprintf("fits on %s: needs %s of %s", backend, sizeText(need), sizeText(m.room()))
			}
		}
		fmt.Fprintf(table, "%s\tllm\t%s:%s\t%s\t%s\n", name, recipe.repo, recipe.quant, strings.Join(recipe.places(), ", "), here)
	}
	table.Flush()
	fmt.Fprintln(stdout, "\nAny GGUF model on the Hub also serves: hi serve llm owner/Model-GGUF --quant Q4_K_M --on local")
	return nil
}

func isServeKind(word string) bool {
	for _, kind := range serveKinds {
		if word == kind {
			return true
		}
	}
	return false
}

func servePullCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := newComputeFlags("serve pull", stderr)
	quant := flags.String("quant", "", "GGUF quant")
	backend := flags.String("backend", "", "rocm, vulkan, cuda, or cpu (default: what the GPU points to)")
	yes := flags.Bool("yes", false, "skip confirmation")
	positional, err := parseInterspersedFlags(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 2 || positional[0] != "llm" {
		if len(positional) >= 1 && isServeKind(positional[0]) && positional[0] != "llm" {
			return fmt.Errorf("hi serve %s isn't built yet; hi serve llm is", positional[0])
		}
		return usageError{"usage: hi serve pull llm <recipe | owner/repo-GGUF --quant Q> [--backend rocm|vulkan|cuda|cpu]"}
	}
	if *backend == "" {
		if *backend = defaultServeBackend(); *backend == "" {
			return errors.New("no GPU was found here to pull an image for; choose one with --backend rocm, vulkan, cuda, or cpu")
		}
	} else if !isServeBackend(*backend) {
		return usageError{fmt.Sprintf("--backend %s: hi serve knows %s", *backend, strings.Join(serveBackends, ", "))}
	}
	plan, err := planLocal(positional[1], *quant, 0, "", "", *backend, "")
	if err != nil {
		return err
	}
	modelsDir, err := serveModelsDir()
	if err != nil {
		return err
	}
	modelDir := filepath.Join(modelsDir, modelDirName(plan.recipe.repo))
	download := toDownload(modelDir, plan.model)
	if download > 0 {
		free, err := freeDisk(modelsDir)
		if err != nil {
			return err
		}
		if free < download+download/10 {
			return fmt.Errorf("it needs %s to download, with 10%% to spare, and %s has %s free", sizeText(download+download/10), modelsDir, sizeText(free))
		}
		describeDownload(plan.model, modelDir, download, stdout)
		if err := confirm(stdin, stdout, *yes, fmt.Sprintf("Download %s?", sizeText(download))); err != nil {
			return err
		}
	}
	if err := downloadModel(modelDir, plan.model, stdout); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s:%s at %s is here, checked: %s\n", plan.recipe.repo, plan.recipe.quant, shortCommit(plan.model.Revision), modelDir)
	path, err := boxLookPath("podman")
	if err != nil {
		fmt.Fprintln(stdout, "Podman isn't installed, so its image isn't pulled; hi install podman adds it.")
		return nil
	}
	if err := pullServeImage(boxEngine{name: "podman", bin: path}, plan.profile.Image, *backend, stdout, stderr); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Its image is here: %s\n", plan.profile.Image)
	return nil
}

// downloadedModel is one repository's folder under the models folder.
type downloadedModel struct {
	Repo  string   `json:"repo"`
	Path  string   `json:"path"`
	Size  int64    `json:"size"`
	Users []string `json:"used_by"`
}

func downloadedModels() ([]downloadedModel, error) {
	dir, err := serveModelsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	served, err := loadServed()
	if err != nil {
		return nil, err
	}
	var models []downloadedModel
	for _, entry := range entries {
		if !entry.IsDir() || !strings.Contains(entry.Name(), "--") {
			continue
		}
		model := downloadedModel{Repo: strings.Replace(entry.Name(), "--", "/", 1), Path: filepath.Join(dir, entry.Name()), Users: []string{}}
		filepath.WalkDir(model.Path, func(_ string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if info, err := d.Info(); err == nil {
					model.Size += info.Size()
				}
			}
			return nil
		})
		for _, name := range sortedKeys(served) {
			if record := served[name]; record.Provider == "local" && record.ModelDir == entry.Name() {
				model.Users = append(model.Users, name)
			}
		}
		models = append(models, model)
	}
	return models, nil
}

func serveModelsCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "rm" {
		return serveModelsRemove(args[1:], stdin, stdout, stderr)
	}
	flags := newComputeFlags("serve models", stderr)
	asJSON := flags.Bool("json", false, "print JSON")
	positional, err := parseInterspersedFlags(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 0 {
		return usageError{"usage: hi serve models [--json] | hi serve models rm <owner/repo>"}
	}
	models, err := downloadedModels()
	if err != nil {
		return err
	}
	if *asJSON {
		if models == nil {
			models = []downloadedModel{}
		}
		data, _ := json.MarshalIndent(models, "", "  ")
		fmt.Fprintln(stdout, string(data))
		return nil
	}
	if len(models) == 0 {
		fmt.Fprintln(stdout, "No models are downloaded here. hi serve pull llm <recipe> downloads one.")
		return nil
	}
	table := newServeTable(stdout)
	fmt.Fprintln(table, "MODEL\tSIZE\tUSED BY")
	for _, model := range models {
		users := strings.Join(model.Users, ", ")
		if users == "" {
			users = "-"
		}
		fmt.Fprintf(table, "%s\t%s\t%s\n", model.Repo, sizeText(model.Size), users)
	}
	table.Flush()
	return nil
}

func serveModelsRemove(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := newComputeFlags("serve models rm", stderr)
	yes := flags.Bool("yes", false, "skip confirmation")
	positional, err := parseInterspersedFlags(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return usageError{"usage: hi serve models rm <owner/repo>"}
	}
	models, err := downloadedModels()
	if err != nil {
		return err
	}
	for _, model := range models {
		if !strings.EqualFold(model.Repo, positional[0]) {
			continue
		}
		if len(model.Users) > 0 {
			return fmt.Errorf("%s is used by %s; hi serve rm %s first", model.Repo, strings.Join(model.Users, ", "), model.Users[0])
		}
		if err := confirm(stdin, stdout, *yes, fmt.Sprintf("Delete %s (%s)?", model.Path, sizeText(model.Size))); err != nil {
			return err
		}
		if err := os.RemoveAll(model.Path); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Deleted %s.\n", model.Path)
		return nil
	}
	return fmt.Errorf("%s isn't downloaded here; see hi serve models", positional[0])
}

func newServeTable(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
}
