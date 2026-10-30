package main

// Google's Gemini API CLI (gemini-api) in an Antigravity box. agy's own
// Google sign-in doesn't reach the Gemini API (Google refuses its token
// there: ACCESS_TOKEN_SCOPE_INSUFFICIENT, 2026-10-30), so gemini-api takes
// an API key, which stays outside the box like Claude's token: the box's
// gemini-api has a placeholder and the proxy as its server (--server-url),
// and the proxy puts the key on each call. Only the calls that make and
// read things go through; the ones that would leave something running at
// Google after the box is gone (webhooks, triggers, credentials,
// environments, agent definitions) are refused.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// geminiAPIUpstream is the Gemini API.
	geminiAPIUpstream = "https://generativelanguage.googleapis.com"
	// geminiBoxEnv is the proxy's Gemini address in the box, for a URI
	// that a result gives on the Gemini API's own host.
	geminiBoxEnv = "HI_GEMINI_URL"
)

// geminiKeyPath is the Gemini API key, from hi agent token gemini.
func geminiKeyPath() string { return filepath.Join(qConfigDirectory(), "gemini-api-key") }

// geminiAllowed are the Gemini API paths a box may call: generation and
// agents' runs (interactions), files and their uploads, models, and the
// files those made. Agent definitions may be read.
var geminiAllowed = regexp.MustCompile(`^/(upload/)?v1[a-z0-9]*/(interactions|files|models|generatedFiles)([/:?]|$)`)
var geminiAgentsRead = regexp.MustCompile(`^/v1[a-z0-9]*/agents([/:?]|$)`)

func geminiPathAllowed(method, path string) bool {
	return geminiAllowed.MatchString(path) || method == http.MethodGet && geminiAgentsRead.MatchString(path)
}

type geminiBoxHostKey struct{}

// newBoxGeminiInjector passes the box's gemini-api calls on to the Gemini
// API with the key from keyFile, read again for each call, in place of
// whatever key the box sent. An upload's session address comes back as
// the proxy's, so the upload goes through it too.
func newBoxGeminiInjector(keyFile, upstream string, log *boxNetworkLog) http.Handler {
	target, _ := url.Parse(upstream)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Host = target.Host
		r.Header.Del("Authorization")
		if data, err := os.ReadFile(keyFile); err == nil {
			r.Header.Set("X-Goog-Api-Key", strings.TrimSpace(string(data)))
		} else {
			log.record("gemini-key-problem", err.Error())
		}
		log.record("gemini", r.Method+" "+r.URL.Path)
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		if session := response.Header.Get("X-Goog-Upload-Url"); session != "" {
			if host, ok := response.Request.Context().Value(geminiBoxHostKey{}).(string); ok {
				response.Header.Set("X-Goog-Upload-Url", geminiBoxURL(session, upstream, "http://"+host))
			}
		}
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !geminiPathAllowed(r.Method, r.URL.Path) {
			log.record("gemini-refused", r.Method+" "+r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"hi's proxy doesn't pass %s %s on: a box makes and reads things with gemini-api (interactions, files, models), but doesn't leave webhooks, triggers, credentials, environments, or agent definitions at Google"}}`, r.Method, r.URL.Path)
			return
		}
		proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), geminiBoxHostKey{}, r.Host)))
	})
}

// geminiBoxURL is a URI on the Gemini API's host as the box reaches it,
// through the proxy at base.
func geminiBoxURL(uri, upstream, base string) string {
	if rest, ok := strings.CutPrefix(uri, upstream); ok {
		return base + rest
	}
	return uri
}

// geminiReady says whether an agy box gets gemini-api: it's installed on
// this machine and there's a key. The path is the binary's.
func geminiReady() (string, bool) {
	binary, err := boxHostBinary("gemini-api")
	if err != nil || !fileExists(geminiKeyPath()) {
		return "", false
	}
	return binary, true
}

// geminiWrapper is gemini-api in the box: the real one, with the proxy as
// its server.
const geminiWrapper = `#!/bin/sh
exec /opt/gemini-api/gemini-api --server-url "$` + geminiBoxEnv + `" "$@"
`

// geminiSkill tells agy what gemini-api is for, as an Antigravity skill.
// Google suggests the numbered steps for any agent (aistudio.google.com/
// docs/cli); the rest is how it works in hi's box.
const geminiSkill = `---
name: gemini-api
description: Make video, speech, music, and images, transcribe audio or video, analyze PDFs, audio, video, and YouTube links, and run Google's deep research agent, with the gemini-api command. Use it when a task needs one of these; it is billed per call.
---

# gemini-api

You have access to gemini-api through the shell. Use it to call Gemini
models, generate or edit media, analyze files, or launch managed agents.

1. Inspect --help or --usage to choose a command, and --schema when building
   a --body JSON payload.
2. Preview API calls with --dry-run (or --dry-run --output-format json).
3. For discrete JSON output or --jq filtering on streaming commands
   (generate, agent run, agent status --stream), pass --stream=false
   --output-format json (--background on agent run returns a single
   interaction without streaming unless --stream is passed).
4. Capture returned file paths (from media commands) and interaction IDs
   (from agent/model runs) to continue multi-step tasks.

## What to use it for

| The task needs | Command |
|---|---|
| A video | ` + "`gemini-api video \"<prompt>\" --out <file>`" + ` (add --async for a long one, then poll) |
| Speech | ` + "`gemini-api tts --stdin --voice <name> --out <file>`" + ` |
| Music | ` + "`gemini-api music \"<prompt>\" --out <file>`" + ` |
| A transcript | ` + "`gemini-api transcribe -i <file> --format md|text|json|srt --out <file>`" + ` |
| Questions about PDFs, audio, video, images, or a YouTube link | ` + "`gemini-api analyze -i <file or URL> \"<question>\"`" + ` |
| Research across the web, written up | ` + "`gemini-api agent run \"<question>\" --agent deep-research-preview-04-2026 --background`" + `, then ` + "`gemini-api agent status <id>`" + ` |
| An image | your own image tool first: it is on the plan you run on; ` + "`gemini-api image`" + ` when the task asks for it, or to edit an image |

A file over 14 MB goes up first with ` + "`gemini-api files upload <file>`" + `;
uploads are deleted after 48 hours.

## In this box

- Every call is billed to the person's Gemini API key, by the call: video,
  music, and deep research cost the most. Make one when the task needs it,
  keep videos short unless the task says otherwise, and don't try
  variations nobody asked for.
- The key isn't in the box, and doesn't need to be: hi's proxy adds it.
  Don't configure a key, or sign in.
- A result that gives a URI on https://generativelanguage.googleapis.com
  instead of the file: fetch it through the proxy, with $` + geminiBoxEnv + ` in
  place of that host: ` + "`curl -fsSL -o <file> \"$" + geminiBoxEnv + "/v1beta/files/<id>:download?alt=media\"`" + `.
- Webhooks, triggers, credentials, environments, and agent definitions are
  refused here: nothing stays running at Google after the box.
- Say in your report which files you made, and what they cost if gemini-api
  said.
`

// geminiBoxSetup gives an agy box gemini-api, when this machine has it and
// a key: the binary read-only, the wrapper, the skill, and the proxy's
// address. It reports whether the box has it.
func geminiBoxSetup(meta boxMeta, homeDir string, run *[]string, env map[string]string) (bool, error) {
	binary, ok := geminiReady()
	if !ok {
		return false, nil
	}
	wrapper := boxStateFile(meta.Name, "gemini-api")
	if err := os.WriteFile(wrapper, []byte(geminiWrapper), 0o755); err != nil {
		return false, err
	}
	*run = append(*run, "-v", binary+":/opt/gemini-api/gemini-api:ro", "-v", wrapper+":/usr/local/bin/gemini-api:ro")
	skill := filepath.Join(homeDir, ".gemini", "antigravity-cli", "skills", "gemini-api")
	if err := os.MkdirAll(skill, 0o700); err != nil {
		return false, err
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(geminiSkill), 0o600); err != nil {
		return false, err
	}
	env["GEMINI_API_KEY"] = boxClaudePlacehold
	env["GEMINI_NO_KEYRING"] = "1"
	env[geminiBoxEnv] = fmt.Sprintf("http://%s:%d", meta.ProxyIP, boxInjectPort)
	return true, nil
}

// geminiTokenCommand is hi agent token gemini: it stores a Gemini API key
// from Google AI Studio for agy boxes' gemini-api.
func geminiTokenCommand(stdin io.Reader, stdout io.Writer) error {
	fmt.Fprintln(stdout, "Create a Gemini API key at https://aistudio.google.com/apikey and paste it. A Google AI Pro or Ultra plan's monthly Cloud credits can pay for it (developers.google.com/program/my-benefits).")
	key, err := readOptionalKey("Gemini API", stdin, stdout, false)
	if err != nil {
		return err
	}
	if len(key) < 20 || strings.ContainsAny(key, " \t") {
		return errors.New("that doesn't look like a Gemini API key from AI Studio")
	}
	if err := os.MkdirAll(qConfigDirectory(), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(geminiKeyPath(), []byte(key+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Stored in %s. Antigravity's boxes get gemini-api from the next run on; the key never enters a box.\n", geminiKeyPath())
	if _, err := boxHostBinary("gemini-api"); err != nil {
		fmt.Fprintln(stdout, "gemini-api isn't installed on this machine yet: hi install gemini-api.")
	}
	return nil
}
