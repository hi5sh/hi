package cli

// The admin page's other machines (hi_app_move.md, release 1): this
// machine's key for moving apps, the machines it moves them to, the ones it
// takes them from, and an app's Move… with the check before it.

import (
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// adminMachinesView is Machine's Other machines.
type adminMachinesView struct {
	Machine string            `json:"machine"` // this one, as the others name it
	Key     string            `json:"key"`
	Line    string            `json:"line"` // what the other machine runs, or pastes, to take apps from this one
	To      []adminMoveTarget `json:"to"`
	From    []string          `json:"from"`
}

// adminMoveTarget is a machine this one moves apps to, as it answered just now.
type adminMoveTarget struct {
	Name    string `json:"name"`
	Takes   bool   `json:"takes"`
	Version string `json:"version,omitempty"`
	Problem string `json:"problem,omitempty"`
}

func adminMachines() (adminMachinesView, error) {
	key, err := appEnsureMoveKey()
	if err != nil {
		return adminMachinesView{}, err
	}
	view := adminMachinesView{Machine: appMoveThisMachine(), Key: key, To: []adminMoveTarget{}, From: appMachinesFrom()}
	view.Line = "hi app machines add " + view.Machine + " " + key
	if view.From == nil {
		view.From = []string{}
	}
	machines := loadAppMoveTo()
	view.To = make([]adminMoveTarget, len(machines))
	var wait sync.WaitGroup
	for i, machine := range machines {
		wait.Add(1)
		go func() {
			defer wait.Done()
			view.To[i] = adminProbe(machine.Name)
		}()
	}
	wait.Wait()
	return view, nil
}

func adminProbe(machine string) adminMoveTarget {
	takes, version, problem := appMoveProbe(machine)
	return adminMoveTarget{Name: machine, Takes: takes, Version: version, Problem: problem}
}

// adminPastedKey reads what an admin pastes as another machine's key: the
// line that machine's page or hi app machines shows, or the key alone.
var adminPastedLine = regexp.MustCompile(`^(?:hi\s+app\s+machines\s+add\s+(\S+)\s+)?(ssh-ed25519\s+\S+)`)

func adminPastedKey(name, pasted string) (string, string, error) {
	match := adminPastedLine.FindStringSubmatch(strings.TrimSpace(pasted))
	if match == nil {
		return "", "", fmt.Errorf("paste the line the other machine's admin page shows under This machine's key, or hi app machines prints there")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = match[1]
	}
	if name == "" {
		return "", "", fmt.Errorf("say which machine the key is from")
	}
	return name, match[2], nil
}

// adminChangeMachines is Other machines' changes.
func adminChangeMachines(admin adminPerson, action, name, key string) (map[string]any, error) {
	switch action {
	case "to":
		target, err := newAppMoveTarget(strings.TrimSpace(name))
		if err != nil {
			return nil, err
		}
		appMoveRemember(target.machine)
		adminRecord(admin.Name, "", "added "+target.machine+" to the machines this one moves apps to")
		probe := adminProbe(target.machine)
		return map[string]any{"machine": probe}, nil
	case "forget":
		if !appMoveForget(strings.TrimSpace(name)) {
			return nil, fmt.Errorf("this machine doesn't move apps to %q", name)
		}
		adminRecord(admin.Name, "", "took "+name+" off the machines this one moves apps to")
		return map[string]any{"text": "This machine no longer lists " + name + "."}, nil
	case "take":
		machine, key, err := adminPastedKey(name, key)
		if err != nil {
			return nil, err
		}
		if err := appMachinesAdd(machine, key, io.Discard); err != nil {
			return nil, err
		}
		adminRecord(admin.Name, "", "let "+machine+" move apps to this machine")
		return map[string]any{"text": "This machine takes apps from " + machine + " now. Its key runs only hi's receiving end here, not a shell."}, nil
	case "untake":
		removed, err := appMachinesRemove(strings.TrimSpace(name))
		if err != nil {
			return nil, err
		}
		if !removed {
			return nil, fmt.Errorf("this machine doesn't take apps from %q", name)
		}
		adminRecord(admin.Name, "", "stopped taking apps from "+name)
		return map[string]any{"text": "This machine no longer takes apps from " + name + "."}, nil
	}
	return nil, fmt.Errorf("no such change: %q", action)
}

// adminMoveCheckView is what Move… shows before the move.
type adminMoveCheckView struct {
	Machine  string   `json:"machine"`
	Version  string   `json:"version,omitempty"`
	Folder   string   `json:"folder,omitempty"`
	Problems []string `json:"problems"`
	Warnings []string `json:"warnings"`
	Access   string   `json:"access,omitempty"` // the machine doesn't take apps from here yet: the line for it
	Error    string   `json:"error,omitempty"`
}

func adminMoveCheck(app, machine string) (adminMoveCheckView, error) {
	if !appNamePattern.MatchString(app) {
		return adminMoveCheckView{}, fmt.Errorf("no app %q on this machine", app)
	}
	if _, err := loadTeam(app); err != nil {
		return adminMoveCheckView{}, fmt.Errorf("no app %q on this machine", app)
	}
	target, err := newAppMoveTarget(strings.TrimSpace(machine))
	if err != nil {
		return adminMoveCheckView{}, err
	}
	view := adminMoveCheckView{Machine: target.machine, Problems: []string{}, Warnings: []string{}}
	if takes, _, problem := appMoveProbe(target.machine); !takes {
		appMoveRemember(target.machine)
		if strings.Contains(problem, "doesn't take apps") {
			key, _ := appEnsureMoveKey()
			view.Access = "hi app machines add " + appMoveThisMachine() + " " + key
		}
		view.Error = target.machine + " " + problem
		return view, nil
	}
	report, needs, err := appMoveCheck(app, target, appDir(app))
	if err != nil {
		view.Error = err.Error()
		return view, nil
	}
	view.Version, view.Folder = report.Version, filepath.Clean(needs.Folder)
	view.Problems = append(view.Problems, report.Problems...)
	view.Warnings = append(view.Warnings, report.Warnings...)
	return view, nil
}
