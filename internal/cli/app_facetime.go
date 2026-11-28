package cli

// Face time: how long each person has spent on an app's dev page with a
// window in use (open, shown, and with a key, a click, or a scroll in the
// last few minutes), from the page's presence (presence.go). hi keeps
// each person's total in run/face-time.json of the app; the admin page's
// People view adds them up across the apps.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// appFaceTime is one person's total on an app's dev page.
type appFaceTime struct {
	Seconds float64   `json:"seconds"`
	Last    time.Time `json:"last,omitzero"` // when it last grew
}

var appFaceTimeMu sync.Mutex

func appFaceTimeFile(team string) string {
	return filepath.Join(appDir(team), "run", "face-time.json")
}

// loadAppFaceTime is each person's total on an app's dev page, by the ID
// they signed in with (a Slack member ID or an email).
func loadAppFaceTime(team string) map[string]appFaceTime {
	times := map[string]appFaceTime{}
	data, err := os.ReadFile(appFaceTimeFile(team))
	if err == nil {
		json.Unmarshal(data, &times)
	}
	return times
}

// appFaceTimeAdd adds d to a person's total; a negative d takes back time
// that turned out to be idle, down to none.
func appFaceTimeAdd(team, who string, d time.Duration) {
	if who == "" || d == 0 {
		return
	}
	appFaceTimeMu.Lock()
	defer appFaceTimeMu.Unlock()
	times := loadAppFaceTime(team)
	entry := times[who]
	entry.Seconds = max(entry.Seconds+d.Seconds(), 0)
	if d > 0 {
		entry.Last = time.Now().UTC().Truncate(time.Second)
	}
	times[who] = entry
	data, _ := json.MarshalIndent(times, "", "  ")
	file := appFaceTimeFile(team)
	err := os.MkdirAll(filepath.Dir(file), 0o700)
	if err == nil {
		err = writeFileAtomic(file, append(data, '\n'), 0o600)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "hi: face time of %s: %v\n", team, err)
	}
}
