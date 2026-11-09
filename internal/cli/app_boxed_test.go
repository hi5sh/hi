package cli

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A folder a box writes in: hi's reads and writes there never follow a
// link out of it, never block on a FIFO, and never take a huge file.
func TestBoxedFolder(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secrets.json")
	os.WriteFile(secret, []byte(`{"key":"real"}`), 0o600)

	// A link at the name, or at name.tmp, is replaced, not written through.
	os.Symlink(secret, filepath.Join(home, "CLAUDE.md"))
	os.Symlink(secret, filepath.Join(home, "CLAUDE.md.tmp"))
	if err := boxedWriteFile(home, filepath.Join(home, "CLAUDE.md"), []byte("hi's block\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(secret); string(data) != `{"key":"real"}` {
		t.Fatalf("the write went through the link: %q", data)
	}
	if info, _ := os.Lstat(filepath.Join(home, "CLAUDE.md")); !info.Mode().IsRegular() {
		t.Fatalf("CLAUDE.md is %v", info.Mode())
	}

	// A link out of the folder is never read.
	os.Symlink(secret, filepath.Join(home, ".gitconfig"))
	if data, err := boxedReadFile(home, filepath.Join(home, ".gitconfig")); err == nil {
		t.Fatalf("read through the link: %q", data)
	}
	if data, ok := boxedReadToUpdate(home, filepath.Join(home, ".gitconfig")); ok || data != nil {
		t.Fatalf("a link out is to leave as it is: %q %v", data, ok)
	}
	if _, ok := boxedReadToUpdate(home, filepath.Join(home, "missing")); !ok {
		t.Fatal("a missing file is one to write")
	}

	// A folder that is a link out: no write or mkdir lands outside.
	os.Symlink(outside, filepath.Join(home, "hi"))
	if err := boxedMkdirAll(home, filepath.Join(home, "hi", "chat", "w1"), 0o700); err == nil {
		t.Fatal("made a folder through a link out")
	}
	if err := boxedCreateFile(home, filepath.Join(home, "hi", "authorized_keys"), []byte("key"), 0o600); err == nil {
		t.Fatal("wrote through a link out")
	}
	appCopyTreeIn(home, t.TempDir(), filepath.Join(home, "hi", "agy", "a-1"))
	if entries, _ := os.ReadDir(outside); len(entries) != 1 {
		t.Fatalf("outside has %d entries", len(entries))
	}
	if err := boxedRemoveAll(home, filepath.Join(home, "hi", "x")); err == nil {
		t.Fatal("removed through a link out")
	}

	// A FIFO doesn't hold hi; a file over the limit isn't read.
	fifo := filepath.Join(home, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err == nil {
		if _, err := boxedReadFile(home, fifo); err == nil {
			t.Fatal("read a FIFO")
		}
	}
	big := filepath.Join(home, "big")
	os.WriteFile(big, []byte(strings.Repeat("x", 100)), 0o600)
	if _, err := boxedReadFileLimit(home, big, 10); !errors.Is(err, errBoxedTooLarge) {
		t.Fatalf("a large file: %v", err)
	}
	if _, err := boxedReadFile(home, filepath.Join(home, "none")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a missing file: %v", err)
	}

	// A new file never takes a name that is there, a link included.
	os.Symlink(secret, filepath.Join(home, "taken"))
	if err := boxedCreateFile(home, filepath.Join(home, "taken"), []byte("x"), 0o600); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("created over a link: %v", err)
	}
}

// A line over the limit is passed over, its bytes still counted, so the
// next line is read where it starts.
func TestAppReadBoxLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	long := strings.Repeat("x", appBoxLineMax+10)
	os.WriteFile(path, []byte("one\n"+long+"\ntwo\npartial"), 0o600)
	f, _ := os.Open(path)
	defer f.Close()
	reader := bufioReader(f)
	var lines []string
	var offset int64
	for {
		line, n, err := appReadBoxLine(reader)
		if err != nil {
			break
		}
		if line != nil {
			lines = append(lines, strings.TrimSpace(string(line)))
		}
		offset += n
	}
	if strings.Join(lines, ",") != "one,two" || offset != int64(4+len(long)+1+4) {
		t.Fatalf("lines %q, offset %d", lines, offset)
	}
}

func bufioReader(f *os.File) *bufio.Reader { return bufio.NewReaderSize(f, 1<<20) }
