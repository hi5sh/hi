package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Files the lead sends from its box (hi team web post --attach), and the
// helpers that copy them: from hi 2.4 the lead hands out no tasks.

const (
	teamMaxAttachment   = 50 << 20 // as large as the channel's log keeps
	teamMaxAttachments  = 150 << 20
	teamMaxConversation = 24 << 10
)

// teamAttachment is a file the lead sends, from its box.
type teamAttachment struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
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
