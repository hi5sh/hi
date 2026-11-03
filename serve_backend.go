package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// A local server runs on one backend: ROCm or Vulkan on an AMD GPU, CUDA on
// an NVIDIA one, or the CPU (hi_serve.md, release 4). Each has its own
// llama.cpp image, from the one build a recipe pins, and its own devices.
// Which one a machine has is found from evidence, never from names.

const (
	backendROCm   = "rocm"
	backendVulkan = "vulkan"
	backendCUDA   = "cuda"
	backendCPU    = "cpu"

	serveVRAMHeadroom = 1 << 30
)

var serveBackends = []string{backendROCm, backendVulkan, backendCUDA, backendCPU}

// serveDefaultImages serve an ad hoc model, from llama.cpp build b12033.
// ROCm's is its amd64 image, as it is built for no other; the others are
// pinned by their index, so arm64 machines get theirs.
var serveDefaultImages = map[string]string{
	backendROCm:   "ghcr.io/ggml-org/llama.cpp:server-rocm@sha256:1bc7c96d72e2e948d30fe8ff10ee204881d86a9260290bab352c4eb770ce7756",
	backendVulkan: "ghcr.io/ggml-org/llama.cpp:server-vulkan@sha256:421137599beb2135a9e1d664fa8991a3f4f5f953440a9d7622c7a61b63185fe8",
	backendCUDA:   "ghcr.io/ggml-org/llama.cpp:server-cuda@sha256:0a9f6c7cfe5a6b90b6c048b7a86865bf55a88af34bada927482eab2ef5f25b4a",
	backendCPU:    "ghcr.io/ggml-org/llama.cpp:server@sha256:0b0212befa4facc6fd1e34ee04e19f13b570d0b9ec87d26edf4c402dc0dd636f",
}

// serveDefaultCUDA is the CUDA the default CUDA image is built with.
const serveDefaultCUDA = "12.8"

// serveImageSizes are what each backend's image is, roughly, for the pull.
var serveImageSizes = map[string]string{
	backendROCm: "about 23 GB", backendVulkan: "about 1 GB", backendCUDA: "about 3 GB", backendCPU: "about 1 GB",
}

func isServeBackend(name string) bool {
	for _, backend := range serveBackends {
		if name == backend {
			return true
		}
	}
	return false
}

// backendOf is a record's backend; records from before release 4 are ROCm.
func backendOf(record servedModel) string {
	if record.Backend == "" {
		return backendROCm
	}
	return record.Backend
}

// usesGPU reports whether a backend loads the model onto a GPU.
func usesGPU(backend string) bool { return backend != backendCPU }

// serveNvidiaSMI runs nvidia-smi; tests replace it.
var serveNvidiaSMI = func(args ...string) (string, error) {
	out, err := exec.Command("nvidia-smi", args...).Output()
	return string(out), err
}

// defaultServeBackend is the backend this machine's GPU points to: ROCm
// for an AMD GPU, CUDA for an NVIDIA one, and none without a GPU, since
// the CPU is never a silent fallback.
func defaultServeBackend() string {
	switch {
	case kfdGPU() != "":
		return backendROCm
	case nvidiaDriverVersion() != "":
		return backendCUDA
	}
	return ""
}

// nvidiaGPUs is what nvidia-smi says of this machine's NVIDIA GPUs.
type nvidiaGPUs struct {
	names       []string
	driver      string // such as 570.124.06
	cuda        string // the newest CUDA the driver runs, such as 12.8
	total, free int64  // memory over all of them
}

// nvidiaDriverVersion is the loaded kernel module's version, or "".
func nvidiaDriverVersion() string {
	if data, err := os.ReadFile(servePath("/sys/module/nvidia/version")); err == nil {
		if version := strings.TrimSpace(string(data)); version != "" {
			return version
		}
	}
	data, err := os.ReadFile(servePath("/proc/driver/nvidia/version"))
	if err != nil {
		return ""
	}
	// NVRM version: NVIDIA UNIX x86_64 Kernel Module  570.124.06  Wed Feb 26 ...
	// NVRM version: NVIDIA UNIX Open Kernel Module for x86_64  580.126.09  Release Build ...
	for _, field := range strings.Fields(strings.SplitN(string(data), "\n", 2)[0]) {
		if strings.Contains(field, ".") && strings.Trim(field, "0123456789.") == "" {
			return field
		}
	}
	return "unknown"
}

// readNvidiaGPUs asks nvidia-smi, which comes with the driver.
func readNvidiaGPUs() (nvidiaGPUs, error) {
	var gpus nvidiaGPUs
	out, err := serveNvidiaSMI("--query-gpu=name,driver_version,memory.total,memory.free", "--format=csv,noheader,nounits")
	if err != nil {
		return gpus, err
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.Split(line, ",")
		if len(parts) != 4 {
			continue
		}
		gpus.names = append(gpus.names, strings.TrimSpace(parts[0]))
		gpus.driver = strings.TrimSpace(parts[1])
		total, _ := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
		free, _ := strconv.ParseInt(strings.TrimSpace(parts[3]), 10, 64)
		gpus.total += total << 20
		gpus.free += free << 20
	}
	if len(gpus.names) == 0 {
		return gpus, fmt.Errorf("nvidia-smi lists no GPU")
	}
	// The newest CUDA it runs is only in the header of its plain output.
	header, err := serveNvidiaSMI()
	if err != nil {
		return gpus, err
	}
	if _, after, ok := strings.Cut(header, "CUDA Version:"); ok {
		if fields := strings.Fields(after); len(fields) > 0 {
			gpus.cuda = strings.Trim(fields[0], "|")
		}
	}
	return gpus, nil
}

// nvidiaCDI reports whether Podman can find the NVIDIA GPUs by name: a CDI
// spec for nvidia.com/gpu, which NVIDIA's Container Toolkit writes.
func nvidiaCDI() bool {
	for _, dir := range []string{"/etc/cdi", "/var/run/cdi"} {
		files, _ := filepath.Glob(servePath(filepath.Join(dir, "*")))
		for _, file := range files {
			if ext := filepath.Ext(file); ext != ".yaml" && ext != ".yml" && ext != ".json" {
				continue
			}
			if data, err := os.ReadFile(file); err == nil && strings.Contains(string(data), "nvidia.com/gpu") {
				return true
			}
		}
	}
	return false
}

// detectAMD finds an AMD GPU for ROCm, which opens /dev/kfd and /dev/dri,
// or for Vulkan, which opens /dev/dri only.
func (m *serveMachine) detectAMD() {
	m.gpu = kfdGPU()
	m.devices = fileExists(servePath("/dev/dri"))
	if m.backend == backendROCm {
		m.devices = m.devices && fileExists(servePath("/dev/kfd"))
	}
	switch {
	case m.gpu == "":
		m.problems = append(m.problems, "No AMD GPU was found (the kernel's KFD lists none). On a Strix Halo, hi install strix sets it up, and hi verify strix says what is missing.")
	case m.gpu != "gfx1151":
		m.problems = append(m.problems, fmt.Sprintf("The GPU is %s; local recipes are tested on Strix Halo (gfx1151) only so far.", m.gpu))
	}
	if m.gpu != "" && !m.devices {
		devices := "/dev/dri is"
		if m.backend == backendROCm {
			devices = "/dev/kfd or /dev/dri is"
		}
		m.problems = append(m.problems, devices+" missing; hi install strix sets the GPU up.")
	}
	groups := serveGroupNames()
	for _, want := range []string{"render", "video"} {
		found := false
		for _, group := range groups {
			found = found || group == want
		}
		if !found {
			m.missingGroups = append(m.missingGroups, want)
		}
	}
	if len(m.missingGroups) > 0 {
		m.problems = append(m.problems, fmt.Sprintf("You aren't in the %s %s that open the GPU; hi install strix adds you, and you log in again after.",
			strings.Join(m.missingGroups, " and "), map[bool]string{true: "groups", false: "group"}[len(m.missingGroups) > 1]))
	}
}

// detectNVIDIA finds an NVIDIA GPU, its driver, and the CDI spec Podman
// passes it in by.
func (m *serveMachine) detectNVIDIA() {
	m.driver = nvidiaDriverVersion()
	if m.driver == "" {
		m.problems = append(m.problems, "No NVIDIA driver is loaded (no /sys/module/nvidia). Installing it takes sudo and a reboot; ask your IT team.")
		return
	}
	gpus, err := readNvidiaGPUs()
	if err != nil {
		m.problems = append(m.problems, fmt.Sprintf("The NVIDIA driver %s is loaded, but nvidia-smi doesn't answer (%v); it comes with the driver.", m.driver, err))
		return
	}
	m.nvidia = gpus
	m.devices = nvidiaCDI()
	if !m.devices {
		m.problems = append(m.problems, "Podman passes NVIDIA GPUs in through CDI, and this machine has no CDI spec for them. With NVIDIA's Container Toolkit installed, an admin runs: sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml (ask your IT team).")
	}
}

// checkCUDA refuses an image built with a newer CUDA than the driver runs,
// before anything is downloaded or pulled.
func checkCUDA(m serveMachine, imageCUDA string) error {
	if imageCUDA == "" || m.nvidia.cuda == "" {
		return nil
	}
	if compareVersions(m.nvidia.cuda, imageCUDA) < 0 {
		return fmt.Errorf("its image is built with CUDA %s, and this machine's NVIDIA driver %s runs CUDA %s at most: "+
			"a driver for CUDA %s or later is needed (ask your IT team), so nothing was downloaded or started", imageCUDA, m.nvidia.driver, m.nvidia.cuda, imageCUDA)
	}
	return nil
}

// compareVersions compares dotted numbers, such as 12.8 and 12.10.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(as), len(bs)); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// vramRoom is the GPU memory a model may take on an NVIDIA machine.
func (m serveMachine) vramRoom() int64 {
	return max(0, m.nvidia.free-serveVRAMHeadroom)
}

// backendUnitLines are the box's devices and what they need, and the
// box's own Podman arguments. GroupAdd= and Entrypoint= are written as
// arguments: Quadlet only has them from Podman 5, and Ubuntu 24.04 has 4.9.
func backendUnitLines(backend string) (lines, podmanArgs string) {
	switch backend {
	case backendVulkan:
		// Mesa's shader cache goes on the box's tmpfs: its root is read-only.
		return "AddDevice=/dev/dri\nEnvironment=MESA_SHADER_CACHE_DIR=/tmp/mesa\nReadOnly=true\n", " --group-add=keep-groups"
	case backendCUDA:
		// NVIDIA's CDI hooks link the driver's libraries into the box's root
		// as it starts, so its root can't be read-only; the box runs as you,
		// and can't write the image's files (hi_serve.md, Findings 9).
		return "AddDevice=nvidia.com/gpu=all\n", ""
	case backendCPU:
		return "ReadOnly=true\n", ""
	}
	return "AddDevice=/dev/kfd\nAddDevice=/dev/dri\nReadOnly=true\n", " --group-add=keep-groups"
}

// backendText says what a backend is, for detect.
func backendText(backend string) string {
	switch backend {
	case backendROCm:
		return "rocm (the image brings ROCm; --backend vulkan or cpu also run here)"
	case backendVulkan:
		return "vulkan (the image brings Mesa's RADV driver)"
	case backendCUDA:
		return "cuda (the image brings CUDA; the driver is the host's)"
	case backendCPU:
		return "cpu (only for recipes with a CPU profile)"
	}
	return "none: no GPU found (--backend cpu serves a recipe with a CPU profile)"
}
