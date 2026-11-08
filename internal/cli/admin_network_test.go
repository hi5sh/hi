package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Network and Data run their hi app commands; restore, rollback, and a
// copy to production need the app's name typed, and run as jobs.
func TestAdminNetworkAndDataChanges(t *testing.T) {
	a := setupAdminTest(t)
	olga := a.signIn(t, "Olga")
	type run struct{ args, stdin string }
	var runs []run
	previous := adminRunHi
	adminRunHi = func(_, _ string, stdin string, args ...string) (string, error) {
		runs = append(runs, run{strings.Join(args, " "), stdin})
		return "ok", nil
	}
	t.Cleanup(func() { adminRunHi = previous })

	var net adminAppNetwork
	if code := a.get(t, olga, "/api/app/network?name=payments", &net); code != http.StatusOK || net.Peer == "" || !strings.Contains(net.Ask, "TCP 80 and 8080") || !strings.Contains(net.Ask, "TCP 8888 and 22") {
		t.Fatalf("network: %d %+v", code, net)
	}
	for _, c := range []struct {
		change map[string]any
		args   string
		stdin  string
	}{
		{map[string]any{"what": "addresses", "mode": "names", "production": "pay.example.com", "https": true}, "", ""}, // a job
		{map[string]any{"what": "allowip", "ips": "10.0.0.0/8"}, "app allow-ip payments 10.0.0.0/8", ""},
		{map[string]any{"what": "sso", "sso": "google", "domains": "example.com"}, "app sso payments on google --domain example.com", ""},
		{map[string]any{"what": "fork"}, "app data payments fork", ""},
		{map[string]any{"what": "stagedata", "on": true}, "app data payments preview empty", ""},
		{map[string]any{"what": "keep", "keep": "7,4,6"}, "app backups payments --keep 7,4,6", ""},
		{map[string]any{"what": "offsite", "offsite": "s3:https://s3.example.com/b", "key_id": "AKID", "secret": "SECRET"}, "app backups payments --offsite s3:https://s3.example.com/b", "AKID\nSECRET\n"},
		{map[string]any{"what": "advisory", "id": "GHSA-1", "because": "not reachable", "package": "lodash"}, "app advisory payments ignore GHSA-1 --because not reachable --package lodash", ""},
	} {
		runs = nil
		c.change["app"] = "payments"
		var answer map[string]any
		if code := a.post(t, olga, "/api/app/settings", c.change, &answer); code != http.StatusOK {
			t.Fatalf("%v: %d %v", c.change["what"], code, answer)
		}
		if c.args == "" {
			if answer["job"] == nil {
				t.Fatalf("%v isn't a job: %v", c.change["what"], answer)
			}
			continue
		}
		if len(runs) != 1 || runs[0].args != c.args || runs[0].stdin != c.stdin {
			t.Fatalf("%v ran %+v", c.change["what"], runs)
		}
	}
	// The NetBird key goes in a file the command reads and deletes, never
	// in its arguments.
	runs = nil
	var answer map[string]any
	if code := a.post(t, olga, "/api/app/settings", map[string]any{"app": "payments", "what": "netbird", "key": "NB-SECRET"}, &answer); code != http.StatusOK || len(runs) != 1 || strings.Contains(runs[0].args, "NB-SECRET") || !strings.HasPrefix(runs[0].args, "app dev payments netbird ") {
		t.Fatalf("netbird: %d %+v", code, runs)
	}
	keyFile := strings.Fields(runs[0].args)[4]
	if data, _ := os.ReadFile(keyFile); strings.TrimSpace(string(data)) != "NB-SECRET" {
		t.Fatalf("the key file: %q", data)
	}
	os.Remove(keyFile)
	// What can't be undone needs the app's name typed.
	for _, what := range []map[string]any{{"what": "restore", "snapshot": "abc123"}, {"what": "rollback", "version": 2}, {"what": "copy"}} {
		what["app"] = "payments"
		if code := a.post(t, olga, "/api/app/settings", what, &answer); code != http.StatusBadRequest || !strings.Contains(answer["error"].(string), "type the app's name") {
			t.Fatalf("%v without the name: %d %v", what["what"], code, answer)
		}
		what["confirm"] = "payments"
		if code := a.post(t, olga, "/api/app/settings", what, &answer); code != http.StatusOK || answer["job"] == nil {
			t.Fatalf("%v: %d %v", what["what"], code, answer)
		}
	}
	a.mu.Lock()
	jobs := *a.jobs
	a.mu.Unlock()
	var args []string
	for _, job := range jobs {
		args = append(args, strings.Join(job.Args, " "))
	}
	for _, want := range []string{"app addresses payments names pay.example.com --https", "app restore payments abc123 --yes", "app rollback payments v2 --yes", "app data payments copy preview production --yes"} {
		if !strings.Contains(strings.Join(args, "|"), want) {
			t.Errorf("no job %q in %v", want, args)
		}
	}
	// Advisories, and the machine's Network.
	var advisories map[string]any
	if code := a.get(t, olga, "/api/advisories", &advisories); code != http.StatusOK || advisories["advisories"] == nil {
		t.Fatalf("advisories: %d %v", code, advisories)
	}
	var machine adminMachineNetwork
	if code := a.get(t, olga, "/api/network", &machine); code != http.StatusOK || len(machine.Apps) != 1 || !strings.Contains(machine.Ask, "App payments") {
		t.Fatalf("machine network: %d %+v", code, machine)
	}
}

// The Overview knows whether the app's peer is on NetBird, which every
// address goes through, so it doesn't offer addresses nobody reaches.
func TestAdminOverviewSaysWhenTheAppIsntOnNetBird(t *testing.T) {
	newAppTest(t)
	if adminAppOf("payments").Joined {
		t.Fatal("joined without a peer")
	}
	writeSkillTestFile(t, filepath.Join(appDevDir("payments"), "netbird", "default.json"), "{}", 0o600)
	if !adminAppOf("payments").Joined {
		t.Fatal("not joined with a peer")
	}
}
