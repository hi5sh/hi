package cli

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// A shared server is also published on this machine's NetBird address,
// where its front takes a key (hi_serve.md, Sharing). --share makes the
// key that is handed over by hand, "shared"; --org makes the org server's,
// "org", and names the server to the org, so devices reach it through
// hi org ai (The org knows). Each key is a file in the server's keys folder,
// 0600, which only the front mounts; the box never sees one.

const (
	serveSharedKey = "shared"
	serveOrgKey    = "org"
	serveFrontKeys = "/run/hi/keys"
	serveFrontPort = 8081 // the front's listener that takes a key
	serveOrgPrefix = "local/"
)

// serveNetBirdAddress is this machine's NetBird address; tests replace it.
var serveNetBirdAddress = netbirdAddress

// serveOrgClient reaches the org server this device joined; tests replace it.
var serveOrgClient = func() (*serverClient, error) {
	connection, err := loadServerConnection()
	if err != nil {
		return nil, err
	}
	if connection == nil {
		return nil, errors.New("--org needs an org server; connect this device with `hi org join <server>`")
	}
	key, err := loadDeviceKey(false)
	if err != nil {
		return nil, err
	}
	return newServerClient(connection.URL, key), nil
}

// servesShared reports whether the front also listens with a key.
func (s servedModel) servesShared() bool { return s.Shared || s.Org }

// sharedURL is the API's address on the NetBird address.
func (s servedModel) sharedURL() string {
	return fmt.Sprintf("http://%s:%d/v1", s.Address, s.Port)
}

func serveKeysDir(name string) string {
	return filepath.Join(hiUserConfigBase(), "hi", "serve", name)
}

func newServeKey() string {
	buffer := make([]byte, 32)
	rand.Read(buffer)
	return "hi-serve-" + base64.RawURLEncoding.EncodeToString(buffer)
}

// readServeKey is one of a server's keys, or "".
func readServeKey(name, which string) string {
	data, err := os.ReadFile(filepath.Join(serveKeysDir(name), which))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// writeServeKey makes a new key, replacing the one there.
func writeServeKey(name, which string) (string, error) {
	dir := serveKeysDir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	os.Chmod(dir, 0o700)
	key := newServeKey()
	path := filepath.Join(dir, which)
	if err := os.WriteFile(path+".tmp", []byte(key+"\n"), 0o600); err != nil {
		return "", err
	}
	return key, os.Rename(path+".tmp", path)
}

// ensureServeKeys makes the keys a record needs and removes the others,
// so the front takes exactly those.
func ensureServeKeys(record servedModel) error {
	for which, wanted := range map[string]bool{serveSharedKey: record.Shared, serveOrgKey: record.Org} {
		switch {
		case wanted && readServeKey(record.Name, which) == "":
			if _, err := writeServeKey(record.Name, which); err != nil {
				return err
			}
		case !wanted:
			os.Remove(filepath.Join(serveKeysDir(record.Name), which))
		}
	}
	return nil
}

// shareAddress finds the NetBird address to publish on.
func shareAddress() (string, error) {
	address, err := serveNetBirdAddress()
	if err != nil {
		return "", fmt.Errorf("--share and --org publish on this machine's NetBird address: %w", err)
	}
	return address, nil
}

// serveKeyCommand prints a shared server's key, or makes a new one.
func serveKeyCommand(args []string, stdout, stderr io.Writer) error {
	flags := newComputeFlags("serve key", stderr)
	renew := flags.Bool("new", false, "make a new key; the old one stops working within seconds")
	positional, err := parseInterspersedFlags(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return usageError{"usage: hi serve key <name> [--new]"}
	}
	served, err := loadServed()
	if err != nil {
		return err
	}
	record, ok := served[positional[0]]
	switch {
	case !ok || record.Provider != "local":
		return fmt.Errorf("%s isn't served on this machine; see hi serve ls", positional[0])
	case !record.Shared && record.Org:
		return fmt.Errorf("%s is shared through the org server, which holds its key; nobody needs it by hand. "+
			"To hand one over too, serve it with --share", record.Name)
	case !record.Shared:
		return fmt.Errorf("%s isn't shared, so it takes no key on 127.0.0.1:%d; serve it with --share to share it", record.Name, record.Port)
	}
	key := readServeKey(record.Name, serveSharedKey)
	if *renew || key == "" {
		if key, err = writeServeKey(record.Name, serveSharedKey); err != nil {
			return err
		}
		fmt.Fprintln(stderr, "A new key; the old one stops working within seconds. Hand this one to whoever should keep using it.")
	}
	fmt.Fprintln(stdout, key)
	return nil
}

// sharedKeysText says which keys a shared front takes.
func sharedKeysText(record servedModel) string {
	switch {
	case record.Shared && record.Org:
		return "the one handed over, hi serve key " + record.Name + ", and the org server's"
	case record.Org:
		return "the org server's"
	}
	return "hi serve key " + record.Name
}

// printShareHandOver says what to hand a teammate.
func printShareHandOver(record servedModel, key string, stdout io.Writer) {
	fmt.Fprintf(stdout, "\nShared on this machine's NetBird address. Hand a teammate these two, as you would any API key:\n")
	fmt.Fprintf(stdout, "  OPENAI_BASE_URL=%s\n", record.sharedURL())
	if key != "" {
		fmt.Fprintf(stdout, "  OPENAI_API_KEY=%s\n", key)
		fmt.Fprintf(stdout, "The key isn't printed again; hi serve key %s prints it, and hi serve key %s --new replaces it.\n", record.Name, record.Name)
	}
	fmt.Fprintln(stdout, "Their device reaches it if NetBird's access control lets it reach this machine; that's for your IT admins.")
}

// ---------------------------------------------------------------------------
// the org

// apiServeUpstream is a local server named to the org, as a device sends it
// and the org server answers with it.
type apiServeUpstream struct {
	Name    string `json:"name"`
	Model   string `json:"model"` // the alias the server answers to
	Recipe  string `json:"recipe,omitempty"`
	Repo    string `json:"repo,omitempty"`
	URL     string `json:"url,omitempty"`
	Key     string `json:"key,omitempty"` // only from the device
	Reason  string `json:"reason,omitempty"`
	Up      bool   `json:"up"`
	State   string `json:"state,omitempty"` // pending, approved, denied, expired
	Request string `json:"request,omitempty"`
	User    string `json:"user,omitempty"`
	Host    string `json:"host,omitempty"`
	Problem string `json:"problem,omitempty"`
}

// orgModelID is the model devices ask hi org ai for.
func orgModelID(name string) string { return serveOrgPrefix + name }

// nameToOrg asks the org server to add a ready server to hi org ai.
func nameToOrg(record servedModel, reason string, stdout io.Writer) (servedModel, error) {
	client, err := serveOrgClient()
	if err != nil {
		return record, err
	}
	if strings.TrimSpace(reason) == "" {
		reason = fmt.Sprintf("serve %s from this machine to the org", record.Model)
	}
	body := apiServeUpstream{Name: record.Name, Model: record.Model, Recipe: record.Recipe, Repo: record.Repo,
		URL: record.sharedURL(), Key: readServeKey(record.Name, serveOrgKey), Reason: reason, Up: true}
	var answer apiServeUpstream
	if err := client.call(http.MethodPost, "/v1/serve", body, &answer); err != nil {
		return record, fmt.Errorf("the org server didn't take it: %w", err)
	}
	record.OrgRequest = answer.Request
	switch answer.State {
	case "approved":
		fmt.Fprintf(stdout, "\nNamed to the org: devices reach it through hi org ai as model %s.\n", orgModelID(record.Name))
	default:
		fmt.Fprintf(stdout, "\nAsked the org server to add it to hi org ai as model %s (%s); an admin approves it in Slack.\n"+
			"hi serve status %s shows when it is approved.\n", orgModelID(record.Name), answer.Request, record.Name)
	}
	return record, nil
}

// tellOrg says a named server is up or down, or gone, best effort: the org
// server finds out at the next change if it can't be reached now.
func tellOrg(record servedModel, action string, stderr io.Writer) {
	if !record.Org {
		return
	}
	client, err := serveOrgClient()
	if err == nil {
		path := "/v1/serve/" + url.PathEscape(record.Name)
		switch action {
		case "rm":
			err = client.call(http.MethodDelete, path, nil, nil)
		default:
			body := apiServeUpstream{Name: record.Name, URL: record.sharedURL(), Up: action == "up"}
			err = client.call(http.MethodPost, path+"/state", body, nil)
		}
	}
	if err != nil {
		var reply *serverReplyError
		if errors.As(err, &reply) && reply.status == http.StatusNotFound && action == "rm" {
			return
		}
		fmt.Fprintf(stderr, "hi: the org server wasn't told that %s is %s: %v\n", record.Name,
			map[string]string{"up": "up", "down": "stopped", "rm": "removed"}[action], err)
	}
}

// orgUpstreamState is what the org server says of a named server, for
// hi serve status.
func orgUpstreamState(record servedModel) string {
	client, err := serveOrgClient()
	if err != nil {
		return "unknown: " + err.Error()
	}
	var answer apiServeUpstream
	if err := client.call(http.MethodGet, "/v1/serve/"+url.PathEscape(record.Name), nil, &answer); err != nil {
		return "unknown: " + err.Error()
	}
	switch answer.State {
	case "approved":
		text := "in hi org ai as model " + orgModelID(record.Name)
		if !answer.Up {
			text += ", marked stopped"
		}
		if answer.Problem != "" {
			text += "; the org server last said: " + answer.Problem
		}
		return text
	case "pending":
		return fmt.Sprintf("waiting for an admin's approval (%s)", answer.Request)
	default:
		return fmt.Sprintf("%s (%s); hi serve rm %s, then serve it again with --org to ask again", answer.State, answer.Request, record.Name)
	}
}
