package cli

// The lead starts work on a task in the dev box when an owner asks in
// Slack, as Start does on the dev page, while no other task is in
// progress there: one piece of work at a time from the channel.

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
)

// appDevTaskBranch is a task's number in its branch, such as 12 in
// 12-chart-misses-the-last-day (appDevTicketBranch).
var appDevTaskBranch = regexp.MustCompile(`^([0-9]+)(?:-|$)`)

// appDevTaskInProgress is the task number a workspace works on, or 0.
func appDevTaskInProgress(space appDevSpace) int {
	match := appDevTaskBranch.FindStringSubmatch(space.Branch)
	if match == nil || space.Path == "" {
		return 0
	}
	number, _ := strconv.Atoi(match[1])
	return number
}

// appDevTaskLink is the dev page's link that opens on a task's work,
// for the channel: without the address's key, which a public dev page's
// URL has. Owners open it signed in, from the link in their DM.
func appDevTaskLink(team string, number int) string {
	page, ok := loadTeamDevPage(team)
	if !ok {
		return ""
	}
	link, err := url.Parse(page.URL)
	if err != nil {
		return ""
	}
	link.RawQuery, link.Fragment = "", fmt.Sprintf("task-%d", number)
	return link.String()
}

// leadStart is hi app task start <n> from the lead, for user, who asked.
func (b *appBroker) leadStart(number int, user string) (string, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return "", err
	}
	if !slackIDPattern.MatchString(user) || !appBuilds(config, user) {
		return "", fmt.Errorf("only the app's owners and developers start work in the dev box (%s); offer to tell them it's ready", appPeople(slices.Concat(config.Owners, config.Developers)))
	}
	shown, err := appTicketDo(b.team, appTicketRequest{Action: "show", Number: number})
	if err != nil {
		return "", err
	}
	if shown.Ticket == nil {
		return "", fmt.Errorf("there is no task %d", number)
	}
	if shown.Ticket.State == "closed" {
		return "", fmt.Errorf("task %d is closed; an owner reopens it on the dev page", number)
	}
	spaces, err := appDevSpaces(b.team)
	if err != nil {
		return "", fmt.Errorf("the dev box isn't answering, so nothing started: %w", err)
	}
	progress := func(text string) string {
		if link := appDevTaskLink(b.team, number); link != "" {
			return text + " You should follow the progress in the developer view: " + link
		}
		return text + " You should follow the progress on the dev page."
	}
	for _, space := range spaces {
		switch other := appDevTaskInProgress(space); {
		case other == number:
			return progress(fmt.Sprintf("Task #%d is already in progress!", number)), nil
		case other != 0:
			return "", fmt.Errorf("task #%d is in progress in the dev box (%s), and work starts from the channel one task at a time: an owner finishes it first, with Done ✓ on the dev page", other, space.Branch)
		}
	}
	name := user
	if b.channel != nil {
		name = firstNonEmpty(b.channel.userName(user), user)
	}
	branch, err := appDevStart(config, number, "claude", name)
	if branch == "" {
		if err == nil {
			err = errors.New("the dev box made no branch")
		}
		return "", err
	}
	appDevSpacesCache.Lock()
	appDevSpacesCache.team = ""
	appDevSpacesCache.Unlock()
	appTicketDo(b.team, appTicketRequest{Action: "comment", Number: number, Author: "the lead",
		Body: fmt.Sprintf("Started in the dev box by the lead, as %s asked in Slack: branch %s, with Claude Code.", name, branch)})
	fmt.Fprintf(os.Stderr, "app %s: the lead started task %d for %s (%s)\n", b.team, number, name, branch)
	if err != nil {
		return progress(fmt.Sprintf("Task #%d is now in progress, though %s.", number, err)), nil
	}
	return progress(fmt.Sprintf("Task #%d is now in progress!", number)), nil
}
