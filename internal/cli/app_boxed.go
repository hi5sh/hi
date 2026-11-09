package cli

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// A folder a box can write in, such as the dev box's home or its clone, is
// the box's: code in it can put a link anywhere there, or a FIFO, or a
// file of any size. hi reads and writes such a folder only through these:
// an os.Root at the folder never follows a link out of it, a read takes
// regular files only and at most appBoxedMaxRead, and a write goes to a
// fresh temporary name and is renamed into place, so a link the box left
// at the name, or at name.tmp, is replaced, never written through.

// appBoxedMaxRead is the most hi reads of one file a box wrote.
const appBoxedMaxRead = 16 << 20

// errBoxedTooLarge says a box's file is over the limit hi reads.
var errBoxedTooLarge = errors.New("larger than hi reads from a box")

// boxedOpen opens base as a root and gives path relative to it.
func boxedOpen(base, path string) (*os.Root, string, error) {
	return boxedOpenRoot(base, path, false)
}

// boxedOpenToWrite is boxedOpen, making base when it isn't there yet: base
// is hi's folder (the box's mount), so it is made as any of hi's are.
func boxedOpenToWrite(base, path string) (*os.Root, string, error) {
	return boxedOpenRoot(base, path, true)
}

func boxedOpenRoot(base, path string, create bool) (*os.Root, string, error) {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return nil, "", err
	}
	if !filepath.IsLocal(rel) && rel != "." {
		return nil, "", fmt.Errorf("%s isn't in %s", path, base)
	}
	if create {
		if err := os.MkdirAll(base, 0o700); err != nil {
			return nil, "", err
		}
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, "", err
	}
	return root, rel, nil
}

// boxedReadFile reads a regular file under base, up to appBoxedMaxRead.
// A missing file's error is fs.ErrNotExist, as os.ReadFile's.
func boxedReadFile(base, path string) ([]byte, error) {
	return boxedReadFileLimit(base, path, appBoxedMaxRead)
}

func boxedReadFileLimit(base, path string, limit int64) ([]byte, error) {
	root, rel, err := boxedOpen(base, path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return boxedReadRoot(root, rel, limit)
}

// boxedReadRoot reads a regular file in root, up to limit.
func boxedReadRoot(root *os.Root, rel string, limit int64) ([]byte, error) {
	// O_NONBLOCK: a FIFO the box left there doesn't hold hi.
	file, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, &fs.PathError{Op: "read", Path: rel, Err: errors.New("not a regular file")}
	}
	if info.Size() > limit {
		return nil, &fs.PathError{Op: "read", Path: rel, Err: errBoxedTooLarge}
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, &fs.PathError{Op: "read", Path: rel, Err: errBoxedTooLarge}
	}
	return data, nil
}

// boxedWriteFile writes path under base whole, or not at all.
func boxedWriteFile(base, path string, data []byte, perm os.FileMode) error {
	root, rel, err := boxedOpenToWrite(base, path)
	if err != nil {
		return err
	}
	defer root.Close()
	return boxedWriteRoot(root, rel, data, perm)
}

// boxedWriteRoot writes rel in root through a fresh temporary name.
func boxedWriteRoot(root *os.Root, rel string, data []byte, perm os.FileMode) error {
	suffix := make([]byte, 8)
	rand.Read(suffix)
	tmp := filepath.Join(filepath.Dir(rel), "."+filepath.Base(rel)+".hi-"+hex.EncodeToString(suffix))
	file, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = root.Rename(tmp, rel)
	}
	if err != nil {
		root.Remove(tmp)
	}
	return err
}

// boxedCreateFile writes a new file under base; it fails when the name is
// taken, by a file or a link.
func boxedCreateFile(base, path string, data []byte, perm os.FileMode) error {
	root, rel, err := boxedOpenToWrite(base, path)
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		root.Remove(rel)
	}
	return err
}

// boxedMkdirAll makes path and its parents under base.
func boxedMkdirAll(base, path string, perm os.FileMode) error {
	root, rel, err := boxedOpenToWrite(base, path)
	if err != nil {
		return err
	}
	defer root.Close()
	if rel == "." {
		return nil
	}
	return root.MkdirAll(rel, perm)
}

// boxedRemoveAll removes path under base, and nothing a link in it points
// at.
func boxedRemoveAll(base, path string) error {
	root, rel, err := boxedOpen(base, path)
	if err != nil {
		return err
	}
	defer root.Close()
	if rel == "." {
		return errors.New("hi doesn't remove a box's whole folder")
	}
	return root.RemoveAll(rel)
}

// boxedReadlink is a link's target under base.
func boxedReadlink(base, path string) (string, error) {
	root, rel, err := boxedOpen(base, path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	return root.Readlink(rel)
}

// boxedSymlink makes a link under base.
func boxedSymlink(base, target, path string) error {
	root, rel, err := boxedOpenToWrite(base, path)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.Symlink(target, rel)
}

// boxedExists says whether path under base is there, as a file or a
// folder that stays inside base.
func boxedExists(base, path string) bool {
	root, rel, err := boxedOpen(base, path)
	if err != nil {
		return false
	}
	defer root.Close()
	_, err = root.Stat(rel)
	return err == nil
}

// appCopyTreeIn copies a folder's regular files to to, a folder under
// base that a box can write in. The files read are hi's own run's; links
// among them are skipped.
func appCopyTreeIn(base, from, to string) error {
	root, rel, err := boxedOpenToWrite(base, to)
	if err != nil {
		return err
	}
	defer root.Close()
	return filepath.WalkDir(from, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		name, err := filepath.Rel(from, path)
		if err != nil {
			return nil
		}
		source, err := os.OpenRoot(filepath.Dir(path))
		if err != nil {
			return nil
		}
		data, err := boxedReadRoot(source, filepath.Base(path), appBoxedMaxRead)
		source.Close()
		if err != nil {
			return nil
		}
		target := filepath.Join(rel, name)
		if root.MkdirAll(filepath.Dir(target), 0o700) == nil {
			boxedWriteRoot(root, target, data, 0o600)
		}
		return nil
	})
}

// boxedReadToUpdate reads a file under base that hi adds to. ok is false
// when hi leaves it as it is: a link out of base (a dotfiles manager's
// link to /box/home/... is one, as the host sees it), not a regular file,
// or too large. A missing file is ok, and empty.
func boxedReadToUpdate(base, path string) ([]byte, bool) {
	data, err := boxedReadFile(base, path)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return data, true
	}
	fmt.Fprintf(os.Stderr, "hi: left %s as it is: %v\n", path, err)
	return nil, false
}

// boxedOpenFile opens a regular file under base for reading.
func boxedOpenFile(base, path string) (*os.File, error) {
	root, rel, err := boxedOpen(base, path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, &fs.PathError{Op: "open", Path: rel, Err: errors.New("not a regular file")}
	}
	return file, nil
}

// appBoxLineMax is the longest line hi reads of a box's file, such as a
// transcript; a longer one is passed over.
const appBoxLineMax = 32 << 20

// appReadBoxLine reads one line of a box's file, up to and with its '\n'.
// line is nil for a line over appBoxLineMax, which is passed over; n is
// the bytes read either way. An error is a line not finished yet, unless
// it is already too long: that one is passed over too, so the file's
// reader moves past it.
func appReadBoxLine(r *bufio.Reader) (line []byte, n int64, err error) {
	over := false
	for {
		chunk, err := r.ReadSlice('\n')
		n += int64(len(chunk))
		if !over && len(line)+len(chunk) <= appBoxLineMax {
			line = append(line, chunk...)
		} else {
			over, line = true, nil
		}
		switch {
		case err == bufio.ErrBufferFull:
			continue
		case err != nil && over:
			return nil, n, nil
		case err != nil:
			return nil, 0, err
		case over:
			return nil, n, nil
		}
		return line, n, nil
	}
}
