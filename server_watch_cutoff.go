package main

// hi watch, release 4: the steps that work on a machine that is itself
// compromised, because they don't ask it. Revoke disables the team's
// OpenRouter key at OpenRouter, which the server can do when it made the
// key (with a provisioning key); a new key undoes it. Cut off takes the
// team's machine off the NetBird network, with a NetBird API token; reconnect
// undoes it. Both need an admin; nothing does them on its own.
//
// Spec: docs/specs/approved/hi_watch.md (release 4).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var openRouterKeysAPI = "https://openrouter.ai/api/v1/keys"

// watchSecrets are watch/keys.json: what the server acts with, and the
// new keys not yet delivered.
type watchSecrets struct {
	OpenRouter string            `json:"openrouter_provisioning,omitempty"`
	NetBird    string            `json:"netbird_token,omitempty"`
	NetBirdURL string            `json:"netbird_url,omitempty"`
	Pending    map[string]string `json:"pending,omitempty"` // team → a new key, until its machine has it
}

func (w *serverWatch) secretsPath() string { return filepath.Join(w.dir, "keys.json") }

func (w *serverWatch) loadSecretsLocked() watchSecrets {
	var secrets watchSecrets
	if data, err := os.ReadFile(w.secretsPath()); err == nil {
		json.Unmarshal(data, &secrets)
	}
	return secrets
}

func (w *serverWatch) saveSecretsLocked(secrets watchSecrets) error {
	os.MkdirAll(w.dir, 0o700)
	data, _ := json.MarshalIndent(secrets, "", "  ")
	return writeFileAtomic(w.secretsPath(), append(data, '\n'), 0o600)
}

// ---------------------------------------------------------------------------
// OpenRouter's provisioning API

type openRouterKey struct {
	Hash     string  `json:"hash"`
	Name     string  `json:"name"`
	Label    string  `json:"label"`
	Disabled bool    `json:"disabled"`
	Usage    float64 `json:"usage"`
}

func openRouterKeys(provisioning, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, _ := json.Marshal(in)
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, openRouterKeysAPI+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+provisioning)
	if in != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("OpenRouter: %w", err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode >= 300 {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.Unmarshal(data, &failure)
		message := firstNonEmpty(failure.Error.Message, strings.TrimSpace(string(data)), response.Status)
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			message += " (it must be a provisioning key, from openrouter.ai/settings/provisioning-keys)"
		}
		return fmt.Errorf("OpenRouter: %s", message)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// newTeamKey makes the team a key of its own; the old one, if the server
// made it, is deleted.
func (s *hiServer) newTeamKey(name string) (string, string, error) {
	w := s.watch
	w.mu.Lock()
	secrets := w.loadSecretsLocked()
	team := w.state.Teams[name]
	old, host := "", ""
	if team != nil {
		old, host = team.KeyHash, team.Hostname
	}
	w.mu.Unlock()
	if secrets.OpenRouter == "" {
		return "", "", errors.New("the hi server has no OpenRouter provisioning key: hi server watch --openrouter on the server")
	}
	var made struct {
		Data openRouterKey `json:"data"`
		Key  string        `json:"key"`
	}
	label := "hi team " + name
	if host != "" {
		label += " on " + host
	}
	if err := openRouterKeys(secrets.OpenRouter, http.MethodPost, "", map[string]any{"name": label}, &made); err != nil {
		return "", "", err
	}
	if made.Key == "" || made.Data.Hash == "" {
		return "", "", errors.New("OpenRouter made a key but didn't return it")
	}
	if old != "" && old != made.Data.Hash {
		if err := openRouterKeys(secrets.OpenRouter, http.MethodDelete, "/"+old, nil, nil); err != nil {
			fmt.Fprintf(s.log, "watch: deleting %s's old key: %v\n", name, err)
		}
	}
	return made.Key, made.Data.Hash, nil
}

// handleTeamKey is POST /v1/watch/key: a team's machine asks for a key of
// the server's making (hi team key <team> openrouter --from-server).
func (s *hiServer) handleTeamKey(w http.ResponseWriter, _ *http.Request, device serverDevice, body []byte) {
	var input struct {
		Team string `json:"team"`
	}
	if err := json.Unmarshal(body, &input); err != nil || !teamNamePattern.MatchString(input.Team) {
		writeAPIError(w, http.StatusBadRequest, "malformed request")
		return
	}
	s.watch.mu.Lock()
	team := s.watch.state.Teams[input.Team]
	switch {
	case team == nil:
		s.watch.mu.Unlock()
		writeAPIError(w, http.StatusForbidden, "the team sends no heartbeat yet: start it with hi team up, then ask again")
		return
	case team.Device != device.Fingerprint:
		s.watch.mu.Unlock()
		writeAPIError(w, http.StatusForbidden, "team "+input.Team+" is watched from another machine")
		return
	case team.KeyRevoked:
		s.watch.mu.Unlock()
		writeAPIError(w, http.StatusForbidden, "an admin revoked the team's key; a new one comes when an admin runs hi server watch rekey "+input.Team)
		return
	}
	s.watch.mu.Unlock()
	key, hash, err := s.newTeamKey(input.Team)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.watch.mu.Lock()
	if team := s.watch.state.Teams[input.Team]; team != nil {
		team.KeyHash, team.KeyRevoked = hash, false
		s.watch.saveLocked()
	}
	s.watch.mu.Unlock()
	s.audit(device.User, "made a key", input.Team, "OpenRouter key "+hash[:min(12, len(hash))]+" for "+device.Hostname)
	writeJSON(w, http.StatusOK, map[string]string{"key": key, "hash": hash})
}

// revokeTeamKey disables the team's key at OpenRouter.
func (s *hiServer) revokeTeamKey(name, by, because string) error {
	w := s.watch
	w.mu.Lock()
	team := w.state.Teams[name]
	secrets := w.loadSecretsLocked()
	if team == nil || team.KeyHash == "" {
		w.mu.Unlock()
		return fmt.Errorf("the hi server didn't make team %s's key, so it can't revoke it: rotate it at openrouter.ai, and from then on the team asks the server for its key (hi team key %s openrouter --from-server)", name, name)
	}
	hash := team.KeyHash
	w.mu.Unlock()
	if secrets.OpenRouter == "" {
		return errors.New("the hi server has no OpenRouter provisioning key any more: hi server watch --openrouter")
	}
	if err := openRouterKeys(secrets.OpenRouter, http.MethodPatch, "/"+hash, map[string]any{"disabled": true}, nil); err != nil {
		return err
	}
	w.mu.Lock()
	if team := w.state.Teams[name]; team != nil {
		team.KeyRevoked = true
		delete(secrets.Pending, name)
		w.saveSecretsLocked(secrets)
		w.saveLocked()
	}
	w.mu.Unlock()
	s.audit(by, "revoked the key", name, because)
	s.watchStopped(name, fmt.Sprintf(":no_entry: %s revoked team %s's OpenRouter key%s. Its model calls fail from now on, wherever they come from; hi server watch rekey %s gives it a new one.", by, name, watchBecause(because), name))
	return nil
}

// rekeyTeam makes the team a new key and gives it to its machine with
// the next heartbeat.
func (s *hiServer) rekeyTeam(name, by, because string) error {
	key, hash, err := s.newTeamKey(name)
	if err != nil {
		return err
	}
	w := s.watch
	w.mu.Lock()
	team := w.state.Teams[name]
	if team == nil {
		w.mu.Unlock()
		return fmt.Errorf("no team %s sends heartbeats", name)
	}
	secrets := w.loadSecretsLocked()
	if secrets.Pending == nil {
		secrets.Pending = map[string]string{}
	}
	secrets.Pending[name] = key
	team.KeyHash, team.KeyRevoked = hash, false
	err = w.saveSecretsLocked(secrets)
	w.saveLocked()
	w.mu.Unlock()
	if err != nil {
		return err
	}
	s.audit(by, "made a new key", name, because)
	s.watchPost("", fmt.Sprintf(":key: %s gave team %s a new OpenRouter key%s; its machine takes it with its next heartbeat.", by, name, watchBecause(because)), false)
	return nil
}

func watchBecause(because string) string {
	if because == "" {
		return ""
	}
	return ": " + because
}

// watchStopped posts in the watch channel and pushes the owners.
func (s *hiServer) watchStopped(name, text string) {
	s.watchPost("", text, false)
	w := s.watch
	w.mu.Lock()
	config := w.config
	w.mu.Unlock()
	if config.Ntfy != "" {
		go w.push(config, config.topic(name), "hi watch: "+name, strings.Trim(strings.SplitN(text, ". ", 2)[0], ":* "))
	}
}

// ---------------------------------------------------------------------------
// NetBird

// watchNetBird is the NetBird API, with the server's token; tests replace
// how it is made.
var watchNetBird = func(secrets watchSecrets) (netbirdAPI, error) {
	if secrets.NetBird == "" {
		return netbirdAPI{}, errors.New("the hi server has no NetBird API token: hi server watch --netbird on the server")
	}
	base := secrets.NetBirdURL
	if base == "" {
		var err error
		if base, err = netbirdAPIURL(); err != nil {
			return netbirdAPI{}, err
		}
	}
	return netbirdAPI{base: strings.TrimRight(base, "/"), token: secrets.NetBird, client: &http.Client{Timeout: 30 * time.Second}}, nil
}

type netbirdPeer struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	IP                   string `json:"ip"`
	SSHEnabled           bool   `json:"ssh_enabled"`
	LoginExpiration      bool   `json:"login_expiration_enabled"`
	InactivityExpiration bool   `json:"inactivity_expiration_enabled"`
	ApprovalRequired     bool   `json:"approval_required"`
	Groups               []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"groups"`
}

type netbirdGroup struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Peers []struct {
		ID string `json:"id"`
	} `json:"peers"`
	Resources []json.RawMessage `json:"resources"`
}

// watchCutOff is how a team's machine was cut off, to undo it.
type watchCutOff struct {
	Peer     string    `json:"peer"`
	Name     string    `json:"name"`
	Approval bool      `json:"approval,omitempty"` // by requiring approval again
	Groups   []string  `json:"groups,omitempty"`   // or by leaving these groups
	At       time.Time `json:"at"`
	Still    string    `json:"still,omitempty"` // what still lets it in
}

func (s *hiServer) teamPeer(api netbirdAPI, name string) (netbirdPeer, error) {
	s.watch.mu.Lock()
	team := s.watch.state.Teams[name]
	ip := ""
	if team != nil {
		ip = team.PeerIP
	}
	s.watch.mu.Unlock()
	if ip == "" {
		return netbirdPeer{}, fmt.Errorf("the hi server doesn't know team %s's machine's NetBird address yet: it learns it from a heartbeat", name)
	}
	var peers []netbirdPeer
	if err := api.call(http.MethodGet, "/api/peers?ip="+ip, nil, &peers); err != nil {
		return netbirdPeer{}, err
	}
	if len(peers) != 1 {
		return netbirdPeer{}, fmt.Errorf("NetBird knows no peer at %s", ip)
	}
	return peers[0], nil
}

// cutOffTeam takes the team's machine off the NetBird network: by asking
// for its approval again where the account approves peers, or else by
// taking it out of every group it is in, and says what still lets it in.
func (s *hiServer) cutOffTeam(name, by, because string) (watchCutOff, error) {
	s.watch.mu.Lock()
	secrets := s.watch.loadSecretsLocked()
	s.watch.mu.Unlock()
	api, err := watchNetBird(secrets)
	if err != nil {
		return watchCutOff{}, err
	}
	peer, err := s.teamPeer(api, name)
	if err != nil {
		return watchCutOff{}, err
	}
	cut := watchCutOff{Peer: peer.ID, Name: peer.Name, At: computeNow().UTC()}
	var account []struct {
		Settings struct {
			Extra struct {
				PeerApproval bool `json:"peer_approval_enabled"`
			} `json:"extra"`
		} `json:"settings"`
	}
	api.call(http.MethodGet, "/api/accounts", nil, &account)
	if len(account) == 1 && account[0].Settings.Extra.PeerApproval {
		update := map[string]any{"name": peer.Name, "ssh_enabled": peer.SSHEnabled, "login_expiration_enabled": peer.LoginExpiration,
			"inactivity_expiration_enabled": peer.InactivityExpiration, "approval_required": true}
		if err := api.call(http.MethodPut, "/api/peers/"+peer.ID, update, nil); err != nil {
			return watchCutOff{}, err
		}
		cut.Approval = true
	} else {
		for _, member := range peer.Groups {
			if member.Name == "All" {
				continue
			}
			var group netbirdGroup
			if err := api.call(http.MethodGet, "/api/groups/"+member.ID, nil, &group); err != nil {
				return cut, err
			}
			var peers []string
			for _, p := range group.Peers {
				if p.ID != peer.ID {
					peers = append(peers, p.ID)
				}
			}
			update := map[string]any{"name": group.Name, "peers": peers}
			if group.Resources != nil {
				update["resources"] = group.Resources
			}
			if err := api.call(http.MethodPut, "/api/groups/"+group.ID, update, nil); err != nil {
				return cut, err
			}
			cut.Groups = append(cut.Groups, group.ID)
		}
		cut.Still = s.netbirdStillAllows(api)
	}
	s.watch.mu.Lock()
	if team := s.watch.state.Teams[name]; team != nil {
		team.CutOff = &cut
		s.watch.saveLocked()
	}
	s.watch.mu.Unlock()
	how := "NetBird asks for its approval again"
	if !cut.Approval {
		how = fmt.Sprintf("it left its %d NetBird group(s)", len(cut.Groups))
	}
	s.audit(by, "cut off", name, how+watchBecause(because))
	text := fmt.Sprintf(":scissors: %s cut team %s's machine (%s) off the NetBird network: %s%s.", by, name, peer.Name, how, watchBecause(because))
	if cut.Still != "" {
		text += " " + cut.Still
	}
	s.watchStopped(name, text+" hi server watch reconnect "+name+" undoes it.")
	return cut, nil
}

// netbirdStillAllows says which enabled policies let every peer in, so a
// peer out of its groups still reaches what they cover.
func (s *hiServer) netbirdStillAllows(api netbirdAPI) string {
	var policies []struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		Rules   []struct {
			Sources []struct {
				Name string `json:"name"`
			} `json:"sources"`
			Destinations []struct {
				Name string `json:"name"`
			} `json:"destinations"`
		} `json:"rules"`
	}
	if err := api.call(http.MethodGet, "/api/policies", nil, &policies); err != nil {
		return "hi couldn't read the NetBird policies to say whether it still reaches anything."
	}
	var open []string
	for _, policy := range policies {
		if !policy.Enabled {
			continue
		}
		for _, rule := range policy.Rules {
			all := false
			for _, group := range append(rule.Sources, rule.Destinations...) {
				all = all || group.Name == "All"
			}
			if all {
				open = append(open, policy.Name)
				break
			}
		}
	}
	if len(open) == 0 {
		return ""
	}
	return fmt.Sprintf("Policy %s still lets every peer in, this one too: turn on peer approval in NetBird (Settings > Authentication), or narrow the policy, for a cut off that holds.", strings.Join(open, ", "))
}

// reconnectTeam undoes a cut off.
func (s *hiServer) reconnectTeam(name, by, because string) error {
	s.watch.mu.Lock()
	secrets := s.watch.loadSecretsLocked()
	team := s.watch.state.Teams[name]
	var cut *watchCutOff
	if team != nil {
		cut = team.CutOff
	}
	s.watch.mu.Unlock()
	if cut == nil {
		return fmt.Errorf("team %s isn't cut off", name)
	}
	api, err := watchNetBird(secrets)
	if err != nil {
		return err
	}
	if cut.Approval {
		var peer netbirdPeer
		if err := api.call(http.MethodGet, "/api/peers/"+cut.Peer, nil, &peer); err != nil {
			return err
		}
		update := map[string]any{"name": peer.Name, "ssh_enabled": peer.SSHEnabled, "login_expiration_enabled": peer.LoginExpiration,
			"inactivity_expiration_enabled": peer.InactivityExpiration, "approval_required": false}
		if err := api.call(http.MethodPut, "/api/peers/"+cut.Peer, update, nil); err != nil {
			return err
		}
	}
	for _, id := range cut.Groups {
		var group netbirdGroup
		if err := api.call(http.MethodGet, "/api/groups/"+id, nil, &group); err != nil {
			return err
		}
		peers := []string{cut.Peer}
		for _, p := range group.Peers {
			if p.ID != cut.Peer {
				peers = append(peers, p.ID)
			}
		}
		update := map[string]any{"name": group.Name, "peers": peers}
		if group.Resources != nil {
			update["resources"] = group.Resources
		}
		if err := api.call(http.MethodPut, "/api/groups/"+id, update, nil); err != nil {
			return err
		}
	}
	s.watch.mu.Lock()
	if team := s.watch.state.Teams[name]; team != nil {
		team.CutOff = nil
		s.watch.saveLocked()
	}
	s.watch.mu.Unlock()
	s.audit(by, "reconnected", name, because)
	s.watchPost("", fmt.Sprintf(":link: %s put team %s's machine back on the NetBird network%s.", by, name, watchBecause(because)), false)
	return nil
}

// notePeer keeps the NetBird address a team's heartbeats come from.
func (w *serverWatch) notePeer(team string, remote string) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if t := w.state.Teams[team]; t != nil && t.PeerIP != host {
		t.PeerIP = host
		w.saveLocked()
	}
}

// ---------------------------------------------------------------------------
// the admins' commands

func (s *hiServer) watchCutoffRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/watch/act/{team}/{action}", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Because string `json:"because"`
			As      string `json:"as"`
		}
		json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&input)
		err := s.watchAct(r.PathValue("team"), r.PathValue("action"), firstNonEmpty(input.As, currentUserName()), input.Because)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /admin/watch/secret/{which}", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Value string `json:"value"`
			URL   string `json:"url"`
		}
		json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&input)
		value := strings.TrimSpace(input.Value)
		var err error
		switch r.PathValue("which") {
		case "openrouter":
			if value != "off" {
				err = openRouterKeys(value, http.MethodGet, "", nil, nil)
			}
		case "netbird":
			if value != "off" {
				var api netbirdAPI
				if api, err = watchNetBird(watchSecrets{NetBird: value, NetBirdURL: input.URL}); err == nil {
					err = api.call(http.MethodGet, "/api/peers?ip=0.0.0.0", nil, &[]netbirdPeer{})
				}
			}
		default:
			err = errors.New("openrouter or netbird")
		}
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.watch.mu.Lock()
		secrets := s.watch.loadSecretsLocked()
		if value == "off" {
			value = ""
		}
		if r.PathValue("which") == "openrouter" {
			secrets.OpenRouter = value
		} else {
			secrets.NetBird, secrets.NetBirdURL = value, input.URL
		}
		err = s.watch.saveSecretsLocked(secrets)
		s.watch.mu.Unlock()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.audit(currentUserName(), "set the "+r.PathValue("which")+" key", "watch", "")
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
}

// watchAct is every admin's step on a team: freeze, unfreeze, pause,
// unpause, revoke, rekey, cutoff, reconnect.
func (s *hiServer) watchAct(team, action, by, because string) error {
	switch action {
	case "freeze", "unfreeze", "pause", "unpause":
		_, err := s.stopTeam(team, action, by, because)
		return err
	case "revoke":
		return s.revokeTeamKey(team, by, because)
	case "rekey":
		return s.rekeyTeam(team, by, because)
	case "cutoff":
		_, err := s.cutOffTeam(team, by, because)
		return err
	case "reconnect":
		return s.reconnectTeam(team, by, because)
	}
	return fmt.Errorf("%q isn't freeze, unfreeze, pause, unpause, revoke, rekey, cutoff, or reconnect", action)
}

// apiWatchKey is a new key for a team, in a heartbeat's answer.
type apiWatchKey struct {
	Key  string `json:"key"`
	Hash string `json:"hash"`
}
