package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Files the lead sends from its box (hi app web post --attach), and the
// helpers that copy them: from hi 2.4 the lead hands out no tasks.

const (
	appMaxAttachment   = 50 << 20 // as large as the channel's log keeps
	appMaxAttachments  = 150 << 20
	appMaxConversation = 24 << 10
)

// appAttachment is a file the lead sends, from its box.
type appAttachment struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

// appChainRoot is the first task of the idea a task belongs to.
func appChainRoot(tasks []appTask, id string) string {
	byID := map[string]appTask{}
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

// appClipText keeps the end of text when it is too long: the latest
// messages matter most.
func appClipText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	text = text[len(text)-limit:]
	if _, rest, ok := strings.Cut(text, "\n"); ok {
		text = rest
	}
	return "(earlier messages left out)\n\n" + text
}

func appUniqueFileName(dir, name string) string {
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

func appCopyFile(source, target string) error {
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
