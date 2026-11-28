package cli

import (
	"testing"
	"time"
)

// Face time: a person's time with a window in use, once however many
// windows they have, less the quiet before a window said it's idle.
func TestPresenceFaceTime(t *testing.T) {
	board := newPresenceBoard()
	got := map[string]time.Duration{}
	board.record = func(who string, d time.Duration) { got[who] += d }
	near := func(who string, want time.Duration) {
		t.Helper()
		if d := got[who] - want; d < -time.Second || d > time.Second {
			t.Fatalf("%s: %v, want %v", who, got[who], want)
		}
	}
	back := func(who string, d time.Duration) { // the stretch began d earlier
		board.mu.Lock()
		defer board.mu.Unlock()
		board.active[who].start = board.active[who].start.Add(-d)
		board.active[who].since = board.active[who].since.Add(-d)
	}

	// Ten minutes, the last three without input: seven.
	one, _ := board.join("olga", "Olga")
	back("olga", 10*time.Minute)
	board.setIdle(one, true, 3*time.Minute)
	near("olga", 7*time.Minute)

	// Two windows in use count once; a minute's tally as it goes on.
	board.setIdle(one, false, 0)
	two, _ := board.join("olga", "Olga")
	back("olga", 5*time.Minute)
	board.mu.Lock()
	board.tally(time.Now(), false)
	board.mu.Unlock()
	near("olga", 12*time.Minute)
	// Closing one window, the other still in use: nothing taken back.
	back("olga", 2*time.Minute)
	board.leave(two)
	near("olga", 14*time.Minute)
	// The last goes idle at once (hidden): nothing taken back either.
	board.setIdle(one, true, 0)
	near("olga", 14*time.Minute)
	// A window closed while idle adds nothing.
	board.leave(one)
	near("olga", 14*time.Minute)

	// Quiet longer than the stretch takes back no more than it.
	three, _ := board.join("sam", "Sam")
	back("sam", time.Minute)
	board.mu.Lock()
	board.tally(time.Now(), false) // told: since is now
	board.mu.Unlock()
	board.setIdle(three, true, 3*time.Minute)
	near("sam", 0)
}

// The totals are kept per app, and never below none.
func TestAppFaceTimeFile(t *testing.T) {
	setupTeamTest(t)
	appFaceTimeAdd("payments", "U07ANNA", 90*time.Second)
	appFaceTimeAdd("payments", "U07ANNA", -30*time.Second)
	appFaceTimeAdd("payments", "U07SAM", -time.Minute)
	times := loadAppFaceTime("payments")
	if times["U07ANNA"].Seconds != 60 || times["U07ANNA"].Last.IsZero() || times["U07SAM"].Seconds != 0 {
		t.Fatalf("%+v", times)
	}
}
