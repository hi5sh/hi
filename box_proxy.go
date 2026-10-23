package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A box's only way out is this proxy, which hi runs in a second container
// on the box's internal network and on a normal one. It allows CONNECT
// tunnels (and plain HTTP) to the hosts in an allowlist file, which it
// rereads when it changes, and logs every decision. A second listener
// passes Claude Code's requests to api.anthropic.com and puts the real
// token in place of the box's placeholder, so the token never enters the
// box. A team lead's Slack tokens are swapped in the same way, inside the
// CONNECTs to Slack's API (box_slack.go).

const (
	boxProxyPort       = 3128
	boxInjectPort      = 3129
	boxDataPort        = 3130
	boxClaudePlacehold = "hi-box-placeholder"
)

// boxAllowlist is the set of hosts a box may reach. "*" allows everything.
type boxAllowlist struct {
	path    string
	mu      sync.Mutex
	modTime time.Time
	hosts   []string
	// log, when set, hears of each host added after the first read (hi
	// watch: a box's reach grew).
	log *boxNetworkLog
}

func (a *boxAllowlist) reload() {
	info, err := os.Stat(a.path)
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if info.ModTime().Equal(a.modTime) && a.hosts != nil {
		return
	}
	data, err := os.ReadFile(a.path)
	if err != nil {
		return
	}
	hosts := parseBoxAllowlist(string(data))
	if a.hosts != nil && a.log != nil {
		for _, host := range hosts {
			if !slices.Contains(a.hosts, host) {
				a.log.record("allowlist-added", host)
			}
		}
	}
	a.hosts = hosts
	a.modTime = info.ModTime()
}

func parseBoxAllowlist(text string) []string {
	hosts := []string{}
	for _, line := range strings.Split(text, "\n") {
		line, _, _ = strings.Cut(line, "#")
		if line = strings.ToLower(strings.TrimSpace(line)); line != "" {
			hosts = append(hosts, strings.TrimPrefix(strings.TrimPrefix(line, "*."), "."))
		}
	}
	return hosts
}

// allows matches a host and its subdomains against the list.
func (a *boxAllowlist) allows(host string) bool {
	a.reload()
	a.mu.Lock()
	defer a.mu.Unlock()
	return boxHostAllowed(a.hosts, host)
}

func boxHostAllowed(hosts []string, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, allowed := range hosts {
		if allowed == "*" || host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

// boxNetworkLog appends one line per decision.
type boxNetworkLog struct {
	mu   sync.Mutex
	file io.Writer
}

func (l *boxNetworkLog) record(decision, host string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.file, "%s %s %s\n", time.Now().UTC().Format(time.RFC3339), decision, host)
}

// runBoxProxy is `hi box __proxy`, run inside the proxy container.
func runBoxProxy(args []string, stderr io.Writer) int {
	if len(args) != 3 && len(args) != 5 {
		fmt.Fprintln(stderr, "usage: hi box __proxy <allowlist> <log> <claude credentials, openrouter:<.env>, or -> [<data token> <server's /hf URL>]")
		return 2
	}
	logFile, err := os.OpenFile(args[1], os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintf(stderr, "hi: %v\n", err)
		return 1
	}
	log := &boxNetworkLog{file: logFile}
	allow := &boxAllowlist{path: args[0], log: log}
	egress := &boxEgressProxy{allow: allow, log: log}
	if keys := os.Getenv(boxSlackKeysEnv); keys != "" {
		// A team's lead: its Slack tokens are swapped in here, if hi put
		// them in its keys.
		if egress.slack, err = loadBoxSlackSwap(keys, egress.dialer(), log); err != nil {
			fmt.Fprintf(stderr, "hi: the lead's Slack tokens can't be swapped in: %v\n", err)
			log.record("slack-problem", err.Error())
		}
	}
	errs := make(chan error, 2)
	go func() {
		errs <- http.ListenAndServe(fmt.Sprintf(":%d", boxProxyPort), egress)
	}()
	if env, ok := strings.CutPrefix(args[2], "openrouter:"); ok {
		var inject http.Handler = newBoxOpenRouterInjector(env, "https://openrouter.ai", log)
		if token := os.Getenv(boxChatGPTEnv); token != "" {
			// A team's roles on ChatGPT call its Codex endpoint here; the
			// rest is OpenRouter.
			inject = boxRouteChatGPT(newBoxChatGPTInjector(token, chatGPTBackend, log), inject)
		}
		go func() {
			errs <- http.ListenAndServe(fmt.Sprintf(":%d", boxInjectPort), inject)
		}()
	} else if args[2] != "-" {
		var inject http.Handler = newBoxClaudeInjector(args[2], "https://api.anthropic.com", log)
		if keys := os.Getenv(boxFallbackEnv); keys != "" {
			// A team's member: OpenRouter takes over when Claude refuses.
			inject = newBoxClaudeFailover(inject, &boxFallback{keys: keys, model: os.Getenv(boxFallbackModelEnv), role: os.Getenv(boxRoleEnv),
				events: os.Getenv(boxEventsEnv), upstream: "https://openrouter.ai", log: log})
		}
		go func() {
			errs <- http.ListenAndServe(fmt.Sprintf(":%d", boxInjectPort), inject)
		}()
	}
	if len(args) == 5 {
		go func() {
			errs <- http.ListenAndServe(fmt.Sprintf(":%d", boxDataPort), newBoxDataInjector(args[3], args[4], log))
		}()
	}
	fmt.Fprintf(stderr, "hi box proxy listening on :%d\n", boxProxyPort)
	err = <-errs
	fmt.Fprintf(stderr, "hi: %v\n", err)
	return 1
}

// boxEgressProxy is the CONNECT and plain-HTTP proxy.
type boxEgressProxy struct {
	allow *boxAllowlist
	log   *boxNetworkLog
	// dial is replaced in tests.
	dial func(ctx context.Context, network, address string) (net.Conn, error)
	// slack, for a team's lead, swaps its Slack tokens in (box_slack.go).
	slack *boxSlackSwap
}

func (p *boxEgressProxy) dialer() func(ctx context.Context, network, address string) (net.Conn, error) {
	if p.dial != nil {
		return p.dial
	}
	return (&net.Dialer{Timeout: 15 * time.Second}).DialContext
}

func (p *boxEgressProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Hostname()
	if r.Method == http.MethodConnect {
		host, _, _ = net.SplitHostPort(r.Host)
	}
	if host == "" || !p.allow.allows(host) {
		p.log.record("blocked", host)
		http.Error(w, fmt.Sprintf("hi box: %s is not allowed; on the host run: hi box allow <name> %s", host, host), http.StatusForbidden)
		return
	}
	p.log.record("allowed", host)
	if r.Method == http.MethodConnect && p.slack.intercepts(r.Host) {
		if client := p.hijack(w); client != nil {
			p.slack.serve(client, host)
		}
		return
	}
	if r.Method == http.MethodConnect {
		p.tunnel(w, r)
		return
	}
	// Plain HTTP: forward the request as it is.
	if r.URL.Scheme != "http" {
		http.Error(w, "hi box: only http:// requests and CONNECT tunnels", http.StatusBadRequest)
		return
	}
	transport := &http.Transport{DialContext: p.dialer(), Proxy: nil}
	r.RequestURI = ""
	started := time.Now()
	sent := &boxCounter{Reader: r.Body}
	if r.Body != nil {
		r.Body = io.NopCloser(sent)
	}
	response, err := transport.RoundTrip(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	for name, values := range response.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	received, _ := io.Copy(w, response.Body)
	p.log.closed(host, sent.n, received, time.Since(started))
}

// boxCounter counts what passes through.
type boxCounter struct {
	io.Reader
	n int64
}

func (c *boxCounter) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	c.n += int64(n)
	return n, err
}

// closed logs what a connection sent and received, after its decision
// line, for hi watch's rules on how much leaves a box.
func (l *boxNetworkLog) closed(host string, up, down int64, took time.Duration) {
	l.record("closed", fmt.Sprintf("%s %d %d %.1f", host, up, down, took.Seconds()))
}

func (p *boxEgressProxy) tunnel(w http.ResponseWriter, r *http.Request) {
	upstream, err := p.dialer()(r.Context(), "tcp", r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	client := p.hijack(w)
	if client == nil {
		upstream.Close()
		return
	}
	started := time.Now()
	host, _, _ := net.SplitHostPort(r.Host)
	up := make(chan int64, 1)
	go func() {
		n, _ := io.Copy(upstream, client)
		upstream.Close()
		up <- n
	}()
	down, _ := io.Copy(client, upstream)
	client.Close()
	p.log.closed(host, <-up, down, time.Since(started))
}

// hijack takes the CONNECT's connection and says it's established; nil
// when it can't.
func (p *boxEgressProxy) hijack(w http.ResponseWriter) net.Conn {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return nil
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		return nil
	}
	client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
	// Bytes the client sent after the CONNECT line are already buffered.
	if buffered.Reader.Buffered() > 0 {
		return &boxBufferedConn{Conn: client, reader: buffered.Reader}
	}
	return client
}

// newBoxClaudeInjector passes Claude Code's API requests on with the real
// token. secret is a file with a token from `claude setup-token`, or Claude
// Code's .credentials.json, read again for each request so a refresh on
// the host is picked up.
func newBoxClaudeInjector(secret, upstream string, log *boxNetworkLog) http.Handler {
	target, _ := url.Parse(upstream)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Host = target.Host
		// secret may name several files, the first that has a token winning:
		// a team's own token, then this machine's sign-in.
		var token string
		var err error
		for _, file := range strings.Split(secret, ",") {
			if token, err = readBoxClaudeToken(file); err == nil && token != "" {
				break
			}
		}
		if err != nil {
			// The request goes on with the placeholder and fails; the log
			// says why.
			log.record("claude-sign-in-problem", err.Error())
		}
		if auth := r.Header.Get("Authorization"); strings.Contains(auth, boxClaudePlacehold) && token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		r.Header.Del("X-Api-Key")
		log.record("claude", r.URL.Path)
	}
	return proxy
}

// newBoxOpenRouterInjector passes Hermes' requests on to OpenRouter with
// the real API key in place of the placeholder. env is Hermes' .env file,
// or several, comma-separated, the first with a key winning (a team's own,
// then Hermes'), mounted read-only into the proxy and read again for each
// request; only OPENROUTER_API_KEY is taken from them.
func newBoxOpenRouterInjector(env, upstream string, log *boxNetworkLog) http.Handler {
	target, _ := url.Parse(upstream)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Host = target.Host
		var key string
		var err error
		for _, file := range strings.Split(env, ",") {
			if key, err = readDotEnvValue(file, "OPENROUTER_API_KEY"); err == nil {
				break
			}
		}
		if err != nil {
			log.record("openrouter-key-problem", err.Error())
		}
		if strings.Contains(r.Header.Get("Authorization"), boxClaudePlacehold) {
			if key != "" {
				r.Header.Set("Authorization", "Bearer "+key)
			}
		} else {
			// The caller sent a key of its own, which goes on as it is:
			// OpenRouter refusing it is then not hi's key.
			log.record("openrouter-own-key", r.URL.Path)
		}
		log.record("openrouter", r.URL.Path)
	}
	// What each call cost, as a line of its own, so a team's watcher can
	// add up what the key spent through hi (team_watch.go).
	proxy.ModifyResponse = func(response *http.Response) error {
		response.Body = &teamCostReader{body: response.Body, done: func(cost float64) {
			if cost > 0 {
				log.record("spend", strconv.FormatFloat(cost, 'f', -1, 64))
			}
		}}
		return nil
	}
	return proxy
}

// boxRouteChatGPT sends calls under chatGPTPath to chatgpt, and the rest
// to other.
func boxRouteChatGPT(chatgpt, other http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, chatGPTPath) {
			chatgpt.ServeHTTP(w, r)
			return
		}
		other.ServeHTTP(w, r)
	})
}

// newBoxChatGPTInjector passes calls on to ChatGPT's Codex endpoint with
// the team's access token in place of the placeholder, and the account
// headers ChatGPT wants with it. tokenFile is written by the team's
// server, which alone refreshes the sign-in, and read again for each call;
// without it the placeholder goes on and ChatGPT refuses the call, so the
// role falls back. A refusal that sends a role to its fallback is logged
// as chatgpt-refused <status>:signin or :limit, which the server tells
// the channel about (team_subscriptions.go).
func newBoxChatGPTInjector(tokenFile, upstream string, log *boxNetworkLog) http.Handler {
	target, _ := url.Parse(upstream)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Host = target.Host
		var key struct {
			AccessToken string `json:"access_token"`
			AccountID   string `json:"account_id"`
		}
		if data, err := os.ReadFile(tokenFile); err != nil || json.Unmarshal(data, &key) != nil || key.AccessToken == "" {
			log.record("chatgpt-sign-in-problem", "no-token")
		} else if strings.Contains(r.Header.Get("Authorization"), boxClaudePlacehold) {
			for name, value := range chatGPTHeaders(key.AccessToken, key.AccountID) {
				r.Header.Set(name, value)
			}
		}
		// The harness names itself to OpenAI, as Hermes does on its own.
		r.Header.Set("originator", "hermes-agent")
		r.Header.Set("User-Agent", "HermesAgent (hi "+version_()+")")
		log.record("chatgpt", r.URL.Path)
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		// Only model calls count: Hermes also looks for paths ChatGPT
		// hasn't, such as a model list, which say nothing of the sign-in.
		if !strings.HasSuffix(response.Request.URL.Path, "/responses") {
			return nil
		}
		if kind := boxChatGPTRefusal(response); kind != "" {
			log.record("chatgpt-refused", fmt.Sprintf("%d:%s", response.StatusCode, kind))
		}
		return nil
	}
	return proxy
}

// boxChatGPTRefusal says what a refused call means for the role: "signin"
// when the sign-in doesn't work, "limit" when the plan's usage limit is
// reached, "other" for anything else that failed, and "" for success. A
// 429 is read for the plan's limit; a short rate limit is retried, not a
// reason to fall back.
func boxChatGPTRefusal(response *http.Response) string {
	switch {
	case response.StatusCode < 400:
		return ""
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return "signin"
	case response.StatusCode == http.StatusTooManyRequests:
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		response.Body.Close()
		response.Body = io.NopCloser(bytes.NewReader(body))
		if bytes.Contains(body, []byte("usage_limit")) || bytes.Contains(body, []byte("usage_not_included")) {
			return "limit"
		}
		return "other"
	}
	return "other"
}

// readDotEnvValue reads one KEY=value from a .env file.
func readDotEnvValue(path, key string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s can't be read: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if value != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("no %s in %s", key, path)
}

func readBoxClaudeToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("the Claude sign-in can't be read: %w", err)
	}
	text := strings.TrimSpace(string(data))
	if !strings.HasPrefix(text, "{") {
		if text == "" {
			return "", errors.New("the Claude token file is empty")
		}
		return text, nil
	}
	var credentials struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &credentials); err != nil || credentials.ClaudeAiOauth.AccessToken == "" {
		return "", errors.New("no Claude sign-in in the credentials file; run claude on the host and sign in")
	}
	if expires := credentials.ClaudeAiOauth.ExpiresAt; expires > 0 && time.Now().UnixMilli() > expires {
		return credentials.ClaudeAiOauth.AccessToken, errors.New("the host's Claude sign-in has expired; run claude once on the host, or store a long-lived token with hi agent token claude")
	}
	return credentials.ClaudeAiOauth.AccessToken, nil
}

// boxReadLines reads a small text file as lines, for logs.
func boxReadLines(path string, last int) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) > last {
		lines = lines[len(lines)-last:]
	}
	return lines
}
