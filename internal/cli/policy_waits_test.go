package cli

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// A request the policy leaves for a person says why, in the request, in
// Slack, and the admin page's Budgets and approval gets each user's and
// group's spend and the requests that show what a rule would approve.
func TestPolicySaysWhyARequestWaits(t *testing.T) {
	ts := newTestServer(t)
	fake := withSlack(t, ts)
	writePolicy(t, ts, testPolicy)
	ts.connectAs(t, "alice", "staff")
	code, _, stderr := runHi("compute", "up", "--on", "runpod", "--gpu", "a100", "--name", "pricey", "--max", "1h",
		"--reason", "x", "--yes", "--no-wait")
	if code != exitPending {
		t.Fatalf("%d %s", code, stderr)
	}
	var waits, group string
	ts.server.mu.Lock()
	for _, request := range ts.server.state.Requests {
		if request.Name == "pricey" {
			waits, group = request.Waits, request.PolicyGroup
		}
	}
	ts.server.mu.Unlock()
	if group != "staff" || waits != "$1.99/h is over the staff group's auto-approval, up to $1.00/h" {
		t.Fatalf("%q %q", group, waits)
	}
	ts.server.slack.flush()
	posts, _, _, _ := fake.snapshot()
	if !strings.Contains(posts[len(posts)-1], "Waits for a person: $1.99/h is over the staff group's auto-approval") || !strings.Contains(posts[len(posts)-1], "Budgets and approval") {
		t.Fatal(posts[len(posts)-1])
	}

	recorder := httptest.NewRecorder()
	ts.server.adminHandler().ServeHTTP(recorder, httptest.NewRequest("GET", "/admin/policy", nil))
	var answer struct {
		Spend policySpendView `json:"spend"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &answer); err != nil {
		t.Fatal(err, recorder.Body.String())
	}
	if answer.Spend.Users["alice"].Group != "staff" || len(answer.Spend.Asked) != 1 || answer.Spend.Asked[0].Rate != 1.99 || answer.Spend.Asked[0].Hours != 1 || answer.Spend.Asked[0].Group != "staff" {
		t.Fatalf("%+v", answer.Spend)
	}
}

func TestPolicyWaitsWords(t *testing.T) {
	policy, _ := parsePolicy([]byte(testPolicy))
	for _, c := range []struct {
		group, rate string
		hours       float64
		over        bool
		want        string
	}{
		{"students", "$0.59/h", 4, false, "the students group has no auto-approval"},
		{"agents", "$0.59/h", 4, false, "the agents group isn't in the policy, so a person approves all it asks"},
		{"staff", "$0.59/h", 3, false, "3h is over the staff group's auto-approval, up to 2h"},
		{"staff", "$1.50/h", 3, false, "$1.50/h for 3h is over the staff group's auto-approval, up to $1.00/h and 2h"},
		{"staff", "$0.59/h", 1, true, "it's over a monthly budget"},
		{"staff", "$0.59/h", 1, false, ""},
		{"", "$0.59/h", 1, false, ""},
	} {
		if got := policyWaits(policy, c.group, budgetStatus{UserOver: c.over}, c.rate, c.hours); got != c.want {
			t.Errorf("%+v: %q", c, got)
		}
	}
}
