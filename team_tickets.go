package main

// A team's tickets (docs/specs/approved/hi_team_dev.md, "Tickets"): where
// the team's repository is, read from its remote. On github.com they are
// GitHub Issues, through gh; on gitlab.com or a GitLab of one's own,
// GitLab issues, through glab; elsewhere, hi's own in tickets/. hi runs gh
// and glab on the machine, with its sign-in, so no box holds a forge
// token. hi team task on the machine and hi dev task in the dev box
// are the same commands.

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

type teamTicket struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"` // open or closed
	Body   string `json:"body,omitempty"`
	Author string `json:"author,omitempty"`
	// Asked is who asked for it, when its text says: the lead files a
	// channel's asks, and the dev page an owner's, as the forge's account.
	Asked   string    `json:"asked,omitempty"`
	URL     string    `json:"url,omitempty"`
	Labels  []string  `json:"labels,omitempty"`
	Created time.Time `json:"created,omitzero"`
	// Edited is when its title or text last changed, on hi's own tickets.
	Edited time.Time `json:"edited,omitzero"`
	// Updated is when anything about it last changed: its text, a comment.
	Updated  time.Time           `json:"updated,omitzero"`
	Comments []teamTicketComment `json:"comments,omitempty"`
	// Files are what was attached, kept on the team's machine
	// (tickets/files/<n>/), whichever forge has the ticket.
	Files []string `json:"files,omitempty"`
}

type teamTicketComment struct {
	// ID names the comment where it's kept, for deleting it: GitHub's
	// own, or for hi's own tickets the time it was written.
	ID     string    `json:"id,omitempty"`
	Author string    `json:"author,omitempty"`
	Body   string    `json:"body"`
	At     time.Time `json:"at,omitzero"`
	Edited time.Time `json:"edited,omitzero"`
}

// teamTicketRequest is one ticket command: list, show, add, comment, or
// close.
type teamTicketRequest struct {
	Action string `json:"action"`
	Number int    `json:"number,omitempty"`
	Title  string `json:"title,omitempty"`
	Body   string `json:"body,omitempty"`
	Closed bool   `json:"closed,omitempty"` // list closed ones too
	// Author is who files or comments on hi's own tickets.
	Author string `json:"author,omitempty"`
	// Files go with an add or a comment: a picture someone shared, a PDF.
	Files []teamAttachment `json:"files,omitempty"`
	// Chat and Thread are the Slack conversation the lead files from, as
	// Hermes tells its commands: hi attaches the files shared in it.
	Chat   string `json:"chat,omitempty"`
	Thread string `json:"thread,omitempty"`
	// Comment is the comment an owner deletes (delete-comment), and Name
	// the file (delete-file).
	Comment string `json:"comment,omitempty"`
	Name    string `json:"name,omitempty"`
}

// teamTicketOwnerActions are the dev page's, for the owners only: neither
// the lead nor the dev box sends them.
var teamTicketOwnerActions = map[string]bool{"delete-comment": true, "delete-file": true, "edit-comment": true, "reopen": true}

type teamTicketResult struct {
	Forge   string       `json:"forge"` // github, gitlab, or hi
	Where   string       `json:"where,omitempty"`
	Tickets []teamTicket `json:"tickets,omitempty"`
	Ticket  *teamTicket  `json:"ticket,omitempty"`
	// Files are a ticket's attachments, for files.
	Files []teamAttachment `json:"attachments,omitempty"`
}

// teamForge is where a team's tickets are.
type teamForge struct {
	Kind string // github, gitlab, or hi
	Host string
	Repo string // owner/name, or group/subgroup/name
}

// teamRemotePattern takes the host and path out of a git remote: https,
// ssh://, or scp-like git@host:path.
var teamRemotePattern = regexp.MustCompile(`^(?:https?://(?:[^@/]+@)?|ssh://(?:[^@/]+@)?|[^@/:]+@)([^/:]+)(?::\d+)?[/:](.+?)(?:\.git)?/?$`)

func teamTicketForge(team string) teamForge {
	config, err := loadTeam(team)
	if err != nil {
		return teamForge{Kind: "hi"}
	}
	match := teamRemotePattern.FindStringSubmatch(strings.TrimSpace(config.Remote))
	if match == nil {
		return teamForge{Kind: "hi"}
	}
	host, repo := strings.ToLower(match[1]), match[2]
	switch {
	case host == "github.com":
		return teamForge{Kind: "github", Host: host, Repo: repo}
	case strings.Contains(host, "gitlab"):
		return teamForge{Kind: "gitlab", Host: host, Repo: repo}
	}
	return teamForge{Kind: "hi"}
}

// teamForgeRun runs gh or glab on the machine; tests replace it.
var teamForgeRun = func(env []string, name string, args ...string) (string, error) {
	command := exec.Command(name, args...)
	command.Env = append(os.Environ(), env...)
	out, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return string(out), fmt.Errorf("%s: %s", name, qFirstLine(string(exit.Stderr), err.Error()))
		}
		return string(out), fmt.Errorf("%s: %w", name, err)
	}
	return string(out), nil
}

// teamTicketFilesDir holds a ticket's attachments. hi keeps them on the
// machine for every forge: gh can't attach a file to an issue.
func teamTicketFilesDir(team string, number int) string {
	return filepath.Join(teamTicketsDir(team), "files", strconv.Itoa(number))
}

func teamTicketFileNames(team string, number int) []string {
	entries, _ := os.ReadDir(teamTicketFilesDir(team, number))
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			names = append(names, entry.Name())
		}
	}
	return names
}

// teamSaveTicketFiles keeps a ticket's new attachments, and says what was
// attached, for the ticket's text.
func teamSaveTicketFiles(team string, number int, files []teamAttachment) (string, error) {
	if len(files) == 0 {
		return "", nil
	}
	dir := teamTicketFilesDir(team, number)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	var saved []string
	for _, file := range files {
		name := teamUniqueFileName(dir, filepath.Base(strings.TrimSpace(file.Name)))
		if err := os.WriteFile(filepath.Join(dir, name), file.Data, 0o600); err != nil {
			return "", err
		}
		saved = append(saved, fmt.Sprintf("%s (%s)", name, teamDataSize(int64(len(file.Data)))))
	}
	return fmt.Sprintf("Attached: %s. hi keeps them with the team; in the dev box, hi dev task files %d fetches them.", strings.Join(saved, ", "), number), nil
}

// teamTicketDo runs a ticket command where the team's tickets are, and
// keeps their attachments on the machine.
func teamTicketDo(team string, request teamTicketRequest) (teamTicketResult, error) {
	result, err := teamTicketDoOnce(team, request)
	for i := range result.Tickets {
		result.Tickets[i].Asked = teamTicketAsker(result.Tickets[i].Body)
	}
	if result.Ticket != nil {
		result.Ticket.Asked = teamTicketAsker(result.Ticket.Body)
	}
	if err == nil && request.Action != "list" && request.Action != "show" && request.Action != "files" {
		// The dev page's list shows a change at once, whoever made it.
		teamDevTicketsCache.Lock()
		teamDevTicketsCache.at = time.Time{}
		teamDevTicketsCache.Unlock()
	}
	return result, err
}

var teamTicketAskerLine = regexp.MustCompile(`(?m)^(?:Asked by (.+?) in the channel|Filed by (.+?) on the dev page)\b`)

// teamTicketAsker is who a ticket's text says asked for it: the last
// "Asked by Anna in the channel" or "Filed by Anna on the dev page".
func teamTicketAsker(body string) string {
	found := teamTicketAskerLine.FindAllStringSubmatch(body, -1)
	if len(found) == 0 {
		return ""
	}
	last := found[len(found)-1]
	return strings.TrimSpace(last[1] + last[2])
}

func teamTicketDoOnce(team string, request teamTicketRequest) (teamTicketResult, error) {
	total := 0
	for _, file := range request.Files {
		total += len(file.Data)
		if len(file.Data) > teamMaxAttachment {
			return teamTicketResult{}, fmt.Errorf("%s is larger than %d MB", file.Name, teamMaxAttachment>>20)
		}
	}
	if total > teamMaxAttachments {
		return teamTicketResult{}, fmt.Errorf("the files come to more than %d MB", teamMaxAttachments>>20)
	}
	switch request.Action {
	case "delete-file":
		if !slices.Contains(teamTicketFileNames(team, request.Number), request.Name) {
			return teamTicketResult{}, fmt.Errorf("task #%d has no file %q", request.Number, request.Name)
		}
		return teamTicketResult{Ticket: &teamTicket{Number: request.Number}}, os.Remove(filepath.Join(teamTicketFilesDir(team, request.Number), request.Name))
	case "edit-comment":
		// What it attached stays said, so deleting it still takes its files.
		shown, err := teamTicketForgeDo(team, teamTicketRequest{Action: "show", Number: request.Number})
		if err != nil {
			return shown, err
		}
		for _, comment := range shown.Ticket.Comments {
			at := teamAttachedLine.FindStringIndex(comment.Body)
			if comment.ID != request.Comment || at == nil {
				continue
			}
			line, _, _ := strings.Cut(comment.Body[at[0]:], "\n")
			if !strings.Contains(request.Body, line) {
				request.Body = strings.TrimSpace(request.Body) + "\n\n" + line
			}
		}
	case "delete-comment":
		// The files the comment attached go with it.
		shown, err := teamTicketForgeDo(team, teamTicketRequest{Action: "show", Number: request.Number})
		if err != nil {
			return shown, err
		}
		var attached []string
		for _, comment := range shown.Ticket.Comments {
			if comment.ID == request.Comment {
				attached = teamAttachedNames(comment.Body)
			}
		}
		result, err := teamTicketForgeDo(team, request)
		if err != nil {
			return result, err
		}
		for _, name := range attached {
			if slices.Contains(teamTicketFileNames(team, request.Number), name) {
				os.Remove(filepath.Join(teamTicketFilesDir(team, request.Number), name))
			}
		}
		return result, nil
	case "files":
		var result teamTicketResult
		for _, name := range teamTicketFileNames(team, request.Number) {
			data, err := os.ReadFile(filepath.Join(teamTicketFilesDir(team, request.Number), name))
			if err != nil {
				return result, err
			}
			result.Files = append(result.Files, teamAttachment{Name: name, Data: data})
		}
		if len(result.Files) == 0 {
			return result, fmt.Errorf("task #%d has no files", request.Number)
		}
		return result, nil
	case "comment":
		if len(request.Files) > 0 {
			note, err := teamSaveTicketFiles(team, request.Number, request.Files)
			if err != nil {
				return teamTicketResult{}, err
			}
			request.Body = strings.TrimSpace(request.Body + "\n\n" + note)
		}
	}
	result, err := teamTicketForgeDo(team, request)
	if err == nil && request.Action == "add" && len(request.Files) > 0 && result.Ticket != nil {
		// Its number comes once it's filed: a comment says what's attached.
		note, saveErr := teamSaveTicketFiles(team, result.Ticket.Number, request.Files)
		if err = saveErr; err == nil {
			_, err = teamTicketForgeDo(team, teamTicketRequest{Action: "comment", Number: result.Ticket.Number, Body: note, Author: request.Author})
		}
	}
	if result.Ticket == nil && request.Action == "comment" && err == nil {
		result.Ticket = &teamTicket{Number: request.Number}
	}
	if result.Ticket != nil {
		result.Ticket.Files = teamTicketFileNames(team, result.Ticket.Number)
	}
	return result, err
}

func teamTicketForgeDo(team string, request teamTicketRequest) (teamTicketResult, error) {
	forge := teamTicketForge(team)
	result := teamTicketResult{Forge: forge.Kind, Where: forge.Repo}
	var err error
	switch forge.Kind {
	case "github":
		err = forge.github(request, &result)
	case "gitlab":
		err = forge.gitlab(request, &result)
	default:
		result.Where = "hi's own tasks"
		err = teamLocalTickets(team, request, &result)
	}
	return result, err
}

var teamTicketNumberAtEnd = regexp.MustCompile(`/(\d+)\s*$`)

func (f teamForge) github(request teamTicketRequest, result *teamTicketResult) error {
	run := func(args ...string) (string, error) {
		return teamForgeRun(nil, "gh", append(args, "-R", f.Repo)...)
	}
	type ghTicket struct {
		Number    int                     `json:"number"`
		Title     string                  `json:"title"`
		State     string                  `json:"state"`
		Body      string                  `json:"body"`
		URL       string                  `json:"url"`
		CreatedAt time.Time               `json:"createdAt"`
		UpdatedAt time.Time               `json:"updatedAt"`
		Author    struct{ Login string }  `json:"author"`
		Labels    []struct{ Name string } `json:"labels"`
		Comments  []struct {
			ID        string                 `json:"id"`
			Author    struct{ Login string } `json:"author"`
			Body      string                 `json:"body"`
			CreatedAt time.Time              `json:"createdAt"`
		} `json:"comments"`
	}
	convert := func(g ghTicket) teamTicket {
		ticket := teamTicket{Number: g.Number, Title: g.Title, State: strings.ToLower(g.State), Body: g.Body, URL: g.URL, Author: g.Author.Login, Created: g.CreatedAt, Updated: g.UpdatedAt}
		for _, label := range g.Labels {
			ticket.Labels = append(ticket.Labels, label.Name)
		}
		for _, comment := range g.Comments {
			ticket.Comments = append(ticket.Comments, teamTicketComment{ID: comment.ID, Author: comment.Author.Login, Body: comment.Body, At: comment.CreatedAt})
		}
		return ticket
	}
	switch request.Action {
	case "list":
		state := "open"
		if request.Closed {
			state = "all"
		}
		out, err := run("issue", "list", "--state", state, "--limit", "100", "--json", "number,title,state,labels,author,url,createdAt,updatedAt")
		if err != nil {
			return err
		}
		var tickets []ghTicket
		if err := json.Unmarshal([]byte(out), &tickets); err != nil {
			return fmt.Errorf("gh's answer: %w", err)
		}
		for _, ticket := range tickets {
			result.Tickets = append(result.Tickets, convert(ticket))
		}
	case "show":
		out, err := run("issue", "view", strconv.Itoa(request.Number), "--json", "number,title,state,body,labels,author,url,createdAt,comments")
		if err != nil {
			return err
		}
		var ticket ghTicket
		if err := json.Unmarshal([]byte(out), &ticket); err != nil {
			return fmt.Errorf("gh's answer: %w", err)
		}
		converted := convert(ticket)
		result.Ticket = &converted
	case "add":
		out, err := run("issue", "create", "--title", request.Title, "--body", request.Body)
		if err != nil {
			return err
		}
		ticket := teamTicket{Title: request.Title, State: "open", URL: strings.TrimSpace(out)}
		if match := teamTicketNumberAtEnd.FindStringSubmatch(out); match != nil {
			ticket.Number, _ = strconv.Atoi(match[1])
		}
		result.Ticket = &ticket
	case "comment":
		if _, err := run("issue", "comment", strconv.Itoa(request.Number), "--body", request.Body); err != nil {
			return err
		}
		result.Ticket = &teamTicket{Number: request.Number}
	case "edit":
		args := []string{"issue", "edit", strconv.Itoa(request.Number), "--body", request.Body}
		if request.Title != "" {
			args = append(args, "--title", request.Title)
		}
		if _, err := run(args...); err != nil {
			return err
		}
		result.Ticket = &teamTicket{Number: request.Number}
	case "edit-comment":
		if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(request.Comment) {
			return fmt.Errorf("%q isn't a comment", request.Comment)
		}
		if _, err := teamForgeRun(nil, "gh", "api", "graphql", "-f", "query=mutation($id: ID!, $body: String!) { updateIssueComment(input: {id: $id, body: $body}) { clientMutationId } }", "-f", "id="+request.Comment, "-f", "body="+request.Body); err != nil {
			return err
		}
		result.Ticket = &teamTicket{Number: request.Number}
	case "delete-comment":
		// GitHub's comment IDs are its GraphQL node IDs.
		if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(request.Comment) {
			return fmt.Errorf("%q isn't a comment", request.Comment)
		}
		if _, err := teamForgeRun(nil, "gh", "api", "graphql", "-f", "query=mutation($id: ID!) { deleteIssueComment(input: {id: $id}) { clientMutationId } }", "-f", "id="+request.Comment); err != nil {
			return err
		}
		result.Ticket = &teamTicket{Number: request.Number}
	case "close":
		args := []string{"issue", "close", strconv.Itoa(request.Number)}
		if request.Body != "" {
			args = append(args, "--comment", request.Body)
		}
		if _, err := run(args...); err != nil {
			return err
		}
		result.Ticket = &teamTicket{Number: request.Number, State: "closed"}
	case "reopen":
		args := []string{"issue", "reopen", strconv.Itoa(request.Number)}
		if request.Body != "" {
			args = append(args, "--comment", request.Body)
		}
		if _, err := run(args...); err != nil {
			return err
		}
		result.Ticket = &teamTicket{Number: request.Number, State: "open"}
	default:
		return fmt.Errorf("no task command %q", request.Action)
	}
	return nil
}

func (f teamForge) gitlab(request teamTicketRequest, result *teamTicketResult) error {
	var env []string
	if f.Host != "gitlab.com" {
		env = append(env, "GITLAB_HOST="+f.Host)
	}
	run := func(args ...string) (string, error) {
		return teamForgeRun(env, "glab", append(args, "-R", f.Repo)...)
	}
	type glTicket struct {
		IID         int       `json:"iid"`
		Title       string    `json:"title"`
		State       string    `json:"state"`
		Description string    `json:"description"`
		WebURL      string    `json:"web_url"`
		CreatedAt   time.Time `json:"created_at"`
		UpdatedAt   time.Time `json:"updated_at"`
		Labels      []string  `json:"labels"`
		Author      struct {
			Username string `json:"username"`
		} `json:"author"`
	}
	convert := func(g glTicket) teamTicket {
		state := g.State
		if state == "opened" {
			state = "open"
		}
		return teamTicket{Number: g.IID, Title: g.Title, State: state, Body: g.Description, URL: g.WebURL, Author: g.Author.Username, Labels: g.Labels, Created: g.CreatedAt, Updated: g.UpdatedAt}
	}
	switch request.Action {
	case "list":
		args := []string{"issue", "list", "--output", "json", "--per-page", "100"}
		if request.Closed {
			args = append(args, "--all")
		}
		out, err := run(args...)
		if err != nil {
			return err
		}
		var tickets []glTicket
		if err := json.Unmarshal([]byte(out), &tickets); err != nil {
			return fmt.Errorf("glab's answer: %w", err)
		}
		for _, ticket := range tickets {
			result.Tickets = append(result.Tickets, convert(ticket))
		}
	case "show":
		out, err := run("issue", "view", strconv.Itoa(request.Number), "--output", "json")
		if err != nil {
			return err
		}
		var ticket glTicket
		if err := json.Unmarshal([]byte(out), &ticket); err != nil {
			return fmt.Errorf("glab's answer: %w", err)
		}
		converted := convert(ticket)
		result.Ticket = &converted
	case "add":
		out, err := run("issue", "create", "--title", request.Title, "--description", request.Body, "--yes")
		if err != nil {
			return err
		}
		ticket := teamTicket{Title: request.Title, State: "open"}
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "http") {
				ticket.URL = strings.TrimSpace(line)
			}
		}
		if match := teamTicketNumberAtEnd.FindStringSubmatch(ticket.URL); match != nil {
			ticket.Number, _ = strconv.Atoi(match[1])
		}
		result.Ticket = &ticket
	case "comment":
		if _, err := run("issue", "note", strconv.Itoa(request.Number), "-m", request.Body); err != nil {
			return err
		}
		result.Ticket = &teamTicket{Number: request.Number}
	case "close":
		if request.Body != "" {
			if _, err := run("issue", "note", strconv.Itoa(request.Number), "-m", request.Body); err != nil {
				return err
			}
		}
		if _, err := run("issue", "close", strconv.Itoa(request.Number)); err != nil {
			return err
		}
		result.Ticket = &teamTicket{Number: request.Number, State: "closed"}
	case "reopen":
		if request.Body != "" {
			if _, err := run("issue", "note", strconv.Itoa(request.Number), "-m", request.Body); err != nil {
				return err
			}
		}
		if _, err := run("issue", "reopen", strconv.Itoa(request.Number)); err != nil {
			return err
		}
		result.Ticket = &teamTicket{Number: request.Number, State: "open"}
	case "edit":
		args := []string{"issue", "update", strconv.Itoa(request.Number), "--description", request.Body}
		if request.Title != "" {
			args = append(args, "--title", request.Title)
		}
		if _, err := run(args...); err != nil {
			return err
		}
		result.Ticket = &teamTicket{Number: request.Number}
	case "delete-comment", "edit-comment":
		return errors.New("hi doesn't show GitLab's comments yet: change it on GitLab")
	default:
		return fmt.Errorf("no task command %q", request.Action)
	}
	return nil
}

// ---------------------------------------------------------------------------
// hi's own tickets, in tickets/<n>.json

func teamTicketsDir(team string) string { return filepath.Join(teamDir(team), "tickets") }

func loadTeamLocalTickets(team string) []teamTicket {
	entries, _ := os.ReadDir(teamTicketsDir(team))
	var tickets []teamTicket
	for _, entry := range entries {
		var ticket teamTicket
		if data, err := os.ReadFile(filepath.Join(teamTicketsDir(team), entry.Name())); err == nil && json.Unmarshal(data, &ticket) == nil && ticket.Number > 0 {
			tickets = append(tickets, ticket)
		}
	}
	sort.Slice(tickets, func(i, j int) bool { return tickets[i].Number < tickets[j].Number })
	return tickets
}

func saveTeamLocalTicket(team string, ticket teamTicket) error {
	if err := os.MkdirAll(teamTicketsDir(team), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(ticket, "", "  ")
	return writeFileAtomic(filepath.Join(teamTicketsDir(team), fmt.Sprintf("%d.json", ticket.Number)), append(data, '\n'), 0o600)
}

func teamLocalTickets(team string, request teamTicketRequest, result *teamTicketResult) error {
	tickets := loadTeamLocalTickets(team)
	find := func() (teamTicket, error) {
		for _, ticket := range tickets {
			if ticket.Number == request.Number {
				return ticket, nil
			}
		}
		return teamTicket{}, fmt.Errorf("no task #%d", request.Number)
	}
	now := time.Now().UTC()
	switch request.Action {
	case "list":
		for _, ticket := range tickets {
			if request.Closed || ticket.State != "closed" {
				ticket.Updated = ticket.Created
				for _, at := range append([]time.Time{ticket.Edited}, teamCommentTimes(ticket.Comments)...) {
					if at.After(ticket.Updated) {
						ticket.Updated = at
					}
				}
				ticket.Comments, ticket.Body = nil, ""
				result.Tickets = append(result.Tickets, ticket)
			}
		}
	case "show":
		ticket, err := find()
		if err != nil {
			return err
		}
		for i := range ticket.Comments {
			ticket.Comments[i].ID = teamLocalCommentID(ticket.Comments[i])
		}
		result.Ticket = &ticket
	case "edit":
		ticket, err := find()
		if err != nil {
			return err
		}
		if request.Title != "" {
			ticket.Title = request.Title
		}
		ticket.Body, ticket.Edited = request.Body, now
		if err := saveTeamLocalTicket(team, ticket); err != nil {
			return err
		}
		result.Ticket = &ticket
	case "edit-comment":
		ticket, err := find()
		if err != nil {
			return err
		}
		found := false
		for i, comment := range ticket.Comments {
			if teamLocalCommentID(comment) == request.Comment {
				ticket.Comments[i].Body, ticket.Comments[i].Edited, found = request.Body, now, true
			}
		}
		if !found {
			return fmt.Errorf("task #%d has no such comment", request.Number)
		}
		if err := saveTeamLocalTicket(team, ticket); err != nil {
			return err
		}
		result.Ticket = &ticket
	case "delete-comment":
		ticket, err := find()
		if err != nil {
			return err
		}
		kept := ticket.Comments[:0]
		for _, comment := range ticket.Comments {
			if teamLocalCommentID(comment) != request.Comment {
				kept = append(kept, comment)
			}
		}
		if len(kept) == len(ticket.Comments) {
			return fmt.Errorf("task #%d has no such comment", request.Number)
		}
		ticket.Comments = kept
		if err := saveTeamLocalTicket(team, ticket); err != nil {
			return err
		}
		result.Ticket = &ticket
	case "add":
		n := 1
		if len(tickets) > 0 {
			n = tickets[len(tickets)-1].Number + 1
		}
		ticket := teamTicket{Number: n, Title: request.Title, State: "open", Body: request.Body, Author: request.Author, Created: now}
		if err := saveTeamLocalTicket(team, ticket); err != nil {
			return err
		}
		result.Ticket = &ticket
	case "comment", "close", "reopen":
		ticket, err := find()
		if err != nil {
			return err
		}
		if request.Body != "" {
			ticket.Comments = append(ticket.Comments, teamTicketComment{Author: request.Author, Body: request.Body, At: now})
		}
		switch request.Action {
		case "close":
			ticket.State = "closed"
		case "reopen":
			ticket.State = "open"
		}
		if err := saveTeamLocalTicket(team, ticket); err != nil {
			return err
		}
		result.Ticket = &ticket
	default:
		return fmt.Errorf("no task command %q", request.Action)
	}
	return nil
}

// ---------------------------------------------------------------------------
// the commands

const teamTicketUsage = "usage: task list [--closed] | show <n> | add <title> [<body|->] [--attach <file>]... | comment <n> <text|-> [--attach <file>]... | edit <n> [--title <title>] <body|-> | close <n> [<comment>] | files <n> | start <n> (the lead, when an owner asks)"

func parseTeamTicketArgs(args []string, stdin io.Reader) (teamTicketRequest, error) {
	usage := usageError{teamTicketUsage}
	if len(args) == 0 {
		return teamTicketRequest{Action: "list"}, nil
	}
	request := teamTicketRequest{Action: args[0]}
	// --attach <file>, repeated, with an add or a comment.
	var kept []string
	for i := 0; i < len(args); i++ {
		if args[i] != "--attach" {
			kept = append(kept, args[i])
			continue
		}
		if i+1 >= len(args) || (request.Action != "add" && request.Action != "comment") {
			return request, usage
		}
		i++
		file, err := teamReadAttachment(args[i])
		if err != nil {
			return request, err
		}
		request.Files = append(request.Files, file)
	}
	args = kept
	number := func(word string) error {
		n, err := strconv.Atoi(strings.TrimPrefix(word, "#"))
		if err != nil || n < 1 {
			return fmt.Errorf("a task's number, such as 12, not %q", word)
		}
		request.Number = n
		return nil
	}
	rest := args[1:]
	switch request.Action {
	case "list":
		for _, arg := range rest {
			if arg != "--closed" && arg != "--all" {
				return request, usage
			}
			request.Closed = true
		}
	case "show", "files":
		if len(rest) != 1 {
			return request, usage
		}
		return request, number(rest[0])
	case "add":
		if len(rest) == 0 || len(rest) > 2 {
			return request, usage
		}
		request.Title = strings.TrimSpace(rest[0])
		if len(rest) == 2 {
			body, err := teamDevText(rest[1:], stdin)
			if err != nil {
				return request, err
			}
			request.Body = body
		}
		if request.Title == "" {
			return request, errors.New("a task needs a title")
		}
	case "comment":
		if len(rest) < 1 || len(rest) < 2 && len(request.Files) == 0 {
			return request, usage
		}
		if err := number(rest[0]); err != nil {
			return request, err
		}
		if len(rest) > 1 {
			body, err := teamDevText(rest[1:], stdin)
			if err != nil {
				return request, err
			}
			request.Body = body
		}
	case "edit":
		// edit <n> [--title <title>] <body|body file|->: the whole new text.
		if len(rest) >= 3 && rest[1] == "--title" {
			request.Title = strings.TrimSpace(rest[2])
			rest = append(rest[:1:1], rest[3:]...)
		}
		if len(rest) != 2 {
			return request, usage
		}
		if err := number(rest[0]); err != nil {
			return request, err
		}
		body, err := teamDevText(rest[1:], stdin)
		if err != nil {
			return request, err
		}
		request.Body = body
	case "close":
		if len(rest) < 1 {
			return request, usage
		}
		if err := number(rest[0]); err != nil {
			return request, err
		}
		request.Body = strings.TrimSpace(strings.Join(rest[1:], " "))
	default:
		return request, usage
	}
	return request, nil
}

func printTeamTicketResult(request teamTicketRequest, result teamTicketResult, w io.Writer) {
	switch request.Action {
	case "list":
		if len(result.Tickets) == 0 {
			fmt.Fprintf(w, "No open tasks (%s).\n", result.Where)
			return
		}
		fmt.Fprintf(w, "Tasks (%s):\n", result.Where)
		for _, ticket := range result.Tickets {
			state := ""
			if ticket.State == "closed" {
				state = " (closed)"
			}
			labels := ""
			if len(ticket.Labels) > 0 {
				labels = " [" + strings.Join(ticket.Labels, ", ") + "]"
			}
			fmt.Fprintf(w, "  #%-5d %s%s%s\n", ticket.Number, ticket.Title, labels, state)
		}
	case "show":
		ticket := result.Ticket
		if ticket == nil {
			return
		}
		fmt.Fprintf(w, "#%d %s (%s)\n", ticket.Number, ticket.Title, ticket.State)
		if ticket.Asked != "" && ticket.Asked != ticket.Author {
			fmt.Fprintf(w, "asked by %s, ", ticket.Asked)
		}
		if ticket.Author != "" {
			fmt.Fprintf(w, "by %s", ticket.Author)
			if !ticket.Created.IsZero() {
				fmt.Fprintf(w, ", %s", ticket.Created.UTC().Format("Jan 2 15:04"))
			}
			fmt.Fprintln(w)
		}
		if ticket.URL != "" {
			fmt.Fprintln(w, ticket.URL)
		}
		if ticket.Body != "" {
			fmt.Fprintf(w, "\n%s\n", ticket.Body)
		}
		for _, comment := range ticket.Comments {
			fmt.Fprintf(w, "\n%s:\n%s\n", firstNonEmpty(comment.Author, "someone"), comment.Body)
		}
		if len(ticket.Files) > 0 {
			fmt.Fprintf(w, "\nFiles: %s (hi dev task files %d, in the dev box)\n", strings.Join(ticket.Files, ", "), ticket.Number)
		}
	case "add":
		if ticket := result.Ticket; ticket != nil {
			fmt.Fprintf(w, "Filed #%d: %s %s\n", ticket.Number, ticket.Title, ticket.URL)
			if len(ticket.Files) > 0 {
				fmt.Fprintf(w, "With %s.\n", strings.Join(ticket.Files, ", "))
			}
		}
	case "comment":
		fmt.Fprintf(w, "Commented on #%d.\n", request.Number)
		if result.Ticket != nil && len(request.Files) > 0 {
			fmt.Fprintf(w, "Its files: %s.\n", strings.Join(result.Ticket.Files, ", "))
		}
	case "close":
		fmt.Fprintf(w, "Closed #%d.\n", request.Number)
	case "edit":
		fmt.Fprintf(w, "Edited #%d.\n", request.Number)
	}
}

// teamTicketCommand is hi team task <team> ..., on the machine.
func teamTicketCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError{"usage: hi team task <team> [list [--closed] | show <n> | add <title> [<body|->] [--attach <file>]... | comment <n> <text|-> [--attach <file>]... | edit <n> [--title <title>] <body|-> | close <n> [<comment>] | files <n>]"}
	}
	if _, err := loadTeam(args[0]); err != nil {
		return err
	}
	request, err := parseTeamTicketArgs(args[1:], stdin)
	if err != nil {
		return err
	}
	request.Author = firstNonEmpty(os.Getenv("USER"), "the machine's admin")
	result, err := teamTicketDo(args[0], request)
	if err != nil {
		return err
	}
	if request.Action == "files" {
		// On the machine they're in the team's folder already.
		fmt.Fprintf(stdout, "Task #%d's files are in %s:\n", request.Number, teamTicketFilesDir(args[0], request.Number))
		for _, file := range result.Files {
			fmt.Fprintf(stdout, "  %s\n", file.Name)
		}
		return nil
	}
	printTeamTicketResult(request, result, stdout)
	return nil
}

// teamSaveFetchedTicketFiles writes a ticket's files in a box, to
// ~/hi/tasks/<n>/, and says where.
func teamSaveFetchedTicketFiles(number int, result teamTicketResult, stdout io.Writer) error {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "hi", "tasks", strconv.Itoa(number))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Task #%d's files, in %s:\n", number, dir)
	for _, file := range result.Files {
		name := filepath.Base(file.Name)
		if err := os.WriteFile(filepath.Join(dir, name), file.Data, 0o600); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "  %s\n", filepath.Join(dir, name))
	}
	return nil
}

// threadFiles are the files people shared in the channel's thread a lead's
// ticket or comment comes from, that it doesn't have yet: hi attaches them,
// so nobody has to ask. The thread's first message may be newer than the
// channel's log, which hi then reads first.
func (b *teamBroker) threadFiles(request teamTicketRequest) []teamAttachment {
	config, err := loadTeam(b.team)
	if err != nil || request.Thread == "" || !slices.Contains(config.teamChannels(), request.Chat) {
		return nil
	}
	dir := teamChannelLogDir(b.team, config, request.Chat)
	shared := func() ([]teamChannelMessage, bool) {
		// The day before the thread's first message, as readTeamChannel reads.
		messages, _ := readTeamChannel(dir, teamSlackTS(teamTSTime(request.Thread).Add(-time.Second)))
		var in []teamChannelMessage
		found := false
		for _, message := range messages {
			if message.TS == request.Thread || message.Thread == request.Thread {
				found = found || message.TS == request.Thread
				if !message.Bot {
					in = append(in, message)
				}
			}
		}
		return in, found
	}
	messages, found := shared()
	if !found && b.pollChannel != nil {
		b.pollChannel()
		messages, _ = shared()
	}
	// What the ticket has, and what the lead attached itself, by content.
	have := map[[32]byte]bool{}
	for _, file := range request.Files {
		have[sha256.Sum256(file.Data)] = true
	}
	if request.Action == "comment" {
		for _, name := range teamTicketFileNames(b.team, request.Number) {
			if data, err := os.ReadFile(filepath.Join(teamTicketFilesDir(b.team, request.Number), name)); err == nil {
				have[sha256.Sum256(data)] = true
			}
		}
	}
	var files []teamAttachment
	total := 0
	for _, message := range messages {
		for _, file := range message.Files {
			data, err := os.ReadFile(filepath.Join(dir, file.Path))
			if err != nil || file.Path == "" || len(data) > teamMaxAttachment || total+len(data) > teamMaxAttachments/2 || have[sha256.Sum256(data)] {
				continue
			}
			have[sha256.Sum256(data)] = true
			total += len(data)
			files = append(files, teamAttachment{Name: file.Name, Data: data})
		}
	}
	return files
}

// teamLocalCommentID names a comment on hi's own tickets: the time it was
// written, to the nanosecond.
func teamLocalCommentID(comment teamTicketComment) string {
	return comment.At.UTC().Format(time.RFC3339Nano)
}

var teamAttachedLine = regexp.MustCompile(`(?m)^Attached: (.+)\. hi keeps them`)

// teamAttachedNames are the files a ticket's text says it attached, from
// the line teamSaveTicketFiles writes: "Attached: a.png (12 KB), b.pdf (3 MB)."
func teamAttachedNames(body string) []string {
	match := teamAttachedLine.FindStringSubmatch(body)
	if match == nil {
		return nil
	}
	var names []string
	for _, part := range regexp.MustCompile(` \([^()]*\)(, |$)`).Split(match[1], -1) {
		if part = strings.TrimSpace(part); part != "" {
			names = append(names, part)
		}
	}
	return names
}

func teamCommentTimes(comments []teamTicketComment) []time.Time {
	var times []time.Time
	for _, comment := range comments {
		times = append(times, comment.At, comment.Edited)
	}
	return times
}
