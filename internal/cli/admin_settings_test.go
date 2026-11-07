package cli

import (
	"net/http"
	"strings"
	"testing"
)

// Each change to an app's settings runs its hi app command, a secret on
// stdin and never in its arguments, and the app's ops channel hears it.
func TestAdminAppSettingsRunTheCommands(t *testing.T) {
	a := setupAdminTest(t)
	olga := a.signIn(t, "Olga")
	type run struct{ args, stdin string }
	var runs []run
	previous := adminRunHi
	adminRunHi = func(_, by, stdin string, args ...string) (string, error) {
		if by != "Olga" {
			t.Errorf("run as %q", by)
		}
		runs = append(runs, run{strings.Join(args, " "), stdin})
		return "done", nil
	}
	t.Cleanup(func() { adminRunHi = previous })

	var view adminAppSettings
	if code := a.get(t, olga, "/api/app/settings?name=payments", &view); code != http.StatusOK || len(view.Members) != 3 || len(view.People) != 1 || view.People[0].Level != "owner" || view.Handle == "" {
		t.Fatalf("settings: %d %+v", code, view)
	}
	for _, c := range []struct {
		change map[string]any
		args   string
		stdin  string
	}{
		{map[string]any{"what": "level", "person": "U07SAM", "level": "pro"}, "app access payments U07SAM pro", ""},
		{map[string]any{"what": "level", "person": "U07NEW", "level": "owner"}, "app owners payments add U07NEW", ""},
		{map[string]any{"what": "member", "role": "coder", "model": "sonnet", "effort": "high"}, "app member payments coder --model sonnet --effort high", ""},
		{map[string]any{"what": "openrouter", "key": "sk-or-SECRET"}, "app key payments openrouter", "sk-or-SECRET\n"},
		{map[string]any{"what": "claude", "machine": true}, "app signin payments claude --machine", ""},
		{map[string]any{"what": "limit", "usd": "$3"}, "app limit payments preview 3", ""},
		{map[string]any{"what": "memory", "box": "dev", "size": "24g"}, "app limit payments memory dev 24g", ""},
		{map[string]any{"what": "ops", "channel": ""}, "app ops payments off", ""},
		{map[string]any{"what": "slack", "bot": "xoxb-NEW", "app_token": ""}, "app key payments slack", "xoxb-NEW\n\n"},
		{map[string]any{"what": "env", "name": "STRIPE_KEY", "value": "sk_live_SECRET", "scope": "production"}, "app env payments STRIPE_KEY --production", "sk_live_SECRET\n"},
		{map[string]any{"what": "env", "name": "STRIPE_KEY", "remove": true, "scope": "production"}, "app env payments --unset STRIPE_KEY --production", ""},
		{map[string]any{"what": "helper", "slot": "preview", "on": false}, "app helper payments preview off", ""},
		{map[string]any{"what": "watch", "on": true}, "app watch payments --auto-freeze on", ""},
		{map[string]any{"what": "models", "on": false}, "app models payments off", ""},
	} {
		runs = nil
		c.change["app"] = "payments"
		var answer map[string]any
		if code := a.post(t, olga, "/api/app/settings", c.change, &answer); code != http.StatusOK {
			t.Fatalf("%v: %d %v", c.change, code, answer)
		}
		if len(runs) != 1 || runs[0].args != c.args || runs[0].stdin != c.stdin {
			t.Fatalf("%v ran %+v, not %q", c.change["what"], runs, c.args)
		}
		if strings.Contains(runs[0].args, "SECRET") {
			t.Fatalf("a secret in the arguments: %s", runs[0].args)
		}
	}
	a.mu.Lock()
	posts := strings.Join(*a.posts, "\n")
	a.mu.Unlock()
	for _, want := range []string{"Olga made <@U07SAM> a Pro Dev, from the admin page", "Olga gave the app an OpenRouter key of its own", "Olga set STRIPE_KEY, production's own"} {
		if !strings.Contains(posts, want) {
			t.Errorf("the channel didn't hear %q:\n%s", want, posts)
		}
	}
	if strings.Contains(posts, "SECRET") {
		t.Fatalf("a secret in Slack: %s", posts)
	}
	// An owner made something else stops being an owner first.
	runs = nil
	var answer map[string]any
	if code := a.post(t, olga, "/api/app/settings", map[string]any{"app": "payments", "what": "level", "person": "U07OWNER1", "level": "vibe"}, &answer); code != http.StatusOK ||
		len(runs) != 2 || runs[0].args != "app owners payments remove U07OWNER1" || runs[1].args != "app access payments U07OWNER1 vibe" {
		t.Fatalf("owner to vibe: %d %+v", code, runs)
	}
	// Signing in to ChatGPT shows a code, so it's a job.
	if code := a.post(t, olga, "/api/app/settings", map[string]any{"app": "payments", "what": "chatgpt"}, &answer); code != http.StatusOK || answer["job"] == nil {
		t.Fatalf("chatgpt: %d %v", code, answer)
	}
	// People across apps, and the machine's keys.
	var people struct {
		Apps   []string         `json:"apps"`
		People []adminPeopleRow `json:"people"`
	}
	a.get(t, olga, "/api/people", &people)
	if len(people.Apps) != 1 || len(people.People) != 1 || people.People[0].Levels["payments"] != "owner" {
		t.Fatalf("people: %+v", people)
	}
	var keys adminMachineKeys
	a.get(t, olga, "/api/keys", &keys)
	if !keys.OpenRouter || len(keys.Apps) != 1 || keys.Apps[0].OpenRouter != "machine" {
		t.Fatalf("keys: %+v", keys)
	}
}

// The dev page links its notes to the admin page for its admins only.
func TestAdminPageForTheDevPage(t *testing.T) {
	setupAdminTest(t)
	if got := adminPageFor("U07OWNER1"); got != "http://127.0.0.1:1984/" {
		t.Fatalf("an admin: %q", got)
	}
	if got := adminPageFor("U07SOMEONE"); got != "" {
		t.Fatalf("someone else: %q", got)
	}
}
