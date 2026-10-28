package main

// The dev page's Files tab: the project in the dev box, its clone and each
// piece of work's worktree, to browse, preview, download, and upload into.
// Every path goes through an os.Root at the worktree, so nothing reaches
// outside it, through .. or a symlink.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// teamDevFileRoot is a tree the File view shows: dev's clone, or a piece
// of work's worktree.
type teamDevFileRoot struct {
	ID    string `json:"id"`    // "repo", or the work's branch
	Label string `json:"label"` // what the page says
	Path  string `json:"path"`  // in the box
}

// teamDevFileEntry is a file or folder in a listing.
type teamDevFileEntry struct {
	Name     string    `json:"name"`
	Dir      bool      `json:"dir,omitempty"`
	Size     int64     `json:"size,omitempty"`
	Modified time.Time `json:"modified,omitzero"`
}

const (
	teamDevFilePreviewMax = 1 << 20   // text the page previews
	teamDevFileUploadMax  = 200 << 20 // one upload's files together
)

// teamDevSpacesCache keeps the box's workspaces a few seconds: a listing,
// a preview, and a download each need the worktrees, and asking herdr in
// the box for each would be slow.
var teamDevSpacesCache struct {
	sync.Mutex
	team   string
	at     time.Time
	spaces []teamDevSpace
}

func teamDevSpacesCached(team string) ([]teamDevSpace, error) {
	cache := &teamDevSpacesCache
	cache.Lock()
	defer cache.Unlock()
	if cache.team == team && time.Since(cache.at) < 10*time.Second {
		return cache.spaces, nil
	}
	spaces, err := teamDevSpaces(team)
	if err != nil {
		return nil, err
	}
	cache.team, cache.at, cache.spaces = team, time.Now(), spaces
	return spaces, nil
}

// teamDevFileRoots are the trees the File view offers: dev's clone first.
func teamDevFileRoots(team string) []teamDevFileRoot {
	roots := []teamDevFileRoot{{ID: "repo", Label: "dev (the project's clone)", Path: "/box/repo"}}
	spaces, _ := teamDevSpacesCached(team)
	for _, space := range spaces {
		if space.Path != "" && space.Branch != "" {
			roots = append(roots, teamDevFileRoot{ID: space.Branch, Label: space.Branch, Path: space.Path})
		}
	}
	return roots
}

// teamDevHostPath is where a path in the dev box is on this machine: the
// box's home and its clone are folders of the team's dev/.
func teamDevHostPath(team, boxPath string) (string, bool) {
	clean := path.Clean(boxPath)
	for box, host := range map[string]string{"/box/repo": "repo", "/box/home": "home"} {
		if clean == box || strings.HasPrefix(clean, box+"/") {
			return filepath.Join(teamDevDir(team), host, filepath.FromSlash(strings.TrimPrefix(clean, box))), true
		}
	}
	return "", false
}

// teamDevOpenRoot opens one of the File view's trees, as an os.Root.
func teamDevOpenRoot(team, id string) (*os.Root, error) {
	for _, root := range teamDevFileRoots(team) {
		if root.ID != id {
			continue
		}
		host, ok := teamDevHostPath(team, root.Path)
		if !ok {
			return nil, fmt.Errorf("%s isn't in the dev box's home or clone", root.Path)
		}
		return os.OpenRoot(host)
	}
	return nil, fmt.Errorf("the dev box has no %s to look into", id)
}

// teamDevFilePath makes the page's path one an os.Root takes: relative,
// forward slashes, "." for the top.
func teamDevFilePath(p string) string {
	p = strings.Trim(path.Clean("/"+p), "/")
	if p == "" {
		return "."
	}
	return filepath.FromSlash(p)
}

// teamDevListFiles lists a folder: folders first, then files, by name.
// .git is the clone's own, and stays out.
func teamDevListFiles(root *os.Root, dir string) ([]teamDevFileEntry, error) {
	folder, err := root.Open(teamDevFilePath(dir))
	if err != nil {
		return nil, err
	}
	defer folder.Close()
	items, err := folder.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	entries := []teamDevFileEntry{}
	for _, item := range items {
		if item.Name() == ".git" {
			continue
		}
		entry := teamDevFileEntry{Name: item.Name(), Dir: item.IsDir()}
		if info, err := item.Info(); err == nil {
			entry.Modified = info.ModTime().UTC()
			if !entry.Dir {
				entry.Size = info.Size()
			}
			// A link to a folder lists as a folder; the root still keeps
			// it inside the tree.
			if info.Mode()&fs.ModeSymlink != 0 {
				if target, err := root.Stat(filepath.Join(teamDevFilePath(dir), item.Name())); err == nil {
					entry.Dir = target.IsDir()
				}
			}
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}

// teamDevLooksText says a file's start is text a page can show.
func teamDevLooksText(head []byte) bool {
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	// A multi-byte character may be cut at the end of what was read.
	for i := 0; i < 4 && len(head) > 0 && !utf8.Valid(head); i++ {
		head = head[:len(head)-1]
	}
	return utf8.Valid(head)
}

// serveDevFile sends a file of a tree: to view (pictures and PDFs as they
// are, anything that reads as text as plain text, the rest not at all), or
// to download. Nothing runs as a page on the dev page's address: an HTML
// file is text, in a sandbox.
func serveDevFile(w http.ResponseWriter, r *http.Request, root *os.Root, name string, download bool) {
	file, err := root.Open(teamDevFilePath(name))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.Error(w, name+" is a folder", http.StatusBadRequest)
		return
	}
	base := path.Base(filepath.ToSlash(name))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	if download {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": base}))
		http.ServeContent(w, r, "", info.ModTime(), file)
		return
	}
	kind, _, _ := strings.Cut(mime.TypeByExtension(strings.ToLower(path.Ext(base))), ";")
	switch kind {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "application/pdf":
		w.Header().Set("Content-Type", kind)
	default:
		head := make([]byte, 8192)
		n, _ := io.ReadFull(file, head)
		if !teamDevLooksText(head[:n]) {
			http.Error(w, "hi doesn't preview this kind of file yet; download it", http.StatusUnsupportedMediaType)
			return
		}
		if info.Size() > teamDevFilePreviewMax {
			http.Error(w, fmt.Sprintf("%s is %s, more than the %s hi previews; download it", base, formatDataSize(info.Size()), formatDataSize(teamDevFilePreviewMax)), http.StatusRequestEntityTooLarge)
			return
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	if kind != "application/pdf" { // Chromium's PDF viewer doesn't open in a sandbox
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'; style-src 'unsafe-inline'")
	}
	http.ServeContent(w, r, "", info.ModTime(), file)
}

// teamDevUploadName is a file's name as an upload gives it, for the
// folder it goes in: its last part, never a path.
func teamDevUploadName(name string) (string, error) {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "" || name == "." || name == ".." || name == "/" || name == ".git" || strings.ContainsAny(name, "\x00") {
		return "", fmt.Errorf("%q isn't a file's name", name)
	}
	return name, nil
}

// uploadDevFiles writes the files of a multipart upload into a folder of a
// tree. Without overwrite, a file that's there already stops it, before
// anything is written.
func uploadDevFiles(r *http.Request, root *os.Root, dir string, overwrite bool) ([]string, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, err
	}
	folder := teamDevFilePath(dir)
	if info, err := root.Stat(folder); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%s isn't a folder", dir)
	}
	var written []string
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return written, nil
		}
		if err != nil {
			return written, err
		}
		if part.FormName() != "file" || part.FileName() == "" {
			continue
		}
		name, err := teamDevUploadName(part.FileName())
		if err != nil {
			return written, err
		}
		target := filepath.Join(folder, name)
		flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		if !overwrite {
			flags |= os.O_EXCL
		}
		if info, err := root.Lstat(target); err == nil && (info.IsDir() || info.Mode()&fs.ModeSymlink != 0) {
			return written, fmt.Errorf("%s is a folder or a link there; hi doesn't replace those", name)
		}
		file, err := root.OpenFile(target, flags, 0o644)
		if errors.Is(err, fs.ErrExist) {
			return written, &teamDevFileExists{Name: name}
		}
		if err != nil {
			return written, err
		}
		_, err = io.Copy(file, part)
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			root.Remove(target)
			return written, fmt.Errorf("writing %s: %w", name, err)
		}
		written = append(written, name)
	}
}

// teamDevFileExists is an upload that would replace a file.
type teamDevFileExists struct{ Name string }

func (e *teamDevFileExists) Error() string { return e.Name + " is there already" }

// devFileRoutes are the File view's: the trees, a folder's listing, a
// file to view or download, and an upload. For the owners only.
func (b *teamBroker) devFileRoutes(mux *http.ServeMux, builder func(http.ResponseWriter, *http.Request) (teamPersonKey, bool),
	reply func(http.ResponseWriter, int, any), failure func(http.ResponseWriter, int, error)) {
	open := func(w http.ResponseWriter, r *http.Request) (*os.Root, bool) {
		if _, ok := builder(w, r); !ok {
			return nil, false
		}
		root, err := teamDevOpenRoot(b.team, r.URL.Query().Get("root"))
		if err != nil {
			failure(w, http.StatusNotFound, err)
			return nil, false
		}
		return root, true
	}
	mux.HandleFunc("GET "+teamDevPagePrefix+"api/files/roots", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); ok {
			reply(w, http.StatusOK, teamDevFileRoots(b.team))
		}
	})
	mux.HandleFunc("GET "+teamDevPagePrefix+"api/files/list", func(w http.ResponseWriter, r *http.Request) {
		root, ok := open(w, r)
		if !ok {
			return
		}
		defer root.Close()
		entries, err := teamDevListFiles(root, r.URL.Query().Get("path"))
		if err != nil {
			failure(w, http.StatusNotFound, err)
			return
		}
		reply(w, http.StatusOK, entries)
	})
	mux.HandleFunc("GET "+teamDevPagePrefix+"api/files/raw", func(w http.ResponseWriter, r *http.Request) {
		root, ok := open(w, r)
		if !ok {
			return
		}
		defer root.Close()
		serveDevFile(w, r, root, r.URL.Query().Get("path"), r.URL.Query().Get("download") != "")
	})
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/files/upload", func(w http.ResponseWriter, r *http.Request) {
		root, ok := open(w, r)
		if !ok {
			return
		}
		defer root.Close()
		r.Body = http.MaxBytesReader(w, r.Body, teamDevFileUploadMax)
		written, err := uploadDevFiles(r, root, r.URL.Query().Get("path"), r.URL.Query().Get("overwrite") != "")
		var exists *teamDevFileExists
		switch {
		case errors.As(err, &exists):
			reply(w, http.StatusConflict, map[string]any{"error": err.Error(), "exists": exists.Name, "written": written})
		case err != nil:
			reply(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "written": written})
		default:
			reply(w, http.StatusOK, map[string]any{"written": written})
		}
	})
}
