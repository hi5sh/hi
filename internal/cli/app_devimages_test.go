package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pictures a message names, as the page shows them: absolute, from
// the home, or relative to the session's folder; only the dev box's home
// and clone; no URL's.
func TestAppDevImages(t *testing.T) {
	cwd := "/box/home/.herdr/worktrees/repo/1-chart"
	got := appDevImages("Saved shots/after.png and ~/Downloads/a.JPG. See /box/repo/docs/logo.webp, "+
		"https://example.com/x.png, /etc/passwd.png, (../before.gif) and shots/after.png again.", cwd)
	want := []string{cwd + "/shots/after.png", "/box/home/Downloads/a.JPG", "/box/repo/docs/logo.webp", "/box/home/.herdr/worktrees/repo/before.gif"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", got)
	}
	if got := appDevImages("shots/after.png", ""); len(got) != 0 {
		t.Fatal(got)
	}
}

// api/chat/image serves a picture of the box's, and nothing else.
func TestTeamDevChatImage(t *testing.T) {
	b, _ := appTestDevPage(t)
	handler := b.devPageHandler()
	shots := filepath.Join(appDevHome(b.team), "shots")
	os.MkdirAll(shots, 0o700)
	os.WriteFile(filepath.Join(shots, "a.png"), []byte("\x89PNG\r\n\x1a\n"), 0o600)
	os.WriteFile(filepath.Join(shots, "notes.txt"), []byte("secret"), 0o600)
	os.WriteFile(filepath.Join(shots, "a.png.txt"), []byte("secret"), 0o600)
	if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/chat/image?path=/box/home/shots/a.png", nil); code != 200 || !strings.HasPrefix(body, "\x89PNG") {
		t.Fatal(code, body)
	}
	for _, p := range []string{"/box/home/shots/notes.txt", "/box/home/shots/a.png.txt", "/etc/x.png", "/box/home/../../etc/x.png", "shots/a.png", "/box/home/shots/none.png"} {
		if code, body := appTestDevPageCall(t, handler, appTestOwnerKey, "GET", "api/chat/image?path="+p, nil); code == 200 {
			t.Fatal(p, body)
		}
	}
}
