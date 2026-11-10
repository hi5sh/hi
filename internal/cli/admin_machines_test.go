package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminMoveIsTheCommand(t *testing.T) {
	a := setupAdminTest(t)
	cookie := a.signIn(t, "Olga")
	var job adminJob
	if code := a.post(t, cookie, "/api/action", map[string]string{"app": "payments", "action": "move", "machine": "ws2.corp.example"}, &job); code != http.StatusOK {
		t.Fatal(code)
	}
	if strings.Join(job.Args, " ") != "app move payments ws2.corp.example --yes" || job.Title != "Move payments to ws2.corp.example" {
		t.Fatalf("%+v", job)
	}
	var failed map[string]string
	if code := a.post(t, cookie, "/api/action", map[string]string{"app": "payments", "action": "move", "machine": "ws2;id"}, &failed); code != http.StatusBadRequest {
		t.Fatal("moved to a machine that isn't one:", code)
	}
}

func TestAdminOtherMachines(t *testing.T) {
	a := setupAdminTest(t)
	t.Setenv("HOME", t.TempDir())
	probe := appMoveProbe
	appMoveProbe = func(machine string) (bool, string, string) {
		if machine == "ws2" {
			return true, "v2.6.112", ""
		}
		return false, "", "doesn't take apps from this machine yet"
	}
	t.Cleanup(func() { appMoveProbe = probe })
	cookie := a.signIn(t, "Olga")

	// Machines to move apps to: each with whether it takes them.
	for _, name := range []string{"ws2", "ws3"} {
		if code := a.post(t, cookie, "/api/machines", map[string]string{"action": "to", "name": name}, nil); code != http.StatusOK {
			t.Fatal(name, code)
		}
	}
	var view adminMachinesView
	a.get(t, cookie, "/api/machines", &view)
	if len(view.To) != 2 || !view.To[0].Takes || view.To[0].Version != "v2.6.112" || view.To[1].Takes || !strings.Contains(view.To[1].Problem, "doesn't take apps") {
		t.Fatalf("%+v", view.To)
	}
	if !strings.HasPrefix(view.Line, "hi app machines add "+view.Machine+" ssh-ed25519 ") {
		t.Fatal(view.Line)
	}
	a.post(t, cookie, "/api/machines", map[string]string{"action": "forget", "name": "ws3"}, nil)
	a.get(t, cookie, "/api/machines", &view)
	if len(view.To) != 1 {
		t.Fatalf("%+v", view.To)
	}

	// Taking apps from another machine: its line pasted whole.
	line := "hi app machines add ws1 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGk= hi-move@ws1"
	if code := a.post(t, cookie, "/api/machines", map[string]string{"action": "take", "key": line}, nil); code != http.StatusOK {
		t.Fatal(code)
	}
	if code := a.post(t, cookie, "/api/machines", map[string]string{"action": "take", "key": "my password"}, nil); code != http.StatusBadRequest {
		t.Fatal("took something that isn't a key")
	}
	a.get(t, cookie, "/api/machines", &view)
	if len(view.From) != 1 || view.From[0] != "ws1" {
		t.Fatalf("%+v", view.From)
	}
	data, _ := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".ssh", "authorized_keys"))
	if !strings.Contains(string(data), "restrict,command=") || !strings.Contains(string(data), "app __receive ws1") {
		t.Fatal(string(data))
	}
	a.post(t, cookie, "/api/machines", map[string]string{"action": "untake", "name": "ws1"}, nil)
	a.get(t, cookie, "/api/machines", &view)
	if len(view.From) != 0 {
		t.Fatalf("%+v", view.From)
	}

	// A check against a machine that doesn't take apps says what to paste there.
	var check adminMoveCheckView
	if code := a.post(t, cookie, "/api/move/check", map[string]string{"app": "payments", "machine": "ws3"}, &check); code != http.StatusOK {
		t.Fatal(code)
	}
	if !strings.HasPrefix(check.Access, "hi app machines add ") || !strings.Contains(check.Error, "doesn't take apps") {
		t.Fatalf("%+v", check)
	}
}
