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
	"unicode/utf8"
)

// The helper puts a chat with the lead into every page of a team's app:
// hi net expose adds its script to the pages it relays and sends /__hi/ to
// the team's service, on a socket per address. The lead answers through
// Hermes' API server, in a conversation of its own per web chat, and hi
// copies each chat into a thread: in the DM of the person signed in through
// Slack, or in the channel for a chat whose person isn't checked
// (docs/specs/approved/hi_team_helper.md).

//go:embed helper/helper.js
var teamHelperSource []byte

//go:embed third_party/finder/finder.js
var teamFinderSource []byte

// teamSnapSource is modern-screenshot, an ES module the helper imports
// when it first takes a picture of what the person points at.
//
//go:embed third_party/modern-screenshot/modern-screenshot.js
var teamSnapSource []byte

var teamSnapVersion = fmt.Sprintf("%x", sha256.Sum256(teamSnapSource))[:16]

// teamHelperScript is what the browser loads: finder, the selector
// generator select mode uses, and the helper, in one closure, so the page
// gets one file and no globals.
var teamHelperScript = func() []byte {
	finder := regexp.MustCompile(`(?m)^export `).ReplaceAll(teamFinderSource, nil)
	script := "(() => {\n\"use strict\";\n" + string(finder) + "\n" + string(teamHelperSource) + "\n})();\n"
	return []byte(script)
}()

const (
	teamHelperPrefix = "/__hi/"
	// teamLeadAPIPort is Hermes' API server in the lead's box; hi team
	// __leadapi relays a socket the host reaches to it.
	teamLeadAPIPort   = 8642
	teamLeadAPISocket = "lead-api.sock"
	// teamWebMaxText is the longest message the helper takes, and
	// teamWebMaxRefs how many elements one message may point at.
	teamWebMaxText = 8000
	teamWebMaxRefs = 10
	// teamLeadTimeout is how long the lead may take over one answer: it may
	// look at the project or start a task first.
	teamLeadTimeout = 20 * time.Minute
)

// teamHelperVersion is the script's ETag: browsers check it on each page
// load, so a new hi's helper reaches pages at once, without new links.
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

// teamExposeHelperArgs are what a slot's hi net expose gets for the helper
// and the docs.
func teamExposeHelperArgs(team, slot string) []string {
	return []string{"--route", teamHelperPrefix + "=unix:" + teamWebSocket(team, slot),
		"--route-always", teamDocsPrefix + "=unix:" + teamWebSocket(team, slot),
		"--inject", teamHelperPrefix + "helper.js",
		"--inject-if", teamHelperFile(team, slot)}
}

// teamRelayStamp is what a slot's relay routes to hi, and how, in short:
// when a new hi routes more, such as /docs/, or relays differently, an
// address that stays gets a new relay.
func teamRelayStamp(team, slot string) string {
	args := append([]string{teamRelayGeneration}, teamExposeHelperArgs(team, slot)...)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(args, "\x00"))))[:12]
}

// teamRelayGeneration changes when the relay itself does in a way running
// relays should get: 2 waits for a service that is restarting (v0.84.0).
const teamRelayGeneration = "relay 2"

// ---------------------------------------------------------------------------
// web conversations, in web/

// teamWebConversation is web/<id>.json: one chat in the app.
type teamWebConversation struct {
	ID     string `json:"id"` // w-12
	Slot   string `json:"slot"`
	Person string `json:"person,omitempty"` // a Slack member ID
	Name   string `json:"name"`
	// Verified says the person signed in with /hi, so Person is who they
	// are; before v0.60 the helper asked, and anyone could claim a name.
	Verified bool      `json:"verified,omitempty"`
	Started  time.Time `json:"started"`
	Ended    bool      `json:"ended,omitempty"`  // the person started a new one
	Thread   string    `json:"thread,omitempty"` // its thread in Slack
	// DM is the person's DM with the bot when Thread is in it, not in the
	// channel: a chat in the app is between them and the lead, and a copy
	// of each in the channel was too much for the team.
	DM       string           `json:"dm,omitempty"`
	Messages []teamWebMessage `json:"messages"`
}

// teamWebMessage is one message of a web chat. Its ID is the
// conversation's and its number: w-12.3.
type teamWebMessage struct {
	N     int           `json:"n"`
	From  string        `json:"from"` // person or lead
	Name  string        `json:"name"`
	Text  string        `json:"text"`
	Page  *teamWebPage  `json:"page,omitempty"`
	Refs  []teamWebRef  `json:"refs,omitempty"`  // the elements the person pointed at
	Files []teamWebFile `json:"files,omitempty"` // in web/<id>-files (team_web.go)
	// Posted is a message of the lead's from outside the chat's
	// conversation with it: hi team web post.
	Posted  bool      `json:"posted,omitempty"`
	At      time.Time `json:"at"`
	SlackTS string    `json:"slack_ts,omitempty"` // its copy in the channel
}

// teamWebPage is where the person was when they wrote.
type teamWebPage struct {
	Path   string `json:"path"`
	Title  string `json:"title,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// teamWebRef is an element of the page the person pointed at in select
// mode, as the helper describes it: enough to find it again on the page,
// and in the code.
type teamWebRef struct {
	N          int               `json:"n"`
	Selector   string            `json:"selector"`
	Tag        string            `json:"tag"`
	Text       string            `json:"text,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	HTML       string            `json:"html,omitempty"`
	Path       []string          `json:"path,omitempty"`      // its ancestors, outermost first
	Landmarks  map[string]string `json:"landmarks,omitempty"` // the nearest heading, its form label
	Box        *teamWebBox       `json:"box,omitempty"`
	Style      map[string]string `json:"style,omitempty"`
	Source     *teamWebSource    `json:"source,omitempty"` // when the app's framework gives it away
}

type teamWebBox struct {
	X        int    `json:"x"`
	Y        int    `json:"y"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Viewport [2]int `json:"viewport"`
}

type teamWebSource struct {
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Component string `json:"component,omitempty"`
}

// teamCleanRefs keeps references to a size the lead can read: the page
// wrote them, so nothing in them is trusted to be short.
func teamCleanRefs(refs []teamWebRef) []teamWebRef {
	if len(refs) > teamWebMaxRefs {
		refs = refs[:teamWebMaxRefs]
	}
	clip := func(text string, limit int) string { return teamClipStart(strings.TrimSpace(text), limit) }
	clipMap := func(in map[string]string, keys, limit int) map[string]string {
		names := make([]string, 0, len(in))
		for name := range in {
			names = append(names, name)
		}
		sort.Strings(names)
		out := map[string]string{}
		for _, name := range names {
			if len(out) == keys {
				break
			}
			if value := clip(in[name], limit); value != "" {
				out[clip(name, 40)] = value
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	var clean []teamWebRef
	for i, ref := range refs {
		ref.N = i + 1
		ref.Selector, ref.Tag, ref.Text, ref.HTML = clip(ref.Selector, 500), strings.ToLower(clip(ref.Tag, 30)), clip(ref.Text, 200), clip(ref.HTML, 2048)
		if ref.Selector == "" || ref.Tag == "" {
			continue
		}
		ref.Attributes, ref.Landmarks, ref.Style = clipMap(ref.Attributes, 20, 200), clipMap(ref.Landmarks, 4, 120), clipMap(ref.Style, 10, 80)
		if len(ref.Path) > 8 {
			ref.Path = ref.Path[len(ref.Path)-8:]
		}
		for j := range ref.Path {
			ref.Path[j] = clip(ref.Path[j], 80)
		}
		if ref.Source != nil {
			ref.Source.File, ref.Source.Component = clip(ref.Source.File, 300), clip(ref.Source.Component, 80)
			if ref.Source.File == "" && ref.Source.Component == "" {
				ref.Source = nil
			}
		}
		clean = append(clean, ref)
	}
	for i := range clean {
		clean[i].N = i + 1
	}
	return clean
}

// teamClipStart keeps the start of text, at most limit bytes of it, cut
// between characters.
func teamClipStart(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit - len("…")
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

// teamWebRefLabel is a reference in a few words: #1 span "Paid".
func teamWebRefLabel(ref teamWebRef) string {
	label := fmt.Sprintf("#%d %s", ref.N, ref.Tag)
	if text := strings.Join(strings.Fields(ref.Text), " "); text != "" {
		text = teamClipStart(text, 40)
		label += " " + strconv.Quote(text)
	}
	return label
}

// teamWebRefsText is the references as the lead and a member read them.
func teamWebRefsText(refs []teamWebRef, indent string) string {
	if len(refs) == 0 {
		return ""
	}
	var text strings.Builder
	text.WriteString(indent + "Referenced elements:\n")
	for _, ref := range refs {
		fmt.Fprintf(&text, "%s%s\n", indent, teamWebRefLabel(ref))
		line := func(name, value string) {
			if value != "" {
				fmt.Fprintf(&text, "%s   %s: %s\n", indent, name, strings.ReplaceAll(value, "\n", " "))
			}
		}
		line("selector", ref.Selector)
		line("html", ref.HTML)
		line("under", strings.Join(ref.Path, " › "))
		var near []string
		for _, name := range []string{"heading", "label", "section", "form"} {
			if value := ref.Landmarks[name]; value != "" {
				near = append(near, name+" "+strconv.Quote(value))
			}
		}
		line("near", strings.Join(near, "; "))
		if box := ref.Box; box != nil {
			look := fmt.Sprintf("%d×%d at (%d, %d) in a %d×%d viewport", box.Width, box.Height, box.X, box.Y, box.Viewport[0], box.Viewport[1])
			if color := ref.Style["color"]; color != "" {
				look += "; " + color
				if background := ref.Style["background-color"]; background != "" && background != "rgba(0, 0, 0, 0)" {
					look += " on " + background
				}
			}
			if size := ref.Style["font-size"]; size != "" {
				look += ", " + size
			}
			line("box", look)
		}
		if source := ref.Source; source != nil {
			where := source.File
			if where != "" && source.Line > 0 {
				where += ":" + strconv.Itoa(source.Line)
			}
			if source.Component != "" {
				where = strings.TrimSpace(where + " (" + source.Component + ")")
			}
			line("source", where)
		}
	}
	return text.String()
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
	for _, feedback := range loadTeamFeedbacks(team) {
		if feedback.SlackTS != "" {
			mirrored[feedback.SlackTS] = teamChannelWeb{Conversation: feedback.ID, From: "feedback"}
		}
	}
	return mirrored
}

// ---------------------------------------------------------------------------
// the helper's API, on the web sockets

// teamWebPerson is who is signed in, from their key.
type teamWebPerson struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// teamWebStatus is GET /__hi/api/status.
type teamWebStatus struct {
	Team    string `json:"team"`
	Lead    string `json:"lead"`
	Slot    string `json:"slot"`
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
	// You is who this browser is signed in as, with /hi; nil gets help
	// and feedback. Ended says its key no longer works.
	You   *teamWebPerson `json:"you,omitempty"`
	Ended bool           `json:"ended,omitempty"`
	// Command is the team's command in Slack for a private link.
	Command string `json:"command"`
	// Answers counts the team's answers to this browser's feedback.
	Answers int `json:"answers,omitempty"`
	// Docs is where the app's docs are on this address, when its relay
	// sends /docs/ to hi (every relay since v0.81.1 records its stamp).
	Docs string `json:"docs,omitempty"`
}

// teamWebSend is POST /__hi/api/messages.
type teamWebSend struct {
	Conversation string          `json:"conversation,omitempty"`
	Text         string          `json:"text"`
	Page         *teamWebPage    `json:"page,omitempty"`
	Refs         []teamWebRef    `json:"refs,omitempty"`
	Files        []teamWebUpload `json:"files,omitempty"`
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
		etag := `"` + teamHelperVersion() + `"`
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Write(teamHelperScript)
	})
	mux.HandleFunc("GET "+teamHelperPrefix+"snap.js", func(w http.ResponseWriter, r *http.Request) {
		// modern-screenshot, for the pictures of what the person points at:
		// the helper loads it the first time it needs it.
		etag := `"` + teamSnapVersion + `"`
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Write(teamSnapSource)
	})
	// you is who sent a request: the person whose key the relay found in
	// the request's cookie. Nobody else says who they are.
	you := func(w http.ResponseWriter, r *http.Request) (teamPersonKey, bool) {
		config, err := loadTeam(b.team)
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return teamPersonKey{}, false
		}
		person, ok, ended := b.webUser(config, r.Header.Get(netExposeUserHeader))
		switch {
		case ended:
			failure(w, http.StatusUnauthorized, fmt.Errorf("your link has ended: type %s in the team's Slack channel for a new one", teamSlashCommand(b.team)))
		case !ok:
			failure(w, http.StatusUnauthorized, fmt.Errorf("the chat with the lead is for people signed in with %s in the team's Slack channel", teamSlashCommand(b.team)))
		}
		return person, ok
	}
	mux.HandleFunc("GET "+teamHelperPrefix+"api/events", func(w http.ResponseWriter, r *http.Request) {
		if person, ok := you(w, r); ok {
			b.webEvents(w, r, slot, person.User)
		}
	})
	mux.HandleFunc("GET "+teamHelperPrefix+"api/files/{id}/{name}", func(w http.ResponseWriter, r *http.Request) {
		if person, ok := you(w, r); ok {
			b.webFile(w, r, slot, person.User)
		}
	})
	mux.HandleFunc("GET "+teamHelperPrefix+"api/status", func(w http.ResponseWriter, r *http.Request) {
		config, err := loadTeam(b.team)
		if err != nil {
			failure(w, http.StatusInternalServerError, err)
			return
		}
		status := teamWebStatus{Team: b.team, Lead: teamBotName(b.team) + " team lead", Slot: slot, Command: teamSlashCommand(b.team)}
		if state, ok := loadTeamSlot(b.team, slot); ok {
			status.Version, status.Commit = state.Label, state.Commit
			if state.Relay != "" {
				status.Docs = teamDocsPrefix
			}
		}
		if person, ok, ended := b.webUser(config, r.Header.Get(netExposeUserHeader)); ok {
			status.You = &teamWebPerson{ID: person.User, Name: person.Name}
		} else {
			status.Ended = ended
		}
		// Production's panel has feedback for people signed in too.
		if status.You == nil || slot == teamSlotProduction {
			_, status.Answers = teamVisitorFeedback(b.team, r.Header.Get(netExposeVisitorHeader), 0)
		}
		reply(w, http.StatusOK, status)
	})
	mux.HandleFunc("POST "+teamHelperPrefix+"api/help", func(w http.ResponseWriter, r *http.Request) {
		var ask teamHelpAsk
		if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&ask); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		b.webHelp(w, r, slot, ask)
	})
	mux.HandleFunc("POST "+teamHelperPrefix+"api/feedback", func(w http.ResponseWriter, r *http.Request) {
		var send teamFeedbackSend
		if err := json.NewDecoder(io.LimitReader(r.Body, teamWebMaxUpload*4/3+(1<<20))).Decode(&send); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		feedback, status, err := b.webFeedback(r, slot, teamVisitor(w, r), send)
		if err != nil {
			failure(w, status, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{"id": feedback.ID})
	})
	mux.HandleFunc("GET "+teamHelperPrefix+"api/feedback", func(w http.ResponseWriter, r *http.Request) {
		sent, _ := teamVisitorFeedback(b.team, r.Header.Get(netExposeVisitorHeader), 20)
		reply(w, http.StatusOK, map[string]any{"feedback": append([]teamFeedbackSent{}, sent...)})
	})
	mux.HandleFunc("GET "+teamHelperPrefix+"api/conversation", func(w http.ResponseWriter, r *http.Request) {
		person, ok := you(w, r)
		if !ok {
			return
		}
		b.webMu.Lock()
		conversation, ok := b.openConversation(slot, person.User)
		b.webMu.Unlock()
		if !ok {
			reply(w, http.StatusOK, map[string]any{})
			return
		}
		reply(w, http.StatusOK, conversation)
	})
	mux.HandleFunc("POST "+teamHelperPrefix+"api/conversation/new", func(w http.ResponseWriter, r *http.Request) {
		person, ok := you(w, r)
		if !ok {
			return
		}
		b.webMu.Lock()
		defer b.webMu.Unlock()
		if conversation, ok := b.openConversation(slot, person.User); ok {
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
		person, ok := you(w, r)
		if !ok {
			return
		}
		var send teamWebSend
		if err := json.NewDecoder(io.LimitReader(r.Body, teamWebMaxUpload*4/3+(1<<20))).Decode(&send); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		b.webSend(w, r, slot, person, send)
	})
	mux.Handle("GET "+teamDocsPrefix, b.docsHandler(slot))
	return mux
}

// openConversation is this person's chat in this slot that they haven't
// ended, from since they signed in. The caller holds webMu.
func (b *teamBroker) openConversation(slot, person string) (teamWebConversation, bool) {
	all := loadTeamWebConversations(b.team)
	for i := len(all) - 1; i >= 0; i-- {
		conversation := all[i]
		if conversation.Slot == slot && !conversation.Ended && conversation.Verified && conversation.Person == person {
			return conversation, true
		}
	}
	return teamWebConversation{}, false
}

// webSend takes a person's message, has the lead answer it, and streams the
// answer back as server-sent events: "message" for each saved message,
// "delta" for the lead's text as it comes, then "done" or "error". The
// answer is kept and copied to the channel even when the browser goes.
func (b *teamBroker) webSend(w http.ResponseWriter, r *http.Request, slot string, person teamPersonKey, send teamWebSend) {
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
	send.Text = strings.TrimSpace(send.Text)
	switch {
	case !teamHelperOn(config, slot):
		fail(http.StatusForbidden, errors.New("the helper is off here"))
		return
	case send.Text == "" && len(send.Files) == 0:
		fail(http.StatusBadRequest, errors.New("the message is empty"))
		return
	case len(send.Text) > teamWebMaxText:
		fail(http.StatusBadRequest, fmt.Errorf("the message is longer than %d characters", teamWebMaxText))
		return
	}
	if send.Page != nil {
		send.Page.Path, send.Page.Title = teamClipStart(send.Page.Path, 300), teamClipStart(send.Page.Title, 200)
	}
	send.Refs = teamCleanRefs(send.Refs)

	b.webMu.Lock()
	conversation, ok := teamWebConversation{}, false
	if send.Conversation != "" {
		if found, err := loadTeamWebConversation(b.team, send.Conversation); err == nil && found.Slot == slot && !found.Ended && found.Verified && found.Person == person.User {
			conversation, ok = found, true
		}
	}
	if !ok {
		conversation, ok = b.openConversation(slot, person.User)
	}
	if !ok {
		next := 1
		if all := loadTeamWebConversations(b.team); len(all) > 0 {
			next = teamWebNumber(all[len(all)-1].ID) + 1
		}
		conversation = teamWebConversation{ID: fmt.Sprintf("w-%d", next), Slot: slot, Person: person.User, Name: person.Name, Verified: true, Started: time.Now().UTC()}
	}
	if b.answering[conversation.ID] {
		b.webMu.Unlock()
		fail(http.StatusConflict, errors.New("the lead is still answering your last message"))
		return
	}
	first := len(conversation.Messages) == 0
	n := len(conversation.Messages) + 1
	files, err := teamSaveWebFiles(b.team, conversation.ID, n, send.Files, len(send.Refs))
	if err != nil {
		b.webMu.Unlock()
		fail(http.StatusBadRequest, err)
		return
	}
	message := teamWebMessage{N: n, From: "person", Name: person.Name, Text: send.Text, Page: send.Page, Refs: send.Refs, Files: files, At: time.Now().UTC()}
	conversation.Messages = append(conversation.Messages, message)
	if err := saveTeamWebConversation(b.team, conversation); err != nil {
		b.webMu.Unlock()
		fail(http.StatusInternalServerError, err)
		return
	}
	b.answering[conversation.ID] = true
	b.webPublish(conversation.ID, message)
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
		toBrowser := func(delta string) {
			select {
			case deltas <- delta:
			default: // a browser that can't keep up gets the whole answer at the end
			}
		}
		pictures, seen := teamWebPictures(b.team, conversation.ID, message.Files, message.Name)
		answer, leadErr = b.lead(ctx, "web-"+conversation.ID, teamWebLeadInput(config, conversation, message, first, seen), pictures, toBrowser)
		if leadErr != nil && answer == "" && len(pictures) > 0 && ctx.Err() == nil {
			// A model that can't look at pictures still reads where they are.
			answer, leadErr = b.lead(ctx, "web-"+conversation.ID, teamWebLeadInput(config, conversation, message, first, nil), nil, toBrowser)
		}
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
			if err = saveTeamWebConversation(b.team, saved); err == nil {
				b.webPublish(conversation.ID, reply)
			}
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
			// What the lead said last may still be on its way.
			for len(deltas) > 0 {
				event("delta", map[string]string{"text": <-deltas})
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
func teamWebLeadInput(config teamConfig, conversation teamWebConversation, message teamWebMessage, first bool, seen map[string]bool) string {
	who := message.Name
	if conversation.Person != "" {
		who += " (" + conversation.Person + ", signed in through Slack)"
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
	if refs := teamWebRefsText(message.Refs, ""); refs != "" {
		input += "\n\n" + refs
	}
	if files := teamWebFilesText(conversation.ID, message.Files, seen, ""); files != "" {
		input = strings.TrimRight(input, "\n") + "\n\n" + files
	}
	// What the lead posted in this chat since its last answer here, from
	// elsewhere, isn't in this conversation's history: say it.
	var posted []string
	for i := message.N - 2; i >= 0 && i < len(conversation.Messages) && conversation.Messages[i].Posted; i-- {
		posted = append([]string{"> " + strings.ReplaceAll(strings.TrimSpace(conversation.Messages[i].Text), "\n", "\n> ")}, posted...)
	}
	if len(posted) > 0 {
		input = "(Since your last answer here, you also wrote in this chat, with hi team web post:\n" + strings.Join(posted, "\n\n") + ")\n\n" + input
	}
	if first {
		note := teamWebFirstNote
		if conversation.Slot == teamSlotProduction {
			note += "\n\n" + teamWebProductionNote
		}
		input = note + "\n\n" + input
	}
	return input
}

// teamWebProductionNote starts a chat from production, where the person
// uses the app rather than shapes it.
const teamWebProductionNote = `(They are on production, the version people rely on. What they ask for goes into dev, as from anywhere: say so, then send them the preview link with hi team web post when it is ready to try, naming the version ("dev, v3 + 1 change"). Production gets it with the next release, which needs an owner's yes in Slack; when you ask for it, say what else in dev would go with it. If they only report a problem or ask how something works, answer that; nothing needs to change.)`

// teamWebFirstNote starts each web chat for the lead.
const teamWebFirstNote = `(This message comes from the helper in the team's app: a chat panel beside the page the person is on, not Slack. The header in brackets says who it is (signed in through Slack, so checked), which address and version they see, and the page. "Referenced elements" are parts of the page they clicked on to show what they mean: #1, #2 in their words are those; put them in the brief, since the coder finds them in the code by their text, classes, and html. A picture of each comes with the message, and so do files they attach, listed with their paths in /team/web. Answer in a few short sentences, as in Slack: the panel is narrow. hi copies this chat into a thread in their DM with you (in the channel when they aren't signed in through Slack). Your answer ends when you stop writing; to tell them something here later, such as that the work is ready to try, run hi team web post <chat> "...". Work still waits for a yes: a yes here counts, with its message id from the header as --approval, such as --approval w-3.4. A release, a rollback, or turning production's helper off needs an owner's yes in Slack.)`

// mirrorWeb copies message n of a web chat into Slack: the first starts a
// thread, the rest reply in it. The thread is in the person's DM when they
// signed in through Slack, else in the channel. It never fails the chat.
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
	// A chat whose thread is already in the channel, from before, stays
	// there.
	inDM := conversation.DM != "" || (conversation.Thread == "" && conversation.Verified && conversation.Person != "")
	var text string
	switch {
	case conversation.Thread == "":
		where := conversation.Slot
		if message.Page != nil && message.Page.Path != "" {
			where += ", " + message.Page.Path
		}
		who := "*" + teamSlackEscape(message.Name) + "*"
		if inDM {
			who = "You"
		}
		text = fmt.Sprintf(":speech_balloon: %s, in the app (%s): %s", who, teamSlackEscape(where), teamSlackEscape(message.Text))
	case message.From == "person" && inDM:
		text = "You, in the app: " + teamSlackEscape(message.Text)
	case message.From == "person":
		text = fmt.Sprintf("*%s*, in the app: %s", teamSlackEscape(message.Name), teamSlackEscape(message.Text))
	default:
		text = message.Text
	}
	for _, ref := range message.Refs {
		text += fmt.Sprintf("\n> %s: `%s`", teamSlackEscape(teamWebRefLabel(ref)), strings.ReplaceAll(teamSlackEscape(ref.Selector), "`", "'"))
	}
	where := config.Channel
	if inDM {
		where = conversation.DM
	}
	var ts string
	switch {
	case conversation.Thread != "":
		ts, err = b.channel.postIn(where, conversation.Thread, text)
	case inDM:
		where, ts, err = b.channel.dm(conversation.Person, text)
	default:
		ts, err = b.channel.post(where, text)
	}
	if err != nil {
		return
	}
	b.webMu.Lock()
	if conversation, err = loadTeamWebConversation(b.team, id); err != nil {
		b.webMu.Unlock()
		return
	}
	if conversation.Thread == "" {
		conversation.Thread = ts
		if inDM {
			conversation.DM = where
		}
	}
	conversation.Messages[n-1].SlackTS = ts
	saveTeamWebConversation(b.team, conversation)
	b.webMu.Unlock()
	// The files follow in the thread, the pictures of what was pointed at
	// first.
	for _, refs := range []bool{true, false} {
		for _, file := range message.Files {
			if (file.Ref > 0) == refs {
				_, name, _ := strings.Cut(file.Name, "-")
				if file.Ref > 0 {
					name = fmt.Sprintf("#%d", file.Ref)
				}
				b.channel.upload(where, conversation.Thread, filepath.Join(teamWebFilesDir(b.team, id), file.Name), name)
			}
		}
	}
}

// teamSlackEscape keeps a person's text from becoming Slack markup.
func teamSlackEscape(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
}

// ---------------------------------------------------------------------------
// approvals from the app

// teamCheckWebApproval checks a yes given in the app's chat: a person's
// message, after a message of the lead's in the same chat, within a week.
// Only a chat of someone signed in with /hi counts; what needs an owner
// still needs a yes in Slack, where the team sees it.
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
	if !conversation.Verified {
		return teamApproval{}, fmt.Errorf("%s is from a chat where %s only said who they are, from before people signed in with /hi; ask them to say yes in Slack, or again in the app after /hi", ref, message.Name)
	}
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
	// A chat copied into the person's DM has no thread in the channel to
	// say more in.
	thread := conversation.Thread
	if conversation.DM != "" {
		thread = ""
	}
	return teamApproval{TS: ref, Name: message.Name, Text: qClip(message.Text), Thread: thread}, nil
}

// teamWebApprovalChat is the web chat an approval came from, up to it, for
// the brief. add gives the member a file of the chat and says where it
// finds it.
func teamWebApprovalChat(team string, approval teamApproval, add func(source, from string) (string, error)) (string, error) {
	match := teamWebMessageID.FindStringSubmatch(approval.TS)
	if match == nil {
		return "", nil
	}
	conversation, err := loadTeamWebConversation(team, match[1])
	if err != nil {
		return "", nil
	}
	n, _ := strconv.Atoi(match[2])
	var said strings.Builder
	for _, message := range conversation.Messages[:min(n, len(conversation.Messages))] {
		where := ""
		if message.Page != nil && message.Page.Path != "" {
			where = ", on " + message.Page.Path
		}
		fmt.Fprintf(&said, "**%s** (%s%s): %s\n\n", message.Name, message.At.Format("Jan 2 15:04"), where, strings.TrimSpace(message.Text))
		if refs := teamWebRefsText(message.Refs, "    "); refs != "" {
			fmt.Fprintf(&said, "  The parts of the page they pointed at:\n\n%s\n", refs)
		}
		for _, file := range message.Files {
			from := message.Name + " in the app's chat"
			if file.Ref > 0 {
				from = fmt.Sprintf("how #%d looked on %s's screen", file.Ref, message.Name)
			}
			inBox, err := add(filepath.Join(teamWebFilesDir(team, conversation.ID), file.Name), from)
			if err != nil {
				return "", err
			}
			if file.Ref > 0 {
				fmt.Fprintf(&said, "  [a picture of #%d: %s]\n", file.Ref, inBox)
			} else {
				fmt.Fprintf(&said, "  [%s attached %s]\n", message.Name, inBox)
			}
		}
		if len(message.Files) > 0 {
			said.WriteString("\n")
		}
	}
	return said.String(), nil
}

// ---------------------------------------------------------------------------
// the lead's API

// teamLeadAPI answers a message as the lead, through Hermes' API server in
// its box: /v1/responses, with the conversation named so Hermes keeps its
// history, streamed.
// errTeamLeadUnreachable is a message that never reached the lead, as
// while its box starts: safe to send again.
var errTeamLeadUnreachable = errors.New("the lead isn't reachable yet; try again in a minute")

func teamLeadAPI(socket, key string) func(ctx context.Context, conversation, input string, pictures []teamLeadPicture, delta func(string)) (string, error) {
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	return func(ctx context.Context, conversation, input string, pictures []teamLeadPicture, delta func(string)) (string, error) {
		var message any = input
		if len(pictures) > 0 {
			parts := []map[string]string{{"type": "input_text", "text": input}}
			for _, picture := range pictures {
				parts = append(parts, map[string]string{"type": "input_text", "text": picture.Label}, map[string]string{"type": "input_image", "image_url": picture.URL})
			}
			message = []map[string]any{{"role": "user", "content": parts}}
		}
		body, _ := json.Marshal(map[string]any{"input": message, "conversation": conversation, "stream": true})
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://lead/v1/responses", strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+key)
		response, err := client.Do(request)
		if err != nil {
			return "", errTeamLeadUnreachable
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
