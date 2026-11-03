package cli

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// appTestOld makes a file look as if it last changed two days ago.
func appTestOld(t *testing.T, slot, name string) {
	t.Helper()
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(appSlotDataDir("payments", slot), name), old, old); err != nil {
		t.Fatal(err)
	}
}

// readTeamTestPreview is a file as the preview's box sees it: its own,
// or else its fork's.
func readTeamTestPreview(name string) string {
	if data, err := os.ReadFile(filepath.Join(appSlotDataDir("payments", appSlotPreview), name)); err == nil {
		return string(data)
	}
	data, _ := os.ReadFile(filepath.Join(appPreviewBaseDir("payments"), name))
	return string(data)
}

func appTestSameFile(a, b string) bool {
	first, err1 := os.Stat(a)
	second, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(first, second)
}

func TestTeamForkPreview(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.takeSnapshot = func(kind, label string) (appSnapshot, error) {
		return appSnapshot{ID: "snap0001", Kind: kind, Label: label}, nil
	}
	appTestRunnable(b)
	appTestMerge(t, b, "Add a news feed")
	if _, err := b.release(appReleaseRequest{host: "Olga", Notes: "News."}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.preview(appPreviewRequest{}); err != nil {
		t.Fatal(err)
	}
	writeTeamTestData(t, appSlotProduction, map[string]string{
		"app.db":              "SQLite format 3\x00 the releases",
		"attachments/old.pdf": "a release from 2019",
		"attachments/new.pdf": "a release from today",
		"notes/kept.txt":      "written once",
	})
	writeTeamTestData(t, appSlotPreview, map[string]string{"tried.txt": "tried in the preview"})
	for _, name := range []string{"app.db", "attachments/old.pdf", "notes/kept.txt"} {
		appTestOld(t, appSlotProduction, name)
	}

	result, err := b.copyData(appDataCopyRequest{From: "production", To: "preview"})
	if err != nil {
		t.Fatal(err)
	}
	fork := result.Fork
	if fork == nil || fork.Full || fork.Files != 4 || fork.Copied != 2 || fork.Version != 1 {
		t.Fatalf("%+v", fork)
	}
	production := appSlotDataDir("payments", appSlotProduction)
	own := appSlotDataDir("payments", appSlotPreview)
	base := appPreviewBaseDir("payments")
	// What production won't change in place is linked, and takes no room.
	if !appTestSameFile(filepath.Join(production, "attachments/old.pdf"), filepath.Join(base, "attachments/old.pdf")) ||
		!appTestSameFile(filepath.Join(production, "notes/kept.txt"), filepath.Join(base, "notes/kept.txt")) {
		t.Fatal("the fork copied what it could link")
	}
	// The database and the file just written are copied, into the preview's own.
	for _, name := range []string{"app.db", "attachments/new.pdf"} {
		if fileExists(filepath.Join(base, name)) || !fileExists(filepath.Join(own, name)) || appTestSameFile(filepath.Join(production, name), filepath.Join(own, name)) {
			t.Fatal(name, "isn't a copy of its own")
		}
	}
	// Production writing to its database changes nothing in the preview.
	writeTeamTestData(t, appSlotProduction, map[string]string{"app.db": "SQLite format 3\x00 entered since"})
	if readTeamTestPreview("app.db") != "SQLite format 3\x00 the releases" || readTeamTestPreview("tried.txt") != "" {
		t.Fatal(readTeamTestPreview("app.db"))
	}
	if kept, _ := os.ReadFile(filepath.Join(result.Kept, "tried.txt")); string(kept) != "tried in the preview" {
		t.Fatal(result.Kept)
	}
	if !appPreviewOverlay("payments") || !fileExists(appPreviewWorkDir("payments")) {
		t.Fatal("no overlay")
	}
	// The preview started again on the same address.
	if state, ok := loadTeamSlot("payments", appSlotPreview); !ok || !b.running(state.Container) {
		t.Fatal("the preview didn't start again")
	}

	// Its box mounts the fork below its own folder.
	mount := strings.Join(appBoxDataMount("payments", appSlotPreview, own), " ")
	if mount != "-v "+base+":/data:O,upperdir="+own+",workdir="+appPreviewWorkDir("payments") {
		t.Fatal(mount)
	}
	if mount := strings.Join(appBoxDataMount("payments", appSlotProduction, production), " "); mount != "-v "+production+":/data" {
		t.Fatal(mount)
	}

	// hi app data says what the preview runs on.
	var out bytes.Buffer
	printTeamDataFolders(&out, appDataFolders("payments", b.running))
	if !strings.Contains(out.String(), "a fork of production v1 from") || !strings.Contains(out.String(), "of its own") {
		t.Fatal(out.String())
	}
	out.Reset()
	printTeamDataCopy(&out, result)
	if !strings.Contains(out.String(), "2 file(s)") || !strings.Contains(out.String(), "the rest linked") {
		t.Fatal(out.String())
	}

	// The next preview deploy stops the box on the overlay before the new
	// one starts: two boxes never write to it.
	before, _ := loadTeamSlot("payments", appSlotPreview)
	appTestMerge(t, b, "Add a search")
	after, err := b.preview(appPreviewRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if after.Container == before.Container || b.running(before.Container) || !b.running(after.Container) {
		t.Fatal(before.Container, after.Container)
	}
}

func TestTeamForkWhereLinksDontWork(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	appOverlayEngine = func() bool { return false }
	appTestMerge(t, b, "Add a news feed")
	b.release(appReleaseRequest{host: "Olga", Notes: "News."})
	writeTeamTestData(t, appSlotProduction, map[string]string{"app.db": "production"})
	result, err := b.copyData(appDataCopyRequest{From: "production", To: "preview"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Fork == nil || !result.Fork.Full || !strings.Contains(result.Fork.Why, "Podman") || appPreviewOverlay("payments") {
		t.Fatalf("%+v", result.Fork)
	}
	if readTeamTestData(appSlotPreview, "app.db") != "production" {
		t.Fatal("the preview has no whole copy")
	}
	// Nor does a release fork, which would copy it all each time.
	if _, done, err := b.previewAfterRelease(); done || err != nil {
		t.Fatal(done, err)
	}
}

func TestTeamPreviewAfterRelease(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.takeSnapshot = func(kind, label string) (appSnapshot, error) {
		return appSnapshot{ID: "snap0001", Kind: kind, Label: label}, nil
	}
	appTestRunnable(b)

	// Production without data leaves what the preview gathered alone.
	writeTeamTestData(t, appSlotPreview, map[string]string{"app.db": "gathered in the preview"})
	appTestMerge(t, b, "Add a news feed")
	result, err := b.release(appReleaseRequest{host: "Olga", Notes: "News."})
	if err != nil {
		t.Fatal(err)
	}
	if result.Preview != nil || readTeamTestPreview("app.db") != "gathered in the preview" {
		t.Fatal(result.Preview)
	}

	// With data in production, the preview gets a new fork at a release.
	writeTeamTestData(t, appSlotProduction, map[string]string{"app.db": "production's"})
	appTestMerge(t, b, "Add a search")
	result, err = b.release(appReleaseRequest{host: "Olga", Notes: "Search."})
	if err != nil {
		t.Fatal(err)
	}
	if result.Preview == nil || result.Preview.Fork == nil || readTeamTestPreview("app.db") != "production's" {
		t.Fatalf("%+v", result.Preview)
	}

	// A team whose previews start empty gets an empty one.
	var mode bytes.Buffer
	if err := appDataCommand([]string{"payments", "preview", "empty"}, strings.NewReader(""), &mode); err != nil || !strings.Contains(mode.String(), "empty") {
		t.Fatal(err, mode.String())
	}
	writeTeamTestData(t, appSlotPreview, map[string]string{"tried.txt": "tried"})
	appTestMerge(t, b, "Add a chart")
	result, err = b.release(appReleaseRequest{host: "Olga", Notes: "Chart."})
	if err != nil {
		t.Fatal(err)
	}
	if result.Preview == nil || result.Preview.Fork != nil || readTeamTestPreview("app.db") != "" || readTeamTestPreview("tried.txt") != "" || appPreviewOverlay("payments") {
		t.Fatalf("%+v", result.Preview)
	}
	folders := appDataFolders("payments", b.running)
	if !folders[1].Empty {
		t.Fatal(folders)
	}
	if err := appDataCommand([]string{"payments", "preview", "fork"}, strings.NewReader(""), &mode); err != nil {
		t.Fatal(err)
	}
	if config, _ := loadTeam("payments"); config.PreviewData != "" {
		t.Fatal(config.PreviewData)
	}
}

func TestTeamCopyForkedPreviewToProduction(t *testing.T) {
	setupTeamTest(t)
	b := newTeamTestBroker(t)
	b.takeSnapshot = func(kind, label string) (appSnapshot, error) {
		return appSnapshot{ID: "snap0001", Kind: kind, Label: label}, nil
	}
	appTestRunnable(b)
	appTestMerge(t, b, "Add a news feed")
	b.release(appReleaseRequest{host: "Olga", Notes: "News."})
	writeTeamTestData(t, appSlotProduction, map[string]string{
		"app.db": "production", "pdfs/a.pdf": "a", "pdfs/b.pdf": "b", "old/x.txt": "x",
	})
	for _, name := range []string{"pdfs/a.pdf", "pdfs/b.pdf", "old/x.txt"} {
		appTestOld(t, appSlotProduction, name)
	}
	if _, err := b.copyData(appDataCopyRequest{From: "production", To: "preview"}); err != nil {
		t.Fatal(err)
	}
	// What the preview's box did, as the overlay writes it: a.pdf changed,
	// b.pdf deleted, old/ deleted and made again, a new file.
	writeTeamTestData(t, appSlotPreview, map[string]string{
		"pdfs/a.pdf": "a, changed in the preview", "pdfs/.wh.b.pdf": "", "old/.wh..wh..opq": "", "old/y.txt": "y", "new.txt": "new",
	})
	result, err := b.copyData(appDataCopyRequest{From: "preview", To: "production", owner: "Olga"})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"app.db": "production", "pdfs/a.pdf": "a, changed in the preview", "pdfs/b.pdf": "", "old/x.txt": "", "old/y.txt": "y", "new.txt": "new"} {
		if got := readTeamTestData(appSlotProduction, name); got != want {
			t.Fatalf("%s: %q, not %q", name, got, want)
		}
	}
	if fileExists(filepath.Join(appSlotDataDir("payments", appSlotProduction), "old/.wh..wh..opq")) {
		t.Fatal("the overlay's marks went to production")
	}
	// The fork's own a.pdf, a link to production's before, wasn't written through.
	if data, _ := os.ReadFile(filepath.Join(appPreviewBaseDir("payments"), "pdfs/a.pdf")); string(data) != "a" {
		t.Fatal(string(data))
	}
	if result.Snapshot == "" {
		t.Fatal(result)
	}
}

func TestTeamForkDatabases(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700)
		os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600)
	}
	write("app.sqlite", "SQLite format 3\x00...")
	write("app.sqlite-wal", "")
	write("data.duckdb", "\x00\x00\x00\x00\x00\x00\x00\x00DUCK....")
	write("pg/PG_VERSION", "17")
	write("pg/base/1/1259", "pages")
	write("doc.pdf", "%PDF-1.7")
	databases := appForkDatabases(dir)
	for _, name := range []string{"app.sqlite", "app.sqlite-wal", "data.duckdb", "pg/PG_VERSION", "pg/base/1/1259"} {
		if !databases[name] {
			t.Error(name, "isn't counted a database")
		}
	}
	if databases["doc.pdf"] {
		t.Error("a PDF is counted a database")
	}
}

func TestTeamEnvPerSlot(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	saveTeamSecrets("payments", appSecrets{})
	var out bytes.Buffer
	run := func(args ...string) {
		t.Helper()
		out.Reset()
		if err := appEnv(append([]string{"payments"}, args...), strings.NewReader(""), &out); err != nil {
			t.Fatal(err)
		}
	}
	run("MAIL_URL=smtp://live", "REGION=eu")
	run("--production", "MARKET_KEY=live-key")
	if !strings.Contains(out.String(), "the preview doesn't") {
		t.Fatal(out.String())
	}
	run("--preview", "MAIL_URL=smtp://sandbox")
	production, preview := appBoxEnv("payments", appSlotProduction, 8080), appBoxEnv("payments", appSlotPreview, 8080)
	if production["MARKET_KEY"] != "live-key" || production["MAIL_URL"] != "smtp://live" || production["REGION"] != "eu" {
		t.Fatal(production)
	}
	if _, ok := preview["MARKET_KEY"]; ok || preview["MAIL_URL"] != "smtp://sandbox" || preview["REGION"] != "eu" {
		t.Fatal(preview)
	}
	run()
	if !strings.Contains(out.String(), "MAIL_URL (a value of its own in each), MARKET_KEY (production only), REGION") {
		t.Fatal(out.String())
	}
	// --production on a setting both had takes it from the preview.
	run("--production", "REGION=us")
	if _, ok := appBoxEnv("payments", appSlotPreview, 8080)["REGION"]; ok {
		t.Fatal("the preview kept REGION")
	}
	// Unsetting the preview's own leaves production's; plain unset, both.
	run("--preview", "--unset", "MAIL_URL")
	if appBoxEnv("payments", appSlotProduction, 8080)["MAIL_URL"] != "smtp://live" {
		t.Fatal("production lost MAIL_URL")
	}
	if _, ok := appBoxEnv("payments", appSlotPreview, 8080)["MAIL_URL"]; ok {
		t.Fatal("the preview kept MAIL_URL")
	}
	run("--unset", "MARKET_KEY")
	if _, ok := appBoxEnv("payments", appSlotProduction, 8080)["MARKET_KEY"]; ok {
		t.Fatal("production kept MARKET_KEY")
	}
	if err := appEnv([]string{"payments", "--preview", "--unset", "NOPE"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("unset a setting there isn't")
	}
	// A setting for one slot restarts the boxes, as any other does.
	stamp := appSettingsStamp("payments")
	run("--preview", "MAIL_URL=smtp://other")
	if appSettingsStamp("payments") == stamp {
		t.Fatal("the stamp didn't change")
	}
}

func TestTeamPreviewModelLimit(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	os.MkdirAll(filepath.Join(appDir("payments"), "run"), 0o700)
	calls := 0
	openrouter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, `{"choices":[{"message":{"content":"the \"cost\": 99 of it"}}],"usage":{"prompt_tokens":10,"cost":2.5,"cost_details":{"upstream_inference_cost":2}}}`)
	}))
	defer openrouter.Close()
	ask := func(slot string) (int, string) {
		proxy := httptest.NewServer(newTeamModelsProxy("payments", openrouter.URL, slot))
		defer proxy.Close()
		response, err := http.Post(proxy.URL+"/api/v1/chat/completions", "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body)
	}
	// $5 a day: two calls at $2.50, then no more.
	for range 2 {
		if code, _ := ask(appSlotPreview); code != 200 {
			t.Fatal(code)
		}
	}
	if spend := loadTeamPreviewSpend("payments"); spend.USD != 5 || spend.Calls != 2 {
		t.Fatalf("%+v", spend)
	}
	code, body := ask(appSlotPreview)
	if code != http.StatusTooManyRequests || !strings.Contains(body, "stopped for today") || calls != 2 {
		t.Fatal(code, body, calls)
	}
	// Production's calls aren't limited or counted against the preview.
	if code, _ := ask(appSlotProduction); code != 200 || loadTeamPreviewSpend("payments").Calls != 2 {
		t.Fatal(code)
	}
	// Every call's cost is in the team's spend log, for hi watch.
	if spent := appSpendSince("payments", time.Now().Add(-time.Minute)); spent != 7.5 {
		t.Fatal(spent)
	}
	// The team sets another limit, or none.
	var out bytes.Buffer
	if err := appLimitCommand([]string{"payments", "preview", "8"}, &out); err != nil || !strings.Contains(out.String(), "$8.00 a day") || !strings.Contains(out.String(), "spent $5.00 in 2 call(s)") {
		t.Fatal(err, out.String())
	}
	if code, _ := ask(appSlotPreview); code != 200 {
		t.Fatal(code)
	}
	if err := appLimitCommand([]string{"payments", "preview", "off"}, &out); err != nil || appPreviewModelLimit("payments") != 0 {
		t.Fatal(err)
	}
	if err := appLimitCommand([]string{"payments", "preview", "-3"}, &out); err == nil {
		t.Fatal("took a limit below 0")
	}
	// Yesterday's spending doesn't count today.
	os.WriteFile(appPreviewSpendPath("payments"), []byte(`{"day":"2020-01-01","usd":100,"calls":9}`), 0o600)
	if spend := loadTeamPreviewSpend("payments"); spend.USD != 0 {
		t.Fatalf("%+v", spend)
	}
}

func TestTeamAnswerCost(t *testing.T) {
	streamed := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"cost\":0.0042}}\n\ndata: [DONE]\n\n"
	for answer, want := range map[string]float64{
		streamed:                    0.0042,
		`{"usage":{"cost":1.5e-3}}`: 0.0015,
		`{"choices":[{"message":{"content":"\"cost\": 9"}}]}`: 0,
		`not json`: 0,
	} {
		if got := appAnswerCost([]byte(answer)); got != want {
			t.Errorf("%q: %v, not %v", answer, got, want)
		}
	}
}

// TestTeamForkInPodman runs a real box on a fork: it reads the fork,
// writes and deletes, and the merge back sees what the box saw.
func TestTeamForkInPodman(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a box")
	}
	engine, _, err := detectBoxEngine()
	if err != nil || engine.name != "podman" {
		t.Skip("needs Podman")
	}
	image := "docker.io/library/alpine:3.20"
	if _, err := engine.output("image", "exists", image); err != nil {
		t.Skip("needs " + image + " pulled")
	}
	dir := t.TempDir()
	production, base, own, work := filepath.Join(dir, "app"), filepath.Join(dir, "base"), filepath.Join(dir, "own"), filepath.Join(dir, "work")
	t.Cleanup(func() { appRemoveAll(own); appRemoveAll(work) })
	for name, content := range map[string]string{"keep.txt": "kept", "gone.txt": "deleted", "remade/old.txt": "old", "app.db": "SQLite format 3\x00 db"} {
		os.MkdirAll(filepath.Dir(filepath.Join(production, name)), 0o700)
		os.WriteFile(filepath.Join(production, name), []byte(content), 0o600)
		old := time.Now().Add(-48 * time.Hour)
		os.Chtimes(filepath.Join(production, name), old, old)
	}
	if _, err := appForkInto(production, base, own, appForkDatabases(production), time.Now().Add(-appForkRecent)); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(work, 0o700)
	mount := fmt.Sprintf("%s:/data:O,upperdir=%s,workdir=%s", base, own, work)
	out, err := engine.output("run", "--rm", "--userns=keep-id", "-v", mount, image, "sh", "-c",
		"cat /data/keep.txt /data/app.db && rm /data/gone.txt && rm -r /data/remade && mkdir /data/remade && echo new > /data/remade/new.txt && echo changed > /data/keep.txt")
	if err != nil || !strings.Contains(out, "kept") {
		t.Fatal(err, out)
	}
	if data, _ := os.ReadFile(filepath.Join(production, "keep.txt")); string(data) != "kept" {
		t.Fatal("the box wrote through to production")
	}
	merged := filepath.Join(dir, "merged")
	if err := appMergeOverlay(base, own, merged); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"keep.txt": "changed\n", "gone.txt": "", "remade/old.txt": "", "remade/new.txt": "new\n", "app.db": "SQLite format 3\x00 db"} {
		if data, _ := os.ReadFile(filepath.Join(merged, name)); string(data) != want {
			t.Errorf("%s: %q, not %q", name, data, want)
		}
	}
}
