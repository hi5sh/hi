package cli

// The dev page's Files view, beside the chat: the project in the dev box,
// its clone and each piece of work's worktree, to browse, preview,
// download, upload into, and edit in a tab of its own.
// Every path goes through an os.Root at the worktree, so nothing reaches
// outside it, through .. or a symlink.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// appDevFileRoot is a tree the File view shows: dev's clone, or a piece
// of work's worktree.
type appDevFileRoot struct {
	ID    string `json:"id"`    // "repo", or the work's branch
	Label string `json:"label"` // what the page says
	Path  string `json:"path"`  // in the box
}

// appDevFileEntry is a file or folder in a listing.
type appDevFileEntry struct {
	Name     string    `json:"name"`
	Dir      bool      `json:"dir,omitempty"`
	Size     int64     `json:"size,omitempty"`
	Modified time.Time `json:"modified,omitzero"`
}

const (
	appDevFilePreviewMax = 1 << 20   // text the page previews
	appDevFileEditMax    = 2 << 20   // a file the page saves
	appDevFileUploadMax  = 200 << 20 // one upload's files together
)

// appDevSpacesCache keeps the box's workspaces a few seconds: a listing,
// a preview, and a download each need the worktrees, and asking herdr in
// the box for each would be slow.
var appDevSpacesCache struct {
	sync.Mutex
	team   string
	at     time.Time
	spaces []appDevSpace
}

func appDevSpacesCached(team string) ([]appDevSpace, error) {
	cache := &appDevSpacesCache
	cache.Lock()
	defer cache.Unlock()
	if cache.team == team && time.Since(cache.at) < 10*time.Second {
		return cache.spaces, nil
	}
	spaces, err := appDevSpaces(team)
	if err != nil {
		return nil, err
	}
	cache.team, cache.at, cache.spaces = team, time.Now(), spaces
	return spaces, nil
}

// appDevFileRoots are the trees the File view offers: dev's clone first.
func appDevFileRoots(team string) []appDevFileRoot {
	roots := []appDevFileRoot{{ID: "repo", Label: "dev (the project's clone)", Path: "/box/repo"}}
	spaces, _ := appDevSpacesCached(team)
	for _, space := range spaces {
		if space.Path != "" && space.Branch != "" {
			roots = append(roots, appDevFileRoot{ID: space.Branch, Label: space.Branch, Path: space.Path})
		}
	}
	return roots
}

// appDevHostPath is where a path in the dev box is on this machine: the
// box's home and its clone are folders of the team's dev/.
func appDevHostPath(team, boxPath string) (string, bool) {
	clean := path.Clean(boxPath)
	for box, host := range map[string]string{"/box/repo": "repo", "/box/home": "home"} {
		if clean == box || strings.HasPrefix(clean, box+"/") {
			return filepath.Join(appDevDir(team), host, filepath.FromSlash(strings.TrimPrefix(clean, box))), true
		}
	}
	return "", false
}

// appDevOpenRoot opens one of the File view's trees, as an os.Root.
func appDevOpenRoot(team, id string) (*os.Root, error) {
	for _, root := range appDevFileRoots(team) {
		if root.ID != id {
			continue
		}
		host, ok := appDevHostPath(team, root.Path)
		if !ok {
			return nil, fmt.Errorf("%s isn't in the dev box's home or clone", root.Path)
		}
		return os.OpenRoot(host)
	}
	return nil, fmt.Errorf("the dev box has no %s to look into", id)
}

// appDevFilePath makes the page's path one an os.Root takes: relative,
// forward slashes, "." for the top.
func appDevFilePath(p string) string {
	p = strings.Trim(path.Clean("/"+p), "/")
	if p == "" {
		return "."
	}
	return filepath.FromSlash(p)
}

// appDevListFiles lists a folder: folders first, then files, by name.
// .git is the clone's own, and stays out.
func appDevListFiles(root *os.Root, dir string) ([]appDevFileEntry, error) {
	folder, err := root.Open(appDevFilePath(dir))
	if err != nil {
		return nil, err
	}
	defer folder.Close()
	items, err := folder.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	entries := []appDevFileEntry{}
	for _, item := range items {
		if item.Name() == ".git" {
			continue
		}
		entry := appDevFileEntry{Name: item.Name(), Dir: item.IsDir()}
		if info, err := item.Info(); err == nil {
			entry.Modified = info.ModTime().UTC()
			if !entry.Dir {
				entry.Size = info.Size()
			}
			// A link to a folder lists as a folder; the root still keeps
			// it inside the tree.
			if info.Mode()&fs.ModeSymlink != 0 {
				if target, err := root.Stat(filepath.Join(appDevFilePath(dir), item.Name())); err == nil {
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

// appDevLooksText says a file's start is text a page can show.
func appDevLooksText(head []byte) bool {
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
	file, err := root.Open(appDevFilePath(name))
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
		if !appDevLooksText(head[:n]) {
			http.Error(w, "hi doesn't preview this kind of file yet; download it", http.StatusUnsupportedMediaType)
			return
		}
		if info.Size() > appDevFilePreviewMax {
			http.Error(w, fmt.Sprintf("%s is %s, more than the %s hi previews; download it", base, formatDataSize(info.Size()), formatDataSize(appDevFilePreviewMax)), http.StatusRequestEntityTooLarge)
			return
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// What the editor saves against: a save over a file changed since
		// is refused.
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err == nil {
			w.Header().Set("X-Hi-Sha256", hex.EncodeToString(hash.Sum(nil)))
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

// appDevEditPath is a file the editor may write: in the tree, not in a
// .git anywhere in it.
func appDevEditPath(p string) (string, error) {
	clean := strings.Trim(path.Clean("/"+p), "/")
	if p == "" || clean == "" || strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%q isn't a file of the project", p)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".git" {
			return "", fmt.Errorf("hi doesn't write in .git: %s", p)
		}
	}
	return filepath.FromSlash(clean), nil
}

// appDevFileState is what the editor knows of a file on disk: its content's
// SHA-256, to tell when it changed under it.
type appDevFileState struct {
	Sha256   string    `json:"sha256"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

func appDevStatFile(root *os.Root, name string) (appDevFileState, error) {
	file, err := root.Open(name)
	if err != nil {
		return appDevFileState{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return appDevFileState{}, err
	}
	if info.IsDir() {
		return appDevFileState{}, fmt.Errorf("%s is a folder", filepath.ToSlash(name))
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, appDevFileEditMax+1)); err != nil {
		return appDevFileState{}, err
	}
	return appDevFileState{Sha256: hex.EncodeToString(hash.Sum(nil)), Size: info.Size(), Modified: info.ModTime()}, nil
}

// appDevFileChanged is a save over a file that changed since the editor
// read it.
type appDevFileChanged struct{ State appDevFileState }

func (e *appDevFileChanged) Error() string {
	return "the file changed since you opened it, by Claude or someone else"
}

// saveDevFile writes an edited file in place, keeping its mode, unless it
// changed since base, its SHA-256 when the editor read it; force writes
// anyway. A file that isn't there, a folder, or a link isn't written.
func saveDevFile(root *os.Root, p string, content []byte, base string, force bool) (appDevFileState, error) {
	name, err := appDevEditPath(p)
	if err != nil {
		return appDevFileState{}, err
	}
	if len(content) > appDevFileEditMax {
		return appDevFileState{}, fmt.Errorf("that's %s, more than the %s hi saves", formatDataSize(int64(len(content))), formatDataSize(appDevFileEditMax))
	}
	info, err := root.Lstat(name)
	if err != nil {
		return appDevFileState{}, fmt.Errorf("%s isn't there any more", p)
	}
	if !info.Mode().IsRegular() {
		return appDevFileState{}, fmt.Errorf("%s is a folder or a link; hi doesn't write those", p)
	}
	now, err := appDevStatFile(root, name)
	if err != nil {
		return appDevFileState{}, err
	}
	if !force && now.Sha256 != base {
		return now, &appDevFileChanged{State: now}
	}
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return appDevFileState{}, err
	}
	_, err = file.Write(content)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return appDevFileState{}, fmt.Errorf("writing %s: %w", p, err)
	}
	return appDevStatFile(root, name)
}

// appDevUploadName is a file's name as an upload gives it, for the
// folder it goes in: its last part, never a path.
func appDevUploadName(name string) (string, error) {
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
	folder := appDevFilePath(dir)
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
		name, err := appDevUploadName(part.FileName())
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
			return written, &appDevFileExists{Name: name}
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

// appDevFileExists is an upload that would replace a file.
type appDevFileExists struct{ Name string }

func (e *appDevFileExists) Error() string { return e.Name + " is there already" }

// devFileRoutes are the File view's: the trees, a folder's listing, a
// file to view or download, an upload, and the editor's state of a file
// and its save. For the owners and developers only.
func (b *appBroker) devFileRoutes(mux *http.ServeMux, builder func(http.ResponseWriter, *http.Request) (appPersonKey, bool),
	reply func(http.ResponseWriter, int, any), failure func(http.ResponseWriter, int, error)) {
	open := func(w http.ResponseWriter, r *http.Request) (*os.Root, bool) {
		if _, ok := builder(w, r); !ok {
			return nil, false
		}
		root, err := appDevOpenRoot(b.team, r.URL.Query().Get("root"))
		if err != nil {
			failure(w, http.StatusNotFound, err)
			return nil, false
		}
		return root, true
	}
	mux.HandleFunc("GET "+appDevPagePrefix+"api/files/roots", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); ok {
			reply(w, http.StatusOK, appDevFileRoots(b.team))
		}
	})
	mux.HandleFunc("GET "+appDevPagePrefix+"api/files/list", func(w http.ResponseWriter, r *http.Request) {
		root, ok := open(w, r)
		if !ok {
			return
		}
		defer root.Close()
		entries, err := appDevListFiles(root, r.URL.Query().Get("path"))
		if err != nil {
			failure(w, http.StatusNotFound, err)
			return
		}
		reply(w, http.StatusOK, entries)
	})
	mux.HandleFunc("GET "+appDevPagePrefix+"api/files/raw", func(w http.ResponseWriter, r *http.Request) {
		root, ok := open(w, r)
		if !ok {
			return
		}
		defer root.Close()
		serveDevFile(w, r, root, r.URL.Query().Get("path"), r.URL.Query().Get("download") != "")
	})
	mux.HandleFunc("GET "+appDevPagePrefix+"api/files/stat", func(w http.ResponseWriter, r *http.Request) {
		root, ok := open(w, r)
		if !ok {
			return
		}
		defer root.Close()
		state, err := appDevStatFile(root, appDevFilePath(r.URL.Query().Get("path")))
		if err != nil {
			failure(w, http.StatusNotFound, err)
			return
		}
		reply(w, http.StatusOK, state)
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/files/save", func(w http.ResponseWriter, r *http.Request) {
		root, ok := open(w, r)
		if !ok {
			return
		}
		defer root.Close()
		var request struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			Base    string `json:"base"`
			Force   bool   `json:"force"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*appDevFileEditMax)).Decode(&request); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		state, err := saveDevFile(root, request.Path, []byte(request.Content), request.Base, request.Force)
		var changed *appDevFileChanged
		switch {
		case errors.As(err, &changed):
			reply(w, http.StatusConflict, map[string]any{"error": err.Error(), "changed": true, "state": changed.State})
		case err != nil:
			failure(w, http.StatusBadRequest, err)
		default:
			reply(w, http.StatusOK, state)
		}
	})
	mux.HandleFunc("POST "+appDevPagePrefix+"api/files/upload", func(w http.ResponseWriter, r *http.Request) {
		root, ok := open(w, r)
		if !ok {
			return
		}
		defer root.Close()
		r.Body = http.MaxBytesReader(w, r.Body, appDevFileUploadMax)
		written, err := uploadDevFiles(r, root, r.URL.Query().Get("path"), r.URL.Query().Get("overwrite") != "")
		var exists *appDevFileExists
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
