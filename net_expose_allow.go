package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

// Who may reach an exposure, beyond its login page.
//
// --allow-ip sets an access rule on the NetBird service through NetBird's
// API, as Reverse Proxy > Access Control does: the proxy turns other
// addresses away before anything else. `netbird expose` has no flag for it.
// The relay refuses everyone until NetBird has the rule, and again whenever
// a check finds it gone.
//
// --vpn puts no public address in front at all: the relay listens on this
// machine's NetBird address, for the devices NetBird lets reach it.

// netExposeAllowCheck is how often hi checks NetBird still has the rule;
// tests shorten it.
var netExposeAllowCheck = time.Minute

// parseNetExposeAllow reads --allow-ip: addresses and CIDR blocks, separated
// by commas. An address is a block of one.
func parseNetExposeAllow(text string) ([]string, error) {
	var cidrs []string
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(part)
		if err != nil {
			address, addrErr := netip.ParseAddr(part)
			if addrErr != nil {
				return nil, fmt.Errorf("--allow-ip %q isn't an address such as 203.0.113.7 or a block such as 203.0.113.0/24", part)
			}
			address = address.Unmap()
			prefix = netip.PrefixFrom(address, address.BitLen())
		}
		cidrs = append(cidrs, prefix.Masked().String())
	}
	if len(cidrs) == 0 {
		return nil, errors.New("--allow-ip names at least one address")
	}
	return cidrs, nil
}

// netExposePrefixes are the blocks in --allow-ip, already checked.
func netExposePrefixes(cidrs []string) []netip.Prefix {
	var prefixes []netip.Prefix
	for _, cidr := range cidrs {
		if prefix, err := netip.ParsePrefix(cidr); err == nil {
			prefixes = append(prefixes, prefix)
		}
	}
	return prefixes
}

// netExposeGuard lets requests through to the relay only while open says
// so and, given blocks, only from them. That second check is for --vpn,
// where the relay sees the device itself; behind NetBird's proxy every
// request comes from the proxy, and the proxy keeps the rule.
func netExposeGuard(next http.Handler, open func() bool, allowed []netip.Prefix) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !open() {
			http.Error(w, "Not open yet: NetBird is getting this address's access rule.", http.StatusServiceUnavailable)
			return
		}
		if len(allowed) > 0 {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			address, err := netip.ParseAddr(host)
			permitted := false
			for _, prefix := range allowed {
				if err == nil && prefix.Contains(address.Unmap()) {
					permitted = true
					break
				}
			}
			if !permitted {
				http.Error(w, "Not from this address.", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// netExposeRestrict sets --allow-ip on the NetBird service behind address,
// the relay at relayPort, and checks NetBird kept it. keep does the same
// again: it puts the rule back if it has gone, and says so when it can't.
// Once a service on a name that stays points at another relay, it is that
// relay's to keep.
func netExposeRestrict(address string, relayPort int, options netExposeOptions) (keep func() error, err error) {
	api, err := newNetbirdAPI()
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	var services []map[string]any
	if err := api.call(http.MethodGet, "/api/reverse-proxies/services", nil, &services); err != nil {
		return nil, err
	}
	id := ""
	for _, service := range services {
		if domain, _ := service["domain"].(string); strings.EqualFold(domain, parsed.Hostname()) {
			id, _ = service["id"].(string)
		}
	}
	if id == "" {
		return nil, fmt.Errorf("NetBird's API has no service for %s, so --allow-ip can't be set", parsed.Hostname())
	}
	path := "/api/reverse-proxies/services/" + url.PathEscape(id)
	keep = func() error {
		var service map[string]any
		if err := api.call(http.MethodGet, path, nil, &service); err != nil {
			return err
		}
		if netExposeHasRule(service, options.AllowIPs) || !netExposeTargets(service, relayPort) {
			return nil
		}
		restrictions, _ := service["access_restrictions"].(map[string]any)
		if restrictions == nil {
			restrictions = map[string]any{}
		}
		restrictions["allowed_cidrs"] = options.AllowIPs
		service["access_restrictions"] = restrictions
		// NetBird leaves secrets out of what it returns: the login page's
		// goes back in, or saving would lose it.
		auth, _ := service["auth"].(map[string]any)
		if auth == nil {
			auth = map[string]any{}
		}
		switch options.Protection {
		case "password":
			auth["password_auth"] = map[string]any{"enabled": true, "password": options.Secret}
		case "pin":
			auth["pin_auth"] = map[string]any{"enabled": true, "pin": options.Secret}
		}
		service["auth"] = auth
		if err := api.call(http.MethodPut, path, service, nil); err != nil {
			return err
		}
		var saved map[string]any
		if err := api.call(http.MethodGet, path, nil, &saved); err != nil {
			return err
		}
		if !netExposeHasRule(saved, options.AllowIPs) {
			return errors.New("NetBird didn't keep the --allow-ip rule")
		}
		return nil
	}
	if err := keep(); err != nil {
		return nil, err
	}
	return keep, nil
}

// netExposeTargets says a service points at the relay on port, or says
// nothing of where it points.
func netExposeTargets(service map[string]any, port int) bool {
	targets, _ := service["targets"].([]any)
	for _, item := range targets {
		target, _ := item.(map[string]any)
		if number, ok := target["port"].(float64); !ok || int(number) == port {
			return true
		}
	}
	return len(targets) == 0
}

// netExposeHasRule says a service lets in exactly the blocks in cidrs.
func netExposeHasRule(service map[string]any, cidrs []string) bool {
	restrictions, _ := service["access_restrictions"].(map[string]any)
	listed, _ := restrictions["allowed_cidrs"].([]any)
	have := map[netip.Prefix]bool{}
	for _, item := range listed {
		text, _ := item.(string)
		prefix, err := netip.ParsePrefix(text)
		if err != nil {
			return false
		}
		have[prefix.Masked()] = true
	}
	want := netExposePrefixes(cidrs)
	if len(have) != len(want) {
		return false
	}
	for _, prefix := range want {
		if !have[prefix] {
			return false
		}
	}
	return true
}

// netExposeVPNName is this machine's name on the NetBird network, else its
// address there; tests replace it.
var netExposeVPNName = func(address string) string {
	out, err := exec.Command(exposeCommand, "status", "--json").Output()
	if err != nil {
		return address
	}
	var status struct {
		FQDN string `json:"fqdn"`
	}
	if json.Unmarshal(out, &status) != nil || status.FQDN == "" {
		return address
	}
	return strings.TrimSuffix(status.FQDN, ".")
}

// netExposeVPNListen opens the relay for --vpn on the service's own port
// when it is free on the NetBird address, so the address is easy to say.
func netExposeVPNListen(address string, port int) (net.Listener, error) {
	if listener, err := net.Listen("tcp", net.JoinHostPort(address, fmt.Sprint(port))); err == nil {
		return listener, nil
	}
	return net.Listen("tcp", net.JoinHostPort(address, "0"))
}
