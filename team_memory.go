package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// A team's long-term memory is Hindsight: a server in a container of its
// own on the team's network, with its database in the team's memory/
// folder, and Hermes' hindsight plugin in the lead, which keeps every turn
// there and recalls what matters before each one. Hermes' built-in memory
// is a small notepad; Hindsight holds the facts, people, and decisions of
// the whole channel and finds them by meaning.
//
// The server's model calls go through the team's proxy, which adds the
// OpenRouter key, so no key enters a container; its embeddings and
// reranking run in the container. The plugin is the one Hermes' catalog
// pins, and its one client library is installed once on this machine and
// mounted read-only, so the lead's box installs nothing.

const (
	teamHindsightImage   = "ghcr.io/vectorize-io/hindsight:0.10.3"
	teamHindsightClient  = "hindsight-client==0.10.3"
	teamHindsightRetry   = "aiohttp-retry==2.9.1"
	teamHindsightPort    = 8888
	teamHindsightHostIP  = 200 // the server's address on the team's /24
	teamHindsightPlugin  = "hindsight"
	teamHindsightReady   = 5 * time.Minute
	teamHindsightSiteDir = "hindsight-client-0.10.3"
)

// teamMemoryOn says whether the team uses Hindsight; team.json's
// "memory": "off" leaves the lead with Hermes' built-in memory alone.
func teamMemoryOn(config teamConfig) bool { return config.Memory != "off" }

func teamMemoryContainer(team string) string { return "hi-box-team-" + team + "-memory" }

// teamMemoryIP is the server's address: the proxy's /24, at .200.
func teamMemoryIP(proxyIP string) string {
	return proxyIP[:strings.LastIndex(proxyIP, ".")+1] + fmt.Sprint(teamHindsightHostIP)
}

func hiShareDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(firstNonEmpty(os.Getenv("XDG_DATA_HOME"), filepath.Join(home, ".local", "share")), "hi")
}

// teamHindsightSite installs the plugin's client library, once per
// machine, in a folder of its own: Hermes' Python has everything else it
// needs.
func teamHindsightSite(runtime hermesRuntime) (string, error) {
	site := filepath.Join(hiShareDir(), teamHindsightSiteDir)
	if fileExists(filepath.Join(site, "hindsight_client")) {
		return site, nil
	}
	python := runtime.Command[0]
	partial := site + ".part"
	os.RemoveAll(partial)
	out, err := exec.Command(python, "-m", "pip", "install", "--quiet", "--disable-pip-version-check", "--no-deps",
		"--target", partial, teamHindsightClient, teamHindsightRetry).CombinedOutput()
	if err != nil {
		os.RemoveAll(partial)
		return "", fmt.Errorf("installing Hindsight's client: %s", qFirstLine(strings.TrimSpace(string(out)), err.Error()))
	}
	os.RemoveAll(site)
	return site, os.Rename(partial, site)
}

// teamHindsightCatalog is the plugin as Hermes' catalog pins it.
type teamHindsightCatalog struct{ Repo, SHA, Subdir string }

func readTeamHindsightCatalog() (teamHindsightCatalog, error) {
	var catalog teamHindsightCatalog
	data, err := os.ReadFile(filepath.Join(hermesInstallDir(), "plugin-catalog", teamHindsightPlugin+".yaml"))
	if err != nil {
		return catalog, fmt.Errorf("this Hermes has no hindsight plugin in its catalog: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch {
		case !ok:
		case key == "repo":
			catalog.Repo = value
		case key == "sha":
			catalog.SHA = value
		case key == "subdir":
			catalog.Subdir = value
		}
	}
	if catalog.Repo == "" || len(catalog.SHA) != 40 {
		return catalog, fmt.Errorf("Hermes' catalog entry for hindsight has no repository and commit")
	}
	return catalog, nil
}

// teamInstallHindsightPlugin puts the plugin in the lead's Hermes home, at
// the commit Hermes' catalog pins, unless that commit is there already.
func teamInstallHindsightPlugin(lead string) error {
	catalog, err := readTeamHindsightCatalog()
	if err != nil {
		return err
	}
	target := filepath.Join(lead, "plugins", teamHindsightPlugin)
	stamp := filepath.Join(target, ".hermes-catalog.json")
	var installed struct {
		SHA string `json:"sha"`
	}
	if data, err := os.ReadFile(stamp); err == nil && json.Unmarshal(data, &installed) == nil && installed.SHA == catalog.SHA {
		return nil
	}
	// One download per commit on this machine; each team copies it.
	cache := filepath.Join(hiShareDir(), "hindsight-plugin-"+catalog.SHA[:12])
	if !fileExists(filepath.Join(cache, "plugin.yaml")) {
		checkout := cache + ".git"
		os.RemoveAll(checkout)
		for _, args := range [][]string{
			{"init", "-q", checkout},
			{"-C", checkout, "remote", "add", "origin", catalog.Repo},
			{"-C", checkout, "sparse-checkout", "set", firstNonEmpty(catalog.Subdir, ".")},
			{"-C", checkout, "fetch", "-q", "--depth", "1", "--filter=blob:none", "origin", catalog.SHA},
			{"-C", checkout, "checkout", "-q", "FETCH_HEAD"},
		} {
			if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
				os.RemoveAll(checkout)
				return fmt.Errorf("downloading Hermes' hindsight plugin: git %s: %s", args[len(args)-1], qFirstLine(strings.TrimSpace(string(out)), err.Error()))
			}
		}
		os.RemoveAll(cache)
		if err := os.Rename(filepath.Join(checkout, filepath.FromSlash(catalog.Subdir)), cache); err != nil {
			return err
		}
		os.RemoveAll(checkout)
	}
	os.RemoveAll(target)
	// A new team's lead has no plugins folder yet.
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if out, err := exec.Command("cp", "-a", cache, target).CombinedOutput(); err != nil {
		return fmt.Errorf("copying the hindsight plugin: %s", strings.TrimSpace(string(out)))
	}
	data, _ := json.MarshalIndent(map[string]string{"catalog_name": teamHindsightPlugin, "repo": catalog.Repo, "sha": catalog.SHA,
		"tier": "community", "installed_at": time.Now().UTC().Format(time.RFC3339)}, "", "  ")
	return os.WriteFile(stamp, append(data, '\n'), 0o644)
}

// writeTeamHindsightConfig points the plugin at the team's server, with a
// memory bank named after the team as it was made (teamConfig.madeAs).
func writeTeamHindsightConfig(lead, team, bank, memoryIP string) error {
	dir := filepath.Join(lead, "hindsight")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	settings := map[string]any{}
	if data, err := os.ReadFile(filepath.Join(dir, "config.json")); err == nil {
		json.Unmarshal(data, &settings) // the lead's own tuning stays
	}
	settings["mode"] = "local_external"
	settings["api_url"] = fmt.Sprintf("http://%s:%d", memoryIP, teamHindsightPort)
	settings["bank_id"] = "team-" + bank
	// Its "recalled N memories" and "saving to memory" lines are for
	// developers, not the people in the channel.
	settings["recall_indicator"] = false
	settings["retain_indicator"] = false
	delete(settings, "bank_id_template")
	if _, ok := settings["retain_context"]; !ok {
		settings["retain_context"] = teamMemoryContext(team)
	}
	data, _ := json.MarshalIndent(settings, "", "  ")
	return os.WriteFile(filepath.Join(dir, "config.json"), append(data, '\n'), 0o600)
}

func teamMemoryContext(team string) string {
	return "the " + team + " team's Slack channel: the people, and the team's lead"
}

// teamPrepareMemory readies everything the lead's memory needs before the
// team starts: the image, the client library, and the plugin. Any failure
// leaves the team on Hermes' built-in memory, with a line in the journal.
func teamPrepareMemory(engine boxEngine, config teamConfig, runtime hermesRuntime, stderr io.Writer) (site string, ok bool) {
	if !teamMemoryOn(config) {
		return "", false
	}
	fail := func(err error) (string, bool) {
		fmt.Fprintf(stderr, "hi: the lead's long-term memory (Hindsight) is off this time: %v\n", err)
		return "", false
	}
	if _, err := engine.output("image", "exists", teamHindsightImage); err != nil {
		fmt.Fprintf(stderr, "hi: downloading Hindsight's image (%s, about 2.6 GB) for the lead's memory\n", teamHindsightImage)
		if out, err := engine.output("pull", "-q", teamHindsightImage); err != nil {
			return fail(fmt.Errorf("%s pull: %s", engine.name, qFirstLine(strings.TrimSpace(out), err.Error())))
		}
	}
	site, err := teamHindsightSite(runtime)
	if err != nil {
		return fail(err)
	}
	if err := teamInstallHindsightPlugin(filepath.Join(teamDir(config.Name), "lead")); err != nil {
		return fail(err)
	}
	return site, true
}

// startTeamMemory starts the team's Hindsight server on its network.
func startTeamMemory(engine boxEngine, config teamConfig, proxyIP string) error {
	data := filepath.Join(teamDir(config.Name), "memory")
	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	// Hindsight calls chat completions on OpenRouter: the lead's model
	// there, or its fallback for a lead on a subscription.
	model := teamLeadOpenRouterModel(config)
	proxy := fmt.Sprintf("http://%s:%d", proxyIP, boxProxyPort)
	run := []string{"run", "-d", "--rm", "--init", "--name", teamMemoryContainer(config.Name), "--hostname", config.Name + "-memory",
		"--network", "hi-box-team-" + config.Name, "--ip", teamMemoryIP(proxyIP), "--dns", "127.0.0.1"}
	run = append(run, engine.userArgs()...)
	run = append(run, boxHardening("4g")...)
	run = append(run, "-v", data+":/home/hindsight/.pg0",
		// The API only: its web console stays off.
		"-e", "HINDSIGHT_ENABLE_CP=false",
		"-e", "HTTPS_PROXY="+proxy, "-e", "HTTP_PROXY="+proxy, "-e", "NO_PROXY=localhost,127.0.0.1,"+proxyIP,
		"-e", "HINDSIGHT_API_LLM_PROVIDER=openai",
		"-e", fmt.Sprintf("HINDSIGHT_API_LLM_BASE_URL=http://%s:%d/api/v1", proxyIP, boxInjectPort),
		"-e", "HINDSIGHT_API_LLM_API_KEY="+boxClaudePlacehold)
	if model != "" {
		run = append(run, "-e", "HINDSIGHT_API_LLM_MODEL="+model)
	}
	run = append(run, teamHindsightImage)
	if out, err := engine.output(run...); err != nil {
		return fmt.Errorf("%s run: %s", engine.name, qFirstLine(strings.TrimSpace(out), err.Error()))
	}
	return nil
}

// teamWaitMemory waits until the server answers, which takes a minute or
// so on its first start, while it sets up its database.
func teamWaitMemory(engine boxEngine, team string) error {
	deadline := time.Now().Add(teamHindsightReady)
	for {
		out, err := engine.output("exec", teamMemoryContainer(team), "python", "-c",
			fmt.Sprintf("import urllib.request; urllib.request.urlopen('http://127.0.0.1:%d/health', timeout=3)", teamHindsightPort))
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			logs := engine.lastLines(teamMemoryContainer(team), 5)
			return fmt.Errorf("Hindsight didn't answer within %s: %s", teamHindsightReady, qFirstLine(strings.TrimSpace(logs), qFirstLine(out, err.Error())))
		}
		time.Sleep(3 * time.Second)
	}
}
