package cli

import (
	"strings"
	"testing"
)

// An app's containers are told by their whole names: pay's pause doesn't
// take pay-ops's boxes, nor pay-app's.
func TestAppContainersByWholeName(t *testing.T) {
	names := []string{
		"hi-box-app-pay", "hi-box-app-pay-proxy", "hi-box-app-pay-memory", "hi-box-app-pay-dev", "hi-box-app-pay-dev-proxy",
		"hi-box-app-pay-app-production-41234", "hi-box-pay-t-olga-1",
		"hi-box-app-pay-ops", "hi-box-app-pay-ops-proxy", "hi-box-app-pay-ops-app-preview-41000", "hi-box-pay-ops-t-ari-1",
		"hi-box-app-pay-app", "hi-box-app-pay-app-proxy", "hi-box-app-pay-app-app-production-42000",
	}
	if got := strings.Join(appTeamContainers("pay", names, false), " "); got != "hi-box-app-pay hi-box-app-pay-proxy hi-box-app-pay-memory hi-box-app-pay-dev hi-box-app-pay-dev-proxy hi-box-app-pay-app-production-41234 hi-box-pay-t-olga-1" {
		t.Fatalf("pay's: %s", got)
	}
	if got := strings.Join(appTeamContainers("pay", names, true), " "); got != "hi-box-app-pay-app-production-41234" {
		t.Fatalf("pay's app boxes: %s", got)
	}
	if got := strings.Join(appTeamContainers("pay-app", names, true), " "); got != "hi-box-app-pay-app-app-production-42000" {
		t.Fatalf("pay-app's app boxes: %s", got)
	}
}
