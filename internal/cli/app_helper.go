package cli

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

	hi "github.com/hi5sh/hi"
)

// The helper puts help and feedback into every page of a team's app: hi
// net expose adds its script to the pages it relays and sends /__hi/ to
// the team's service, on a socket per address. Feedback goes to the
// channel, where the lead files it as a ticket; from hi 2.4 the chat with
// the lead is in Slack (docs/specs/approved/hi_app_helper.md).

var appHelperSource = hi.AppHelperSource

var appFinderSource = hi.AppFinderSource

// appSnapSource is modern-screenshot, an ES module the helper imports
// when it first takes a picture of what the person points at.
var appSnapSource = hi.AppSnapSource

var appSnapVersion = fmt.Sprintf("%x", sha256.Sum256(appSnapSource))[:16]

// appHelperScript is what the browser loads: finder, the selector
// generator select mode uses, and the helper, in one closure, so the page
// gets one file and no globals.
var appHelperScript = func() []byte {
	finder := regexp.MustCompile(`(?m)^export `).ReplaceAll(appFinderSource, nil)
	script := "(() => {\n\"use strict\";\n" + string(finder) + "\n" + string(appHelperSource) + "\n})();\n"
	return []byte(script)
}()

const (
	appHelperPrefix = "/__hi/"
	// appLeadAPIPort is Hermes' API server in the lead's box; hi app
	// __leadapi relays a socket the host reaches to it.
	appLeadAPIPort   = 8642
	appLeadAPISocket = "lead-api.sock"
	// appWebMaxText is the longest message the helper takes, and
	// appWebMaxRefs how many elements one message may point at.
	appWebMaxText = 8000
	appWebMaxRefs = 10
	// appLeadTimeout is how long the lead may take over one answer: it may
	// look at the project or start a task first.
	appLeadTimeout = 20 * time.Minute
)

// appHelperVersion is the script's ETag: browsers check it on each page
// load, so a new hi's helper reaches pages at once, without new links.
func appHelperVersion() string {
	return fmt.Sprintf("%x", sha256.Sum256(appHelperScript))[:12]
}

// appHelperOn says whether a slot's address has the helper: on unless
// team.json turns it off.
func appHelperOn(config appConfig, slot string) bool {
	on, set := config.Helper[slot]
	return !set || on
}

// appHelperFile exists while the slot's helper is on; the slot's
// exposure reads it on every page (--inject-if).
func appHelperFile(team, slot string) string {
	return filepath.Join(appDir(team), "run", slot+".helper")
}

// appSyncHelper puts the helper files in line with team.json.
func appSyncHelper(config appConfig) error {
	if err := os.MkdirAll(filepath.Join(appDir(config.Name), "run"), 0o700); err != nil {
		return err
	}
	for _, slot := range []string{appSlotPreview, appSlotProduction} {
		path := appHelperFile(config.Name, slot)
		if appHelperOn(config, slot) {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				return err
			}
		} else if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// appHelperLine is the helper's state, for hi app status and the lead.
func appHelperLine(config appConfig) string {
	state := func(slot string) string {
		if appHelperOn(config, slot) {
			return "on"
		}
		return "off"
	}
	preview, production := state(appSlotPreview), state(appSlotProduction)
	if preview == production {
		return fmt.Sprintf("The helper (help and feedback in the app): %s in preview and production.", preview)
	}
	return fmt.Sprintf("The helper (help and feedback in the app): %s in preview, %s in production.", preview, production)
}

// appWebDir holds the sockets the exposures send /__hi/ to. Like the
// host's socket, no box mounts it.
func appWebDir(team string) string { return appBrokerDir(team) + "-web" }

func appWebSocket(team, slot string) string {
	return filepath.Join(appWebDir(team), slot+".sock")
}

// appExposeHelperArgs are what a slot's hi net expose gets for the helper
// and the docs.
func appExposeHelperArgs(team, slot string) []string {
	return []string{"--route", appHelperPrefix + "=unix:" + appWebSocket(team, slot),
		"--route-always", appDocsPrefix + "=unix:" + appWebSocket(team, slot),
		"--route-always", appLLMsPath + "=unix:" + appWebSocket(team, slot),
		"--route-always", appLLMsFullPath + "=unix:" + appWebSocket(team, slot),
		"--inject", appHelperPrefix + "helper.js",
		"--inject-if", appHelperFile(team, slot),
		"--sso", appSSOFile(team)}
}

// appRelayStamp is what a slot's relay routes to hi, and how, in short:
// when a new hi routes more, such as /docs/, or relays differently, an
// address that stays gets a new relay.
func appRelayStamp(team, slot string) string {
	args := append([]string{appRelayGeneration}, appExposeHelperArgs(team, slot)...)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(args, "\x00"))))[:12]
}

// appRelayGeneration changes when the relay itself does in a way running
// relays should get: 2 waits for a service that is restarting (v0.84.0).
const appRelayGeneration = "relay 2"

// ---------------------------------------------------------------------------
// web conversations, in web/

// appWebConversation is web/<id>.json: one chat in the app.
type appWebConversation struct {
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
	DM       string          `json:"dm,omitempty"`
	Messages []appWebMessage `json:"messages"`
}

// appWebMessage is one message of a web chat. Its ID is the
// conversation's and its number: w-12.3.
type appWebMessage struct {
	N     int          `json:"n"`
	From  string       `json:"from"` // person or lead
	Name  string       `json:"name"`
	Text  string       `json:"text"`
	Page  *appWebPage  `json:"page,omitempty"`
	Refs  []appWebRef  `json:"refs,omitempty"`  // the elements the person pointed at
	Files []appWebFile `json:"files,omitempty"` // in web/<id>-files (app_web.go)
	// Posted is a message of the lead's from outside the chat's
	// conversation with it: hi app web post.
	Posted  bool      `json:"posted,omitempty"`
	At      time.Time `json:"at"`
	SlackTS string    `json:"slack_ts,omitempty"` // its copy in the channel
}

// appWebPage is where the person was when they wrote.
type appWebPage struct {
	Path   string `json:"path"`
	Title  string `json:"title,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// appWebRef is an element of the page the person pointed at in select
// mode, as the helper describes it: enough to find it again on the page,
// and in the code.
type appWebRef struct {
	N          int               `json:"n"`
	Selector   string            `json:"selector"`
	Tag        string            `json:"tag"`
	Text       string            `json:"text,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	HTML       string            `json:"html,omitempty"`
	Path       []string          `json:"path,omitempty"`      // its ancestors, outermost first
	Landmarks  map[string]string `json:"landmarks,omitempty"` // the nearest heading, its form label
	Box        *appWebBox        `json:"box,omitempty"`
	Style      map[string]string `json:"style,omitempty"`
	Source     *appWebSource     `json:"source,omitempty"` // when the app's framework gives it away
}

type appWebBox struct {
	X        int    `json:"x"`
	Y        int    `json:"y"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Viewport [2]int `json:"viewport"`
}

type appWebSource struct {
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Component string `json:"component,omitempty"`
}

// appCleanRefs keeps references to a size the lead can read: the page
// wrote them, so nothing in them is trusted to be short.
func appCleanRefs(refs []appWebRef) []appWebRef {
	if len(refs) > appWebMaxRefs {
		refs = refs[:appWebMaxRefs]
	}
	clip := func(text string, limit int) string { return appClipStart(strings.TrimSpace(text), limit) }
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
	var clean []appWebRef
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

// appClipStart keeps the start of text, at most limit bytes of it, cut
// between characters.
func appClipStart(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit - len("…")
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

// appWebRefLabel is a reference in a few words: #1 span "Paid".
func appWebRefLabel(ref appWebRef) string {
	label := fmt.Sprintf("#%d %s", ref.N, ref.Tag)
	if text := strings.Join(strings.Fields(ref.Text), " "); text != "" {
		text = appClipStart(text, 40)
		label += " " + strconv.Quote(text)
	}
	return label
}

// appWebRefsText is the references as the lead and a member read them.
func appWebRefsText(refs []appWebRef, indent string) string {
	if len(refs) == 0 {
		return ""
	}
	var text strings.Builder
	text.WriteString(indent + "Referenced elements:\n")
	for _, ref := range refs {
		fmt.Fprintf(&text, "%s%s\n", indent, appWebRefLabel(ref))
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
	appWebConversationID = regexp.MustCompile(`^w-([0-9]{1,6})$`)
	appWebMessageID      = regexp.MustCompile(`^(w-[0-9]{1,6})\.([0-9]{1,4})$`)
)

func appWebStore(team string) string { return filepath.Join(appDir(team), "web") }

func loadTeamWebConversation(team, id string) (appWebConversation, error) {
	var conversation appWebConversation
	if !appWebConversationID.MatchString(id) {
		return conversation, fmt.Errorf("a web chat is named like w-3, not %q", id)
	}
	data, err := os.ReadFile(filepath.Join(appWebStore(team), id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return conversation, fmt.Errorf("no web chat %s", id)
	}
	if err != nil {
		return conversation, err
	}
	return conversation, json.Unmarshal(data, &conversation)
}

// loadTeamWebConversations lists a team's web chats, oldest first.
func loadTeamWebConversations(team string) []appWebConversation {
	entries, _ := os.ReadDir(appWebStore(team))
	var all []appWebConversation
	for _, entry := range entries {
		if id, ok := strings.CutSuffix(entry.Name(), ".json"); ok {
			if conversation, err := loadTeamWebConversation(team, id); err == nil {
				all = append(all, conversation)
			}
		}
	}
	sort.Slice(all, func(i, j int) bool { return appWebNumber(all[i].ID) < appWebNumber(all[j].ID) })
	return all
}

func appWebNumber(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "w-"))
	return n
}

// appWebMirrored maps each message hi copied into the channel to where it
// came from, so the channel's log can say so.
func appWebMirrored(team string) map[string]appChannelWeb {
	mirrored := map[string]appChannelWeb{}
	for _, conversation := range loadTeamWebConversations(team) {
		for _, message := range conversation.Messages {
			if message.SlackTS != "" {
				mirrored[message.SlackTS] = appChannelWeb{Conversation: conversation.ID, Person: conversation.Person, From: message.From}
			}
		}
	}
	for _, feedback := range loadTeamFeedbacks(team) {
		if feedback.SlackTS != "" {
			mirrored[feedback.SlackTS] = appChannelWeb{Conversation: feedback.ID, From: "feedback"}
		}
	}
	return mirrored
}

// ---------------------------------------------------------------------------
// the helper's API, on the web sockets

// appWebPerson is who is signed in, from their key.
type appWebPerson struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// appWebStatus is GET /__hi/api/status.
type appWebStatus struct {
	Team    string `json:"team"`
	Lead    string `json:"lead"`
	Slot    string `json:"slot"`
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
	// Answers counts the team's answers to this browser's feedback.
	Answers int `json:"answers,omitempty"`
	// Docs is where the app's docs are on this address, when its relay
	// sends /docs/ to hi (every relay since v0.81.1 records its stamp).
	Docs string `json:"docs,omitempty"`
	// You is who is signed in with their own link, and Report says they
	// may report bugs as tasks, in place of Feedback (app_report.go).
	You    string `json:"you,omitempty"`
	Report bool   `json:"report,omitempty"`
}

// appWebSend is POST /__hi/api/messages.
type appWebSend struct {
	Conversation string         `json:"conversation,omitempty"`
	Text         string         `json:"text"`
	Page         *appWebPage    `json:"page,omitempty"`
	Refs         []appWebRef    `json:"refs,omitempty"`
	Files        []appWebUpload `json:"files,omitempty"`
}

// listenWeb opens a socket per address for the helper.
func (b *appBroker) listenWeb(ctx context.Context) ([]func() error, error) {
	if err := os.MkdirAll(appWebDir(b.team), 0o700); err != nil {
		return nil, err
	}
	var serves []func() error
	for _, slot := range []string{appSlotPreview, appSlotProduction} {
		serve, err := b.listenWith(ctx, appWebSocket(b.team, slot), b.webHandler(slot))
		if err != nil {
			return nil, err
		}
		serves = append(serves, serve)
	}
	return serves, nil
}

func (b *appBroker) webHandler(slot string) http.Handler {
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
	// An owner's one-time page for the team's Claude token
	// (app_signin_slack.go).
	mux.HandleFunc("GET "+appHelperPrefix+"signin/{id}", b.claudeSigninPage)
	mux.HandleFunc("POST "+appHelperPrefix+"signin/{id}", b.claudeSigninPage)
	mux.HandleFunc("GET "+appHelperPrefix+"helper.js", func(w http.ResponseWriter, r *http.Request) {
		etag := `"` + appHelperVersion() + `"`
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Write(appHelperScript)
	})
	mux.HandleFunc("GET "+appHelperPrefix+"snap.js", func(w http.ResponseWriter, r *http.Request) {
		// modern-screenshot, for the pictures of what the person points at:
		// the helper loads it the first time it needs it.
		etag := `"` + appSnapVersion + `"`
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Write(appSnapSource)
	})
	mux.HandleFunc("GET "+appHelperPrefix+"api/status", func(w http.ResponseWriter, r *http.Request) {
		status := appWebStatus{Team: b.team, Lead: appBotName(b.team) + " lead", Slot: slot}
		if config, err := loadTeam(b.team); err == nil && config.Members["lead"].Name != "" {
			status.Lead = config.Members["lead"].Name
		}
		if state, ok := loadTeamSlot(b.team, slot); ok {
			status.Version, status.Commit = state.Label, state.Commit
			if state.Relay != "" {
				status.Docs = appDocsPrefix
			}
		}
		_, status.Answers = appVisitorFeedback(b.team, r.Header.Get(netExposeVisitorHeader), 0)
		if config, err := loadTeam(b.team); err == nil {
			if person, err := b.webReporter(config, r); err == nil {
				status.You, status.Report = person.Name, true
			}
		}
		reply(w, http.StatusOK, status)
	})
	mux.HandleFunc("POST "+appHelperPrefix+"api/report", func(w http.ResponseWriter, r *http.Request) {
		var send appFeedbackSend
		if err := json.NewDecoder(io.LimitReader(r.Body, appWebMaxUpload*4/3+(1<<20))).Decode(&send); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		sent, status, err := b.webReport(r, slot, send)
		if err != nil {
			failure(w, status, err)
			return
		}
		reply(w, http.StatusOK, sent)
	})
	mux.HandleFunc("POST "+appHelperPrefix+"api/help", func(w http.ResponseWriter, r *http.Request) {
		var ask appHelpAsk
		if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&ask); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		b.webHelp(w, r, slot, ask)
	})
	mux.HandleFunc("POST "+appHelperPrefix+"api/feedback", func(w http.ResponseWriter, r *http.Request) {
		var send appFeedbackSend
		if err := json.NewDecoder(io.LimitReader(r.Body, appWebMaxUpload*4/3+(1<<20))).Decode(&send); err != nil {
			failure(w, http.StatusBadRequest, err)
			return
		}
		feedback, status, err := b.webFeedback(r, slot, appVisitor(w, r), send)
		if err != nil {
			failure(w, status, err)
			return
		}
		reply(w, http.StatusOK, map[string]string{"id": feedback.ID})
	})
	mux.HandleFunc("GET "+appHelperPrefix+"api/feedback", func(w http.ResponseWriter, r *http.Request) {
		sent, _ := appVisitorFeedback(b.team, r.Header.Get(netExposeVisitorHeader), 20)
		reply(w, http.StatusOK, map[string]any{"feedback": append([]appFeedbackSent{}, sent...)})
	})
	mux.Handle("GET "+appDocsPrefix, b.docsHandler(slot))
	mux.Handle("GET "+appLLMsPath, b.llmsHandler(slot, false))
	mux.Handle("GET "+appLLMsFullPath, b.llmsHandler(slot, true))
	return mux
}

// appWebChatSummary is a chat in the helper's history.
type appWebChatSummary struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"` // its first message, clipped
	Started  time.Time `json:"started"`
	Last     time.Time `json:"last"`
	Messages int       `json:"messages"`
	Open     bool      `json:"open,omitempty"` // the chat the panel shows
}

// appWebHistory is how many past chats the helper lists.
const appWebHistory = 30

// appWebProductionNote starts a chat from production, where the person
// uses the app rather than shapes it.
const appWebProductionNote = `(They are on production, the version people rely on. What they ask for goes into dev, as from anywhere: say so, then send them the preview link with hi app web post when it is ready to try, naming the version ("dev, v3 + 1 change"). Production gets it with the next release, which an owner or a developer makes on the dev page; when you ask for it, say what else in dev would go with it. If they only report a problem or ask how something works, answer that; nothing needs to change.)`

// appWebFirstNote starts each web chat for the lead.
const appWebFirstNote = `(This message comes from the helper in the app: a chat panel beside the page the person is on, not Slack. The header in brackets says who it is (signed in through Slack, so checked), which address and version they see, and the page. "Referenced elements" are parts of the page they clicked on to show what they mean: #1, #2 in their words are those; put them in a task, since the owners' agents find them in the code by their text, classes, and html. A picture of each comes with the message, and so do files they attach, listed with their paths in /team/web. Answer in a few short sentences, as in Slack: the panel is narrow. hi copies this chat into a thread in their DM with you (in the channel when they aren't signed in through Slack). Your answer ends when you stop writing; to tell them something here later, run hi app web post <chat> "...". What they ask to change becomes a task for the owners (hi app task add): say its number.)`

// appSlackEscape keeps a person's text from becoming Slack markup.
func appSlackEscape(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
}

// ---------------------------------------------------------------------------
// approvals from the app

// ---------------------------------------------------------------------------
// the lead's API

// appLeadAPI answers a message as the lead, through Hermes' API server in
// its box: /v1/responses, with the conversation named so Hermes keeps its
// history, streamed.
// errTeamLeadUnreachable is a message that never reached the lead, as
// while its box starts: safe to send again.
var errTeamLeadUnreachable = errors.New("the lead isn't reachable yet; try again in a minute")

func appLeadAPI(socket, key string) func(ctx context.Context, conversation, input string, pictures []appLeadPicture, delta func(string)) (string, error) {
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	return func(ctx context.Context, conversation, input string, pictures []appLeadPicture, delta func(string)) (string, error) {
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
		return appReadLeadStream(response.Body, delta)
	}
}

// appReadLeadStream reads the Responses API's events: text deltas, then
// response.completed, or response.failed.
func appReadLeadStream(stream io.Reader, delta func(string)) (string, error) {
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

// appKeepLeadAPI starts the relay to the lead's API server in its box,
// and starts it again whenever the host can't reach it.
func appKeepLeadAPI(engine boxEngine, container, socket string, done <-chan struct{}, stderr io.Writer) {
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
		out, err := engine.output("exec", "-d", container, "/usr/local/bin/hi", "app", "__leadapi", "/team/run/"+appLeadAPISocket, strconv.Itoa(appLeadAPIPort))
		if err != nil && !reported {
			fmt.Fprintf(stderr, "hi: the helper can't reach the lead yet: %s\n", qFirstLine(strings.TrimSpace(out), err.Error()))
			reported = true
		}
	}
}

// ---------------------------------------------------------------------------
// hi app helper

// appHelperRequest turns a slot's helper on or off.
type appHelperRequest struct {
	Slot string `json:"slot"`
	On   bool   `json:"on"`
	// owner is an owner on the dev page, whose click is the yes; no
	// socket sets it.
	owner string
}

// setHelper turns a slot's helper on or off for the lead: the preview's
// when asked, production's with an owner's yes in Slack.
func (b *appBroker) setHelper(request appHelperRequest) (appConfig, error) {
	if request.Slot != appSlotPreview && request.Slot != appSlotProduction {
		return appConfig{}, fmt.Errorf("the helper is on preview or production, not %q", request.Slot)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	config, err := loadTeam(b.team)
	if err != nil {
		return config, err
	}
	var approval appApproval
	if request.owner != "" {
		approval = appApproval{Name: request.owner}
	} else if request.Slot == appSlotProduction {
		return config, errors.New("production's helper is the owners': they turn it on or off on the dev page")
	}
	if err := appSetHelper(&config, request.Slot, request.On); err != nil {
		return config, err
	}
	if request.Slot == appSlotProduction {
		state := "off"
		if request.On {
			state = "on"
		}
		versions, err := loadTeamVersions(b.team)
		if err == nil {
			versions.History = append(versions.History, appVersionEvent{What: "production's helper " + state, Approval: &approval, At: time.Now().UTC()})
			err = saveTeamVersions(b.team, versions)
		}
		if err != nil {
			return config, err
		}
		if b.channel != nil {
			b.channel.post(config.Channel, fmt.Sprintf(":speech_balloon: The helper, help and feedback in the app, is %s in production now. %s approved it.", state, appSlackEscape(approval.Name)))
		}
	}
	return config, nil
}

// appSetHelper saves the setting and puts the helper file in line: the
// next page load has the helper or doesn't.
func appSetHelper(config *appConfig, slot string, on bool) error {
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
	return appSyncHelper(*config)
}

// appHelperCommand is hi app helper <name> [preview|production on|off],
// on the machine: whoever runs the machine needs no approval.
func appHelperCommand(args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi app helper <name> [preview|production on|off]"}
	if len(args) != 1 && len(args) != 3 {
		return usage
	}
	config, err := loadTeam(args[0])
	if err != nil {
		return err
	}
	if len(args) == 3 {
		if (args[1] != appSlotPreview && args[1] != appSlotProduction) || (args[2] != "on" && args[2] != "off") {
			return usage
		}
		if err := appSetHelper(&config, args[1], args[2] == "on"); err != nil {
			return err
		}
	}
	fmt.Fprintln(stdout, appHelperLine(config))
	if len(args) == 3 {
		fmt.Fprintln(stdout, "It applies from the next page load. An address made before hi had the helper gets it when its link is next renewed.")
	}
	return nil
}
