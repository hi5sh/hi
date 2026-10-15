package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

// The lead answers only when someone @-mentions it, but it should know
// everything said in its channel: a link someone found, a video, a wish
// in passing. Hermes drops the messages that don't mention it, so the
// broker keeps the channel's log itself, from the host with the bot's
// token, in channel/: a JSON line per message, a file per day, and the
// files people share. The lead reads it with hi team channel, and a
// Hermes cron job (channel-watch) has it read what's new and keep what
// matters in its memory.

const (
	teamChannelEvery   = 2 * time.Minute
	teamChannelBack    = 14 * 24 * time.Hour // how far back threads are followed, and the first log reaches
	teamChannelMaxFile = 50 << 20

	teamChannelJob    = "channel-watch"
	teamChannelScript = "channel-watch.sh"
)

// teamChannelInBox is the log in the lead's box; tests move it.
var teamChannelInBox = "/team/channel"

// teamChannelMessage is a line of channel/<day>.jsonl.
type teamChannelMessage struct {
	TS     string            `json:"ts"`
	Thread string            `json:"thread,omitempty"` // the thread's first message, for a reply
	User   string            `json:"user,omitempty"`
	Name   string            `json:"name"`
	Bot    bool              `json:"bot,omitempty"` // a bot's, such as the lead's
	Text   string            `json:"text"`
	Files  []teamChannelFile `json:"files,omitempty"`
	// Web is set on hi's copy of a message from the app's chat.
	Web *teamChannelWeb `json:"web,omitempty"`
}

// teamChannelWeb says which web chat a message was copied from.
type teamChannelWeb struct {
	Conversation string `json:"conversation"`
	Person       string `json:"person,omitempty"`
	From         string `json:"from"` // person or lead
}

type teamChannelFile struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
	Size int    `json:"size,omitempty"`
	Path string `json:"path,omitempty"` // under channel/, when it was downloaded
	Link string `json:"link,omitempty"` // Slack's page for it, for people
}

// teamChannelState is channel/.watch.json: how far the log goes.
type teamChannelState struct {
	Latest  string            `json:"latest,omitempty"`  // the newest message outside threads
	Threads map[string]string `json:"threads,omitempty"` // a thread's first message → its newest reply logged
}

// teamChannelSource is Slack; tests replace it.
type teamChannelSource interface {
	history(channel, oldest string) ([]slack.Message, error)
	replies(channel, thread, oldest string) ([]slack.Message, error)
	userName(id string) string
	download(url string, w io.Writer) error
	// post returns the message's timestamp, which update and permalink take.
	post(channel, text string) (string, error)
	// postIn replies in a thread.
	postIn(channel, thread, text string) (string, error)
	update(channel, ts, text string) error
	permalink(channel, ts string) (string, error)
}

type liveTeamChannel struct {
	api   *slack.Client
	names map[string]string
}

func newLiveTeamChannel(botToken string) *liveTeamChannel {
	return &liveTeamChannel{api: slack.New(botToken), names: map[string]string{}}
}

func (l *liveTeamChannel) history(channel, oldest string) ([]slack.Message, error) {
	var all []slack.Message
	cursor := ""
	for page := 0; page < 20; page++ {
		response, err := l.api.GetConversationHistory(&slack.GetConversationHistoryParameters{ChannelID: channel, Oldest: oldest, Limit: 200, Cursor: cursor})
		if err != nil {
			return all, err
		}
		all = append(all, response.Messages...)
		if !response.HasMore || response.ResponseMetaData.NextCursor == "" {
			break
		}
		cursor = response.ResponseMetaData.NextCursor
	}
	return all, nil
}

func (l *liveTeamChannel) replies(channel, thread, oldest string) ([]slack.Message, error) {
	var all []slack.Message
	cursor := ""
	for page := 0; page < 20; page++ {
		messages, more, next, err := l.api.GetConversationReplies(&slack.GetConversationRepliesParameters{ChannelID: channel, Timestamp: thread, Oldest: oldest, Limit: 200, Cursor: cursor})
		if err != nil {
			return all, err
		}
		all = append(all, messages...)
		if !more || next == "" {
			break
		}
		cursor = next
	}
	return all, nil
}

func (l *liveTeamChannel) userName(id string) string {
	if name, ok := l.names[id]; ok {
		return name
	}
	name := id
	if user, err := l.api.GetUserInfo(id); err == nil {
		name = firstNonEmpty(user.Profile.DisplayName, user.RealName, user.Name, id)
	}
	l.names[id] = name
	return name
}

func (l *liveTeamChannel) download(url string, w io.Writer) error { return l.api.GetFile(url, w) }

func (l *liveTeamChannel) post(channel, text string) (string, error) {
	_, ts, err := l.api.PostMessage(channel, slack.MsgOptionText(text, false), slack.MsgOptionDisableLinkUnfurl())
	return ts, err
}

func (l *liveTeamChannel) postIn(channel, thread, text string) (string, error) {
	_, ts, err := l.api.PostMessage(channel, slack.MsgOptionText(text, false), slack.MsgOptionTS(thread), slack.MsgOptionDisableLinkUnfurl())
	return ts, err
}

func (l *liveTeamChannel) update(channel, ts, text string) error {
	_, _, _, err := l.api.UpdateMessage(channel, ts, slack.MsgOptionText(text, false))
	return err
}

func (l *liveTeamChannel) permalink(channel, ts string) (string, error) {
	return l.api.GetPermalink(&slack.PermalinkParameters{Channel: channel, Ts: ts})
}

// teamChannelMu keeps one poll at a time: the watch's and the broker's,
// when it checks an approval that may not be logged yet.
var teamChannelMu sync.Mutex

func teamChannelDir(team string) string { return filepath.Join(teamDir(team), "channel") }

// teamWatchChannel logs the channel until done is closed.
func teamWatchChannel(done <-chan struct{}, team, channel string, source teamChannelSource, stderr io.Writer) {
	last := ""
	for {
		err := teamPollChannel(team, channel, source, time.Now())
		// Each new problem once in the journal, not every two minutes.
		if message := fmt.Sprint(err); err != nil && message != last {
			fmt.Fprintf(stderr, "hi: logging the channel: %v\n", err)
			last = message
		} else if err == nil {
			last = ""
		}
		select {
		case <-done:
			return
		case <-time.After(teamChannelEvery):
		}
	}
}

// teamPollChannel adds what's new in the channel to its log: messages, the
// replies in threads up to two weeks old, and the files shared in them.
func teamPollChannel(team, channel string, source teamChannelSource, now time.Time) error {
	teamChannelMu.Lock()
	defer teamChannelMu.Unlock()
	dir := teamChannelDir(team)
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o700); err != nil {
		return err
	}
	var state teamChannelState
	if data, err := os.ReadFile(filepath.Join(dir, ".watch.json")); err == nil {
		json.Unmarshal(data, &state)
	}
	if state.Threads == nil {
		state.Threads = map[string]string{}
	}
	oldest := teamSlackTS(now.Add(-teamChannelBack))
	messages, err := source.history(channel, oldest)
	if err != nil {
		return err
	}
	sort.Slice(messages, func(i, j int) bool { return teamTSBefore(messages[i].Timestamp, messages[j].Timestamp) })
	var logged []teamChannelMessage
	for _, message := range messages {
		// A reply also sent to the channel comes with its thread.
		if message.ThreadTimestamp != "" && message.ThreadTimestamp != message.Timestamp {
			continue
		}
		if teamTSBefore(state.Latest, message.Timestamp) {
			if entry, ok := teamChannelEntry(dir, message, "", source); ok {
				logged = append(logged, entry)
			}
			state.Latest = message.Timestamp
		}
	}
	var failed error
	for _, message := range messages {
		if message.ReplyCount == 0 {
			continue
		}
		thread := message.Timestamp
		seen := firstNonEmpty(state.Threads[thread], thread)
		if !teamTSBefore(seen, message.LatestReply) {
			continue
		}
		replies, err := source.replies(channel, thread, seen)
		if err != nil {
			failed = err
			continue
		}
		sort.Slice(replies, func(i, j int) bool { return teamTSBefore(replies[i].Timestamp, replies[j].Timestamp) })
		for _, reply := range replies {
			if reply.Timestamp == thread || !teamTSBefore(seen, reply.Timestamp) {
				continue
			}
			if entry, ok := teamChannelEntry(dir, reply, thread, source); ok {
				logged = append(logged, entry)
			}
			seen = reply.Timestamp
		}
		state.Threads[thread] = seen
	}
	for thread := range state.Threads {
		if teamTSBefore(thread, oldest) {
			delete(state.Threads, thread)
		}
	}
	if len(logged) > 0 {
		mirrored := teamWebMirrored(team)
		for i := range logged {
			if web, ok := mirrored[logged[i].TS]; ok {
				logged[i].Web = &web
			}
		}
	}
	if err := teamAppendChannel(dir, logged); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, ".watch.json.tmp"), append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(dir, ".watch.json.tmp"), filepath.Join(dir, ".watch.json")); err != nil {
		return err
	}
	return failed
}

// teamChannelSkip are the messages that say nothing: joins, leaves, topic
// changes, and the like.
var teamChannelSkip = map[string]bool{
	"channel_join": true, "channel_leave": true, "channel_topic": true, "channel_purpose": true,
	"channel_name": true, "group_join": true, "group_leave": true, "bot_add": true, "bot_remove": true,
	"pinned_item": true, "unpinned_item": true, "message_deleted": true,
}

func teamChannelEntry(dir string, message slack.Message, thread string, source teamChannelSource) (teamChannelMessage, bool) {
	if teamChannelSkip[message.SubType] {
		return teamChannelMessage{}, false
	}
	entry := teamChannelMessage{TS: message.Timestamp, Thread: thread, User: message.User,
		Bot: message.BotID != "" || message.SubType == "bot_message"}
	switch {
	case message.User != "":
		entry.Name = source.userName(message.User)
	default:
		entry.Name = message.Username
		if entry.Name == "" && message.BotProfile != nil {
			entry.Name = message.BotProfile.Name
		}
		entry.Name = firstNonEmpty(entry.Name, "a bot")
	}
	entry.Text = teamSlackText(message.Text, source)
	for _, file := range message.Files {
		shared := teamChannelFile{Name: firstNonEmpty(file.Name, file.Title, file.ID), Type: file.Mimetype, Size: file.Size, Link: file.Permalink}
		if url := firstNonEmpty(file.URLPrivateDownload, file.URLPrivate); url != "" && file.Size <= teamChannelMaxFile {
			path := filepath.Join("files", file.ID+"-"+teamSafeFileName(shared.Name))
			if teamDownload(source, url, filepath.Join(dir, path)) == nil {
				shared.Path = path
			}
		}
		entry.Files = append(entry.Files, shared)
	}
	if entry.Text == "" && len(entry.Files) == 0 {
		return entry, false
	}
	return entry, true
}

func teamDownload(source teamChannelSource, url, path string) error {
	if fileExists(path) {
		return nil
	}
	file, err := os.OpenFile(path+".part", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = source.download(url, file)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path + ".part")
		return err
	}
	return os.Rename(path+".part", path)
}

var teamUnsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func teamSafeFileName(name string) string {
	name = strings.Trim(teamUnsafeName.ReplaceAllString(name, "_"), "._")
	if len(name) > 80 {
		name = name[len(name)-80:]
	}
	return firstNonEmpty(name, "file")
}

func teamAppendChannel(dir string, messages []teamChannelMessage) error {
	files := map[string]*os.File{}
	defer func() {
		for _, file := range files {
			file.Close()
		}
	}()
	for _, message := range messages {
		day := teamTSTime(message.TS).Local().Format("2006-01-02")
		file := files[day]
		if file == nil {
			var err error
			if file, err = os.OpenFile(filepath.Join(dir, day+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err != nil {
				return err
			}
			files[day] = file
		}
		data, _ := json.Marshal(message)
		if _, err := file.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	return nil
}

var teamSlackMarkup = regexp.MustCompile(`<([^<>|]+)(?:\|([^<>]*))?>`)

// teamSlackText turns Slack's markup into plain text: <@U1> is @Name,
// <#C1|name> is #name, and <url|label> is label (url).
func teamSlackText(text string, source teamChannelSource) string {
	text = teamSlackMarkup.ReplaceAllStringFunc(text, func(markup string) string {
		parts := teamSlackMarkup.FindStringSubmatch(markup)
		target, label := parts[1], parts[2]
		switch {
		case strings.HasPrefix(target, "@"):
			return "@" + firstNonEmpty(label, source.userName(target[1:]))
		case strings.HasPrefix(target, "#"):
			return "#" + firstNonEmpty(label, target[1:])
		case strings.HasPrefix(target, "!"):
			return "@" + strings.TrimPrefix(firstNonEmpty(label, target[1:]), "@")
		case label != "" && label != target:
			return label + " (" + target + ")"
		}
		return target
	})
	return strings.TrimSpace(strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(text))
}

func teamSlackTS(t time.Time) string {
	return fmt.Sprintf("%d.%06d", t.Unix(), t.Nanosecond()/1000)
}

func teamTSTime(ts string) time.Time {
	seconds, _ := strconv.ParseFloat(ts, 64)
	return time.Unix(int64(seconds), int64((seconds-float64(int64(seconds)))*1e9))
}

// teamTSBefore says whether Slack timestamp a comes before b; "" is before
// everything.
func teamTSBefore(a, b string) bool {
	if a == "" || b == "" {
		return a == "" && b != ""
	}
	secondsA, fractionA, _ := strings.Cut(a, ".")
	secondsB, fractionB, _ := strings.Cut(b, ".")
	if len(secondsA) != len(secondsB) {
		return len(secondsA) < len(secondsB)
	}
	if secondsA != secondsB {
		return secondsA < secondsB
	}
	return fractionA < fractionB
}

// ---------------------------------------------------------------------------
// hi team channel: reading the log

// runTeamChannel prints the log: in the lead's box from /team/channel, on
// the host for a team by name.
func runTeamChannel(args []string, stdout io.Writer) error {
	dir, seenPath := "", ""
	var since time.Duration = 24 * time.Hour
	newOnly, ids := false, false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--new":
			newOnly = true
		case args[i] == "--ids":
			ids = true
		case args[i] == "--since" && i+1 < len(args):
			duration, err := parseTeamSince(args[i+1])
			if err != nil {
				return err
			}
			since = duration
			i++
		case !strings.HasPrefix(args[i], "-") && dir == "":
			if _, err := loadTeam(args[i]); err != nil {
				return err
			}
			dir = teamChannelDir(args[i])
		default:
			return usageError{"usage: hi team channel [<name>] [--since 2h | --new] [--ids]"}
		}
	}
	if dir == "" {
		if !fileExists(teamChannelInBox) {
			return usageError{"usage: hi team channel <name> [--since 2h]"}
		}
		dir = teamChannelInBox
		home, _ := os.UserHomeDir()
		seenPath = filepath.Join(firstNonEmpty(home, "/box/home"), ".hi-team-channel-seen")
	} else if newOnly {
		return errors.New("--new is the lead's: it remembers what the lead has read")
	}
	after := teamSlackTS(time.Now().Add(-since))
	if newOnly {
		after = "0"
		if data, err := os.ReadFile(seenPath); err == nil {
			after = strings.TrimSpace(string(data))
		}
	}
	messages, err := readTeamChannel(dir, after)
	if err != nil {
		return err
	}
	shown := messages
	if newOnly {
		// The lead was in the app's chats it copied here.
		shown = nil
		for _, message := range messages {
			if message.Web == nil {
				shown = append(shown, message)
			}
		}
	}
	printTeamChannel(shown, dir, ids, stdout)
	if newOnly && len(messages) > 0 {
		latest := after
		for _, message := range messages {
			if teamTSBefore(latest, message.TS) {
				latest = message.TS
			}
		}
		return os.WriteFile(seenPath, []byte(latest+"\n"), 0o600)
	}
	if !newOnly && len(messages) == 0 {
		fmt.Fprintln(stdout, "Nothing was said in that time.")
	}
	return nil
}

func parseTeamSince(text string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(text, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n > 0 {
			return time.Duration(n) * 24 * time.Hour, nil
		}
	}
	duration, err := time.ParseDuration(text)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("--since takes a time such as 30m, 2h, or 3d, not %q", text)
	}
	return duration, nil
}

// readTeamChannel reads the messages logged after Slack timestamp after.
func readTeamChannel(dir, after string) ([]teamChannelMessage, error) {
	days, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(days)
	from := teamTSTime(after).Local().AddDate(0, 0, -1).Format("2006-01-02")
	var messages []teamChannelMessage
	for _, day := range days {
		if strings.TrimSuffix(filepath.Base(day), ".jsonl") < from {
			continue
		}
		data, err := os.ReadFile(day)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			var message teamChannelMessage
			if json.Unmarshal([]byte(line), &message) == nil && message.TS != "" && teamTSBefore(after, message.TS) {
				messages = append(messages, message)
			}
		}
	}
	sort.SliceStable(messages, func(i, j int) bool { return teamTSBefore(messages[i].TS, messages[j].TS) })
	return messages, nil
}

func printTeamChannel(messages []teamChannelMessage, dir string, ids bool, w io.Writer) {
	names := map[string]string{}
	for _, message := range messages {
		names[message.TS] = message.Name
	}
	lastDay := ""
	for _, message := range messages {
		at := teamTSTime(message.TS).Local()
		if day := at.Format("Mon 2 Jan 2006"); day != lastDay {
			fmt.Fprintf(w, "— %s —\n", day)
			lastDay = day
		}
		who := message.Name
		if message.Thread != "" {
			started := teamTSTime(message.Thread).Local().Format("Jan 2 15:04")
			if name := names[message.Thread]; name != "" {
				who += fmt.Sprintf(", in %s's thread from %s", name, started)
			} else {
				who += ", in a thread from " + started
			}
		}
		if ids {
			who += " [" + message.TS + "]"
		}
		fmt.Fprintf(w, "%s  %s: %s\n", at.Format("15:04"), who, strings.ReplaceAll(message.Text, "\n", "\n       "))
		for _, file := range message.Files {
			where := firstNonEmpty(file.Link, "not downloaded")
			if file.Path != "" {
				where = filepath.Join(dir, file.Path)
			}
			fmt.Fprintf(w, "       [file: %s, %s, %s: %s]\n", file.Name, firstNonEmpty(file.Type, "unknown type"), formatTeamSize(file.Size), where)
		}
	}
}

func formatTeamSize(size int) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%d KB", size>>10)
	}
	return fmt.Sprintf("%d bytes", size)
}
