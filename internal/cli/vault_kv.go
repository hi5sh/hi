package cli

// A team's OpenBao or Vault (hi_server_vault.md, release 3): KV over
// HTTP, written with net/http and no Vault library, the same code for
// both. Login is a JWT hi signs with a key pair of its own (recommended:
// no secret is handed over) or an AppRole; the token is in memory only.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// vaultConn is one team store hi reads, or keeps hi's own paths in: where
// it is, how hi logs in, and the login's secrets. Every connection is in
// the built-in store at hi/machine/vault.
type vaultConn struct {
	// Prefix is hi's name for it: corp names corp/payments/prod. A moved
	// store's is "hi".
	Prefix    string `json:"prefix"`
	Address   string `json:"address"`
	Mount     string `json:"mount"`
	Path      string `json:"path,omitempty"` // in the mount
	Namespace string `json:"namespace,omitempty"`
	CA        string `json:"ca,omitempty"` // PEM, the team's CA
	// Login is jwt or approle, at AuthMount, as Role.
	Login     string `json:"login"`
	AuthMount string `json:"auth_mount"`
	Role      string `json:"role"`
	// Seed is the jwt login's Ed25519 key; OldSeed the one before --new-key,
	// which still logs in until the store's owners swap the public key.
	Seed     []byte `json:"seed,omitempty"`
	OldSeed  []byte `json:"old_seed,omitempty"`
	RoleID   string `json:"role_id,omitempty"`
	SecretID string `json:"secret_id,omitempty"`
	// SecretIDEnds is when the AppRole's secret ID ends, as the store said.
	SecretIDEnds time.Time `json:"secret_id_ends,omitzero"`
	KV           int       `json:"kv"` // 1 or 2; 0 until known
	Created      time.Time `json:"created"`
	// Forgot says a moved store's built-in copy was forgotten.
	Forgot bool `json:"forgot,omitempty"`
}

// vaultConnSubject is the server's name in its JWTs.
func vaultConnSubject() string {
	return "hi-server:" + vaultServerName()
}

func vaultServerName() string {
	name := adminMachineName()
	if i := strings.IndexByte(name, '.'); i > 0 {
		name = name[:i]
	}
	return strings.ToLower(name)
}

// vaultConnPublicPEM is the jwt login's public key, PEM, for the store's
// owners.
func vaultConnPublicPEM(seed []byte) string {
	if len(seed) != ed25519.SeedSize {
		return ""
	}
	public := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		return ""
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// vaultJWT is a login JWT, good for five minutes, signed with seed.
func vaultJWT(seed []byte, now time.Time) string {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iss": "hi", "sub": vaultConnSubject(), "aud": "hi",
		"iat": now.Unix(), "nbf": now.Add(-time.Minute).Unix(), "exp": now.Add(5 * time.Minute).Unix()})
	unsigned := header + "." + enc.EncodeToString(claims)
	signature := ed25519.Sign(ed25519.NewKeyFromSeed(seed), []byte(unsigned))
	return unsigned + "." + enc.EncodeToString(signature)
}

// vaultKVError is the store's answer when it isn't a success.
type vaultKVError struct {
	Status int
	Errors []string
	Op     string
}

func (e *vaultKVError) Error() string {
	why := strings.Join(e.Errors, "; ")
	switch {
	case e.Status == http.StatusForbidden:
		return fmt.Sprintf("the team's store refused %s (permission denied)%s", e.Op, firstNonEmpty(map[bool]string{true: ": " + why}[why != "" && why != "permission denied"], ""))
	case e.Status == http.StatusServiceUnavailable && strings.Contains(strings.ToLower(why), "sealed"):
		return "the team's store is sealed"
	case e.Status == http.StatusNotFound:
		return fmt.Sprintf("the team's store has no %s", e.Op)
	}
	return fmt.Sprintf("the team's store answered %d to %s: %s", e.Status, e.Op, firstNonEmpty(why, http.StatusText(e.Status)))
}

// vaultKVRefused is whether err is a refusal (403), as opposed to the
// store being away.
func vaultKVRefused(err error) bool {
	var kv *vaultKVError
	return errors.As(err, &kv) && kv.Status == http.StatusForbidden
}

func vaultKVMissing(err error) bool {
	var kv *vaultKVError
	return errors.As(err, &kv) && kv.Status == http.StatusNotFound
}

// vaultKV is a logged-in client for one connection.
type vaultKV struct {
	conn   vaultConn
	client *http.Client
	mu     sync.Mutex
	token  string
	until  time.Time
}

var (
	vaultKVMu      sync.Mutex
	vaultKVClients = map[string]*vaultKV{}
)

// vaultKVFor is the client for conn, one per process and connection.
func vaultKVFor(conn vaultConn) (*vaultKV, error) {
	if err := vaultCheckAddress(conn.Address); err != nil {
		return nil, err
	}
	key := conn.Prefix + "\x00" + conn.Address + "\x00" + conn.Mount + "\x00" + conn.Role + "\x00" + string(conn.Seed) + string(conn.OldSeed) + conn.SecretID
	vaultKVMu.Lock()
	defer vaultKVMu.Unlock()
	if kv := vaultKVClients[key]; kv != nil {
		kv.conn = conn
		return kv, nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if conn.CA != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(conn.CA)) {
			return nil, errors.New("the team's CA isn't a PEM certificate")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	kv := &vaultKV{conn: conn, client: &http.Client{Timeout: 20 * time.Second, Transport: transport}}
	vaultKVClients[key] = kv
	return kv, nil
}

// vaultKVFresh is a client of its own, logged in anew: to check a key.
func vaultKVFresh(conn vaultConn) (*vaultKV, error) {
	if err := vaultCheckAddress(conn.Address); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if conn.CA != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(conn.CA)) {
			return nil, errors.New("the team's CA isn't a PEM certificate")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &vaultKV{conn: conn, client: &http.Client{Timeout: 20 * time.Second, Transport: transport}}, nil
}

// vaultCheckAddress takes https, and http only to this machine.
func vaultCheckAddress(address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%q isn't the store's address, such as https://bao.corp.example:8200", address)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if host := u.Hostname(); host == "127.0.0.1" || host == "::1" || host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsLoopback() {
			return nil
		}
		return errors.New("the store's address is https://, or http:// to 127.0.0.1 only: a token and the secrets cross it")
	}
	return fmt.Errorf("%q isn't the store's address, such as https://bao.corp.example:8200", address)
}

// do sends one request; body is JSON, or nil.
func (kv *vaultKV) do(method, path string, body any, op string, authed bool) (map[string]any, error) {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, strings.TrimRight(kv.conn.Address, "/")+"/v1/"+strings.TrimLeft(path, "/"), payload)
	if err != nil {
		return nil, err
	}
	if kv.conn.Namespace != "" {
		request.Header.Set("X-Vault-Namespace", kv.conn.Namespace)
	}
	if authed {
		token, err := kv.login()
		if err != nil {
			return nil, err
		}
		request.Header.Set("X-Vault-Token", token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := kv.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("the team's store at %s doesn't answer: %w", kv.conn.Address, err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	var answer map[string]any
	if len(bytes.TrimSpace(data)) > 0 {
		json.Unmarshal(data, &answer)
	}
	if response.StatusCode/100 != 2 {
		failure := &vaultKVError{Status: response.StatusCode, Op: op}
		if list, ok := answer["errors"].([]any); ok {
			for _, item := range list {
				failure.Errors = append(failure.Errors, fmt.Sprint(item))
			}
		}
		if response.StatusCode == http.StatusForbidden && authed {
			kv.mu.Lock()
			kv.token = "" // an expired token: the next request logs in again
			kv.mu.Unlock()
		}
		return answer, failure
	}
	return answer, nil
}

// login is a token, logging in again a minute before it ends.
func (kv *vaultKV) login() (string, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	if kv.token != "" && time.Now().Before(kv.until) {
		return kv.token, nil
	}
	mount := firstNonEmpty(kv.conn.AuthMount, kv.conn.Login)
	var bodies []map[string]any
	switch kv.conn.Login {
	case "jwt":
		for _, seed := range [][]byte{kv.conn.Seed, kv.conn.OldSeed} {
			if len(seed) == ed25519.SeedSize {
				bodies = append(bodies, map[string]any{"role": kv.conn.Role, "jwt": vaultJWT(seed, time.Now())})
			}
		}
	case "approle":
		bodies = append(bodies, map[string]any{"role_id": kv.conn.RoleID, "secret_id": kv.conn.SecretID})
	default:
		return "", fmt.Errorf("no login %q: jwt or approle", kv.conn.Login)
	}
	var last error
	for _, body := range bodies {
		kv.mu.Unlock()
		answer, err := kv.do("POST", "auth/"+mount+"/login", body, "hi's login ("+kv.conn.Login+" at auth/"+mount+", role "+firstNonEmpty(kv.conn.Role, "its AppRole")+")", false)
		kv.mu.Lock()
		if err != nil {
			last = err
			continue
		}
		auth, _ := answer["auth"].(map[string]any)
		token, _ := auth["client_token"].(string)
		if token == "" {
			last = errors.New("the team's store gave no token")
			continue
		}
		lease, _ := auth["lease_duration"].(float64)
		if lease <= 0 {
			lease = 300
		}
		kv.token, kv.until = token, time.Now().Add(time.Duration(lease)*time.Second-time.Minute)
		return token, nil
	}
	if last == nil {
		last = errors.New("hi has no key to log in with")
	}
	return "", last
}

// full is a path in the mount, from hi's path under the connection.
func (kv *vaultKV) full(path string) string {
	parts := []string{}
	for _, part := range []string{kv.conn.Path, path} {
		if part = strings.Trim(part, "/"); part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "/")
}

// vaultKVVersion is one version of a path in the team's store.
type vaultKVVersion struct {
	Version   int
	Created   time.Time
	Deleted   bool
	Destroyed bool
}

// vaultKVSecret is what a read gives: the names and values, and the
// version (0 for KV v1).
type vaultKVSecret struct {
	Data    map[string]any
	Version int
	Created time.Time
}

// kind is the mount's KV version, asked once.
func (kv *vaultKV) kind() int {
	if kv.conn.KV == 1 || kv.conn.KV == 2 {
		return kv.conn.KV
	}
	answer, err := kv.do("GET", "sys/internal/ui/mounts/"+kv.conn.Mount, nil, "the mount "+kv.conn.Mount, true)
	if err == nil {
		data, _ := answer["data"].(map[string]any)
		options, _ := data["options"].(map[string]any)
		if fmt.Sprint(options["version"]) == "1" {
			kv.conn.KV = 1
			return 1
		}
	}
	kv.conn.KV = 2
	return 2
}

// read is a path's latest version, or version when it isn't 0.
func (kv *vaultKV) read(path string, version int) (vaultKVSecret, error) {
	full := kv.full(path)
	if kv.kind() == 1 {
		answer, err := kv.do("GET", kv.conn.Mount+"/"+full, nil, kv.conn.Mount+"/"+full, true)
		if err != nil {
			return vaultKVSecret{}, err
		}
		data, _ := answer["data"].(map[string]any)
		return vaultKVSecret{Data: data}, nil
	}
	query := ""
	if version > 0 {
		query = "?version=" + strconv.Itoa(version)
	}
	answer, err := kv.do("GET", kv.conn.Mount+"/data/"+full+query, nil, kv.conn.Mount+"/data/"+full, true)
	if err != nil {
		return vaultKVSecret{}, err
	}
	outer, _ := answer["data"].(map[string]any)
	data, _ := outer["data"].(map[string]any)
	meta, _ := outer["metadata"].(map[string]any)
	secret := vaultKVSecret{Data: data}
	if n, ok := meta["version"].(float64); ok {
		secret.Version = int(n)
	}
	if created, ok := meta["created_time"].(string); ok {
		secret.Created, _ = time.Parse(time.RFC3339Nano, created)
	}
	if data == nil {
		return secret, &vaultKVError{Status: http.StatusNotFound, Op: kv.conn.Mount + "/data/" + full}
	}
	return secret, nil
}

// current is a path's latest version number, from its metadata; KV v1
// has none (0).
func (kv *vaultKV) current(path string) (int, map[int]vaultKVVersion, error) {
	if kv.kind() == 1 {
		return 0, nil, nil
	}
	full := kv.full(path)
	answer, err := kv.do("GET", kv.conn.Mount+"/metadata/"+full, nil, kv.conn.Mount+"/metadata/"+full, true)
	if err != nil {
		return 0, nil, err
	}
	data, _ := answer["data"].(map[string]any)
	current := 0
	if n, ok := data["current_version"].(float64); ok {
		current = int(n)
	}
	versions := map[int]vaultKVVersion{}
	if listed, ok := data["versions"].(map[string]any); ok {
		for key, value := range listed {
			n, err := strconv.Atoi(key)
			if err != nil {
				continue
			}
			item, _ := value.(map[string]any)
			v := vaultKVVersion{Version: n}
			if created, ok := item["created_time"].(string); ok {
				v.Created, _ = time.Parse(time.RFC3339Nano, created)
			}
			v.Destroyed, _ = item["destroyed"].(bool)
			if deleted, ok := item["deletion_time"].(string); ok && deleted != "" {
				v.Deleted = true
			}
			versions[n] = v
		}
	}
	return current, versions, nil
}

// write puts a new version; it gives its number (0 for KV v1).
func (kv *vaultKV) write(path string, data map[string]string) (int, error) {
	full := kv.full(path)
	values := map[string]any{}
	for k, v := range data {
		values[k] = v
	}
	if kv.kind() == 1 {
		_, err := kv.do("POST", kv.conn.Mount+"/"+full, values, kv.conn.Mount+"/"+full, true)
		return 0, err
	}
	answer, err := kv.do("POST", kv.conn.Mount+"/data/"+full, map[string]any{"data": values}, "writing "+kv.conn.Mount+"/data/"+full, true)
	if err != nil {
		return 0, err
	}
	out, _ := answer["data"].(map[string]any)
	n, _ := out["version"].(float64)
	return int(n), nil
}

// destroy destroys one version's value.
func (kv *vaultKV) destroy(path string, version int) error {
	if kv.kind() == 1 {
		return errors.New("a KV v1 mount keeps no versions to destroy")
	}
	full := kv.full(path)
	_, err := kv.do("POST", kv.conn.Mount+"/destroy/"+full, map[string]any{"versions": []int{version}}, "destroying a version of "+kv.conn.Mount+"/"+full, true)
	return err
}

// remove deletes a path, versions and all.
func (kv *vaultKV) remove(path string) error {
	full := kv.full(path)
	where := kv.conn.Mount + "/metadata/" + full
	if kv.kind() == 1 {
		where = kv.conn.Mount + "/" + full
	}
	_, err := kv.do("DELETE", where, nil, "deleting "+where, true)
	if vaultKVMissing(err) {
		return nil
	}
	return err
}

// list is every path under prefix, recursively, relative to the
// connection.
func (kv *vaultKV) list(prefix string) ([]string, error) {
	var out []string
	var walk func(dir string) error
	walk = func(dir string) error {
		where := kv.conn.Mount + "/metadata/" + kv.full(dir)
		if kv.kind() == 1 {
			where = kv.conn.Mount + "/" + kv.full(dir)
		}
		answer, err := kv.do("LIST", strings.TrimRight(where, "/")+"/", nil, "listing "+where, true)
		if vaultKVMissing(err) {
			return nil
		}
		if err != nil {
			return err
		}
		data, _ := answer["data"].(map[string]any)
		keys, _ := data["keys"].([]any)
		for _, key := range keys {
			name := fmt.Sprint(key)
			child := strings.Trim(strings.TrimRight(dir, "/")+"/"+name, "/")
			if strings.HasSuffix(name, "/") {
				if err := walk(child); err != nil {
					return err
				}
				continue
			}
			out = append(out, child)
		}
		return nil
	}
	err := walk(prefix)
	sort.Strings(out)
	return out, err
}

// vaultKVStrings is a secret's string values, and why each other name is
// left out.
func vaultKVStrings(data map[string]any) (map[string]string, map[string]string) {
	values, left := map[string]string{}, map[string]string{}
	for name, value := range data {
		text, ok := value.(string)
		if !ok {
			left[name] = "its value isn't a string"
			continue
		}
		values[name] = text
	}
	return values, left
}

// vaultReadCA reads --ca's file.
func vaultReadCA(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !bytes.Contains(data, []byte("BEGIN CERTIFICATE")) {
		return "", fmt.Errorf("%s isn't a PEM certificate", path)
	}
	return string(data), nil
}
