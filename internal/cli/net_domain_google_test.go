package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libdns/googleclouddns"
	"github.com/libdns/libdns"
)

const hiTestGoogleKey = `{
  "type": "service_account",
  "project_id": "demo-dns",
  "private_key_id": "0",
  "private_key": "-----BEGIN PRIVATE KEY-----\nnot a key\n-----END PRIVATE KEY-----\n",
  "client_email": "hi-dns@demo-dns.iam.gserviceaccount.com"
}`

// hi net domain --dns googleclouddns: the key from --key-file, checked,
// kept in the settings (the vault), its project from the key.
func TestNetDomainGoogle(t *testing.T) {
	setupTeamTest(t)
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "sa.json")
	os.WriteFile(keyPath, []byte(hiTestGoogleKey), 0o600)
	// The provider is the test's, so nothing reaches Google; its fields
	// are the real one's.
	real := hiDNSKinds["googleclouddns"]
	fake := real
	dns := &appTestDNS{}
	fake.New = func() hiDNSProvider { return dns }
	hiDNSKinds["googleclouddns"] = fake
	t.Cleanup(func() { hiDNSKinds["googleclouddns"] = real })

	var out bytes.Buffer
	args := []string{"dev.example.com", "--dns", "googleclouddns", "--key-file", keyPath, "--email", ""}
	if err := runNetDomain(args, strings.NewReader(""), &out, io.Discard); err != nil || !strings.Contains(out.String(), "you may delete "+keyPath) {
		t.Fatal(err, out.String())
	}
	domain, ok := loadHiDomain()
	if !ok || domain.Provider != "googleclouddns" || domain.Settings[hiGoogleDNSKeyField] != hiTestGoogleKey || domain.Settings["gcp_project"] != "" {
		t.Fatalf("%+v", domain)
	}
	// Asked for the path when --key-file isn't given; ~/ is the home.
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.WriteFile(filepath.Join(home, "key.json"), []byte(hiTestGoogleKey), 0o600)
	out.Reset()
	if err := runNetDomain([]string{"dev.example.com", "--dns", "googleclouddns", "--project", "other"}, strings.NewReader("~/key.json\n\n"), &out, io.Discard); err != nil {
		t.Fatal(err, out.String())
	}
	if domain, _ := loadHiDomain(); domain.Settings["gcp_project"] != "other" || domain.Settings[hiGoogleDNSKeyField] != hiTestGoogleKey {
		t.Fatalf("%+v", domain)
	}

	// What it refuses: not a key, a key without a project, and the flags
	// with another provider.
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{"type": "authorized_user"}`), 0o600)
	noProject := filepath.Join(dir, "noproject.json")
	os.WriteFile(noProject, []byte(strings.Replace(hiTestGoogleKey, `"demo-dns"`, `""`, 1)), 0o600)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"dev.example.com", "--dns", "googleclouddns", "--key-file", bad}, "isn't a service account's key file"},
		{[]string{"dev.example.com", "--dns", "googleclouddns", "--key-file", noProject}, "--project"},
		{[]string{"dev.example.com", "--dns", "googleclouddns", "--key-file", filepath.Join(dir, "none.json")}, "no such file"},
		{[]string{"dev.example.com", "--dns", "gandi", "--key-file", keyPath}, "are for --dns googleclouddns"},
	} {
		if err := runNetDomain(c.args, strings.NewReader("\n\n"), io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: %v", c.args, err)
		}
	}
	// The settings make the real provider, with the key.
	hiDNSKinds["googleclouddns"] = real
	provider, err := hiDomain{Provider: "googleclouddns", Settings: map[string]string{hiGoogleDNSKeyField: hiTestGoogleKey}}.provider()
	if g, ok := provider.(*hiGoogleDNS); err != nil || !ok || g.Key != hiTestGoogleKey {
		t.Fatalf("%T %v", provider, err)
	}
}

// The library gets the key as a file, mode 0600, only for a call's time.
func TestGoogleDNSKeyFile(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	g := &hiGoogleDNS{Key: hiTestGoogleKey}
	var during string
	_, err := g.with(func(p *googleclouddns.Provider) ([]libdns.Record, error) {
		during = p.ServiceAccountJSON
		if p.Project != "demo-dns" {
			t.Errorf("project %q", p.Project)
		}
		info, err := os.Stat(during)
		data, _ := os.ReadFile(during)
		if err != nil || info.Mode().Perm() != 0o600 || string(data) != hiTestGoogleKey || filepath.Dir(during) != os.Getenv("XDG_RUNTIME_DIR") {
			t.Errorf("%v %v %q", err, info, during)
		}
		return nil, nil
	})
	if err != nil || during == "" {
		t.Fatal(err, during)
	}
	if _, err := os.Stat(during); !os.IsNotExist(err) {
		t.Fatal("the key's file is still there:", err)
	}
	if g.inner.ServiceAccountJSON != "" {
		t.Fatal("the library still names the file")
	}
	// --project wins over the key's; no key says how to give one.
	g.Project = "other"
	g.with(func(p *googleclouddns.Provider) ([]libdns.Record, error) {
		if p.Project != "other" {
			t.Errorf("project %q", p.Project)
		}
		return nil, nil
	})
	if _, err := (&hiGoogleDNS{}).GetRecords(context.Background(), "example.com."); err == nil || !strings.Contains(err.Error(), "--key-file") {
		t.Fatal(err)
	}
}
