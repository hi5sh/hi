package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// A model served on a team's machine with hi serve llm --on local --org is
// named to the org (hi_serve.md, The org knows): the device asks, an admin
// approves it in Slack, and devices then reach it through hi org ai as
// model local/<name>, signed as for everything else. The org server holds
// its key, so nobody hands one around, and its use is recorded with the
// rest of hi org ai's, at no cost.

const (
	serveUpstreamTimeout = 10 * time.Minute // a local model answers slower than a hosted one
	serveKeyPrefix       = "serve:"         // in keys.json
)

// netbirdRange is where NetBird gives addresses: a named server must be on
// one, so the org server is never sent to anything else on its networks.
var netbirdRange = func() *net.IPNet {
	_, network, _ := net.ParseCIDR("100.64.0.0/10")
	return network
}()

// serverServed is a local server named to the org; its key is in keys.json.
type serverServed struct {
	Name     string    `json:"name"`
	Model    string    `json:"model"`
	Recipe   string    `json:"recipe,omitempty"`
	Repo     string    `json:"repo,omitempty"`
	URL      string    `json:"url"`
	User     string    `json:"user"`
	Device   string    `json:"device"`
	Hostname string    `json:"hostname"`
	Request  string    `json:"request"`
	Approved bool      `json:"approved,omitempty"`
	Up       bool      `json:"up,omitempty"`
	Added    time.Time `json:"added"`
	Problem  string    `json:"problem,omitempty"`
}

func (s *orgServer) serveRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/serve", s.device(s.handleServeName))
	mux.HandleFunc("GET /v1/serve/{name}", s.device(s.handleServeGet))
	mux.HandleFunc("POST /v1/serve/{name}/state", s.device(s.handleServeState))
	mux.HandleFunc("DELETE /v1/serve/{name}", s.device(s.handleServeRemove))
}

// validServedURL accepts http://<NetBird address>:<port>/v1 only.
func validServedURL(address string) error {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "http" || parsed.Path != "/v1" || parsed.RawQuery != "" || parsed.User != nil {
		return fmt.Errorf("the address must be http://<NetBird address>:<port>/v1, not %q", address)
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !netbirdRange.Contains(ip) || parsed.Port() == "" {
		return fmt.Errorf("%s isn't a NetBird address with a port; the org server only reaches served models inside NetBird", parsed.Host)
	}
	return nil
}

func (s *orgServer) handleServeName(w http.ResponseWriter, _ *http.Request, device serverDevice, body []byte) {
	if s.isViewer(device.User) {
		writeAPIError(w, http.StatusForbidden, "a viewer can only watch")
		return
	}
	var input apiServeUpstream
	if err := json.Unmarshal(body, &input); err != nil {
		writeAPIError(w, http.StatusBadRequest, "malformed request")
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	switch {
	case !validInstanceName(input.Name):
		writeAPIError(w, http.StatusBadRequest, fmt.Sprintf("invalid name %q", input.Name))
		return
	case strings.TrimSpace(input.Model) == "" || len(input.Model) > 100 || len(input.Recipe) > 100 || len(input.Repo) > 200:
		writeAPIError(w, http.StatusBadRequest, "say which model it serves, briefly")
		return
	case len(input.Key) < 20 || len(input.Key) > 200:
		writeAPIError(w, http.StatusBadRequest, "a named server sends its key for the org server")
		return
	case input.Reason == "" || len(input.Reason) > 500:
		writeAPIError(w, http.StatusBadRequest, "a reason is required, at most 500 characters; approvers see it")
		return
	}
	if err := validServedURL(input.URL); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.Lock()
	if s.state.Served == nil {
		s.state.Served = map[string]*serverServed{}
	}
	existing, taken := s.state.Served[input.Name]
	if taken && existing.User != device.User {
		s.mu.Unlock()
		writeAPIError(w, http.StatusConflict, fmt.Sprintf("%s already serves a model named %s to the org; choose another --name", existing.User, input.Name))
		return
	}
	served := &serverServed{Name: input.Name, Model: input.Model, Recipe: input.Recipe, Repo: input.Repo, URL: input.URL,
		User: device.User, Device: device.Fingerprint, Hostname: device.Hostname, Up: input.Up, Added: computeNow()}
	reapproved := taken && existing.Approved && existing.Device == device.Fingerprint && existing.Model == input.Model
	if reapproved {
		// The same server, asked again: nothing new to approve.
		served.Approved, served.Request, served.Added = true, existing.Request, existing.Added
	} else {
		if taken {
			if old, ok := s.state.Requests[existing.Request]; ok && old.State == "pending" {
				old.State = "withdrawn"
				defer s.notifyRequest(old.ID)
			}
		}
		request := &serverRequest{
			ID: newServerID("s"), Kind: "serve", State: "pending", User: device.User, Device: device.Fingerprint,
			Hostname: device.Hostname, Created: computeNow(), Name: input.Name, Reason: input.Reason,
			Model: input.Model, Endpoint: input.URL,
		}
		s.state.Requests[request.ID] = request
		served.Request = request.ID
	}
	s.state.Served[input.Name] = served
	s.keys[serveKeyPrefix+input.Name] = strings.TrimSpace(input.Key)
	err := s.saveLocked()
	if err == nil {
		err = s.saveKeysLocked()
	}
	answer := s.servedAnswerLocked(served)
	s.mu.Unlock()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if reapproved {
		s.audit(device.User, "served again", orgModelID(input.Name), input.URL)
		writeJSON(w, http.StatusOK, answer)
		return
	}
	s.audit(device.User, "asked to serve", answer.Request, fmt.Sprintf("%s (%s) from %s at %s: %s",
		orgModelID(input.Name), input.Model, device.Hostname, input.URL, input.Reason))
	s.notifyRequest(answer.Request)
	writeJSON(w, http.StatusAccepted, answer)
}

// servedAnswerLocked is what a device sees of its named server.
func (s *orgServer) servedAnswerLocked(served *serverServed) apiServeUpstream {
	answer := apiServeUpstream{Name: served.Name, Model: served.Model, URL: served.URL, Up: served.Up,
		Request: served.Request, User: served.User, Host: served.Hostname, Problem: served.Problem, State: "approved"}
	if !served.Approved {
		answer.State = "pending"
		if request, ok := s.state.Requests[served.Request]; ok {
			answer.State = request.State
		}
	}
	return answer
}

// ownServed finds a named server of the device's user.
func (s *orgServer) ownServedLocked(name, user string) (*serverServed, bool) {
	served, ok := s.state.Served[name]
	return served, ok && served.User == user
}

func (s *orgServer) handleServeGet(w http.ResponseWriter, r *http.Request, device serverDevice, _ []byte) {
	s.mu.Lock()
	served, ok := s.ownServedLocked(r.PathValue("name"), device.User)
	var answer apiServeUpstream
	if ok {
		answer = s.servedAnswerLocked(served)
	}
	s.mu.Unlock()
	if !ok {
		writeAPIError(w, http.StatusNotFound, fmt.Sprintf("you serve nothing named %q to the org", r.PathValue("name")))
		return
	}
	writeJSON(w, http.StatusOK, answer)
}

func (s *orgServer) handleServeState(w http.ResponseWriter, r *http.Request, device serverDevice, body []byte) {
	var input apiServeUpstream
	if err := json.Unmarshal(body, &input); err != nil {
		writeAPIError(w, http.StatusBadRequest, "malformed request")
		return
	}
	if input.URL != "" {
		if err := validServedURL(input.URL); err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	s.mu.Lock()
	served, ok := s.ownServedLocked(r.PathValue("name"), device.User)
	if !ok {
		s.mu.Unlock()
		writeAPIError(w, http.StatusNotFound, fmt.Sprintf("you serve nothing named %q to the org", r.PathValue("name")))
		return
	}
	served.Up, served.Problem = input.Up, ""
	if input.URL != "" {
		served.URL = input.URL
	}
	err := s.saveLocked()
	answer := s.servedAnswerLocked(served)
	s.mu.Unlock()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(device.User, map[bool]string{true: "served up", false: "served down"}[input.Up], orgModelID(answer.Name), answer.URL)
	writeJSON(w, http.StatusOK, answer)
}

func (s *orgServer) handleServeRemove(w http.ResponseWriter, r *http.Request, device serverDevice, _ []byte) {
	s.mu.Lock()
	_, ok := s.ownServedLocked(r.PathValue("name"), device.User)
	s.mu.Unlock()
	if !ok {
		writeAPIError(w, http.StatusNotFound, fmt.Sprintf("you serve nothing named %q to the org", r.PathValue("name")))
		return
	}
	if err := s.removeServed(r.PathValue("name"), device.User); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

// removeServed forgets a named server and its key, and withdraws its
// request if it still waits.
func (s *orgServer) removeServed(name, actor string) error {
	s.mu.Lock()
	served, ok := s.state.Served[name]
	if !ok {
		s.mu.Unlock()
		return serverUsageError{fmt.Sprintf("nothing named %s is served to the org", name)}
	}
	pending := ""
	if request, ok := s.state.Requests[served.Request]; ok && request.State == "pending" {
		request.State, pending = "withdrawn", request.ID
	}
	delete(s.state.Served, name)
	delete(s.keys, serveKeyPrefix+name)
	err := s.saveLocked()
	if err == nil {
		err = s.saveKeysLocked()
	}
	s.mu.Unlock()
	if pending != "" {
		s.notifyRequest(pending)
	}
	s.audit(actor, "stopped serving", orgModelID(name), "")
	return err
}

// approveServeLocked names an approved server to the org. It is called
// with s.mu held, and unlocks it.
func (s *orgServer) approveServeLocked(request *serverRequest, actor string) (serverRequest, error) {
	served, ok := s.state.Served[request.Name]
	if !ok || served.Request != request.ID {
		request.State, request.Error = "failed", "it was removed before the decision"
		err := s.saveLocked()
		copy := *request
		s.mu.Unlock()
		s.notifyRequest(copy.ID)
		return copy, err
	}
	served.Approved = true
	request.State = "approved"
	err := s.saveLocked()
	copy := *request
	s.mu.Unlock()
	s.audit(actor, "approved serving", copy.ID, fmt.Sprintf("%s (%s) from %s", orgModelID(copy.Name), copy.Model, copy.Hostname))
	s.notifyRequest(copy.ID)
	go func() {
		text := fmt.Sprintf("Approved by %s. Devices reach it with hi q --model %s.", actor, orgModelID(copy.Name))
		if problem := s.checkServed(copy.Name); problem != "" {
			text += " The org server can't reach it now, though: " + problem
		}
		s.notifyThread(copy.ID, text)
	}()
	return copy, err
}

// forgetDeniedServeLocked drops a denied server's entry and key.
func (s *orgServer) forgetDeniedServeLocked(request *serverRequest) {
	if served, ok := s.state.Served[request.Name]; ok && served.Request == request.ID {
		delete(s.state.Served, request.Name)
		delete(s.keys, serveKeyPrefix+request.Name)
		s.saveKeysLocked()
	}
}

// servedUpstream is a named server's address and key, if devices may use it.
func (s *orgServer) servedUpstream(name string) (serverServed, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	served, ok := s.state.Served[name]
	switch {
	case !ok || !served.Approved:
		return serverServed{}, "", fmt.Errorf("no model %s is served to the org; hi org ai lists them", orgModelID(name))
	case !served.Up:
		return *served, "", fmt.Errorf("%s is stopped on %s; %s starts it again with hi serve start %s", orgModelID(name), served.Hostname, served.User, name)
	}
	return *served, s.keys[serveKeyPrefix+name], nil
}

// checkServed asks a named server for its models, and keeps what went
// wrong, or "".
func (s *orgServer) checkServed(name string) string {
	served, key, err := s.servedUpstream(name)
	if err != nil {
		return err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, served.URL+"/models", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	problem := ""
	response, err := http.DefaultClient.Do(request)
	switch {
	case err != nil:
		problem = err.Error()
	default:
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			problem = "it answered " + response.Status
		}
	}
	s.mu.Lock()
	if current, ok := s.state.Served[name]; ok {
		current.Problem = problem
		s.saveLocked()
	}
	s.mu.Unlock()
	return problem
}

// handleServedChat passes a chat completion for local/<name> to the
// machine that serves it, with its key, and says for whom.
func (s *orgServer) handleServedChat(w http.ResponseWriter, r *http.Request, device serverDevice, request map[string]any, model string) {
	name := strings.TrimPrefix(model, serveOrgPrefix)
	served, key, err := s.servedUpstream(name)
	if err != nil {
		writeAIError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	request["model"] = served.Model
	request["stream"] = false
	payload, _ := json.Marshal(request)
	ctx, cancel := context.WithTimeout(r.Context(), serveUpstreamTimeout)
	defer cancel()
	upstream, _ := http.NewRequestWithContext(ctx, http.MethodPost, served.URL+"/chat/completions", bytes.NewReader(payload))
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("Authorization", "Bearer "+key)
	upstream.Header.Set(serveUserHeader, device.User)
	response, err := http.DefaultClient.Do(upstream)
	if err != nil {
		s.recordAIUsage(device, model, nil, http.StatusBadGateway)
		writeAIError(w, http.StatusBadGateway, fmt.Sprintf("%s on %s isn't answering: %v", model, served.Hostname, err))
		return
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		writeAIError(w, http.StatusBadGateway, "the served model's answer could not be read")
		return
	}
	s.recordAIUsage(device, model, answer, response.StatusCode)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	w.Write(answer)
}

// servedModelList is the named servers devices may use, as OpenAI's model
// list entries.
func (s *orgServer) servedModelList() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var models []map[string]any
	for _, name := range sortedKeys(s.state.Served) {
		served := s.state.Served[name]
		if !served.Approved || !served.Up {
			continue
		}
		models = append(models, map[string]any{"id": orgModelID(name), "object": "model", "owned_by": served.User,
			"name":    fmt.Sprintf("%s, served on %s", served.Model, served.Hostname),
			"pricing": map[string]string{"prompt": "0", "completion": "0"}})
	}
	return models
}

// withServedModels adds the named servers to an upstream's model list.
func withServedModels(list []byte, served []map[string]any) []byte {
	if len(served) == 0 {
		return list
	}
	var parsed map[string]any
	if json.Unmarshal(list, &parsed) != nil || parsed == nil {
		parsed = map[string]any{"object": "list"}
	}
	data, _ := parsed["data"].([]any)
	for _, model := range served {
		data = append(data, model)
	}
	parsed["data"] = data
	out, _ := json.Marshal(parsed)
	return out
}

// servedList is every named server, for admins.
func (s *orgServer) servedList() []apiServeUpstream {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []apiServeUpstream
	for _, name := range sortedKeys(s.state.Served) {
		list = append(list, s.servedAnswerLocked(s.state.Served[name]))
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}

// describeServed is the served part of hi org ai.
func describeServed(list []apiServeUpstream) string {
	if len(list) == 0 {
		return ""
	}
	lines := []string{"Served on the team's machines (hi serve llm --org)"}
	for _, served := range list {
		state := "up"
		switch {
		case served.State != "approved":
			state = served.State + " (" + served.Request + ")"
		case !served.Up:
			state = "stopped"
		}
		line := fmt.Sprintf("- %s: %s on %s, by %s, %s", orgModelID(served.Name), served.Model, served.Host, served.User, state)
		if served.Problem != "" {
			line += "; last check: " + served.Problem
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
