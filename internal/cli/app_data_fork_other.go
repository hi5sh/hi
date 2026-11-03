//go:build !linux

package cli

import "io/fs"

// Overlays are Linux's; elsewhere a preview's folder is never one.
func appOverlayWhiteout(info fs.FileInfo) bool { return false }

func appOverlayOpaque(path string) bool { return false }
