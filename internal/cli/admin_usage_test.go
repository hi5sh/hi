package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Home's numbers: the machine's CPU, memory, and swap from /proc, and each
// app's from its service's cgroup and its boxes' together.
func TestAdminUsageOfTheMachineAndItsApps(t *testing.T) {
	a := setupAdminTest(t)
	root := t.TempDir()
	write := func(path, text string) {
		t.Helper()
		full := filepath.Join(root, path)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := "user.slice/user-1000.slice/user@1000.service"
	write("proc/self/cgroup", "0::/"+base+"/app.slice/hi-admin.service\n")
	write("proc/self/mounts", "/dev/root / ext4 rw 0 0\n")
	write("proc/meminfo", "MemTotal:       16000000 kB\nMemFree:  1000 kB\nMemAvailable:    4000000 kB\nSwapTotal: 2000000 kB\nSwapFree: 1500000 kB\n")
	write("proc/loadavg", "1.50 1.20 0.90 2/900 12345\n")
	write("proc/uptime", "3600.5 100.0\n")
	write("proc/stat", "cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 1 1 1 1\n")
	service := "sys/fs/cgroup/" + base + "/app.slice/hi-app-payments.service/"
	write(service+"memory.current", "1000000\n")
	write(service+"cpu.stat", "usage_usec 1000000\nuser_usec 1\n")
	box := "sys/fs/cgroup/" + base + "/user.slice/libpod-abc.scope/"
	write(box+"memory.current", "3000000\n")
	write(box+"cpu.stat", "usage_usec 0\n")
	previous := adminProcRoot
	adminProcRoot = root
	t.Cleanup(func() { adminProcRoot = previous })
	adminBoxCgroups = func() map[string]string {
		return map[string]string{"hi-box-app-payments-dev": "/" + base + "/user.slice/libpod-abc.scope", "hi-box-app-paymentsx": "/elsewhere", "hi-admin-peer": "/x"}
	}

	a.s.sample()
	first := a.s.sampler.latest
	if first.CPU != nil || first.MemTotal != 16000000*1024 || first.MemUsed != 12000000*1024 || first.SwapUsed != 500000*1024 || first.Load[0] != 1.5 || first.Uptime != 3600.5 {
		t.Fatalf("the machine: %+v", first)
	}
	app := first.Apps["payments"]
	if app == nil || app.Memory != 4000000 || app.Boxes != 1 || app.CPU != nil {
		t.Fatalf("payments: %+v", app)
	}
	// A second sample: half the machine's CPU time was busy, and the app
	// used some of it.
	write("proc/stat", "cpu  300 0 300 1200 0 0 0 0 0 0\n")
	write(service+"cpu.stat", "usage_usec 1500000\n")
	a.s.sampler.mu.Lock()
	a.s.sampler.at = time.Now().Add(-time.Second)
	a.s.sampler.mu.Unlock()
	a.s.sample()
	second := a.s.sampler.latest
	if second.CPU == nil || *second.CPU != 0.5 || second.Apps["payments"].CPU == nil || *second.Apps["payments"].CPU <= 0 {
		t.Fatalf("CPU: %+v %+v", second.CPU, second.Apps["payments"])
	}
	// The folder is measured once, in the background.
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.s.sampler.mu.Lock()
		done := a.s.sampler.disk["payments"] == 42
		a.s.sampler.mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.s.sample()
	if a.s.sampler.latest.Apps["payments"].Disk != 42 {
		t.Fatalf("disk: %+v", a.s.sampler.latest.Apps["payments"])
	}

	// The page gets it with its state.
	olga := a.signIn(t, "Olga")
	var state adminStateView
	a.get(t, olga, "/api/state", &state)
	if state.Usage == nil || state.Usage.Apps["payments"] == nil {
		t.Fatalf("state: %+v", state.Usage)
	}
}

func TestAdminBoxApp(t *testing.T) {
	apps := []string{"cart", "cartman", "goat"}
	for box, want := range map[string]string{
		"hi-box-app-cartman-dev":        "cartman",
		"hi-box-app-cart":               "cart",
		"hi-box-app-cart-proxy":         "cart",
		"hi-box-app-goat-app-preview-1": "goat",
		"hi-admin-peer":                 "",
		"hi-box-app-goats":              "",
	} {
		if got := adminBoxApp(box, apps); got != want {
			t.Errorf("%s: %q, not %q", box, got, want)
		}
	}
}
