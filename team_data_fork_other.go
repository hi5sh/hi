//go:build !linux

package main

import "io/fs"

// Overlays are Linux's; elsewhere a preview's folder is never one.
func teamOverlayWhiteout(info fs.FileInfo) bool { return false }

func teamOverlayOpaque(path string) bool { return false }
