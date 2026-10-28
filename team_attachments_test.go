package main

import (
	"path/filepath"
	"testing"
)

func TestAgentFilesOption(t *testing.T) {
	dir := t.TempDir()
	options, err := parseBoxOptions("agent", []string{"claude", "--files", dir, "do it"})
	if err != nil || len(options.mounts) != 1 || options.mounts[0] != dir+":/box/files:ro" {
		t.Fatal(options.mounts, err)
	}
	if _, err := parseBoxOptions("agent", []string{"--files", filepath.Join(dir, "nope")}); err == nil {
		t.Fatal("a missing folder")
	}
}
