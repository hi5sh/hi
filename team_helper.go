package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The helper puts a chat with the lead into every page of a team's app:
// hi net expose adds its script to the pages it relays and sends /__hi/ to
// the team's service, on a socket per address. The lead answers through
// Hermes' API server, in a conversation of its own per web chat, and hi
// copies each chat into a thread in the channel, the team's record
// (docs/specs/approved/hi_team_helper.md).

//go:embed helper/helper.js
var teamHelperScript []byte

const (
	teamHelperPrefix = "/__hi/"
	// teamLeadAPIPort is Hermes' API server in the lead's box; hi team
	// __leadapi relays a socket the host reaches to it.
	teamLeadAPIPort   = 8642
	teamLeadAPISocket = "lead-api.sock"
	// teamWebMaxText is the longest message the helper takes.
	teamWebMaxText = 8000
	// teamLeadTimeout is how long the lead may take over one answer: it may
	// look at the project or start a task first.
	teamLeadTimeout = 20 * time.Minute
)

// teamHelperVersion keeps a browser's copy of the script until the script
// changes.
func teamHelperVersion() string {
	return fmt.Sprintf("%x", sha256.Sum256(teamHelperScript))[:12]
}

// teamHelperOn says whether a slot's address has the helper: on unless
// team.json turns it off.
func teamHelperOn(config teamConfig, slot string) bool {
	on, set := config.Helper[slot]
	return !set || on
}

// teamHelperFile exists while the slot's helper is on; the slot's
// exposure reads it on every page (--inject-if).
func teamHelperFile(team, slot string) string {
	return filepath.Join(teamDir(team), "run", slot+".helper")
}

// teamSyncHelper puts the helper files in line with team.json.
func teamSyncHelper(config teamConfig) error {
	if err := os.MkdirAll(filepath.Join(teamDir(config.Name), "run"), 0o700); err != nil {
		return err
	}
	for _, slot := range []string{teamSlotPreview, teamSlotProduction} {
		path := teamHelperFile(config.Name, slot)
		if teamHelperOn(config, slot) {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				return err
			}
		} else if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// teamHelperLine is the helper's state, for hi team status and the lead.
func teamHelperLine(config teamConfig) string {
	state := func(slot string) string {
		if teamHelperOn(config, slot) {
			return "on"
		}
		return "off"
	}
	preview, production := state(teamSlotPreview), state(teamSlotProduction)
	if preview == production {
		return fmt.Sprintf("The helper (a chat with the lead in the app): %s in preview and production.", preview)
	}
	return fmt.Sprintf("The helper (a chat with the lead in the app): %s in preview, %s in production.", preview, production)
}

// teamWebDir holds the sockets the exposures send /__hi/ to. Like the
// host's socket, no box mounts it.
func teamWebDir(team string) string { return teamBrokerDir(team) + "-web" }

func teamWebSocket(team, slot string) string {
	return filepath.Join(teamWebDir(team), slot+".sock")
}

// teamExposeHelperArgs are what a slot's hi net expose gets for the helper.
func teamExposeHelperArgs(team, slot string) []string {
	return []string{"--route", teamHelperPrefix + "=unix:" + teamWebSocket(team, slot),
		"--inject", teamHelperPrefix + "helper.js?v=" + teamHelperVersion(),
		"--inject-if", teamHelperFile(team, slot)}
}

// ---------------------------------------------------------------------------
// web conversations, in web/

// teamWebConversation is web/<id>.json: one chat in the app.
type teamWebConversation struct {
	ID       string           `json:"id"` // w-12
	Slot     string           `json:"slot"`
	Person   string           `json:"person,omitempty"` // a Slack member ID, as the person said; "" for a name only
	Name     string           `json:"name"`
	Started  time.Time        `json:"started"`
	Ended    bool             `json:"ended,omitempty"`  // the person started a new one
	Thread   string           `json:"thread,omitempty"` // its thread in the channel
	Messages []teamWebMessage `json:"messages"`
}

// teamWebMessage is one message of a web chat. Its ID is the
// conversation's and its number: w-12.3.
type teamWebMessage struct {
	N       int          `json:"n"`
	From    string       `json:"from"` // person or lead
	Name    string       `json:"name"`
	Text    string       `json:"text"`
	Page    *teamWebPage `json:"page,omitempty"`
	At      time.Time    `json:"at"`
	SlackTS string       `json:"slack_ts,omitempty"` // its copy in the channel
}

// teamWebPage is where the person was when they wrote.
type teamWebPage struct {
	Path   string `json:"path"`
	Title  string `json:"title,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

var (
	teamWebConversationID = regexp.MustCompile(`^w-([0-9]{1,6})$`)
	teamWebMessageID      = regexp.MustCompile(`^(w-[0-9]{1,6})\.([0-9]{1,4})$`)
)

func teamWebStore(team string) string { return filepath.Join(teamDir(team), "web") }

func loadTeamWebConversation(team, id string) (teamWebConversation, error) {
	var conversation teamWebConversation
	if !teamWebConversationID.MatchString(id) {
		return conversation, fmt.Errorf("a web chat is named like w-3, not %q", id)
	}
	data, err := os.ReadFile(filepath.Join(teamWebStore(team), id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return conversation, fmt.Errorf("no web chat %s", id)
	}
	if err != nil {
		return conversation, err
	}
	return conversation, json.Unmarshal(data, &conversation)
}

func saveTeamWebConversation(team string, conversation teamWebConversation) error {
	if err := os.MkdirAll(teamWebStore(team), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(conversation, "", "  ")
	return writeFileAtomic(filepath.Join(teamWebStore(team), conversation.ID+".json"), append(data, '\n'), 0o600)
}

// loadTeamWebConversations lists a team's web chats, oldest first.
func loadTeamWebConversations(team string) []teamWebConversation {
	entries, _ := os.ReadDir(teamWebStore(team))
	var all []teamWebConversation
	for _, entry := range entries {
		if id, ok := strings.CutSuffix(entry.Name(), ".json"); ok {
			if conversation, err := loadTeamWebConversation(team, id); err == nil {
				all = append(all, conversation)
			}
		}
	}
	sort.Slice(all, func(i, j int) bool { return teamWebNumber(all[i].ID) < teamWebNumber(all[j].ID) })
	return all
}

func teamWebNumber(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "w-"))
	return n
}

// teamWebMirrored maps each message hi copied into the channel to where it
// came from, so the channel's log can say so.
func teamWebMirrored(team string) map[string]teamChannelWeb {
	mirrored := map[string]teamChannelWeb{}
	for _, conversation := range loadTeamWebConversations(team) {
		for _, message := range conversation.Messages {
			if message.SlackTS != "" {
				mirrored[message.SlackTS] = teamChannelWeb{Conversation: conversation.ID, Person: conversation.Person, From: message.From}
			}
		}
	}
	return mirrored
}

// ---------------------------------------------------------------------------
// the helper's API, on the web sockets

// teamWebPerson is someone the helper offers in "Who are you?".
type teamWebPerson struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// teamWebStatus is GET /__hi/api/status.
type teamWebStatus struct {
	Team    string          `json:"team"`
	Lead    string          `json:"lead"`
	Slot    string          `json:"slot"`
	Version string          `json:"version,omitempty"`
	Commit  string          `json:"commit,omitempty"`
	People  []teamWebPerson `json:"people"`
	// Anyone says whether people outside the list may write: the team has
	// no askers, so anyone in the channel may ask for work.
	Anyone bool `json:"anyone"`
}

// teamWebSend is POST /__hi/api/messages.
type teamWebSend struct {
	Conversation string       `json:"conversation,omitempty"`
	Person       string       `json:"person,omitempty"`
	Name         string       `json:"name"`
	Text         string       `json:"text"`
	Page         *teamWebPage `json:"page,omitempty"`
}

// listenWeb opens a socket per address for the helper.
func (b *teamBroker) listenWeb(ctx context.Context) ([]func() error, error) {
	if err := os.MkdirAll(teamWebDir(b.team), 0o700); err != nil {
		return nil, err
	}
	var serves []func() error
	for _, slot := range []string{teamSlotPreview, teamSlotProduction} {
		serve, err := b.listenWith(ctx, teamWebSocket(b.team, slot), b.webHandler(slot))
		if err != nil {
			return nil, err
		}
		serves = append(serves, serve)
	}
	return serves, nil
}

func (b *teamBroker) webHandler(slot string) http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(value)
	}
	failure := func(w http.ResponseWriter, status int, err error) {
		reply(w, status, map[string]string{"error": err.Error()})
	}
	mux.HandleFunc("GET "+teamHelperPrefix+"helper.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(teamHelperScript)
	})
	mux.HandleFunc("GET "+teamHelperPrefix+"api/status", func(w http.ResponseWriter, r *http.Request) {
		config, err := loadTeam(b.team)
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		status := teamWebStatus{Team: b.team, Lead: teamBotName(b.team) + " team lead", Slot: slot, People: b.webPeople(config), Anyone: len(config.Askers) == 0}
		if state, ok := loadTeamSlot(b.team, slot); ok {
			status.Version, status.Commit = state.Label, state.Commit
		}
		reply(w, http.StatusOK, status)
	})
	mux.HandleFunc("GET "+teamHelperPrefix+"api/conversation", func(w http.ResponseWriter, r *http.Request) {
		person, name := r.URL.Query().Get("person"), r.URL.Query().Get("name")
		b.webMu.Lock()
		conversation, ok := b.openConversation(slot, person, name)
		b.webMu.Unlock()
		if !ok {
			reply(w, http.StatusOK, map[string]any{})
			return
		}
		reply(w, http.StatusOK, conversation)
	})
	mux.HandleFunc("POST "+teamHelperPrefix+"api/conversation/new", func(w http.ResponseWriter, r *http.Request) {
		var send teamWebSend
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&send); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		b.webMu.Lock()
		defer b.webMu.Unlock()
		if conversation, ok := b.openConversation(slot, send.Person, send.Name); ok {
			if b.answering[conversation.ID] {
				failure(w, http.StatusConflict, errors.New("the lead is still answering; start a new chat when it's done"))
				return
			}
			conversation.Ended = true
			if err := saveTeamWebConversation(b.team, conversation); err != nil {
				failure(w, http.StatusInternalServerError, err)
				return
			}
		}
		reply(w, http.StatusOK, map[string]any{})
	})
	mux.HandleFunc("POST "+teamHelperPrefix+"api/messages", func(w http.ResponseWriter, r *http.Request) {
		var send teamWebSend
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&send); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		b.webSend(w, r, slot, send)
	})
	return mux
}

// webPeople are the people the helper offers: the owners and askers, or
// with no askers, the owners and everyone heard in the channel lately.
func (b *teamBroker) webPeople(config teamConfig) []teamWebPerson {
	names := map[string]string{}
	var order []string
	if messages, err := readTeamChannel(teamChannelDir(b.team), teamSlackTS(time.Now().Add(-teamChannelBack))); err == nil {
		for _, message := range messages {
			if message.User != "" && !teamChannelFromBot(config, message) {
				if _, seen := names[message.User]; !seen {
					order = append(order, message.User)
				}
				names[message.User] = message.Name
			}
		}
	}
	listed := append(append([]string{}, config.Owners...), config.Askers...)
	if len(config.Askers) == 0 {
		listed = append(listed, order...)
	}
	var people []teamWebPerson
	seen := map[string]bool{}
	for _, id := range listed {
		if seen[id] {
			continue
		}
		seen[id] = true
		name := names[id]
		if name == "" && b.channel != nil {
			name = b.channel.userName(id)
		}
		people = append(people, teamWebPerson{ID: id, Name: firstNonEmpty(name, id)})
	}
	sort.SliceStable(people, func(i, j int) bool { return strings.ToLower(people[i].Name) < strings.ToLower(people[j].Name) })
	return people
}

// openConversation is this person's chat in this slot that they haven't
// ended. The caller holds webMu.
func (b *teamBroker) openConversation(slot, person, name string) (teamWebConversation, bool) {
	all := loadTeamWebConversations(b.team)
	for i := len(all) - 1; i >= 0; i-- {
		conversation := all[i]
		if conversation.Slot != slot || conversation.Ended {
			continue
		}
		if (person != "" && conversation.Person == person) || (person == "" && conversation.Person == "" && name != "" && conversation.Name == name) {
			return conversation, true
		}
	}
	return teamWebConversation{}, false
}

// webSend takes a person's message, has the lead answer it, and streams the
// answer back as server-sent events: "message" for each saved message,
// "delta" for the lead's text as it comes, then "done" or "error". The
// answer is kept and copied to the channel even when the browser goes.
func (b *teamBroker) webSend(w http.ResponseWriter, r *http.Request, slot string, send teamWebSend) {
	fail := func(status int, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
	}
	config, err := loadTeam(b.team)
	if err != nil {
		fail(http.StatusInternalServerError, err)
		return
	}
	send.Text, send.Name = strings.TrimSpace(send.Text), strings.TrimSpace(send.Name)
	switch {
	case !teamHelperOn(config, slot):
		fail(http.StatusForbidden, errors.New("the helper is off here"))
		return
	case send.Text == "":
		fail(http.StatusBadRequest, errors.New("the message is empty"))
		return
	case len(send.Text) > teamWebMaxText:
		fail(http.StatusBadRequest, fmt.Errorf("the message is longer than %d characters", teamWebMaxText))
		return
	case send.Person != "" && !slackIDPattern.MatchString(send.Person):
		fail(http.StatusBadRequest, fmt.Errorf("%q isn't a Slack member ID", send.Person))
		return
	case send.Person == "" && (send.Name == "" || len(config.Askers) > 0):
		fail(http.StatusBadRequest, errors.New("say who you are first"))
		return
	}
	if len(send.Name) > 60 {
		send.Name = send.Name[:60]
	}
	if send.Person != "" {
		for _, person := range b.webPeople(config) {
			if person.ID == send.Person {
				send.Name = person.Name
			}
		}
	}
	send.Name = firstNonEmpty(send.Name, send.Person)
	if send.Page != nil {
		send.Page.Path, send.Page.Title = teamClipText(send.Page.Path, 300), teamClipText(send.Page.Title, 200)
	}

	b.webMu.Lock()
	conversation, ok := teamWebConversation{}, false
	if send.Conversation != "" {
		if found, err := loadTeamWebConversation(b.team, send.Conversation); err == nil && found.Slot == slot && !found.Ended && found.Person == send.Person {
			conversation, ok = found, true
		}
	}
	if !ok {
		conversation, ok = b.openConversation(slot, send.Person, send.Name)
	}
	if !ok {
		next := 1
		if all := loadTeamWebConversations(b.team); len(all) > 0 {
			next = teamWebNumber(all[len(all)-1].ID) + 1
		}
		conversation = teamWebConversation{ID: fmt.Sprintf("w-%d", next), Slot: slot, Person: send.Person, Name: send.Name, Started: time.Now().UTC()}
	}
	if b.answering[conversation.ID] {
		b.webMu.Unlock()
		fail(http.StatusConflict, errors.New("the lead is still answering your last message"))
		return
	}
	first := len(conversation.Messages) == 0
	message := teamWebMessage{N: len(conversation.Messages) + 1, From: "person", Name: send.Name, Text: send.Text, Page: send.Page, At: time.Now().UTC()}
	conversation.Messages = append(conversation.Messages, message)
	if err := saveTeamWebConversation(b.team, conversation); err != nil {
		b.webMu.Unlock()
		fail(http.StatusInternalServerError, err)
		return
	}
	b.answering[conversation.ID] = true
	b.webMu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	gone := r.Context().Done()
	event := func(name string, value any) {
		select {
		case <-gone:
			return
		default:
		}
		data, _ := json.Marshal(value)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	event("message", map[string]any{"conversation": conversation.ID, "message": message})

	// The lead's answer outlives the browser: it is kept and copied to the
	// channel either way.
	answered := make(chan struct{})
	deltas := make(chan string, 256)
	var answer string
	var leadErr error
	go func() {
		defer close(answered)
		b.mirrorWeb(config, conversation.ID, message.N)
		ctx, cancel := context.WithTimeout(context.Background(), teamLeadTimeout)
		defer cancel()
		answer, leadErr = b.lead(ctx, "web-"+conversation.ID, teamWebLeadInput(config, conversation, message, first), func(delta string) {
			select {
			case deltas <- delta:
			default: // a browser that can't keep up gets the whole answer at the end
			}
		})
		b.webMu.Lock()
		delete(b.answering, conversation.ID)
		if leadErr != nil {
			b.webMu.Unlock()
			return
		}
		saved, err := loadTeamWebConversation(b.team, conversation.ID)
		if err == nil {
			reply := teamWebMessage{N: len(saved.Messages) + 1, From: "lead", Name: teamBotName(b.team) + " team lead", Text: strings.TrimSpace(answer), At: time.Now().UTC()}
			saved.Messages = append(saved.Messages, reply)
			err = saveTeamWebConversation(b.team, saved)
		}
		b.webMu.Unlock()
		if err == nil {
			saved, _ := loadTeamWebConversation(b.team, conversation.ID)
			b.mirrorWeb(config, conversation.ID, len(saved.Messages))
		}
	}()
	for {
		select {
		case delta := <-deltas:
			event("delta", map[string]string{"text": delta})
		case <-answered:
			for len(deltas) > 0 {
				<-deltas
			}
			if leadErr != nil {
				event("error", map[string]string{"error": "the lead couldn't answer: " + leadErr.Error()})
				return
			}
			saved, _ := loadTeamWebConversation(b.team, conversation.ID)
			if n := len(saved.Messages); n > 0 {
				event("done", map[string]any{"conversation": conversation.ID, "message": saved.Messages[n-1]})
			}
			return
		case <-gone:
			// The lead still answers; the next visit shows it.
			return
		}
	}
}

// teamWebLeadInput is what the lead reads for a message in the app: a
// header saying where it comes from, then the person's words. The first
// message of a chat also says how a chat in the app works.
func teamWebLeadInput(config teamConfig, conversation teamWebConversation, message teamWebMessage, first bool) string {
	who := message.Name
	if conversation.Person != "" {
		who += " (" + conversation.Person + ")"
	}
	parts := []string{"web chat " + conversation.ID, "message " + conversation.ID + "." + strconv.Itoa(message.N), who}
	version := conversation.Slot
	if state, ok := loadTeamSlot(config.Name, conversation.Slot); ok && state.Label != "" {
		version += ": " + state.Label
	}
	parts = append(parts, version)
	if page := message.Page; page != nil {
		parts = append(parts, page.Path)
		if page.Title != "" {
			parts = append(parts, strconv.Quote(page.Title))
		}
		if page.Width > 0 && page.Height > 0 {
			parts = append(parts, fmt.Sprintf("%d×%d", page.Width, page.Height))
		}
	}
	input := "[" + strings.Join(parts, " · ") + "]\n\n" + message.Text
	if first {
		input = teamWebFirstNote + "\n\n" + input
	}
	return input
}

// teamWebFirstNote starts each web chat for the lead.
const teamWebFirstNote = `(This message comes from the helper in the team's app: a chat panel beside the page the person is on, not Slack. The header in brackets says who it is, which address and version they see, and the page. Answer in a few short sentences, as in Slack: the panel is narrow. hi copies this chat into a thread in the channel. Work still waits for a yes: a yes here counts, with its message id from the header as --approval, such as --approval w-3.4. A release, a rollback, or turning production's helper off needs an owner's yes in Slack.)`

// mirrorWeb copies message n of a web chat into the channel: the first
// starts a thread, the rest reply in it. It never fails the chat.
func (b *teamBroker) mirrorWeb(config teamConfig, id string, n int) {
	if b.channel == nil {
		return
	}
	b.webMu.Lock()
	conversation, err := loadTeamWebConversation(b.team, id)
	b.webMu.Unlock()
	if err != nil || n < 1 || n > len(conversation.Messages) {
		return
	}
	message := conversation.Messages[n-1]
	var text string
	switch {
	case conversation.Thread == "":
		where := conversation.Slot
		if message.Page != nil && message.Page.Path != "" {
			where += ", " + message.Page.Path
		}
		text = fmt.Sprintf(":speech_balloon: *%s*, in the app (%s): %s", teamSlackEscape(message.Name), teamSlackEscape(where), teamSlackEscape(message.Text))
	case message.From == "person":
		text = fmt.Sprintf("*%s*, in the app: %s", teamSlackEscape(message.Name), teamSlackEscape(message.Text))
	default:
		text = message.Text
	}
	var ts string
	if conversation.Thread == "" {
		ts, err = b.channel.post(config.Channel, text)
	} else {
		ts, err = b.channel.postIn(config.Channel, conversation.Thread, text)
	}
	if err != nil {
		return
	}
	b.webMu.Lock()
	defer b.webMu.Unlock()
	if conversation, err = loadTeamWebConversation(b.team, id); err != nil {
		return
	}
	if conversation.Thread == "" {
		conversation.Thread = ts
	}
	conversation.Messages[n-1].SlackTS = ts
	saveTeamWebConversation(b.team, conversation)
}

// teamSlackEscape keeps a person's text from becoming Slack markup.
func teamSlackEscape(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
}

// ---------------------------------------------------------------------------
// approvals from the app

// teamCheckWebApproval checks a yes given in the app's chat: a person's
// message, after a message of the lead's in the same chat, within a week.
// The name in the app is only claimed, so it never approves what needs an
// owner.
func teamCheckWebApproval(config teamConfig, ref string, owners bool, now time.Time) (teamApproval, error) {
	if owners {
		return teamApproval{}, fmt.Errorf("%s is from the app's chat; this needs an owner's yes in Slack", ref)
	}
	match := teamWebMessageID.FindStringSubmatch(ref)
	conversation, err := loadTeamWebConversation(config.Name, match[1])
	if err != nil {
		return teamApproval{}, err
	}
	n, _ := strconv.Atoi(match[2])
	if n < 1 || n > len(conversation.Messages) {
		return teamApproval{}, fmt.Errorf("web chat %s has no message %d", conversation.ID, n)
	}
	message := conversation.Messages[n-1]
	if message.From != "person" {
		return teamApproval{}, fmt.Errorf("message %s is yours; the approval must come from one of the people", ref)
	}
	if now.Sub(message.At) > teamApprovalMaxAge {
		return teamApproval{}, fmt.Errorf("message %s is more than a week old; ask again", ref)
	}
	if len(config.Askers) > 0 && !containsString(append(append([]string{}, config.Owners...), config.Askers...), conversation.Person) {
		return teamApproval{}, fmt.Errorf("%s isn't one of the team's people who may ask for work", message.Name)
	}
	answered := false
	for _, earlier := range conversation.Messages[:n-1] {
		if earlier.From == "lead" {
			answered = true
		}
	}
	if !answered {
		return teamApproval{}, fmt.Errorf("message %s from %s doesn't answer you: approval is a reply after you asked, in the same chat. Ask, end your reply, and wait for theirs", ref, message.Name)
	}
	return teamApproval{TS: ref, Name: message.Name, Text: qClip(message.Text)}, nil
}

// teamWebApprovalChat is the web chat an approval came from, up to it, for
// the brief.
func teamWebApprovalChat(team string, approval teamApproval) string {
	match := teamWebMessageID.FindStringSubmatch(approval.TS)
	if match == nil {
		return ""
	}
	conversation, err := loadTeamWebConversation(team, match[1])
	if err != nil {
		return ""
	}
	n, _ := strconv.Atoi(match[2])
	var said strings.Builder
	for _, message := range conversation.Messages[:min(n, len(conversation.Messages))] {
		where := ""
		if message.Page != nil && message.Page.Path != "" {
			where = ", on " + message.Page.Path
		}
		fmt.Fprintf(&said, "**%s** (%s%s): %s\n\n", message.Name, message.At.Format("Jan 2 15:04"), where, strings.TrimSpace(message.Text))
	}
	return said.String()
}

// ---------------------------------------------------------------------------
// the lead's API

// teamLeadAPI answers a message as the lead, through Hermes' API server in
// its box: /v1/responses, with the conversation named so Hermes keeps its
// history, streamed.
func teamLeadAPI(socket, key string) func(ctx context.Context, conversation, input string, delta func(string)) (string, error) {
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	return func(ctx context.Context, conversation, input string, delta func(string)) (string, error) {
		body, _ := json.Marshal(map[string]any{"input": input, "conversation": conversation, "stream": true})
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://lead/v1/responses", strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+key)
		response, err := client.Do(request)
		if err != nil {
			return "", errors.New("the lead isn't reachable yet; try again in a minute")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			return "", fmt.Errorf("%s: %s", response.Status, qFirstLine(strings.TrimSpace(string(data)), ""))
		}
		return teamReadLeadStream(response.Body, delta)
	}
}

// teamReadLeadStream reads the Responses API's events: text deltas, then
// response.completed, or response.failed.
func teamReadLeadStream(stream io.Reader, delta func(string)) (string, error) {
	var text strings.Builder
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	name := ""
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			var data struct {
				Type     string `json:"type"`
				Delta    string `json:"delta"`
				Response struct {
					Error *struct {
						Message string `json:"message"`
					} `json:"error"`
				} `json:"response"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &data) != nil {
				continue
			}
			kind := firstNonEmpty(data.Type, name)
			switch kind {
			case "response.output_text.delta":
				text.WriteString(data.Delta)
				if delta != nil {
					delta(data.Delta)
				}
			case "response.completed":
				return text.String(), nil
			case "response.failed", "error":
				message := "the lead's model failed"
				if data.Response.Error != nil && data.Response.Error.Message != "" {
					message = data.Response.Error.Message
				} else if data.Error != nil && data.Error.Message != "" {
					message = data.Error.Message
				}
				return text.String(), errors.New(message)
			}
		case line == "":
			name = ""
		}
	}
	if err := scanner.Err(); err != nil {
		return text.String(), err
	}
	if text.Len() > 0 {
		return text.String(), nil
	}
	return "", errors.New("the lead's answer ended without text")
}

// runTeamLeadAPI is hi team __leadapi, in the lead's box: it relays a
// socket the host reaches to Hermes' API server, which listens only inside
// the box.
func runTeamLeadAPI(args []string, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "usage: hi team __leadapi <socket> <port>")
		return 2
	}
	socket, port := args[0], args[1]
	os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		fmt.Fprintf(stderr, "hi: %v\n", err)
		return 1
	}
	os.Chmod(socket, 0o600)
	for {
		conn, err := listener.Accept()
		if err != nil {
			return 1
		}
		go func() {
			defer conn.Close()
			upstream, err := net.Dial("tcp", "127.0.0.1:"+port)
			if err != nil {
				return
			}
			defer upstream.Close()
			done := make(chan struct{}, 2)
			go func() { io.Copy(upstream, conn); done <- struct{}{} }()
			go func() { io.Copy(conn, upstream); done <- struct{}{} }()
			<-done
		}()
	}
}

// teamKeepLeadAPI starts the relay to the lead's API server in its box,
// and starts it again whenever the host can't reach it.
func teamKeepLeadAPI(engine boxEngine, container, socket string, done <-chan struct{}, stderr io.Writer) {
	reported := false
	for {
		select {
		case <-done:
			return
		case <-time.After(5 * time.Second):
		}
		if conn, err := net.DialTimeout("unix", socket, time.Second); err == nil {
			conn.Close()
			select {
			case <-done:
				return
			case <-time.After(25 * time.Second):
			}
			continue
		}
		out, err := engine.output("exec", "-d", container, "/usr/local/bin/hi", "team", "__leadapi", "/team/run/"+teamLeadAPISocket, strconv.Itoa(teamLeadAPIPort))
		if err != nil && !reported {
			fmt.Fprintf(stderr, "hi: the helper can't reach the lead yet: %s\n", qFirstLine(strings.TrimSpace(out), err.Error()))
			reported = true
		}
	}
}

// ---------------------------------------------------------------------------
// hi team helper

// teamHelperRequest turns a slot's helper on or off.
type teamHelperRequest struct {
	Slot     string `json:"slot"`
	On       bool   `json:"on"`
	Approval string `json:"approval,omitempty"`
}

// setHelper turns a slot's helper on or off for the lead: the preview's
// when asked, production's with an owner's yes in Slack.
func (b *teamBroker) setHelper(request teamHelperRequest) (teamConfig, error) {
	if request.Slot != teamSlotPreview && request.Slot != teamSlotProduction {
		return teamConfig{}, fmt.Errorf("the helper is on preview or production, not %q", request.Slot)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	config, err := loadTeam(b.team)
	if err != nil {
		return config, err
	}
	var approval teamApproval
	if request.Slot == teamSlotProduction {
		if approval, err = b.teamFindApproval(config, request.Approval, "helper"); err != nil {
			return config, err
		}
	}
	if err := teamSetHelper(&config, request.Slot, request.On); err != nil {
		return config, err
	}
	if request.Slot == teamSlotProduction {
		state := "off"
		if request.On {
			state = "on"
		}
		versions, err := loadTeamVersions(b.team)
		if err == nil {
			versions.History = append(versions.History, teamVersionEvent{What: "production's helper " + state, Approval: &approval, At: time.Now().UTC()})
			err = saveTeamVersions(b.team, versions)
		}
		if err != nil {
			return config, err
		}
		if b.channel != nil {
			b.channel.post(config.Channel, fmt.Sprintf(":speech_balloon: The helper, the chat with the lead in the app, is %s in production now. %s approved it.", state, teamSlackEscape(approval.Name)))
		}
	}
	return config, nil
}

// teamSetHelper saves the setting and puts the helper file in line: the
// next page load has the helper or doesn't.
func teamSetHelper(config *teamConfig, slot string, on bool) error {
	if config.Helper == nil {
		config.Helper = map[string]bool{}
	}
	config.Helper[slot] = on
	if on {
		delete(config.Helper, slot) // on is the default
	}
	if len(config.Helper) == 0 {
		config.Helper = nil
	}
	if err := saveTeam(*config); err != nil {
		return err
	}
	return teamSyncHelper(*config)
}

// teamHelperCommand is hi team helper <name> [preview|production on|off],
// on the machine: whoever runs the machine needs no approval.
func teamHelperCommand(args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi team helper <name> [preview|production on|off]"}
	if len(args) != 1 && len(args) != 3 {
		return usage
	}
	config, err := loadTeam(args[0])
	if err != nil {
		return err
	}
	if len(args) == 3 {
		if (args[1] != teamSlotPreview && args[1] != teamSlotProduction) || (args[2] != "on" && args[2] != "off") {
			return usage
		}
		if err := teamSetHelper(&config, args[1], args[2] == "on"); err != nil {
			return err
		}
	}
	fmt.Fprintln(stdout, teamHelperLine(config))
	if len(args) == 3 {
		fmt.Fprintln(stdout, "It applies from the next page load. An address made before hi had the helper gets it when its link is next renewed.")
	}
	return nil
}
