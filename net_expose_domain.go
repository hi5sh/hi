package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// An address that stays: with --domain, hi net expose publishes its relay
// as a NetBird reverse-proxy service on a name under a domain verified in
// the NetBird account, through NetBird's API. `netbird expose` can't: it
// always puts a random label in front of a custom domain. The service is
// switched off, not removed, when the exposure ends, so the name keeps its
// certificate and the next exposure on it takes it over.

const (
	// netExposeDomainMax is the longest exposure on a domain: the name
	// stays, so there is no new address to hand out each day.
	netExposeDomainMax = 30 * 24 * time.Hour
	// netExposeDomainReady allows for a first certificate.
	netExposeDomainReady = 3 * time.Minute
)

var netExposeDomainName = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// netbirdTokenPath is where hi keeps a NetBird API token for --domain.
func netbirdTokenPath() string {
	return filepath.Join(filepath.Dir(netExposeDir()), "netbird-api-token")
}

// netbirdAPIToken is $NETBIRD_API_TOKEN, else the saved one.
func netbirdAPIToken() (string, error) {
	if token := strings.TrimSpace(os.Getenv("NETBIRD_API_TOKEN")); token != "" {
		return token, nil
	}
	data, err := os.ReadFile(netbirdTokenPath())
	if token := strings.TrimSpace(string(data)); err == nil && token != "" {
		return token, nil
	}
	return "", errors.New("--domain needs a NetBird API token, from a service user that may manage the reverse proxy: in $NETBIRD_API_TOKEN, or saved with hi team domain")
}

func saveNetbirdAPIToken(token string) error {
	if err := os.MkdirAll(filepath.Dir(netbirdTokenPath()), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(netbirdTokenPath(), []byte(strings.TrimSpace(token)+"\n"), 0o600)
}

// netbirdAPIURL is the management server this machine is connected to;
// tests replace it.
var netbirdAPIURL = func() (string, error) {
	out, err := exec.Command(exposeCommand, "status", "--json").Output()
	if err != nil {
		return "", fmt.Errorf("netbird status: %w", err)
	}
	var status struct {
		Management struct {
			URL string `json:"url"`
		} `json:"management"`
	}
	if err := json.Unmarshal(out, &status); err != nil || status.Management.URL == "" {
		return "", errors.New("netbird status doesn't say which management server this machine uses")
	}
	management, err := url.Parse(status.Management.URL)
	if err != nil {
		return "", err
	}
	return "https://" + strings.TrimSuffix(management.Host, ":443"), nil
}

// errNetbirdToken is a token NetBird doesn't take: deleted, expired, or
// mistyped.
var errNetbirdToken = errors.New("NetBird doesn't know the API token (deleted or expired?); hi team domain <domain> --new-token takes another")

type netbirdAPI struct {
	base, token string
	client      *http.Client
}

func newNetbirdAPI() (netbirdAPI, error) {
	token, err := netbirdAPIToken()
	if err != nil {
		return netbirdAPI{}, err
	}
	base, err := netbirdAPIURL()
	if err != nil {
		return netbirdAPI{}, err
	}
	return netbirdAPI{base: strings.TrimRight(base, "/"), token: token, client: &http.Client{Timeout: 30 * time.Second}}, nil
}

func (a netbirdAPI) call(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, _ := json.Marshal(in)
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, a.base+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Token "+a.token)
	request.Header.Set("Accept", "application/json")
	if in != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := a.client.Do(request)
	if err != nil {
		return fmt.Errorf("NetBird's API: %w", err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if response.StatusCode >= 300 {
		var failure struct {
			Message string `json:"message"`
		}
		json.Unmarshal(data, &failure)
		message := firstNonEmpty(failure.Message, strings.TrimSpace(string(data)), response.Status)
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			message += " (the NetBird API token must be a service user's that may manage the reverse proxy)"
		}
		if response.StatusCode == http.StatusUnauthorized || strings.Contains(message, "invalid token") {
			// NetBird says "not found" for a token it doesn't know, and
			// names it: the message stays out of hi's output.
			return fmt.Errorf("NetBird's API, %s %s: %w", method, path, errNetbirdToken)
		}
		return fmt.Errorf("NetBird's API, %s %s: %s", method, path, message)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

type netbirdService struct {
	ID      string          `json:"id,omitempty"`
	Name    string          `json:"name"`
	Domain  string          `json:"domain"`
	Mode    string          `json:"mode,omitempty"`
	Enabled bool            `json:"enabled"`
	Targets []netbirdTarget `json:"targets"`
}

type netbirdTarget struct {
	TargetID   string `json:"target_id"`
	TargetType string `json:"target_type"`
	Protocol   string `json:"protocol"`
	Path       string `json:"path,omitempty"`
	Port       int    `json:"port"`
	Enabled    bool   `json:"enabled"`
}

// netExposePublish points the service on domain at this machine's relay,
// making the service the first time. The protection is the relay's, as
// with netbird expose. stop switches the service off unless another
// exposure has taken it over since.
func netExposePublish(domain, address string, port int) (stop func(), err error) {
	api, err := newNetbirdAPI()
	if err != nil {
		return nil, err
	}
	var peers []struct {
		ID string `json:"id"`
	}
	if err := api.call(http.MethodGet, "/api/peers?ip="+url.QueryEscape(address), nil, &peers); err != nil {
		return nil, err
	}
	if len(peers) != 1 {
		return nil, fmt.Errorf("NetBird's API knows no peer at %s, this machine", address)
	}
	peer := peers[0].ID
	var services []netbirdService
	if err := api.call(http.MethodGet, "/api/reverse-proxies/services", nil, &services); err != nil {
		return nil, err
	}
	service := netbirdService{Name: domain, Domain: domain, Mode: "http", Enabled: true,
		Targets: []netbirdTarget{{TargetID: peer, TargetType: "peer", Protocol: "http", Path: "/", Port: port, Enabled: true}}}
	for _, existing := range services {
		if existing.Domain == domain {
			service.ID, service.Name = existing.ID, firstNonEmpty(existing.Name, domain)
		}
	}
	var saved netbirdService
	if service.ID != "" {
		err = api.call(http.MethodPut, "/api/reverse-proxies/services/"+url.PathEscape(service.ID), service, &saved)
	} else {
		err = api.call(http.MethodPost, "/api/reverse-proxies/services", service, &saved)
	}
	if err != nil {
		if strings.Contains(err.Error(), "domain") {
			err = fmt.Errorf("%w; is %s under a custom domain verified in NetBird (Reverse Proxy > Custom Domains)?", err, domain)
		}
		return nil, err
	}
	id := firstNonEmpty(saved.ID, service.ID)
	return func() {
		var now netbirdService
		if api.call(http.MethodGet, "/api/reverse-proxies/services/"+url.PathEscape(id), nil, &now) != nil {
			return
		}
		if !now.Enabled || len(now.Targets) != 1 || now.Targets[0].TargetID != peer || now.Targets[0].Port != port {
			return // another exposure has it now
		}
		now.Enabled = false
		api.call(http.MethodPut, "/api/reverse-proxies/services/"+url.PathEscape(id), now, nil)
	}, nil
}

// netExposeLookup resolves a name to the proxies that serve it; tests
// replace it.
var netExposeLookup = net.DefaultResolver.LookupHost

// waitForEveryProxy waits until every proxy NetBird answers the name from
// serves it: a new name's certificate reaches them one by one, and a
// browser that picks one without it gets no page at all. It says which
// still don't when time runs out.
func waitForEveryProxy(host string, port string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ready := map[string]bool{}
	for {
		addresses, _ := netExposeLookup(context.Background(), host)
		var waiting []string
		for _, address := range addresses {
			if ready[address] {
				continue
			}
			if netExposeProxyServes(host, netExposeProxyAddress(address, port)) {
				ready[address] = true
			} else {
				waiting = append(waiting, address)
			}
		}
		if len(addresses) > 0 && len(waiting) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s doesn't answer from every NetBird proxy yet (%s): some visitors get no page for a few more minutes", host, strings.Join(waiting, ", "))
		}
		time.Sleep(5 * time.Second)
	}
}

// netExposeProxyServes says one proxy answers the name over HTTPS with
// anything but NetBird's own "can't reach the service".
func netExposeProxyServes(host, address string) bool {
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, address)
			},
			TLSClientConfig: &tls.Config{ServerName: host, RootCAs: netExposeProxyRoots},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	request, _ := http.NewRequest(http.MethodGet, "https://"+host+"/", nil)
	request.Header.Set(netExposeProbeHeader, "1")
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	response.Body.Close()
	return response.StatusCode != http.StatusBadGateway && response.StatusCode != http.StatusNotFound
}

// netExposeProxyAddress is where to reach one proxy; tests replace it.
var netExposeProxyAddress = net.JoinHostPort

// netExposeProxyRoots are the certificates trusted for the proxies; nil
// is the system's, and tests set their own.
var netExposeProxyRoots *x509.CertPool
