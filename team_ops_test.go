package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// With an ops channel, incidents, their threads, and the dev box's site
// asks go there; a work's own thread and the agents' posts stay in the
// main channel; an incident from before stays where it was posted.
func TestTeamOpsChannelRouting(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	config, _ := loadTeam("payments")
	config.OpsChannel = "C07PAYOPS"
	saveTeam(config)
	channel := &fakeTeamChannel{}
	b.channel = channel
	woken := teamTestWakes(b.teamBroker)

	// An incident from before the ops channel, posted in main.
	old := teamIncident{ID: "i-1", Kind: "job", Title: "old", Thread: "1690000000.000001", Status: "open"}
	saveTeamIncident("payments", old)

	incident, err := b.openIncident(teamIncident{Kind: "job", Slot: teamSlotProduction, Job: "backfill", Title: "backfill stopped"}, "backfill stopped.", "", teamIncidentPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if incident.Channel != "C07PAYOPS" || !strings.HasPrefix(channel.calls[0], "post C07PAYOPS :rotating_light: *Incident i-2") {
		t.Fatal(incident, channel.calls)
	}
	if len(*woken) != 1 || !strings.HasPrefix((*woken)[0], "hi-incident-i-2 slack:C07PAYOPS:"+incident.Thread+" ") {
		t.Fatal(*woken)
	}
	b.recovered(incident.ID, "It runs again.")
	b.recovered("i-1", "It ran again.")
	if !strings.HasPrefix(channel.calls[1], "postIn C07PAYOPS "+incident.Thread+" :white_check_mark:") ||
		!strings.HasPrefix(channel.calls[2], "postIn C07PAYMENTS 1690000000.000001 :white_check_mark:") {
		t.Fatal(channel.calls)
	}

	// A task's incident in its work's thread stays there, in main.
	task, _ := b.openIncident(teamIncident{Kind: "task", Task: "t-1", Title: "the task failed"}, "it failed.", "1700000009.000001", nil)
	if task.Channel != "" || !strings.HasPrefix(channel.calls[3], "postIn C07PAYMENTS 1700000009.000001 ") {
		t.Fatal(task, channel.calls)
	}

	// The box's site ask goes to ops; what an agent says, to main.
	channel.calls = nil
	b.devSiteTell("api.example.com", "the quotes", "4-quotes", "k1")
	b.devSay(teamDevSayRequest{Text: "shipped 4-quotes"})
	if len(channel.calls) != 2 || !strings.HasPrefix(channel.calls[0], "postButtons C07PAYOPS :computer: The dev box asks to reach *api.example.com*") ||
		!strings.HasSuffix(channel.calls[0], "[Allow hi_site_allow=api.example.com k1] [Say no hi_site_deny=api.example.com k1]") ||
		!strings.HasPrefix(channel.calls[1], "post C07PAYMENTS :computer: shipped") {
		t.Fatal(channel.calls)
	}

	// Without one, everything goes to main, as before.
	config.OpsChannel = ""
	saveTeam(config)
	channel.calls = nil
	b.openIncident(teamIncident{Kind: "job", Slot: teamSlotProduction, Job: "import", Title: "import stopped"}, "import stopped.", "", nil)
	b.devSiteTell("api.example.com", "the quotes", "", "k1")
	for _, call := range channel.calls {
		if !strings.HasPrefix(call, "post C07PAYMENTS ") && !strings.HasPrefix(call, "postButtons C07PAYMENTS ") {
			t.Fatal(channel.calls)
		}
	}
}

// The lead talks in both channels, and its skill says which is which.
func TestTeamOpsLead(t *testing.T) {
	setupTeamTest(t)
	config := testTeamConfig()
	if strings.Contains(teamSkill(config), "## Two channels") {
		t.Fatal("a team with one channel hears of two")
	}
	config.OpsChannel = "C07PAYOPS"
	os.MkdirAll(filepath.Join(teamDir("payments"), "lead"), 0o700)
	if err := writeTeamLead(config, teamSecrets{SlackBotToken: "xoxb-1", SlackAppToken: "xapp-1"}, "10.234.7.2", ""); err != nil {
		t.Fatal(err)
	}
	env, _ := os.ReadFile(filepath.Join(teamDir("payments"), "lead", ".env"))
	if !strings.Contains(string(env), "SLACK_ALLOWED_CHANNELS=C07PAYMENTS,C07PAYOPS\n") || !strings.Contains(string(env), "SLACK_HOME_CHANNEL=C07PAYMENTS\n") {
		t.Fatal(string(env))
	}
	if skill := teamSkill(config); !strings.Contains(skill, "## Two channels") || !strings.Contains(skill, "The ops channel** (C07PAYOPS)") {
		t.Fatal(skill)
	}
	if dir := teamChannelLogDir("payments", config, "C07PAYOPS"); dir != filepath.Join(teamChannelDir("payments"), "ops") {
		t.Fatal(dir)
	}
	if dir := teamChannelLogDir("payments", config, "C07PAYMENTS"); dir != teamChannelDir("payments") {
		t.Fatal(dir)
	}
}

// hi team ops sets the channel, checked in Slack, and takes it off.
func TestTeamOpsCommand(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	saveTeamSecrets("payments", teamSecrets{SlackBotToken: "xoxb-1", SlackAppToken: "xapp-1"})
	var out bytes.Buffer
	if err := teamOpsCommand([]string{"payments"}, &out); err != nil || !strings.Contains(out.String(), "everything in its channel") {
		t.Fatal(err, out.String())
	}
	for _, bad := range []string{"not-a-channel", "C07PAYMENTS"} {
		if err := teamOpsCommand([]string{"payments", bad}, &out); err == nil {
			t.Errorf("%s was taken", bad)
		}
	}
	out.Reset()
	if err := teamOpsCommand([]string{"payments", "C07PAYOPS"}, &out); err != nil || !strings.Contains(out.String(), "next start") {
		t.Fatal(err, out.String())
	}
	if config, _ := loadTeam("payments"); config.OpsChannel != "C07PAYOPS" {
		t.Fatal(config.OpsChannel)
	}
	out.Reset()
	if err := teamOpsCommand([]string{"payments", "off"}, &out); err != nil || !strings.Contains(out.String(), "everything in C07PAYMENTS again") {
		t.Fatal(err, out.String())
	}
	if config, _ := loadTeam("payments"); config.OpsChannel != "" {
		t.Fatal(config.OpsChannel)
	}
}

// Permissions and the snapshots' warnings are said only in ops.
func TestTeamOpsPermissions(t *testing.T) {
	b, _ := teamTestDevPage(t)
	config, _ := loadTeam("payments")
	config.OpsChannel = "C07PAYOPS"
	saveTeam(config)
	channel := &fakeTeamChannel{}
	b.channel = channel
	handler := b.devPageHandler()
	if code, body := teamTestDevPageCall(t, handler, teamTestOwnerKey, "POST", "api/people", map[string]string{"user": "U07ASKER1", "level": "contributor"}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	b.announceOps(config, ":warning: a snapshot's problem")
	if len(channel.calls) != 2 {
		t.Fatal(channel.calls)
	}
	for _, call := range channel.calls {
		if !strings.HasPrefix(call, "post C07PAYOPS ") {
			t.Fatal(channel.calls)
		}
	}
	if !strings.Contains(channel.calls[0], "made <@U07ASKER1>") {
		t.Fatal(channel.calls)
	}
	if !strings.Contains(teamSkill(config), "Changes to permissions are said only in the ops channel") {
		t.Fatal("the lead isn't told")
	}
}
