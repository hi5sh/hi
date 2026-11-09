package cli

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// startingProvider makes a machine, or fails to, and stops it, or fails
// to, as a test sets it.
type startingProvider struct {
	listingProvider
	mu        sync.Mutex
	made      bool   // the machine exists
	makeFails bool   // create fails; made says whether it made it first
	stopFails int    // how many stops fail before one works
	stops     int    // stops asked for
	seen      []bool // whether a record existed when create ran
}

func (p *startingProvider) name() string              { return "runpod" }
func (p *startingProvider) check() providerStatus      { return providerStatus{installed: true, signedIn: true} }
func (p *startingProvider) maxLifetime() time.Duration { return 0 }
func (p *startingProvider) enforcesLifetime() bool     { return false }
func (p *startingProvider) create(request upRequest, stdout, stderr io.Writer) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	records, _ := loadComputeRecords()
	_, ok := records[request.name]
	p.seen = append(p.seen, ok && records[request.name].Starting && !records[request.name].Deadline.IsZero())
	if p.makeFails {
		return errors.New("the pod didn't start in time")
	}
	p.made = true
	return nil
}
func (p *startingProvider) list() ([]computeInstance, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.made {
		return []computeInstance{{name: "gpu-1", state: "running"}}, nil
	}
	return nil, nil
}
func (p *startingProvider) stop(name string, stdout, stderr io.Writer) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stops++
	if p.stops <= p.stopFails {
		return errors.New("network is unreachable")
	}
	p.made = false
	return nil
}

func computeLimitTest(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	previous := startComputeWatcher
	startComputeWatcher = func(string) (int, error) { return 0, nil }
	t.Cleanup(func() { startComputeWatcher = previous })
}

// The record, with its deadline, is there before the machine is made:
// a create that fails after making it still leaves a record the watcher
// stops; one that made nothing leaves none.
func TestComputeRecordBeforeCreate(t *testing.T) {
	computeLimitTest(t)
	request := upRequest{name: "gpu-1", hardware: computeHardware{name: "rtx-4090", rate: "$0.69/hr", paid: true}, max: time.Hour}

	provider := &startingProvider{makeFails: true}
	if err := startInstance(provider, request, true, false, strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Fatal("the start didn't fail")
	}
	if len(provider.seen) != 1 || !provider.seen[0] {
		t.Fatal("create ran with no record and deadline")
	}
	if records, _ := loadComputeRecords(); len(records) != 0 {
		t.Fatalf("a start that made nothing left %v", records)
	}

	provider = &startingProvider{makeFails: true, made: true} // made, then the wait failed
	startInstance(provider, request, true, false, strings.NewReader(""), io.Discard, io.Discard)
	records, _ := loadComputeRecords()
	if record, ok := records["gpu-1"]; !ok || record.Deadline.IsZero() {
		t.Fatalf("a machine that bills has no record: %v", records)
	}

	removeComputeRecord("gpu-1")
	provider = &startingProvider{}
	if err := startInstance(provider, request, true, false, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	records, _ = loadComputeRecords()
	if record := records["gpu-1"]; record.Starting || record.Deadline.IsZero() {
		t.Fatalf("after the start: %+v", record)
	}
}

// A record still starting isn't forgotten because its provider doesn't
// list the machine yet.
func TestComputeStartingRecordKept(t *testing.T) {
	computeLimitTest(t)
	saved := computeProviders
	t.Cleanup(func() { computeProviders = saved })
	provider := &startingProvider{}
	computeProviders = []computeProvider{provider}
	saveComputeRecord(computeRecord{Name: "gpu-1", Provider: "runpod", Created: computeNow(), Deadline: computeNow().Add(time.Hour), Starting: true})
	listInstances(io.Discard, io.Discard)
	if records, _ := loadComputeRecords(); len(records) != 1 {
		t.Fatal("a record still starting was forgotten")
	}
	saveComputeRecord(computeRecord{Name: "gpu-1", Provider: "runpod", Created: computeNow().Add(-2 * time.Hour), Deadline: computeNow().Add(time.Hour), Starting: true})
	listInstances(io.Discard, io.Discard)
	if records, _ := loadComputeRecords(); len(records) != 0 {
		t.Fatal("a start that never made anything is kept for good")
	}
}

// A stop that fails at the deadline is tried again, not given up on.
func TestComputeWatcherRetriesStop(t *testing.T) {
	computeLimitTest(t)
	saved, interval := computeProviders, watchInterval
	t.Cleanup(func() { computeProviders, watchInterval = saved, interval })
	watchInterval = time.Millisecond
	provider := &startingProvider{made: true, stopFails: 3}
	computeProviders = []computeProvider{provider}
	saveComputeRecord(computeRecord{Name: "gpu-1", Provider: "runpod", Created: computeNow().Add(-time.Hour), Deadline: computeNow().Add(-time.Minute)})
	if err := watchComputeInstance("gpu-1", io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if provider.stops != 4 || provider.made {
		t.Fatalf("stops %d, still made %v", provider.stops, provider.made)
	}
}

// --max takes a minute to a year: what rounds to no limit, inf, and nan
// are refused.
func TestComputeLifetimeBounds(t *testing.T) {
	for _, value := range []string{"0.0001", "inf", "nan", "+Inf", "400d", "0", "-1"} {
		if duration, err := parseLifetime(value); err == nil {
			if validateLifetime(&startingProvider{}, duration) == nil {
				t.Errorf("%q is a lifetime: %v", value, duration)
			}
		}
	}
	for value, want := range map[string]time.Duration{"2": 2 * time.Hour, "30m": 30 * time.Minute, "1.5": 90 * time.Minute, "2d": 48 * time.Hour, "none": noLimit} {
		if duration, err := parseLifetime(value); err != nil || duration != want || validateLifetime(&startingProvider{}, duration) != nil {
			t.Errorf("%q: %v %v", value, duration, err)
		}
	}
}

// A name hi makes is one hi takes back.
func TestComputeGeneratedNames(t *testing.T) {
	for _, hardware := range []string{"rtx-4090@community", "h100@lambdalabs", "A100_80GB", "8x H100 SXM", "4090", "@x", "a-very-long-hardware-name-that-goes-on-and-on"} {
		name, err := chooseInstanceName("", hardware)
		if err != nil || !validInstanceName(name) {
			t.Errorf("%q made %q (%v)", hardware, name, err)
		}
	}
}

// hi serve ls forgets a rented server only when its provider listed
// without it: one that couldn't be listed may still bill.
func TestServeListKeepsWhatItCouldntList(t *testing.T) {
	computeLimitTest(t)
	saved := computeProviders
	t.Cleanup(func() { computeProviders = saved })
	computeProviders = []computeProvider{listingProvider{label: "runpod", err: errors.New("the sign-in expired")}}
	writeServed(map[string]servedModel{"qwen-1": {Provider: "runpod"}})
	if err := serveListCommand(nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if served, _ := loadServed(); len(served) != 1 {
		t.Fatal("forgot a server whose provider couldn't be listed")
	}
	computeProviders = []computeProvider{listingProvider{label: "runpod"}}
	serveListCommand(nil, io.Discard, io.Discard)
	if served, _ := loadServed(); len(served) != 0 {
		t.Fatal("kept a server its provider listed without")
	}
}
