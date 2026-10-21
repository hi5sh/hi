package main

import (
	"io/fs"
	"syscall"
)

// teamOverlayWhiteout says a file in an overlay's own folder marks one the
// box deleted: a character device 0:0.
func teamOverlayWhiteout(info fs.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode()&fs.ModeCharDevice != 0 && stat.Rdev == 0
}

// teamOverlayOpaque says a folder in an overlay's own folder hides the
// one below it: the box deleted it and made it again. Rootless overlays
// mark it in a user attribute, others in a trusted one.
func teamOverlayOpaque(path string) bool {
	for _, attr := range []string{"user.overlay.opaque", "trusted.overlay.opaque"} {
		value := make([]byte, 4)
		if n, err := syscall.Getxattr(path, attr, value); err == nil && n > 0 && value[0] == 'y' {
			return true
		}
	}
	return false
}
