package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

// hi serve downloads a model's files from the Hub itself, on the host and
// not in the box, at the revision its recipe pins, resumably, and checks
// each against the Hub's SHA-256 (hi_serve.md, What happens, step 3). A
// partial file keeps .partial in its name until it checks out, so it is
// never served.

// serveHub is the Hub's address; tests replace it.
var serveHub = "https://huggingface.co"

var serveHubClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   30 * time.Second,
	ResponseHeaderTimeout: 2 * time.Minute,
}}

// hubFile is one file of a model, as the Hub lists it.
type hubFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// hubModel is the files of one quant at one commit.
type hubModel struct {
	Repo     string
	Revision string // the commit
	License  string
	Files    []hubFile
	// Through is the org server's Hub proxy, with its token, for a model
	// in the team's private organizations (hi data); "" for the Hub.
	Through string
	Token   string
}

func (m hubModel) hub() string { return firstNonEmpty(m.Through, serveHub) }

// serveDataToken asks the org server this device joined for a hi data
// token that reads one model; tests replace it.
var serveDataToken = func(repo string) (endpoint, token string, err error) {
	client, connection, err := dataClient()
	if err != nil {
		return "", "", err
	}
	token, endpoint, err = dataToken(client, connection, "model:"+repo)
	return endpoint, token, err
}

func (m hubModel) size() int64 {
	var total int64
	for _, file := range m.Files {
		total += file.Size
	}
	return total
}

// first is the file llama-server opens: the first part of a split model.
func (m hubModel) first() string {
	return m.Files[0].Path
}

// modelDirName is a repository's folder under the models folder.
func modelDirName(repo string) string {
	return strings.ReplaceAll(repo, "/", "--")
}

func serveModelsDir() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "hi", "models"), nil
}

// hubModelFiles asks the Hub for a quant's files at a revision, or at the
// latest commit when revision is "".
func hubModelFiles(repo, revision, quant string) (hubModel, error) {
	model, err := hubModelFilesAt(serveHub, "", repo, revision, quant)
	var closed hubClosedError
	if !errors.As(err, &closed) {
		return model, err
	}
	// Private or gated: the org server reads the team's organizations with
	// its own token (hi_data.md), so no Hugging Face token is here.
	endpoint, token, orgErr := serveDataToken(repo)
	if orgErr != nil {
		return hubModel{}, fmt.Errorf("%w; hi reads a private model through the server, which says: %v", err, orgErr)
	}
	return hubModelFilesAt(endpoint, token, repo, revision, quant)
}

// hubClosedError is a model the Hub doesn't show without a token.
type hubClosedError struct{ error }

func hubModelFilesAt(hub, token, repo, revision, quant string) (hubModel, error) {
	if revision == "" {
		revision = "main"
	}
	address := fmt.Sprintf("%s/api/models/%s/revision/%s?blobs=true", hub, repo, url.PathEscape(revision))
	request, err := http.NewRequest(http.MethodGet, address, nil)
	if err != nil {
		return hubModel{}, err
	}
	where := "the Hub"
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
		where = "the server"
	}
	response, err := serveHubClient.Do(request)
	if err != nil {
		return hubModel{}, fmt.Errorf("ask %s for %s: %w", where, repo, err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		if token != "" {
			return hubModel{}, fmt.Errorf("the server has no %s at %s for you (HTTP %d): %s", repo, revision, response.StatusCode,
				firstNonEmpty(response.Header.Get("X-Error-Message"), "see hi data ls"))
		}
		return hubModel{}, hubClosedError{fmt.Errorf("the Hub has no public %s at %s (HTTP %d)", repo, revision, response.StatusCode)}
	default:
		return hubModel{}, fmt.Errorf("ask %s for %s: HTTP %d", where, repo, response.StatusCode)
	}
	var info struct {
		SHA      string `json:"sha"`
		Gated    any    `json:"gated"`
		CardData struct {
			License any `json:"license"`
		} `json:"cardData"`
		Siblings []struct {
			Name string `json:"rfilename"`
			Size int64  `json:"size"`
			LFS  *struct {
				SHA256 string `json:"sha256"`
				Size   int64  `json:"size"`
			} `json:"lfs"`
		} `json:"siblings"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(&info); err != nil {
		return hubModel{}, fmt.Errorf("read the Hub's answer for %s: %w", repo, err)
	}
	if gated, ok := info.Gated.(string); ok && gated != "" && token == "" {
		return hubModel{}, hubClosedError{fmt.Errorf("%s is gated on the Hub", repo)}
	}
	model := hubModel{Repo: repo, Revision: info.SHA}
	if token != "" {
		model.Through, model.Token = hub, token
	}
	if license, ok := info.CardData.License.(string); ok {
		model.License = license
	}
	for _, sibling := range info.Siblings {
		if sibling.LFS == nil || !quantFile(sibling.Name, quant) {
			continue
		}
		if !validModelPath(sibling.Name) {
			return hubModel{}, fmt.Errorf("the Hub lists a file hi won't write: %q", sibling.Name)
		}
		model.Files = append(model.Files, hubFile{Path: sibling.Name, Size: sibling.LFS.Size, SHA256: sibling.LFS.SHA256})
	}
	if len(model.Files) == 0 {
		return hubModel{}, fmt.Errorf("%s has no GGUF files for the quant %s at %s", repo, quant, revision)
	}
	sort.Slice(model.Files, func(i, j int) bool { return model.Files[i].Path < model.Files[j].Path })
	if model.Revision == "" {
		return hubModel{}, fmt.Errorf("the Hub gave no commit for %s", repo)
	}
	return model, nil
}

var splitSuffix = regexp.MustCompile(`-\d{5}-of-\d{5}$`)

// quantFile reports whether a file in a repository is one of quant's GGUF
// files: in a folder named after it, or named ...-<quant>.gguf, split or
// not. Q3_K_XL doesn't take UD-Q3_K_XL's files, nor the vision projector.
func quantFile(name, quant string) bool {
	if !strings.HasSuffix(strings.ToLower(name), ".gguf") || strings.Contains(strings.ToLower(path.Base(name)), "mmproj") {
		return false
	}
	quant = strings.ToLower(quant)
	if dir := path.Dir(name); dir != "." && strings.ToLower(path.Base(dir)) == quant {
		return true
	}
	stem := strings.ToLower(strings.TrimSuffix(path.Base(name), path.Ext(name)))
	stem = splitSuffix.ReplaceAllString(stem, "")
	for _, separator := range []string{"-", ".", "_"} {
		if strings.HasSuffix(stem, separator+quant) {
			return strings.HasPrefix(quant, "ud-") || !strings.HasSuffix(stem, "ud-"+quant)
		}
	}
	return false
}

func validModelPath(name string) bool {
	clean := path.Clean(name)
	return clean == name && !path.IsAbs(name) && !strings.HasPrefix(clean, "../") && clean != ".." &&
		!strings.ContainsAny(name, "\\\x00\n") && !strings.HasPrefix(path.Base(name), ".")
}

// verifiedFiles is what hi checked in a model's folder: a file whose size
// and time haven't changed since isn't read again.
type verifiedFile struct {
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"`
}

const verifiedName = ".hi-verified.json"

func loadVerified(dir string) map[string]verifiedFile {
	verified := map[string]verifiedFile{}
	if data, err := os.ReadFile(filepath.Join(dir, verifiedName)); err == nil {
		json.Unmarshal(data, &verified)
	}
	return verified
}

func saveVerified(dir string, verified map[string]verifiedFile) error {
	data, err := json.MarshalIndent(verified, "", "  ")
	if err != nil {
		return err
	}
	temporary := filepath.Join(dir, verifiedName+".tmp")
	if err := os.WriteFile(temporary, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(dir, verifiedName))
}

// isVerified reports whether a file is in place and was checked as it is.
func isVerified(dir string, file hubFile, verified map[string]verifiedFile) bool {
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(file.Path)))
	if err != nil || info.Size() != file.Size {
		return false
	}
	entry, ok := verified[file.Path]
	return ok && entry.SHA256 == file.SHA256 && entry.Size == info.Size() && entry.ModTime == info.ModTime().UnixNano()
}

// toDownload is how much of a model isn't on this machine yet, counting a
// file in place but not checked as there.
func toDownload(dir string, model hubModel) int64 {
	verified := loadVerified(dir)
	var missing int64
	for _, file := range model.Files {
		if isVerified(dir, file, verified) {
			continue
		}
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(file.Path))); err == nil && info.Size() == file.Size {
			continue // checked before it is used; a mismatch downloads it again
		}
		partial, _ := os.Stat(filepath.Join(dir, filepath.FromSlash(file.Path)) + ".partial")
		if partial != nil && partial.Size() < file.Size {
			missing += file.Size - partial.Size()
		} else {
			missing += file.Size
		}
	}
	return missing
}

// freeDisk is the space free for this user where path is, or in its
// nearest folder that exists.
func freeDisk(dir string) (int64, error) {
	for {
		var stat syscall.Statfs_t
		err := syscall.Statfs(dir, &stat)
		if err == nil {
			return int64(stat.Bavail) * int64(stat.Bsize), nil
		}
		parent := filepath.Dir(dir)
		if !errors.Is(err, os.ErrNotExist) || parent == dir {
			return 0, err
		}
		dir = parent
	}
}

// downloadModel brings every file of a model into dir and checks it.
func downloadModel(dir string, model hubModel, stdout io.Writer) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	verified := loadVerified(dir)
	for i, file := range model.Files {
		target := filepath.Join(dir, filepath.FromSlash(file.Path))
		label := fmt.Sprintf("%s (%d of %d, %s)", path.Base(file.Path), i+1, len(model.Files), sizeText(file.Size))
		if isVerified(dir, file, verified) {
			continue
		}
		if info, err := os.Stat(target); err == nil && info.Size() == file.Size {
			fmt.Fprintf(stdout, "  checking %s, already here\n", label)
			sum, err := fileSHA256(target)
			if err != nil {
				return err
			}
			if sum == file.SHA256 {
				verified[file.Path] = verifiedFile{SHA256: sum, Size: info.Size(), ModTime: info.ModTime().UnixNano()}
				if err := saveVerified(dir, verified); err != nil {
					return err
				}
				continue
			}
			fmt.Fprintf(stdout, "  %s doesn't match the Hub's SHA-256; downloading it again\n", path.Base(file.Path))
			if err := os.Rename(target, target+".mismatch"); err != nil {
				return err
			}
			os.Remove(target + ".mismatch")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "  downloading %s\n", label)
		if err := downloadHubFile(model, file, target, stdout); err != nil {
			return err
		}
		info, err := os.Stat(target)
		if err != nil {
			return err
		}
		verified[file.Path] = verifiedFile{SHA256: file.SHA256, Size: info.Size(), ModTime: info.ModTime().UnixNano()}
		if err := saveVerified(dir, verified); err != nil {
			return err
		}
	}
	return nil
}

// downloadHubFile downloads one file into target.partial, resuming what is
// there, and moves it into place once its SHA-256 is the Hub's.
func downloadHubFile(model hubModel, file hubFile, target string, stdout io.Writer) error {
	partial := target + ".partial"
	address := fmt.Sprintf("%s/%s/resolve/%s/%s", model.hub(), model.Repo, model.Revision, escapeModelPath(file.Path))
	var lastErr error
	for attempt := 0; attempt < 8; attempt++ {
		if attempt > 0 {
			fmt.Fprintf(stdout, "  %v; resuming\n", lastErr)
			time.Sleep(min(time.Duration(attempt)*5*time.Second, 30*time.Second) * serveRetryScale)
		}
		done, err := downloadPart(address, model.Token, partial, file, stdout)
		if err == nil && done {
			break
		}
		lastErr = err
		if err == nil {
			lastErr = errors.New("the download ended early")
		}
		if errors.Is(err, errHashMismatch) {
			os.Remove(partial)
			return fmt.Errorf("%s doesn't match the Hub's SHA-256 after downloading; it was removed", path.Base(file.Path))
		}
		var permanent permanentError
		if errors.As(err, &permanent) {
			return err
		}
	}
	if lastErr != nil {
		if info, err := os.Stat(partial); err != nil || info.Size() != file.Size {
			return fmt.Errorf("download %s: %w; run it again to resume", path.Base(file.Path), lastErr)
		}
	}
	return os.Rename(partial, target)
}

// serveRetryScale shortens the waits between attempts in tests.
var serveRetryScale = time.Duration(1)

var errHashMismatch = errors.New("SHA-256 mismatch")

type permanentError struct{ error }

func escapeModelPath(name string) string {
	parts := strings.Split(name, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// downloadPart continues partial from where it ends, and reports whether
// it then holds the whole file with the right SHA-256.
func downloadPart(address, token, partial string, file hubFile, stdout io.Writer) (bool, error) {
	output, err := os.OpenFile(partial, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return false, permanentError{err}
	}
	defer output.Close()
	hash := sha256.New()
	have, err := io.Copy(hash, output)
	if err != nil {
		return false, permanentError{err}
	}
	if have > file.Size {
		if err := output.Truncate(0); err != nil {
			return false, permanentError{err}
		}
		output.Seek(0, io.SeekStart)
		hash.Reset()
		have = 0
	}
	if have < file.Size {
		request, err := http.NewRequest(http.MethodGet, address, nil)
		if err != nil {
			return false, permanentError{err}
		}
		if have > 0 {
			request.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
		}
		if token != "" {
			// Go drops it when the proxy sends the download to the Hub's storage.
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := serveHubClient.Do(request)
		if err != nil {
			return false, err
		}
		defer response.Body.Close()
		switch {
		case response.StatusCode == http.StatusPartialContent && have > 0:
		case response.StatusCode == http.StatusOK:
			if have > 0 {
				// The Hub sent all of it again.
				if err := output.Truncate(0); err != nil {
					return false, permanentError{err}
				}
				output.Seek(0, io.SeekStart)
				hash.Reset()
				have = 0
			}
		case response.StatusCode == http.StatusRequestedRangeNotSatisfiable:
			return false, errors.New("the Hub refused to resume; starting the file again")
		case response.StatusCode >= 400 && response.StatusCode < 500:
			return false, permanentError{fmt.Errorf("download %s: HTTP %d", path.Base(file.Path), response.StatusCode)}
		default:
			return false, fmt.Errorf("HTTP %d", response.StatusCode)
		}
		progress := &downloadProgress{out: stdout, total: file.Size, done: have, started: time.Now(), from: have, last: time.Now()}
		_, err = io.Copy(io.MultiWriter(output, hash, progress), io.LimitReader(response.Body, file.Size-have))
		have = progress.done
		progress.finish()
		if err != nil {
			return false, err
		}
	}
	if have != file.Size {
		return false, nil
	}
	if hex.EncodeToString(hash.Sum(nil)) != file.SHA256 {
		return false, errHashMismatch
	}
	return true, output.Sync()
}

// downloadProgress prints how far a download is, every 15 seconds.
type downloadProgress struct {
	out               io.Writer
	total, done, from int64
	started, last     time.Time
	printed           bool
}

var downloadProgressEvery = 15 * time.Second

func (p *downloadProgress) Write(data []byte) (int, error) {
	p.done += int64(len(data))
	if now := time.Now(); now.Sub(p.last) >= downloadProgressEvery {
		p.last = now
		p.print(now)
	}
	return len(data), nil
}

func (p *downloadProgress) print(now time.Time) {
	rate := float64(p.done-p.from) / max(now.Sub(p.started).Seconds(), 0.001)
	line := fmt.Sprintf("    %s of %s, %.0f MB/s", sizeText(p.done), sizeText(p.total), rate/(1<<20))
	if rate > 0 && p.done < p.total {
		line += ", " + formatDuration(time.Duration(float64(p.total-p.done)/rate)*time.Second) + " left"
	}
	fmt.Fprintln(p.out, line)
	p.printed = true
}

func (p *downloadProgress) finish() {
	if p.printed {
		p.print(time.Now())
	}
}

func fileSHA256(name string) (string, error) {
	input, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer input.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
