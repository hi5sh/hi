package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An app's Overview lists its boxes: what each is for, in the order
// people think of them, with its state, image, limits, and what its
// cgroup says; production's and Stage's with what they serve. Another
// app's boxes aren't its.
func TestAdminAppBoxes(t *testing.T) {
	a := setupAdminTest(t)
	writeTeamForAdmin(t, appConfig{Name: "payments-eu", Channel: "C07EU", Owners: []string{"U07OWNER1"}, Members: defaultTeamMembers(), Branch: "main"})
	os.MkdirAll(filepath.Join(appDir("payments"), "run"), 0o700)
	if err := saveTeamSlot("payments", appPreview{Slot: appSlotProduction, Label: "production v3", Commit: "0123456789abcdef", Command: "npm start", Port: 8080, Container: "hi-box-app-payments-app-production-7"}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	group := "user.slice/libpod-lead.scope"
	for file, text := range map[string]string{"memory.peak": "2000\n", "pids.current": "12\n", "memory.events": "low 0\noom_kill 2\n"} {
		path := filepath.Join(root, "sys", "fs", "cgroup", group, file)
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(text), 0o644)
	}
	previous := adminProcRoot
	adminProcRoot = root
	t.Cleanup(func() { adminProcRoot = previous })

	started := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	container := func(name, state string, memory int64) adminInspected {
		var c adminInspected
		c.Name, c.State.Status, c.Config.Image, c.HostConfig.Memory = name, state, "localhost/hi-box:abc", memory
		if state == "running" {
			c.State.StartedAt = started
		} else {
			c.State.FinishedAt, c.State.ExitCode = started, 137
		}
		return c
	}
	var asked []string
	adminInspectBoxes = func(keep func(string) bool) []adminInspected {
		var all []adminInspected
		for _, c := range []adminInspected{
			container("hi-box-app-payments-proxy", "running", 256<<20),
			container("hi-box-app-payments-dev", "running", 16<<30),
			container("hi-box-app-payments-app-production-5", "exited", 0),
			container("hi-box-app-payments-app-production-7", "running", 4<<30),
			container("hi-box-app-payments", "running", 4<<30),
			container("hi-box-payments-t-ab12", "running", 0),
			container("hi-box-app-payments-eu", "running", 4<<30),
			container("hi-admin-peer", "running", 0),
		} {
			if keep(c.Name) {
				asked = append(asked, c.Name)
				if c.Name == "hi-box-app-payments" {
					c.State.CgroupPath = "/" + group
				}
				all = append(all, c)
			}
		}
		return all
	}

	olga := a.signIn(t, "Olga")
	var detail adminAppDetail
	a.get(t, olga, "/api/app?name=payments", &detail)
	var order []string
	for _, box := range detail.Boxes {
		order = append(order, box.What)
	}
	want := []string{"lead box", "production box", "earlier production box", "dev box", "lead proxy", "task box"}
	if len(order) != len(want) {
		t.Fatalf("boxes: %q (asked of %q)", order, asked)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("boxes: %q, not %q", order, want)
		}
	}
	lead, production, earlier := detail.Boxes[0], detail.Boxes[1], detail.Boxes[2]
	if lead.Term != "lead" || lead.State != "running" || !lead.Started.Equal(started) || lead.MemoryMax != 4<<30 || lead.MemoryPeak != 2000 || lead.PIDs != 12 || lead.OOMKills != 2 {
		t.Fatalf("the lead's box: %+v", lead)
	}
	if production.Term != "production" || production.Version != "production v3" || production.Commit != "0123456" || production.Command != "npm start" || production.Port != 8080 {
		t.Fatalf("production's box: %+v", production)
	}
	if earlier.Term != "" || earlier.State != "exited" || earlier.ExitCode != 137 || !earlier.Finished.Equal(started) || !earlier.Started.IsZero() {
		t.Fatalf("the earlier box: %+v", earlier)
	}
}
