package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A member sees only its brief and the project, so what the people gave
// the lead must reach it too. Each task gets a folder of files,
// tasks/<id>-files, mounted read-only in the member's box at /box/files
// (hi agent --files), and its brief says what is there:
//
//   - the conversation the work was approved in, in the people's words,
//     with their links, and the files shared in that thread;
//   - files the lead attaches (hi team task ... --attach <file>), such as a
//     file shared elsewhere in the channel or a page it saved;
//   - for a task that continues another (--on), that task's files and the
//     brief the work started with.
//
// A review gets the files of the task it reviews.

const (
	teamMaxAttachment   = 50 << 20 // as large as the channel's log keeps
	teamMaxAttachments  = 150 << 20
	teamMaxConversation = 24 << 10
)

// teamAttachment is a file the lead sends with a task, from its box.
type teamAttachment struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

func teamTaskFilesDir(team, id string) string {
	return filepath.Join(teamTaskDir(team), id+"-files")
}

// teamTaskFiles gathers a new task's files and says what they are, for its
// brief. It returns the folder, or "" when there are none.
func (b *teamBroker) teamTaskFiles(task teamTask, attachments []teamAttachment) (string, string, error) {
	dir := teamTaskFilesDir(b.team, task.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	var text strings.Builder
	var listed []string
	// add copies a file in and returns where the member finds it.
	add := func(name, from string, copy func(path string) error) (string, error) {
		name = teamUniqueFileName(dir, teamSafeFileName(name))
		if err := copy(filepath.Join(dir, name)); err != nil {
			return "", err
		}
		inBox := agentFilesInBox + "/" + name
		listed = append(listed, fmt.Sprintf("- %s: %s", inBox, from))
		return inBox, nil
	}
	if task.On != "" {
		// How the work started is in the files, not pasted again into
		// every round (hi_team_rounds.md): the member reads it when the
		// change needs it.
		root := teamChainRoot(loadTeamTasks(b.team), task.On)
		started, agreed := root+"-brief.md", root+"-thread.md"
		if from := teamTaskFilesDir(b.team, task.On); fileExists(from) {
			entries, _ := os.ReadDir(from)
			for _, entry := range entries {
				if entry.Type().IsRegular() && entry.Name() != started && entry.Name() != agreed {
					source := filepath.Join(from, entry.Name())
					if _, err := add(entry.Name(), "from "+task.On, func(path string) error { return teamCopyFile(source, path) }); err != nil {
						return "", "", err
					}
				}
			}
		}
		if root != "" && !task.Resumes {
			saved := b.brief(teamTask{ID: root})
			var where []string
			if brief := teamBriefBody(saved); brief != "" {
				inBox, err := add(started, root+"'s brief, as the lead wrote it", func(path string) error { return os.WriteFile(path, []byte(brief+"\n"), 0o600) })
				if err != nil {
					return "", "", err
				}
				where = append(where, fmt.Sprintf("The brief this work started with (%s) is %s.", root, inBox))
			}
			if _, said, ok := strings.Cut(saved, "\n## What the people said\n"); ok {
				said, _, _ = strings.Cut(said, "\n## ")
				inBox, err := add(agreed, "what the people said when they agreed to "+root, func(path string) error { return os.WriteFile(path, []byte(strings.TrimSpace(said)+"\n"), 0o600) })
				if err != nil {
					return "", "", err
				}
				where = append(where, "What the people said when they agreed to it is "+inBox+".")
			}
			if len(where) > 0 {
				them := "it"
				if len(where) > 1 {
					them = "them"
				}
				fmt.Fprintf(&text, "\n## How this work started\n\n%s Read %s when the change needs %s: the work stays within what the people agreed to.\n", strings.Join(where, " "), them, them)
			}
		}
		if task.Approval != nil {
			fmt.Fprintf(&text, "\n## Going on was agreed\n\n**%s**: %s\n", task.Approval.Name, teamClipText(strings.TrimSpace(task.Approval.Text), 4<<10))
		}
	}
	if task.On != "" {
		// The thread is in the files already.
	} else if task.Approval != nil && teamWebMessageID.MatchString(task.Approval.TS) {
		said, err := teamWebApprovalChat(b.team, *task.Approval, func(source, from string) (string, error) {
			_, name, _ := strings.Cut(filepath.Base(source), "-")
			return add(firstNonEmpty(name, filepath.Base(source)), from, func(path string) error { return teamCopyFile(source, path) })
		})
		if err != nil {
			return "", "", err
		}
		if said != "" {
			fmt.Fprintf(&text, "\n## What the people said\n\nThe chat in the app this work was agreed in, in their words, with the page each message was written on. Their links are part of the brief: open them.\n\n%s", teamClipText(said, teamMaxConversation))
		}
	} else if task.Approval != nil {
		if config, err := loadTeam(b.team); err == nil {
			thread := teamApprovalThread(config, *task.Approval)
			if len(thread) > 0 {
				var said strings.Builder
				for _, message := range thread {
					fmt.Fprintf(&said, "**%s** (%s): %s\n", message.Name, teamTSTime(message.TS).UTC().Format("Jan 2 15:04"), strings.TrimSpace(message.Text))
					for _, file := range message.Files {
						if file.Path == "" {
							fmt.Fprintf(&said, "  [%s shared %s, too large to download: %s]\n", message.Name, file.Name, file.Link)
							continue
						}
						source := filepath.Join(teamChannelDir(b.team), file.Path)
						inBox, err := add(filepath.Base(file.Path), "shared by "+message.Name+" in Slack", func(path string) error { return teamCopyFile(source, path) })
						if err != nil {
							return "", "", err
						}
						fmt.Fprintf(&said, "  [%s shared %s: %s]\n", message.Name, file.Name, inBox)
					}
					said.WriteString("\n")
				}
				fmt.Fprintf(&text, "\n## What the people said\n\nThe Slack thread this work was agreed in, in their words. Their links are part of the brief: open them.\n\n%s", teamClipText(said.String(), teamMaxConversation))
			}
		}
	}
	total := 0
	for _, attachment := range attachments {
		total += len(attachment.Data)
		if len(attachment.Data) > teamMaxAttachment {
			return "", "", fmt.Errorf("%s is larger than %d MB", attachment.Name, teamMaxAttachment>>20)
		}
		if total > teamMaxAttachments {
			return "", "", fmt.Errorf("the attachments are larger than %d MB together", teamMaxAttachments>>20)
		}
		data := attachment.Data
		if _, err := add(attachment.Name, "from the lead", func(path string) error { return os.WriteFile(path, data, 0o600) }); err != nil {
			return "", "", err
		}
	}
	if len(listed) == 0 {
		os.Remove(dir)
		return "", text.String(), nil
	}
	if task.Resumes {
		// The member saw the work's files in its first turn.
		var added []string
		for _, line := range listed {
			if strings.HasSuffix(line, ": from the lead") {
				added = append(added, line)
			}
		}
		if len(added) > 0 {
			fmt.Fprintf(&text, "\n## Files\n\nNew in %s with this turn, read-only:\n\n%s\n", agentFilesInBox, strings.Join(added, "\n"))
		}
		return dir, text.String(), nil
	}
	fmt.Fprintf(&text, "\n## Files\n\nThese are in %s, read-only. Look at each one: images show what the people want. Copy what the project needs into it; %s isn't part of the project.\n\n%s\n",
		agentFilesInBox, agentFilesInBox, strings.Join(listed, "\n"))
	return dir, text.String(), nil
}

// teamApprovalThread is the conversation an approval answered: its thread,
// oldest first.
func teamApprovalThread(config teamConfig, approval teamApproval) []teamChannelMessage {
	messages, err := readTeamChannel(teamChannelDir(config.Name), teamSlackTS(teamTSTime(approval.TS).Add(-14*24*time.Hour)))
	if err != nil {
		return nil
	}
	var root string
	for _, message := range messages {
		if message.TS == approval.TS {
			root = firstNonEmpty(message.Thread, message.TS)
		}
	}
	var thread []teamChannelMessage
	for _, message := range messages {
		if root != "" && (message.TS == root || message.Thread == root) && !teamTSBefore(approval.TS, message.TS) {
			thread = append(thread, message)
		}
	}
	return thread
}

// teamChainRoot is the first task of the idea a task belongs to.
func teamChainRoot(tasks []teamTask, id string) string {
	byID := map[string]teamTask{}
	for _, task := range tasks {
		byID[task.ID] = task
	}
	for seen := 0; seen < len(tasks); seen++ {
		task, ok := byID[id]
		if !ok {
			return ""
		}
		if task.On == "" {
			return id
		}
		id = task.On
	}
	return ""
}

// teamBriefBody is the lead's own text of a saved brief: without the
// heading hi adds, and before the sections hi appends.
func teamBriefBody(brief string) string {
	if _, rest, ok := strings.Cut(brief, "\n\n"); ok && strings.HasPrefix(brief, "# Task ") {
		brief = rest
	}
	if before, _, ok := strings.Cut(brief, "\n"+teamSpecsRule); ok {
		brief = before
	}
	return strings.TrimSpace(brief)
}

// teamClipText keeps the end of text when it is too long: the latest
// messages matter most.
func teamClipText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	text = text[len(text)-limit:]
	if _, rest, ok := strings.Cut(text, "\n"); ok {
		text = rest
	}
	return "(earlier messages left out)\n\n" + text
}

func teamUniqueFileName(dir, name string) string {
	if name == "" || name == "." || name == ".." {
		name = "file"
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; fileExists(filepath.Join(dir, name)); i++ {
		name = fmt.Sprintf("%s-%d%s", base, i, ext)
	}
	return name
}

func teamCopyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New(source + " isn't a file")
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
