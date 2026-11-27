package cli

// Google Cloud DNS for hi net domain. Its key is a service account's JSON
// key file, which hi keeps with the domain's other settings, in the vault.
// libdns/googleclouddns takes the key only as a file's path, so each call
// gets a file of its own, mode 0600, in $XDG_RUNTIME_DIR (memory), which
// is removed when the call ends.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/libdns/googleclouddns"
	"github.com/libdns/libdns"
)

// hiGoogleDNSKeyField is the setting that holds the key's JSON.
const hiGoogleDNSKeyField = "service_account_key"

// hiGoogleDNS is the provider hi net domain makes from its settings.
type hiGoogleDNS struct {
	Key     string `json:"service_account_key,omitempty"`
	Project string `json:"gcp_project,omitempty"` // the key's project_id when empty

	mu    sync.Mutex
	inner *googleclouddns.Provider
}

// hiGoogleKey is what hi reads from a service account's key file.
type hiGoogleKey struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	PrivateKey  string `json:"private_key"`
	ClientEmail string `json:"client_email"`
}

// hiGoogleDNSKey checks a key file's contents and returns its project.
func hiGoogleDNSKey(data []byte) (string, error) {
	var key hiGoogleKey
	if err := json.Unmarshal(data, &key); err != nil {
		return "", errors.New("this isn't a service account's key file (JSON): " + err.Error())
	}
	if key.Type != "service_account" || key.PrivateKey == "" || key.ClientEmail == "" {
		return "", errors.New("this isn't a service account's key file: it needs \"type\": \"service_account\", a private_key and a client_email. In Google Cloud: IAM, Service accounts, the account, Keys, Add key, JSON")
	}
	return key.ProjectID, nil
}

// with runs a call of the library, with the key in a file for its time.
func (g *hiGoogleDNS) with(call func(*googleclouddns.Provider) ([]libdns.Record, error)) ([]libdns.Record, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Key == "" {
		return nil, errors.New("Google Cloud DNS has no key: hi net domain <domain> --dns googleclouddns --key-file <key.json>")
	}
	project := g.Project
	if project == "" {
		var err error
		if project, err = hiGoogleDNSKey([]byte(g.Key)); err != nil {
			return nil, err
		}
		if project == "" {
			return nil, errors.New("the key names no project (project_id); give it: hi net domain <domain> --dns googleclouddns --project <id>")
		}
	}
	file, err := os.CreateTemp(os.Getenv("XDG_RUNTIME_DIR"), "hi-gcp-dns-*.json") // "" is the system's temporary folder
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString(g.Key); err != nil {
		file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	// The library makes its client from the file on its first call and
	// keeps it; a call that failed before then makes it again.
	if g.inner == nil || g.inner.Project != project {
		g.inner = &googleclouddns.Provider{Project: project}
	}
	g.inner.ServiceAccountJSON = file.Name()
	records, err := call(g.inner)
	g.inner.ServiceAccountJSON = ""
	if err != nil {
		return records, fmt.Errorf("Google Cloud DNS (project %s): %w", project, err)
	}
	return records, nil
}

func (g *hiGoogleDNS) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	return g.with(func(p *googleclouddns.Provider) ([]libdns.Record, error) { return p.GetRecords(ctx, zone) })
}

func (g *hiGoogleDNS) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	return g.with(func(p *googleclouddns.Provider) ([]libdns.Record, error) { return p.SetRecords(ctx, zone, records) })
}

func (g *hiGoogleDNS) AppendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	return g.with(func(p *googleclouddns.Provider) ([]libdns.Record, error) { return p.AppendRecords(ctx, zone, records) })
}

func (g *hiGoogleDNS) DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	return g.with(func(p *googleclouddns.Provider) ([]libdns.Record, error) { return p.DeleteRecords(ctx, zone, records) })
}

// hiExpandHome reads ~/ at a path's start as the user's home, as a shell
// would for a path typed at hi's prompt.
func hiExpandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}
