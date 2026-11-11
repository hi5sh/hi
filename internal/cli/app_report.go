package cli

// A contributor's bug report from the helper (docs/specs/approved/
// hi_app_dev_access.md): someone signed in with their own link, whom an
// owner hasn't stopped, sends what's wrong, and hi files it as a task at
// once, with who sent it, the address and version, the page, and the
// browser, and what they pointed at and attached. The channel hears it.
// The helper keeps no list: the developer who fixed it, or the lead when
// the task closes, says so in the report's thread.

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// A day's reports, per person and for the team: each is a task.
	appReportPerPerson = 20
	appReportPerDay    = 200
	appReportTitleMax  = 80
)

// appReportSent is what the helper hears: the task's number.
type appReportSent struct {
	Number int    `json:"number"`
	URL    string `json:"url,omitempty"`
}

// webReporter is the signed-in person who may report here, or why not.
func (b *appBroker) webReporter(config appConfig, r *http.Request) (appPersonKey, error) {
	person, ok, ended := b.webUser(config, r.Header.Get(netExposeUserHeader))
	switch {
	case ended:
		return person, fmt.Errorf("your link has ended: type %s in the app's Slack channel for a new one", appSlashCommand(b.team))
	case !ok:
		return person, errors.New("reports are for people signed in with their own link; Feedback works for everyone")
	case b.level(config, person.User) == appLevelNone:
		return person, errors.New("reports are for the app's contributors, whom an owner adds; Feedback works for everyone")
	}
	return person, nil
}

// webReport files a report as a task.
func (b *appBroker) webReport(r *http.Request, slot string, send appFeedbackSend) (appReportSent, int, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return appReportSent{}, http.StatusInternalServerError, err
	}
	person, err := b.webReporter(config, r)
	if err != nil {
		return appReportSent{}, http.StatusForbidden, err
	}
	send.Text = strings.TrimSpace(send.Text)
	switch {
	case !appHelperOn(config, slot):
		return appReportSent{}, http.StatusForbidden, errors.New("the helper is off here")
	case send.Text == "":
		return appReportSent{}, http.StatusBadRequest, errors.New("say what's wrong")
	case len(send.Text) > appWebMaxText:
		return appReportSent{}, http.StatusBadRequest, fmt.Errorf("the report is longer than %d characters", appWebMaxText)
	}
	if !b.counts.disabled {
		if err := b.counts.take("reports", person.User, appReportPerPerson, appReportPerDay); err != nil {
			return appReportSent{}, http.StatusTooManyRequests, err
		}
	}
	if send.Page != nil {
		send.Page.Path, send.Page.Title = appClipStart(send.Page.Path, 300), appClipStart(send.Page.Title, 200)
	}
	send.Refs = appCleanRefs(send.Refs)

	// Its files, checked and kept as feedback's are, go with the task.
	id := fmt.Sprintf("r-%s-%d", person.User, time.Now().UnixNano())
	files, err := appSaveWebFiles(b.team, id, 1, send.Files, len(send.Refs))
	// Read into the task's attachments, and gone: the task keeps them.
	defer os.RemoveAll(appWebFilesDir(b.team, id))
	if err != nil {
		return appReportSent{}, http.StatusBadRequest, err
	}
	var attachments []appAttachment
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(appWebFilesDir(b.team, id), file.Name))
		if err != nil {
			return appReportSent{}, http.StatusInternalServerError, err
		}
		_, name, _ := strings.Cut(file.Name, "-")
		switch {
		case file.Screen:
			name = "what-they-saw" + filepath.Ext(file.Name)
		case file.Ref > 0:
			name = fmt.Sprintf("pointed-at-%d%s", file.Ref, filepath.Ext(file.Name))
		}
		attachments = append(attachments, appAttachment{Name: name, Data: data})
	}

	where := slot
	if state, ok := loadTeamSlot(b.team, slot); ok && state.Label != "" {
		where += " (" + state.Label + ")"
	}
	if send.Page != nil && send.Page.Path != "" {
		where += ", " + send.Page.Path
	}
	title, _, _ := strings.Cut(send.Text, "\n")
	title = appClipStart(strings.TrimSpace(title), appReportTitleMax) // a title is its start
	body := send.Text + "\n\n" + fmt.Sprintf("Reported by %s from the app's helper, on %s.", person.Name, where)
	if browser := appClipStart(r.UserAgent(), 200); browser != "" {
		body += "\nBrowser: " + browser
	}
	if len(send.Refs) > 0 {
		body += "\n\n" + appWebRefsText(send.Refs, "")
	}
	result, err := appTicketDo(b.team, appTicketRequest{Action: "add", Title: title, Body: body, Author: person.Name, Files: attachments})
	if err != nil {
		return appReportSent{}, http.StatusBadGateway, fmt.Errorf("hi couldn't file it as a task: %w", err)
	}
	if result.Ticket == nil {
		return appReportSent{}, http.StatusBadGateway, errors.New("the task was filed, but hi didn't get its number")
	}
	ticket := *result.Ticket
	link := firstNonEmpty(ticket.URL, appDevTaskLink(b.team, ticket.Number))
	if b.channel != nil {
		text := fmt.Sprintf(":lady_beetle: %s reported #%d from the app's helper (%s): %s", appMention(person.User), ticket.Number, appSlackEscape(where), appSlackEscape(ticket.Title))
		if link != "" {
			text += fmt.Sprintf(" <%s|the task>", link)
		}
		b.channel.post(config.Channel, text)
	}
	return appReportSent{Number: ticket.Number, URL: ticket.URL}, http.StatusOK, nil
}
