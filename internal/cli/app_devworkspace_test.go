package cli

import (
	"errors"
	"testing"
)

// A workspace herdr no longer has, after the dev box restarted, is said
// plainly, not as herdr's JSON.
func TestDevWorkspaceGone(t *testing.T) {
	appTestDevPage(t)
	previous := appDevBoxExec
	t.Cleanup(func() { appDevBoxExec = previous })
	appDevBoxExec = func(team string, command ...string) (string, error) {
		return `{"error":{"code":"workspace_not_found","message":"workspace w4 not found"},"id":"cli:pane:list"}`, errors.New("exit status 1")
	}
	if _, _, err := devChatAgent("payments", "w4"); !errors.Is(err, errAppDevWorkspaceGone) {
		t.Fatal(err)
	}
}
