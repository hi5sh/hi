package cli

import "path/filepath"

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
