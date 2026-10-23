package main

// A team member's calls to its subscription fall back to OpenRouter when
// the subscription refuses them (docs/specs/approved/hi_team_subscriptions.md).
// Claude Code has no fallback of its own, so the member's proxy resends the
// same call to OpenRouter's Anthropic-compatible endpoint, with the model's
// OpenRouter name and the team's OpenRouter key, and keeps to OpenRouter
// for a while before it tries the subscription again. Each fallback is
// written to the team's events file, from which the team's server tells
// the channel, once per role.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// boxFallbackEnv names the .env files with the OpenRouter key, the
	// first with a key winning; set, the proxy falls back.
	boxFallbackEnv = "HI_BOX_FALLBACK"
	// boxFallbackModelEnv is a member's own fallback model, for every
	// call; unset, each call's model gets its OpenRouter name.
	boxFallbackModelEnv = "HI_BOX_FALLBACK_MODEL"
	boxRoleEnv          = "HI_BOX_ROLE"
	// boxEventsEnv is the file the proxy writes fallbacks and their spend
	// to, in the team's run folder.
	boxEventsEnv = "HI_BOX_EVENTS"
	// boxFallbackFor is how long the proxy keeps to OpenRouter after the
	// subscription refused, before it tries the subscription again.
	boxFallbackFor = 15 * time.Minute
)

type boxFallback struct {
	keys, model, role, events, upstream string
	log                                 *boxNetworkLog

	mu    sync.Mutex
	until time.Time
	now   func() time.Time // tests set it
}

func (f *boxFallback) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

func (f *boxFallback) active() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clock().Before(f.until)
}

func (f *boxFallback) activate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.until = f.clock().Add(boxFallbackFor)
}

// event appends a line to the team's events file: "<time> fallback <role>
// claude <reason> <model> [nokey]", or "<time> spend <usd>".
func (f *boxFallback) event(fields ...string) {
	if f.events == "" {
		return
	}
	file, err := os.OpenFile(f.events, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintf(file, "%s %s\n", f.clock().UTC().Format(time.RFC3339), strings.Join(fields, " "))
}

func (f *boxFallback) key() string {
	for _, file := range strings.Split(f.keys, ",") {
		if key, err := readDotEnvValue(file, "OPENROUTER_API_KEY"); err == nil && key != "" {
			return key
		}
	}
	return ""
}

var (
	claudeDateSuffix = regexp.MustCompile(`-\d{8}$`)
	claudeVersioned  = regexp.MustCompile(`^claude-([a-z]+)-(\d+)-(\d+)$`)
)

// claudeOpenRouterModel is a Claude model's name on OpenRouter:
// claude-opus-5-5 is anthropic/claude-opus-5.5, claude-haiku-4-5-20251001
// is anthropic/claude-haiku-4.5, and claude-opus-5 is anthropic/claude-opus-5.
func claudeOpenRouterModel(model string) string {
	if model == "" || strings.Contains(model, "/") {
		return model
	}
	if i := strings.Index(model, "["); i > 0 {
		model = model[:i]
	}
	model = claudeDateSuffix.ReplaceAllString(model, "")
	if parts := claudeVersioned.FindStringSubmatch(model); parts != nil {
		return "anthropic/claude-" + parts[1] + "-" + parts[2] + "." + parts[3]
	}
	return "anthropic/" + model
}

// claudeRefusal says what a refused call means for the role: "signin" when
// the sign-in doesn't work, "limit" when the plan's limit is reached
// (rejected for the plan's window, or a wait of more than a minute), and
// "" for anything Claude Code should see and retry itself.
func claudeRefusal(status int, header http.Header) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "signin"
	case status == http.StatusTooManyRequests:
		if strings.EqualFold(header.Get("anthropic-ratelimit-unified-status"), "rejected") {
			return "limit"
		}
		if seconds, err := strconv.Atoi(header.Get("retry-after")); err == nil && seconds > 60 {
			return "limit"
		}
	}
	return ""
}

// boxHeldResponse lets a response through as it comes unless it is a
// refusal that sends the role to its fallback; that one is held back.
type boxHeldResponse struct {
	w      http.ResponseWriter
	header http.Header
	status int
	reason string
	held   bytes.Buffer
}

func (h *boxHeldResponse) Header() http.Header { return h.header }

func (h *boxHeldResponse) WriteHeader(status int) {
	if h.status != 0 {
		return
	}
	h.status = status
	if h.reason = claudeRefusal(status, h.header); h.reason != "" {
		return
	}
	for key, values := range h.header {
		h.w.Header()[key] = values
	}
	h.w.WriteHeader(status)
}

func (h *boxHeldResponse) Write(data []byte) (int, error) {
	if h.status == 0 {
		h.WriteHeader(http.StatusOK)
	}
	if h.reason != "" {
		return h.held.Write(data)
	}
	return h.w.Write(data)
}

func (h *boxHeldResponse) Flush() {
	if flusher, ok := h.w.(http.Flusher); ok && h.reason == "" && h.status != 0 {
		flusher.Flush()
	}
}

// release passes a held refusal on as it was.
func (h *boxHeldResponse) release() {
	for key, values := range h.header {
		h.w.Header()[key] = values
	}
	h.w.WriteHeader(h.status)
	h.w.Write(h.held.Bytes())
}

// newBoxClaudeFailover tries primary, the subscription, and on a refusal
// sends the same call to OpenRouter.
func newBoxClaudeFailover(primary http.Handler, fallback *boxFallback) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 256<<20))
		r.Body.Close()
		if err != nil {
			http.Error(w, "hi: the call couldn't be read", http.StatusBadGateway)
			return
		}
		if fallback.active() {
			fallback.serve(w, r, body, nil)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		held := &boxHeldResponse{w: w, header: http.Header{}}
		primary.ServeHTTP(held, r)
		if held.reason == "" {
			return
		}
		fallback.log.record("claude-refused", fmt.Sprintf("%d:%s", held.status, held.reason))
		fallback.activate()
		fallback.serve(w, r, body, held)
	})
}

// serve sends the call to OpenRouter. refused is the subscription's
// refusal, when this call had one: it goes back as it was if there is no
// OpenRouter key to fall back on.
func (f *boxFallback) serve(w http.ResponseWriter, r *http.Request, body []byte, refused *boxHeldResponse) {
	var fields map[string]json.RawMessage
	model := ""
	if json.Unmarshal(body, &fields) == nil {
		json.Unmarshal(fields["model"], &model)
	}
	target := firstNonEmpty(f.model, claudeOpenRouterModel(model))
	key := f.key()
	if refused != nil {
		event := []string{"fallback", firstNonEmpty(f.role, "member"), "claude", refused.reason, firstNonEmpty(target, "-")}
		if key == "" {
			event = append(event, "nokey")
		}
		f.event(event...)
	}
	if key == "" {
		f.log.record("openrouter-key-problem", "no key to fall back on")
		if refused != nil {
			refused.release()
		} else {
			http.Error(w, "hi: the subscription refused, and the team has no OpenRouter key to fall back on", http.StatusServiceUnavailable)
		}
		return
	}
	if fields != nil && target != "" {
		fields["model"], _ = json.Marshal(target)
		body, _ = json.Marshal(fields)
	}
	request, _ := http.NewRequestWithContext(r.Context(), r.Method, f.upstream+"/api"+r.URL.Path, bytes.NewReader(body))
	request.URL.RawQuery = r.URL.RawQuery
	for name, values := range r.Header {
		switch strings.ToLower(name) {
		case "authorization", "x-api-key", "host", "content-length", "accept-encoding", "connection":
		case "anthropic-beta":
			// The subscription's own beta flag means nothing to OpenRouter.
			var kept []string
			for _, value := range values {
				for _, flag := range strings.Split(value, ",") {
					if flag = strings.TrimSpace(flag); flag != "" && !strings.HasPrefix(flag, "oauth-") {
						kept = append(kept, flag)
					}
				}
			}
			if len(kept) > 0 {
				request.Header.Set(name, strings.Join(kept, ","))
			}
		default:
			request.Header[name] = values
		}
	}
	request.Header.Set("Authorization", "Bearer "+key)
	f.log.record("openrouter", request.URL.Path)
	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		http.Error(w, "hi: OpenRouter can't be reached: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	for name, values := range response.Header {
		w.Header()[name] = values
	}
	w.WriteHeader(response.StatusCode)
	reader := &teamCostReader{body: response.Body, done: func(cost float64) {
		if cost > 0 {
			f.log.record("spend", strconv.FormatFloat(cost, 'f', -1, 64))
			f.event("spend", strconv.FormatFloat(cost, 'f', -1, 64))
		}
	}}
	defer reader.Close()
	buffer := make([]byte, 32<<10)
	flusher, _ := w.(http.Flusher)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			w.Write(buffer[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}
