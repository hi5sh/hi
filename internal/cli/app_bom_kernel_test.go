package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The boxes run on the machine's kernel: the bill lists the machine's
// running kernel as part machine, and an image's kernel headers aren't
// matched. A bill made before hi listed the kernel, or naming another
// one, is made again.
func TestTeamBomListsTheMachinesKernel(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	exec.Command("git", "-C", b.repo, "commit", "-q", "--allow-empty", "-m", "x").Run()
	saveTeamVersions("payments", appVersions{Production: 4, Releases: []appRelease{{Version: 4, Commit: boxGit(b.repo, "rev-parse", "HEAD")}}})
	tools := appTestBomTools()
	image := tools.image
	tools.image = func(name string) (appImageList, error) {
		list, err := image(name)
		list.Packages = append(list.Packages, [4]string{"linux", "7.0.0-42.42", "linux-libc-dev", "7.0.0-42.42"})
		return list, err
	}
	// A bill from before: no kernel.
	before, _, err := appProductionBom("payments", tools)
	if err != nil {
		t.Fatal(err)
	}
	kernel := appKernel{Release: "7.0.0-42-generic", OS: "ubuntu", OSRelease: "26.04", Binary: "linux-image-7.0.0-42-generic", Source: "linux", Version: "7.0.0-42.42"}
	tools.kernel = func() appKernel { return kernel }
	if !appBomMoved("payments", before, tools) {
		t.Fatal("a bill without the kernel stays")
	}
	path, _, err := appProductionBom("payments", tools)
	if err != nil {
		t.Fatal(err)
	}
	var bom cdxBom
	data, _ := os.ReadFile(path)
	json.Unmarshal(data, &bom)
	var machine, headers *cdxComponent
	for i, component := range bom.Components {
		switch {
		case component.property(bomPart) == "machine":
			machine = &bom.Components[i]
		case component.Name == "linux-libc-dev" && component.property(bomPart) == "image:base":
			headers = &bom.Components[i]
		}
	}
	if machine == nil || machine.property(bomOSVSystem) != "Ubuntu:26.04:LTS" || machine.property(bomOSVName) != "linux" ||
		machine.property(bomOSVVersion) != "7.0.0-42.42" || machine.property("hi:kernel:release") != "7.0.0-42-generic" {
		t.Fatalf("%+v", machine)
	}
	if headers == nil || headers.property(bomOSVName) != "" || headers.property(bomPart) != "image:base" {
		t.Fatalf("the image's kernel headers are matched: %+v", headers)
	}
	if appBomMoved("payments", path, tools) {
		t.Fatal("made again for the same kernel")
	}
	// Upgraded and restarted.
	kernel = appKernel{Release: "7.0.0-43-generic", OS: "ubuntu", OSRelease: "26.04", Binary: "linux-image-7.0.0-43-generic", Source: "linux", Version: "7.0.0-43.43"}
	if !appBomMoved("payments", path, tools) {
		t.Fatal("the bill names the old kernel")
	}
	// A machine whose kernel dpkg doesn't know keeps its bill.
	tools.kernel = func() appKernel { return appKernel{} }
	if appBomMoved("payments", path, tools) {
		t.Fatal("made again without a kernel")
	}
}

func TestKernelSourceNames(t *testing.T) {
	for in, want := range map[string]string{"linux-signed": "linux", "linux-signed-hwe-7.0": "linux-hwe-7.0", "linux": "linux", "linux-oem-7.0": "linux-oem-7.0"} {
		if got := appKernelSourceName(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
	for name, want := range map[string]bool{"linux": true, "linux-hwe-7.0": true, "linux-firmware": true, "linux-atm": false, "util-linux": false, "openssl": false} {
		if appKernelSource(name) != want {
			t.Errorf("%s: %v", name, !want)
		}
	}
	if id, version := appOSRelease("NAME=\"Ubuntu\"\nVERSION_ID=\"26.04\"\nID=ubuntu\n"); id != "ubuntu" || version != "26.04" {
		t.Fatal(id, version)
	}
}

// A kernel advisory tells the admin the machine itself needs the update
// and a restart, not hi app rebuild; one that named the image's kernel
// headers closes and says why.
func TestWatchTellsTheMachineNeedsAnUpdate(t *testing.T) {
	kernel := apiAdvisory{ID: "UBUNTU-CVE-2026-98725", Package: "linux", Version: "7.0.0-42.42", Part: "machine", Kernel: "7.0.0-42-generic",
		Fixed: []string{"7.0.0-43.43"}, Severity: "critical"}
	w := &appWatcher{state: watchState{Teams: map[string]*watchTeam{}, Incidents: map[string]*watchIncident{}}}
	team := &watchTeam{Team: "payments", Hostname: "gpu1.corp.example", Advisories: []apiAdvisory{kernel},
		Open: map[string]string{"advisory:UBUNTU-CVE-2026-98725:linux": "w-1"}}
	w.state.Teams["payments"] = team
	w.state.Incidents["w-1"] = &watchIncident{ID: "w-1", Team: "payments", Rule: "advisory:UBUNTU-CVE-2026-98725:linux"}
	w.state.Next = 1
	events := w.advisoryRulesLocked(team, time.Now())
	var opened, closed *watchIncident
	for _, event := range events {
		if event.closed {
			closed = &event.incident
		} else {
			opened = &event.incident
		}
	}
	if closed == nil || closed.ID != "w-1" || !strings.Contains(closed.Cleared, "machine's Linux kernel, not in hi's base image") {
		t.Fatalf("%+v", closed)
	}
	if opened == nil || opened.Rule != "advisory:UBUNTU-CVE-2026-98725:linux:machine" || opened.Tell {
		t.Fatalf("%+v", opened)
	}
	if opened.Title != "payments's machine needs an update: its Linux kernel, linux 7.0.0-42.42, has UBUNTU-CVE-2026-98725, critical" {
		t.Fatal(opened.Title)
	}
	for _, want := range []string{"gpu1: linux 7.0.0-42.42, running as 7.0.0-42-generic (fixed in 7.0.0-43.43)", "someone with sudo on gpu1 (ask your IT team)",
		"sudo apt update && sudo apt full-upgrade", "sudo reboot", "hi app rebuild doesn't change the kernel", "closes once gpu1 runs a fixed kernel"} {
		if !strings.Contains(opened.Detail, want) {
			t.Errorf("no %q in:\n%s", want, opened.Detail)
		}
	}
	if strings.Contains(opened.Detail, "runs hi app rebuild") {
		t.Fatal(opened.Detail)
	}
	// Upgraded and restarted: the machine's kernel no longer has it.
	team.Advisories = nil
	events = w.advisoryRulesLocked(team, time.Now())
	if len(events) != 1 || !events[0].closed || events[0].incident.Cleared != "The machine now runs a kernel without it." {
		t.Fatalf("%+v", events)
	}
	// A kernel advisory only an image's headers had closes without a
	// new one.
	team.Open = map[string]string{"advisory:UBUNTU-CVE-2026-1:linux": "w-9"}
	w.state.Incidents["w-9"] = &watchIncident{ID: "w-9"}
	events = w.advisoryRulesLocked(team, time.Now())
	if len(events) != 1 || !strings.Contains(events[0].incident.Cleared, "which doesn't have it") {
		t.Fatalf("%+v", events)
	}
	// Without a fix yet, it says to wait for Ubuntu's.
	kernel.Fixed = nil
	if action := advisoryAction("payments", "", kernel); !strings.Contains(action, "Ubuntu has no fixed kernel yet. Once it has, someone with sudo on the machine") {
		t.Fatal(action)
	}
}

// The kernel's advisories are many, as an image's: counted, and the
// weekly report says the machine needs updating.
func TestWatchCountsTheKernelsAdvisories(t *testing.T) {
	var advisories []apiAdvisory
	for i := range 500 {
		advisories = append(advisories, apiAdvisory{ID: "UBUNTU-CVE-2026-" + string(rune('a'+i%26)), Package: "linux", Part: "machine", Fixed: []string{"7.0.0-43.43"}})
	}
	listed, counts := appFacingAdvisories(advisories)
	if len(listed) != 0 || len(counts) != 1 || counts[0].Part != "machine" || counts[0].Total != 500 {
		t.Fatalf("%d listed, %+v", len(listed), counts)
	}
	w := &appWatcher{state: watchState{Teams: map[string]*watchTeam{}, Incidents: map[string]*watchIncident{}}}
	report := w.reportLocked(&watchTeam{Team: "payments", BomVersion: "v4", Advisories: advisories}, time.Now())
	if !strings.Contains(report, "the machine's Linux kernel: 500 advisories, 500 of them fixed in a newer kernel: the machine itself needs updating") {
		t.Fatal(report)
	}
}
