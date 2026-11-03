package cli

import (
	"os"
	"path/filepath"
)

// The tests run in internal/cli; these are the repository's root and the
// website's folder from there.
const (
	repoRoot = "../.."
	siteDir  = repoRoot + "/site"
)

// repoPath is a file in the repository, from the tests' folder.
func repoPath(parts ...string) string {
	return filepath.Join(append([]string{repoRoot}, parts...)...)
}

// No test reaches this machine's apps: appsDir would otherwise move its
// links and snapshots from before v2.6.67 (migrateAppsMachine).
func init() {
	if os.Getenv("HI_APPS_DIR") == "" {
		os.Setenv("HI_APPS_DIR", filepath.Join(os.TempDir(), "hi-test-teams"))
	}
}
