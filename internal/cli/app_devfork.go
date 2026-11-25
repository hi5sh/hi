package cli

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// Fork, as in Orca's chat: a new chat from one of the agent's responses.
// hi copies the conversation (Claude Code's transcript) up to the end of
// that response's turn into a new session, beside the old one, and
// resumes the new one in the workspace. The old chat stays, to resume
// from the session menu. Claude Code's only: Codex's sessions aren't
// copied this way.

// appDevForkRecord is what the fork reads of a transcript's line.
type appDevForkRecord struct {
	UUID        string `json:"uuid"`
	ParentUUID  string `json:"parentUuid"`
	Type        string `json:"type"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// prompt says the record is a person's message, which starts a turn: not
// a tool's result, not hi's or Claude Code's own.
func (r appDevForkRecord) prompt() bool {
	if r.Type != "user" || r.IsMeta || r.IsSidechain {
		return false
	}
	var text string
	if json.Unmarshal(r.Message.Content, &text) == nil {
		return strings.TrimSpace(text) != ""
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(r.Message.Content, &blocks) != nil {
		return false
	}
	said := false
	for _, block := range blocks {
		if block.Type == "tool_result" {
			return false
		}
		said = said || block.Type == "text" || block.Type == "image"
	}
	return said
}

// appDevFork is a transcript cut after the turn of the message anchor: the
// conversation as it is now (the branch to its newest message), from its
// start to the record before the next person's message, with its session
// id made newID. Lines without a message (Claude Code's file snapshots)
// before the cut stay, for a rewind in the new chat.
func appDevFork(data []byte, oldID, newID, anchor string) ([]byte, error) {
	type line struct {
		raw    []byte
		record appDevForkRecord
	}
	var lines []line
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64<<10), 64<<20)
	byID := map[string]int{}
	leaf := ""
	for scanner.Scan() {
		raw := append([]byte{}, scanner.Bytes()...)
		var record appDevForkRecord
		if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &record) != nil {
			continue
		}
		lines = append(lines, line{raw, record})
		if record.UUID != "" && !record.IsSidechain {
			byID[record.UUID] = len(lines) - 1
			leaf = record.UUID
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	// The conversation as it is now: from its newest message back.
	var chain []int
	seen := map[string]bool{}
	for id := leaf; id != "" && !seen[id]; {
		seen[id] = true
		at, ok := byID[id]
		if !ok {
			break
		}
		chain = append(chain, at)
		id = lines[at].record.ParentUUID
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	start := -1
	for i, at := range chain {
		if lines[at].record.UUID == anchor {
			start = i
		}
	}
	if start < 0 {
		return nil, errors.New("that response isn't in the conversation now: it was rewound")
	}
	end := len(chain)
	for i := start + 1; i < len(chain); i++ {
		if lines[chain[i]].record.prompt() {
			end = i
			break
		}
	}
	keep := map[int]bool{}
	for _, at := range chain[:end] {
		keep[at] = true
	}
	last := chain[end-1]
	var out bytes.Buffer
	from, to := []byte(`"sessionId":"`+oldID+`"`), []byte(`"sessionId":"`+newID+`"`)
	for i, l := range lines {
		if keep[i] || (l.record.UUID == "" && i < last) {
			out.Write(bytes.ReplaceAll(l.raw, from, to))
			out.WriteByte('\n')
		}
	}
	return out.Bytes(), nil
}

// appDevNewSessionID is a session id as Claude Code makes them: a UUID.
func appDevNewSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// devChatFork forks the workspace's Claude Code session after the turn of
// message anchor, and resumes the new session there. It answers the new
// session's id.
func (b *appBroker) devChatFork(workspace, anchor string) (string, error) {
	agent, session, err := devChatAgent(b.team, workspace)
	switch {
	case err != nil:
		return "", err
	case agent.Running && agent.Agent != "claude":
		return "", errors.New("fork is for Claude Code's sessions")
	case agent.Status == "working" || agent.Status == "blocked":
		return "", errors.New("the agent is at work: stop it first, or wait until it's done, then fork")
	case session == "" || path.Ext(session) != ".jsonl" || !strings.Contains(session, "/.claude/projects/"):
		return "", errors.New("hi can't find this chat's Claude Code session")
	case agent.Pane == "":
		return "", errors.New("the workspace has no pane to run the agent in")
	}
	host, ok := appDevHostPath(b.team, session)
	if !ok {
		return "", fmt.Errorf("%s isn't in the dev box", session)
	}
	data, err := boxedReadFileLimit(appDevHome(b.team), host, 64<<20)
	if err != nil {
		return "", err
	}
	id := appDevNewSessionID()
	forked, err := appDevFork(data, strings.TrimSuffix(path.Base(session), ".jsonl"), id, anchor)
	if err != nil {
		return "", err
	}
	hostDir, _ := appDevHostPath(b.team, path.Dir(session))
	if err := boxedWriteFile(appDevHome(b.team), filepath.Join(hostDir, id+".jsonl"), forked, 0o600); err != nil {
		return "", err
	}
	if agent.Running {
		if agent, err = appDevStopAgent(b.team, workspace, agent); err != nil {
			return "", err
		}
	}
	appDevTrust(b.team, "claude", appDevSpaceCWD(b.team, workspace))
	return id, appDevHerdrDo(b.team, "pane", "run", agent.Pane, "claude --resume "+id)
}
