package main

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// listingProvider is a signed-in provider that lists what it is given, or
// fails to.
type listingProvider struct {
	computeProvider
	label     string
	instances []computeInstance
	err       error
}

func (p listingProvider) name() string { return p.label }
func (p listingProvider) check() providerStatus {
	return providerStatus{installed: true, signedIn: true}
}
func (p listingProvider) list() ([]computeInstance, error) { return p.instances, p.err }

// A provider that can't list, such as one whose sign-in expired, hides
// neither the other providers' instances nor a lookup by name.
func TestOneProviderThatCantListHidesNoOther(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	saved := computeProviders
	t.Cleanup(func() { computeProviders = saved })
	computeProviders = []computeProvider{
		listingProvider{label: "hf", err: errors.New("Hugging Face rejected the token; run `hi login hf` again")},
		listingProvider{label: "shadeform", instances: []computeInstance{{name: "nv-r4", state: "running"}}},
	}
	provider, name, err := findInstance("nv-r4")
	if err != nil || provider.name() != "shadeform" || name != "nv-r4" {
		t.Fatalf("findInstance: %v %v", provider, err)
	}
	if _, _, err := findInstance("gone"); err == nil || !strings.Contains(err.Error(), "among the providers that answered (hf: Hugging Face rejected") {
		t.Fatalf("findInstance of a missing one: %v", err)
	}
	var stderr strings.Builder
	listed, err := listInstances(io.Discard, &stderr)
	if err != nil || len(listed) != 1 || listed[0].instance.name != "nv-r4" || !strings.Contains(stderr.String(), "list hf instances: Hugging Face rejected") {
		t.Fatalf("listInstances: %v %v %q", listed, err, stderr.String())
	}
}
