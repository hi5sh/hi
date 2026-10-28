package main

// VS Code on the dev page, beside Chat and Terminal: code-server (MIT, VS
// Code in the browser, with Open VSX's extensions) runs in the dev box on
// a socket in /box/run, which the team's service reaches, and the dev page
// passes it through for the owners only, as it does the terminal. hi
// fetches code-server into the box's home the first time, so a running
// box gets it without a restart. It opens the workspace's worktree, the
// same files the agents work on; Claude Code links to it with /ide.
//
// Served over plain http, the browser keeps from VS Code what it gives
// only to https pages: previews and extensions' panels (webviews), and the
// clipboard's API. The editor, the files, search, git, and terminals work.

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	// teamCodeServerVersion is the code-server hi fetches: one some weeks
	// old, not the newest.
	teamCodeServerVersion = "4.141.0"
	teamDevCodeSocketName = "code.sock"
)

// teamDevCodeExtensions are what hi installs from Open VSX: Claude Code's,
// for /ide (Claude's changes open as diffs in the editor), and viewers for
// files VS Code shows only as text or not at all. The viewers draw in
// webviews, which the browser gives only to https pages, so they work on a
// team's https address (hi net domain). Each is pinned: extensions don't
// update themselves here (extensions.autoUpdate), and a new version is a
// change to hi, after a look at it.
//
// Left out on 2026-10-27: tomoki1207.pdf and cweijan.vscode-office bundle
// pdf.js older than 4.2.67 (CVE-2024-4367: a PDF that runs script) with
// eval allowed, and vscode-office takes over Markdown's editor;
// battlecook.omni-viewer makes itself the default for some 70 formats,
// audio and video among them.
var teamDevCodeExtensions = []string{
	"anthropic.claude-code",
	"mathematic.vscode-pdf@0.2.6",        // PDFs, with pdf.js 6.2 and a strict CSP
	"mechatroner.rainbow-csv@3.24.1",     // CSV and TSV columns in colours
	"janisdd.vscode-edit-csv@0.11.9",     // CSV as a table, to read and edit
	"bierner.markdown-mermaid@1.32.1",    // Mermaid diagrams in Markdown's preview
	"ms-toolsai.jupyter-renderers@1.3.0", // notebooks' rich outputs: Plotly, Vega, GeoJSON
}

// teamDevCodeHosts are Open VSX's, where VS Code's extensions come from.
var teamDevCodeHosts = []string{"open-vsx.org", "openvsx.eclipsecontent.org", "openvsxorg.blob.core.windows.net"}

// teamDevCodeHome is where code-server is, in the box's home on the
// machine, and its path in the box.
func teamDevCodeHome(team string) (host, box string) {
	rel := filepath.Join(".hi", "code-server", teamCodeServerVersion)
	return filepath.Join(teamDevDir(team), "home", rel), "/box/home/" + rel
}

func teamDevCodeSocket(team string) string {
	return filepath.Join(teamDevSocketDir(team), teamDevCodeSocketName)
}

// teamDevCodeFetch fetches code-server's release; tests replace it.
var teamDevCodeFetch = func(ctx context.Context, url string) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("%s: %s", url, response.Status)
	}
	return response.Body, nil
}

// teamDevCodeStart starts code-server in the box; tests replace it.
var teamDevCodeStart = func(team string, command ...string) error {
	engine, _, err := detectBoxEngine()
	if err != nil {
		return err
	}
	_, err = engine.output(append([]string{"exec", "-d", teamDevContainer(team)}, command...)...)
	return err
}

// teamDevCodeWait is how long code-server has to answer once started.
var teamDevCodeWait = 40 * time.Second

var (
	teamDevCodeMu sync.Mutex
	// teamDevCodeInstalling is held while Claude's extension installs.
	teamDevCodeInstalling sync.Mutex
)

// installTeamDevCode puts code-server in the box's home, once.
func installTeamDevCode(team string) error {
	dir, _ := teamDevCodeHome(team)
	if fileExists(filepath.Join(dir, "bin", "code-server")) {
		return nil
	}
	arch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[runtime.GOARCH]
	if arch == "" {
		return fmt.Errorf("code-server has no build for %s", runtime.GOARCH)
	}
	name := fmt.Sprintf("code-server-%s-linux-%s", teamCodeServerVersion, arch)
	url := fmt.Sprintf("https://github.com/coder/code-server/releases/download/v%s/%s.tar.gz", teamCodeServerVersion, name)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	body, err := teamDevCodeFetch(ctx, url)
	if err != nil {
		return fmt.Errorf("fetching VS Code (code-server): %w", err)
	}
	defer body.Close()
	tmp := dir + ".part"
	os.RemoveAll(tmp)
	if err := untarTeamDevCode(body, tmp, name+"/"); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("unpacking VS Code (code-server): %w", err)
	}
	return os.Rename(tmp, dir)
}

// untarTeamDevCode unpacks a .tar.gz into dir, taking prefix off each
// name, and nothing outside dir.
func untarTeamDevCode(r io.Reader, dir, prefix string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	archive := tar.NewReader(gz)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(header.Name, prefix)
		target := filepath.Join(dir, rel)
		if rel == "" || rel == header.Name || !strings.HasPrefix(target, filepath.Clean(dir)+string(os.PathSeparator)) {
			continue
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)&0o755|0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(file, archive)
			file.Close()
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(header.Linkname) || !strings.HasPrefix(filepath.Join(filepath.Dir(target), header.Linkname), filepath.Clean(dir)) {
				continue
			}
			os.MkdirAll(filepath.Dir(target), 0o755)
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}
		}
	}
}

// teamDevCodeSettings are VS Code's settings in the box, written once:
// VS Code's 2026 Dark, as the page is, no workspace trust (the box is the boundary),
// no telemetry or updates, no VS Code's own AI chat beside the agents',
// and saving as you type, so an agent working on the same files sees your
// changes.
const teamDevCodeSettings = `{
  "workbench.colorTheme": "Dark 2026",
  "workbench.startupEditor": "none",
  "security.workspace.trust.enabled": false,
  "telemetry.telemetryLevel": "off",
  "update.mode": "none",
  "extensions.autoUpdate": false,
  "files.autoSave": "afterDelay",
  "files.autoSaveDelay": 1000,
  "git.openRepositoryInParentFolders": "always",
  "git.autofetch": false,
  "chat.disableAIFeatures": true,
  "workbench.secondarySideBar.defaultVisibility": "hidden"
}
`

// teamDevCodeTheme is the settings' line for the theme, and
// teamDevCodeOldThemes the ones it replaces in a box's older settings:
// following the system's light or dark, before v2.5.52, which in a dark
// browser was 2026 Dark and in a light one VS Code's light; and Visual
// Studio Dark, v2.5.52's, which VS Code's cards don't go with.
var (
	teamDevCodeTheme     = `"workbench.colorTheme": "Dark 2026",`
	teamDevCodeOldThemes = []string{`"window.autoDetectColorScheme": true,`, `"workbench.colorTheme": "Visual Studio Dark",`}
)

// writeTeamDevCodeSettings writes VS Code's settings in the box the first
// time, and moves older ones to the theme. A running VS Code reads them
// again by itself.
func writeTeamDevCodeSettings(team string) error {
	user := filepath.Join(teamDevDir(team), "home", ".local", "share", "code-server", "User")
	if err := os.MkdirAll(user, 0o700); err != nil {
		return err
	}
	settings := filepath.Join(user, "settings.json")
	old, err := os.ReadFile(settings)
	if errors.Is(err, fs.ErrNotExist) {
		return os.WriteFile(settings, []byte(teamDevCodeSettings), 0o600)
	} else if err != nil {
		return err
	}
	text := string(old)
	for _, line := range teamDevCodeOldThemes {
		text = strings.Replace(text, line, teamDevCodeTheme, 1)
	}
	if text == string(old) {
		return nil
	}
	return os.WriteFile(settings, []byte(text), 0o600)
}

// teamDevCodeAlive is whether code-server answers on its socket.
func teamDevCodeAlive(team string) bool {
	client := teamDevCodeClient(team, 3*time.Second)
	response, err := client.Get("http://code-server/healthz")
	if err != nil {
		return false
	}
	response.Body.Close()
	return response.StatusCode == http.StatusOK
}

func teamDevCodeDial(team string) func(ctx context.Context, _, _ string) (net.Conn, error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", teamDevCodeSocket(team))
	}
}

func teamDevCodeClient(team string, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{DialContext: teamDevCodeDial(team)}}
}

// devCodeStart has code-server running in the dev box: fetched the first
// time, started when it isn't answering.
func (b *teamBroker) devCodeStart() error {
	teamDevCodeMu.Lock()
	defer teamDevCodeMu.Unlock()
	if !b.running(teamDevContainer(b.team)) {
		return errors.New("the dev box isn't running")
	}
	_, bin := teamDevCodeHome(b.team)
	if err := writeTeamDevCodeSettings(b.team); err != nil {
		return err
	}
	if teamDevCodeAlive(b.team) {
		// Running from before: the box's list and the extensions as this
		// hi has them.
		if err := writeTeamDevAllow(b.team, ""); err != nil {
			return err
		}
		go b.devCodeExtensions(bin)
		return nil
	}
	if err := installTeamDevCode(b.team); err != nil {
		return err
	}
	// Open VSX, for the extensions.
	if err := writeTeamDevAllow(b.team, ""); err != nil {
		return err
	}
	os.Remove(teamDevCodeSocket(b.team))
	data := "/box/home/.local/share/code-server"
	server := []string{bin + "/bin/code-server", "--socket", "/box/run/" + teamDevCodeSocketName, "--socket-mode", "600",
		"--auth", "none", "--disable-telemetry", "--disable-update-check", "--disable-workspace-trust",
		"--user-data-dir", data, "--extensions-dir", data + "/extensions", "--ignore-last-opened", "/box/repo"}
	command := "exec " + shellQuoteAll(server) + " >>/box/home/.hi/code-server.log 2>&1"
	if err := teamDevCodeStart(b.team, "sh", "-c", command); err != nil {
		return fmt.Errorf("starting VS Code in the dev box: %w", err)
	}
	for deadline := time.Now().Add(teamDevCodeWait); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
		if teamDevCodeAlive(b.team) {
			go b.devCodeExtensions(bin)
			return nil
		}
	}
	return errors.New("VS Code didn't start in the dev box; its log is ~/.hi/code-server.log there")
}

// devCodeExtensions installs the extensions once, through the box's
// proxy, and again when hi's list changes.
func (b *teamBroker) devCodeExtensions(bin string) {
	marker := filepath.Join(teamDevDir(b.team), "home", ".hi", "code-server-extensions")
	want := strings.Join(teamDevCodeExtensions, "\n") + "\n"
	if have, _ := os.ReadFile(marker); string(have) == want || !teamDevCodeInstalling.TryLock() {
		return
	}
	defer teamDevCodeInstalling.Unlock()
	data := "/box/home/.local/share/code-server"
	command := []string{bin + "/bin/code-server", "--user-data-dir", data, "--extensions-dir", data + "/extensions"}
	for _, extension := range teamDevCodeExtensions {
		command = append(command, "--install-extension", extension)
	}
	out, err := teamDevBoxExec(b.team, command...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hi: VS Code's extensions in the dev box: %s\n", qFirstLine(strings.TrimSpace(out), err.Error()))
		return
	}
	os.MkdirAll(filepath.Dir(marker), 0o700)
	os.WriteFile(marker, []byte(want), 0o600)
}

// shellQuoteAll quotes words for sh.
func shellQuoteAll(words []string) string {
	quoted := make([]string, len(words))
	for i, word := range words {
		quoted[i] = "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

// devCodeRoutes pass code/ to code-server, for owners and developers, and
// start it.
func (b *teamBroker) devCodeRoutes(mux *http.ServeMux, builder func(http.ResponseWriter, *http.Request) (teamPersonKey, bool),
	reply func(http.ResponseWriter, int, any), failure func(http.ResponseWriter, int, error)) {
	mux.HandleFunc("POST "+teamDevPagePrefix+"api/code/start", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		if err := b.devCodeStart(); err != nil {
			failure(w, http.StatusBadGateway, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{})
	})
	base := strings.TrimSuffix(teamDevPagePrefix, "/") + "/code"
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme, r.Out.URL.Host = "http", "code-server"
			r.Out.URL.Path = "/" + strings.TrimPrefix(strings.TrimPrefix(r.In.URL.Path, base), "/")
			r.Out.URL.RawPath = ""
			r.SetXForwarded()
			// code-server checks a WebSocket's Origin against the Host, and
			// NetBird's proxy needn't pass on the host the browser asked
			// for: hi checks the Origin as for the terminal, and gives
			// code-server the page's own.
			r.Out.Host = r.In.Host
			if origin, err := url.Parse(r.In.Header.Get("Origin")); err == nil && origin.Host != "" && teamDevSameOrigin(b.team, r.In) {
				r.Out.Host = origin.Host
				r.Out.Header.Set("X-Forwarded-Host", origin.Host)
			}
		},
		Transport: &http.Transport{DialContext: teamDevCodeDial(b.team)},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "VS Code isn't running in the dev box; open the VS Code view on the dev page again.", http.StatusBadGateway)
		},
	}
	serve := func(w http.ResponseWriter, r *http.Request) {
		if _, ok := builder(w, r); !ok {
			return
		}
		if r.URL.Path == base {
			http.Redirect(w, r, base+"/", http.StatusFound)
			return
		}
		proxy.ServeHTTP(w, r)
	}
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		mux.HandleFunc(method+" "+base+"/", serve)
	}
	mux.HandleFunc("GET "+base, serve)
}
