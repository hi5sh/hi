package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// nvidiaMachine makes the fake machine an NVIDIA one: no AMD GPU, a loaded
// driver for CUDA cudaVersion, nvidia-smi with freeMiB free, and, with cdi,
// the CDI spec NVIDIA's Container Toolkit writes.
func nvidiaMachine(t *testing.T, f *fakeLocal, cudaVersion string, freeMiB int, cdi bool) {
	t.Helper()
	os.RemoveAll(filepath.Join(f.root, "sys/class/kfd"))
	os.Remove(filepath.Join(f.root, "dev/kfd"))
	os.MkdirAll(filepath.Join(f.root, "proc/driver/nvidia"), 0o755)
	os.WriteFile(filepath.Join(f.root, "proc/driver/nvidia/version"),
		[]byte("NVRM version: NVIDIA UNIX Open Kernel Module for x86_64  570.124.06  Release Build  (dvs-builder@U22)  Wed Feb 26 2026\nGCC version:  gcc 13\n"), 0o644)
	if cdi {
		os.MkdirAll(filepath.Join(f.root, "etc/cdi"), 0o755)
		os.WriteFile(filepath.Join(f.root, "etc/cdi/nvidia.yaml"), []byte("cdiVersion: 0.5.0\nkind: nvidia.com/gpu\ndevices:\n- name: all\n"), 0o644)
	}
	saved := serveNvidiaSMI
	t.Cleanup(func() { serveNvidiaSMI = saved })
	serveNvidiaSMI = func(args ...string) (string, error) {
		if len(args) == 0 {
			return "| NVIDIA-SMI 570.124.06    Driver Version: 570.124.06    CUDA Version: " + cudaVersion + "     |\n", nil
		}
		return "NVIDIA RTX PRO 6000 Blackwell, 570.124.06, 97887, " + strconv.Itoa(freeMiB) + "\n", nil
	}
}

const adhocModel = "unsloth/Qwen3.8-Flash-Next-GGUF"

// Vulkan opens /dev/dri only, so it serves without /dev/kfd, from its own
// image, with Mesa's shader cache on the box's tmpfs.
func TestServeLocalOnVulkan(t *testing.T) {
	f := newFakeLocal(t)
	os.Remove(filepath.Join(f.root, "dev/kfd"))
	if code, stdout, _ := runHi("serve", "detect"); code != 0 || !strings.Contains(stdout, "Devices:    missing") {
		t.Fatalf("ROCm without /dev/kfd: %d\n%s", code, stdout)
	}
	code, stdout, _ := runHi("serve", "detect", "--backend", "vulkan")
	if code != 0 || !strings.Contains(stdout, "Devices:    /dev/dri\n") || !strings.Contains(stdout, "This machine can serve models") {
		t.Fatalf("detect --backend vulkan: %d\n%s", code, stdout)
	}
	port := strconv.Itoa(f.port)
	code, stdout, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--backend", "vulkan", "--port", port, "--yes")
	if code != 0 || !strings.Contains(stdout, "tested on Strix Halo, 2026-11-03, build 12033: 28 tokens/s") || !strings.Contains(stdout, "about 1 GB") {
		t.Fatalf("serve on vulkan: %d\n%s%s", code, stdout, stderr)
	}
	box := quadletFile(t, "hi-serve-qwen3-8-flash-next.container")
	for _, want := range []string{"Image=ghcr.io/ggml-org/llama.cpp:server-vulkan@sha256:4211375", "AddDevice=/dev/dri\nEnvironment=MESA_SHADER_CACHE_DIR=/tmp/mesa\nReadOnly=true\n",
		"--memory=73728m --oom-score-adj=1000 --group-add=keep-groups\n", "-ngl 999"} {
		if !strings.Contains(box, want) {
			t.Fatalf("the box lacks %q:\n%s", want, box)
		}
	}
	if strings.Contains(box, "/dev/kfd") {
		t.Fatalf("Vulkan was given /dev/kfd:\n%s", box)
	}
	if front := quadletFile(t, "hi-serve-qwen3-8-flash-next-front.container"); strings.Contains(front, "AddDevice") {
		t.Fatalf("the front was given a device:\n%s", front)
	}
	if _, stdout, _ := runHi("serve", "status", "qwen3-8-flash-next"); !strings.Contains(stdout, "Where:    local (vulkan)") {
		t.Fatalf("status: %s", stdout)
	}
	if _, stdout, _ := runHi("serve", "recipes", "--backend", "vulkan"); !strings.Contains(stdout, "local (cpu, cuda, rocm, vulkan)") || !strings.Contains(stdout, "fits on vulkan") {
		t.Fatalf("recipes: %s", stdout)
	}
}

// The CPU is only for a recipe's tested profile, chosen with --backend cpu,
// never a fallback when no GPU is found.
func TestServeLocalOnTheCPU(t *testing.T) {
	f := newFakeLocal(t)
	os.RemoveAll(filepath.Join(f.root, "sys/class/kfd"))
	os.Remove(filepath.Join(f.root, "dev/kfd"))
	code, _, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--yes")
	if code == 0 || !strings.Contains(stderr, "No GPU was found") || !strings.Contains(stderr, "--backend cpu") {
		t.Fatalf("no GPU: %d %s", code, stderr)
	}
	if _, system := f.calls(); f.gets != 0 || system != "" {
		t.Fatalf("no GPU, and something started: %s", system)
	}
	if code, _, stderr := runHi("serve", "llm", adhocModel, "--quant", "UD-Q3_K_XL", "--on", "local", "--backend", "cpu", "--yes"); code == 0 || !strings.Contains(stderr, "isn't served on the CPU") {
		t.Fatalf("an ad hoc model on the CPU: %d %s", code, stderr)
	}
	port := strconv.Itoa(f.port)
	code, stdout, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--backend", "cpu", "--port", port, "--yes")
	if code != 0 || !strings.Contains(stdout, "check      it needs 88.0 GB with its context") || !strings.Contains(stdout, "a box with the CPU only") {
		t.Fatalf("serve on the CPU: %d\n%s%s", code, stdout, stderr)
	}
	box := quadletFile(t, "hi-serve-qwen3-8-flash-next.container")
	for _, want := range []string{"Image=ghcr.io/ggml-org/llama.cpp:server@sha256:0b0212b", "-ngl 0 ", "--memory=90112m --oom-score-adj=1000\n", "ReadOnly=true\n"} {
		if !strings.Contains(box, want) {
			t.Fatalf("the box lacks %q:\n%s", want, box)
		}
	}
	if strings.Contains(box, "AddDevice") || strings.Contains(box, "group-add") {
		t.Fatalf("the CPU's box was given a device:\n%s", box)
	}
	if _, stdout, _ := runHi("serve", "status", "qwen3-8-flash-next"); !strings.Contains(stdout, "needs 88.0 GB, on the CPU") {
		t.Fatalf("status: %s", stdout)
	}
}

// On an NVIDIA machine CUDA is the default. Podman passes the GPU in by its
// CDI name, the driver is checked against the image's CUDA before anything
// is downloaded or pulled, and the model must fit in the GPU's free memory.
func TestServeLocalOnNVIDIA(t *testing.T) {
	f := newFakeLocal(t)
	nvidiaMachine(t, f, "12.8", 90000, false)
	code, stdout, _ := runHi("serve", "detect")
	for _, want := range []string{"GPU:        NVIDIA RTX PRO 6000 Blackwell", "Driver:     570.124.06, for CUDA 12.8 and older", "GPU memory: 95.6 GB, 87.9 GB free",
		"Devices:    missing", "Backend:    cuda", "sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml"} {
		if code != 0 || !strings.Contains(stdout, want) {
			t.Fatalf("detect lacks %q: %d\n%s", want, code, stdout)
		}
	}
	if code, _, stderr := runHi("serve", "llm", adhocModel, "--quant", "UD-Q3_K_XL", "--on", "local", "--yes"); code == 0 || !strings.Contains(stderr, "nvidia-ctk cdi generate") {
		t.Fatalf("no CDI spec: %d %s", code, stderr)
	}

	nvidiaMachine(t, f, "12.4", 90000, true)
	code, stdout, stderr := runHi("serve", "llm", adhocModel, "--quant", "UD-Q3_K_XL", "--on", "local", "--yes")
	if code == 0 || !strings.Contains(stderr, "built with CUDA 12.8, and this machine's NVIDIA driver 570.124.06 runs CUDA 12.4 at most") {
		t.Fatalf("an old driver: %d\n%s%s", code, stdout, stderr)
	}
	if podman, system := f.calls(); f.gets != 0 || system != "" || strings.Contains(podman, "pull") {
		t.Fatalf("an old driver, and something was downloaded or pulled: %d %s %s", f.gets, system, podman)
	}

	nvidiaMachine(t, f, "13.0", 512, true)
	if code, _, stderr := runHi("serve", "llm", adhocModel, "--quant", "UD-Q3_K_XL", "--on", "local", "--yes"); code == 0 || !strings.Contains(stderr, "of GPU memory by estimate") || !strings.Contains(stderr, "short, so nothing was started") {
		t.Fatalf("too little GPU memory: %d %s", code, stderr)
	}
	if code, _, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--yes"); code == 0 || !strings.Contains(stderr, "it needs 66.0 GB of GPU memory with its context") {
		t.Fatalf("the recipe on too small a GPU: %d %s", code, stderr)
	}

	nvidiaMachine(t, f, "13.0", 90000, true)
	port := strconv.Itoa(f.port)
	code, stdout, stderr = runHi("serve", "llm", adhocModel, "--quant", "UD-Q3_K_XL", "--on", "local", "--port", port, "--yes")
	if code != 0 || !strings.Contains(stdout, "of GPU memory and 4.0 GB on the host: fits") || !strings.Contains(stdout, "about 3 GB") {
		t.Fatalf("serve on cuda: %d\n%s%s", code, stdout, stderr)
	}
	box := quadletFile(t, "hi-serve-qwen3-8-flash-next.container")
	for _, want := range []string{"Image=ghcr.io/ggml-org/llama.cpp:server-cuda@sha256:0a9f6c7", "AddDevice=nvidia.com/gpu=all\n", "-ngl 999", "--memory=16384m"} {
		if !strings.Contains(box, want) {
			t.Fatalf("the box lacks %q:\n%s", want, box)
		}
	}
	if strings.Contains(box, "/dev/kfd") || strings.Contains(box, "/dev/dri") || strings.Contains(box, "group-add") {
		t.Fatalf("CUDA was given AMD's devices:\n%s", box)
	}
	// NVIDIA's CDI hooks write the driver's links into the root.
	if strings.Contains(box, "ReadOnly=true") || !strings.Contains(box, "DropCapability=ALL\n") {
		t.Fatalf("the CUDA box's root:\n%s", box)
	}
	served, _ := loadServed()
	record := served["qwen3-8-flash-next"]
	if record.Backend != "cuda" || record.CUDA != "12.8" || record.VRAM <= 1<<30 {
		t.Fatalf("the record: %+v", record)
	}
	if _, stdout, _ := runHi("serve", "status", "qwen3-8-flash-next"); !strings.Contains(stdout, "of GPU memory and 4.0 GB on the host") {
		t.Fatalf("status: %s", stdout)
	}

	// The recipe's own cuda profile, with its arguments for NVIDIA.
	if code, stdout, stderr := runHi("serve", "llm", "qwen3.8-flash-next", "--on", "local", "--name", "recipe", "--dry-run"); code != 0 ||
		!strings.Contains(stdout, "tested on RTX PRO 6000 Blackwell") || !strings.Contains(stdout, `-ot "per_layer_token_embd\\.weight=CPU" --lazy-mode off`) ||
		!strings.Contains(stdout, "--memory=65536m") || !strings.Contains(stdout, "it needs 66.0 GB of GPU memory and 8.0 GB on the host") {
		t.Fatalf("the recipe on cuda: %d\n%s%s", code, stdout, stderr)
	}

	// rm stops Quadlet's network units, so the name serves again.
	if code, stdout, stderr := runHi("serve", "rm", "qwen3-8-flash-next"); code != 0 {
		t.Fatalf("rm: %s%s", stdout, stderr)
	}
	if _, system := f.calls(); !strings.Contains(system, "stop hi-serve-qwen3-8-flash-next-network.service hi-serve-qwen3-8-flash-next-front-network.service") {
		t.Fatalf("rm left the network units:\n%s", system)
	}
	if code, stdout, stderr := runHi("serve", "llm", adhocModel, "--quant", "UD-Q3_K_XL", "--on", "local", "--port", port, "--yes"); code != 0 {
		t.Fatalf("serve again: %s%s", stdout, stderr)
	}

	// The driver is checked again at each start.
	if code, stdout, stderr := runHi("serve", "stop", "qwen3-8-flash-next"); code != 0 {
		t.Fatalf("stop: %s%s", stdout, stderr)
	}
	nvidiaMachine(t, f, "12.2", 90000, true)
	if code, _, stderr := runHi("serve", "start", "qwen3-8-flash-next"); code == 0 || !strings.Contains(stderr, "runs CUDA 12.2 at most") {
		t.Fatalf("start with an old driver: %d %s", code, stderr)
	}
}

// A recipe's local profile says where it was tested, and only a cuda
// profile has the GPU's memory and its image's CUDA.
func TestRecipeLocalProfiles(t *testing.T) {
	recipe := func(local string) error {
		_, err := parseRecipe([]byte(`{"kind":"llm","name":"x","model":{"repo":"o/r","quant":"Q4"},"alias":"x","context":8,"on":{"local":` + local + `}}`))
		return err
	}
	image := `"image":"i@sha256:1","need":"8G","limit":"8G","tested":"here"`
	for local, want := range map[string]string{
		`{"cuda":{` + image + `}}`:                                "on.local.cuda.vram",
		`{"cuda":{` + image + `,"vram":"6G"}}`:                    "on.local.cuda.cuda",
		`{"rocm":{` + image + `,"vram":"6G"}}`:                    "vram and cuda are for the cuda backend",
		`{"cpu":{"image":"i@sha256:1","need":"8G","limit":"8G"}}`: "says where it was tested",
		`{"metal":{` + image + `}}`:                               "unknown backend",
		`{"cuda":{` + image + `,"vram":"6G","cuda":"12.8"}}`:      "",
	} {
		err := recipe(local)
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("%s: %v, want %q", local, err, want)
		}
	}
	if compareVersions("12.10", "12.8") != 1 || compareVersions("12.8", "12.8.0") != 0 || compareVersions("12.4", "13") != -1 {
		t.Error("compareVersions")
	}
}
