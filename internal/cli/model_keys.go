package cli

// Keys hi makes (docs/specs/approved/hi_model_spend.md, release 3): with
// an OpenRouter management key in the vault, hi makes each app a key of
// its own, with the app's monthly budget as the key's limit, so
// OpenRouter stops the app at its budget too, counts each app apart, and
// a leaked key is one app's. The management key can't make model calls,
// so no box ever needs it.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const openRouterManagementPath = "hi/machine/openrouter"

// openRouterManagementKey is the machine's management key, or "".
func openRouterManagementKey() string {
	if !vaultExists() {
		return ""
	}
	store, err := openVault()
	if err != nil {
		return ""
	}
	data, _, found, err := store.get(openRouterManagementPath)
	if err != nil || !found {
		return ""
	}
	return data.Values["management"]
}

// openRouterManage sends one request to OpenRouter's key API with the
// management key.
var openRouterManage = func(management, method, path string, body any) (map[string]any, error) {
	var payload io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		payload = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, appOpenRouterUpstream+"/api/v1"+path, payload)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+management)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("OpenRouter can't be reached: %w", err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var answer map[string]any
	json.Unmarshal(data, &answer)
	if response.StatusCode/100 != 2 {
		message := response.Status
		if e, ok := answer["error"].(map[string]any); ok {
			message = firstNonEmpty(fmt.Sprint(e["message"]), message)
		}
		return answer, fmt.Errorf("OpenRouter answered %d: %s", response.StatusCode, message)
	}
	return answer, nil
}

// openRouterKeyName is a key hi makes: which machine and app it's for.
func openRouterKeyName(app string) string { return "hi " + vaultServerName() + " " + app }

// appManagedLimit is the key's limit for an app's monthly budget: none
// without one.
func appManagedLimit(config appConfig) map[string]any {
	if config.ModelBudget != nil && config.ModelBudget.Monthly > 0 {
		return map[string]any{"limit": config.ModelBudget.Monthly, "limit_reset": "monthly"}
	}
	return map[string]any{"limit": nil, "limit_reset": nil}
}

// appMakeManagedKey makes a key for the app, and gives it and its hash.
func appMakeManagedKey(config appConfig, management string) (string, string, error) {
	body := appManagedLimit(config)
	body["name"] = openRouterKeyName(config.Name)
	answer, err := openRouterManage(management, "POST", "/keys", body)
	if err != nil {
		return "", "", err
	}
	key, _ := answer["key"].(string)
	data, _ := answer["data"].(map[string]any)
	hash, _ := data["hash"].(string)
	if key == "" || hash == "" {
		return "", "", errors.New("OpenRouter made no key")
	}
	return key, hash, nil
}

// appManagedKeyLimit sets OpenRouter's limit on the app's key to its
// monthly budget.
func appManagedKeyLimit(config appConfig) error {
	if config.OpenRouterManaged == "" {
		return nil
	}
	management := openRouterManagementKey()
	if management == "" {
		return errors.New("this machine has no OpenRouter management key (hi server openrouter --management-key)")
	}
	_, err := openRouterManage(management, "PATCH", "/keys/"+config.OpenRouterManaged, appManagedLimit(config))
	return err
}

// appManagedKeyState disables or enables the app's key at OpenRouter, or
// deletes it; best effort, said in what it gives.
func appManagedKeyState(config appConfig, action string) string {
	if config.OpenRouterManaged == "" {
		return ""
	}
	management := openRouterManagementKey()
	if management == "" {
		return "its OpenRouter key stays as it is: this machine has no management key"
	}
	var err error
	switch action {
	case "disable", "enable":
		_, err = openRouterManage(management, "PATCH", "/keys/"+config.OpenRouterManaged, map[string]any{"disabled": action == "disable"})
	case "delete":
		_, err = openRouterManage(management, "DELETE", "/keys/"+config.OpenRouterManaged, nil)
	}
	if err != nil {
		return "its OpenRouter key: " + err.Error()
	}
	return map[string]string{"disable": "its OpenRouter key is disabled", "enable": "its OpenRouter key is enabled again", "delete": "its OpenRouter key is deleted"}[action]
}

// appKeyOpenRouterManaged gives the app a key hi makes, in place of its
// own, a shared one, or the machine's; with rotate, a new one in place of
// the one hi made, the old deleted once the new one is in.
func appKeyOpenRouterManaged(config appConfig, rotate bool, stdout io.Writer) error {
	management := openRouterManagementKey()
	if management == "" {
		return errors.New("this machine has no OpenRouter management key: make one at openrouter.ai/settings/management-keys, then hi server openrouter --management-key")
	}
	if rotate && config.OpenRouterManaged == "" {
		return fmt.Errorf("app %s's OpenRouter key isn't one hi made: hi app key %s openrouter --managed makes one", config.Name, config.Name)
	}
	key, hash, err := appMakeManagedKey(config, management)
	if err != nil {
		return err
	}
	old := config.OpenRouterManaged
	config.OpenRouterManaged, config.OpenRouterFrom = hash, ""
	if err := updateTeamSecretsFile(config.Name, func(s *appSecrets) error {
		s.OpenRouterAPIKey = key
		return nil
	}, false); err != nil {
		openRouterManage(management, "DELETE", "/keys/"+hash, nil)
		return err
	}
	if err := saveTeam(config); err != nil {
		return err
	}
	secrets, _ := loadTeamSecrets(config.Name)
	if err := writeTeamKeys(config.Name, secrets); err != nil {
		return err
	}
	vaultAudit(vaultActor(), "vault.openrouter-key", "hi/app/"+config.Name, map[bool]string{true: "rotated", false: "made"}[rotate])
	limit := "no limit (no budget)"
	if config.ModelBudget != nil && config.ModelBudget.Monthly > 0 {
		limit = formatDollars(config.ModelBudget.Monthly) + " a month, its budget"
	}
	fmt.Fprintf(stdout, "App %s's model calls use a key hi made for it at OpenRouter (%s), with %s as its limit there.\n", config.Name, openRouterKeyName(config.Name), limit)
	if old != "" && old != hash {
		if _, err := openRouterManage(management, "DELETE", "/keys/"+old, nil); err != nil {
			fmt.Fprintf(stdout, "The old key isn't deleted at OpenRouter (%v): delete it there.\n", err)
		} else {
			fmt.Fprintln(stdout, "The old key is deleted at OpenRouter.")
		}
	}
	return nil
}

// openRouterKeyCounts is what OpenRouter counts on a key hi made, by its
// hash, or nil.
type openRouterKeyCounts struct {
	Usage          float64  `json:"usage"`
	UsageMonthly   float64  `json:"usage_monthly"`
	Limit          *float64 `json:"limit"`
	LimitRemaining *float64 `json:"limit_remaining"`
	Disabled       bool     `json:"disabled"`
}

func appManagedKeyCounts(config appConfig) *openRouterKeyCounts {
	if config.OpenRouterManaged == "" {
		return nil
	}
	management := openRouterManagementKey()
	if management == "" {
		return nil
	}
	answer, err := openRouterManage(management, "GET", "/keys/"+config.OpenRouterManaged, nil)
	if err != nil {
		return nil
	}
	data, _ := json.Marshal(answer["data"])
	var counts openRouterKeyCounts
	if json.Unmarshal(data, &counts) != nil {
		return nil
	}
	return &counts
}

// ---------------------------------------------------------------------------
// hi server openrouter

func openRouterCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	usage := usageError{"usage: hi server openrouter [--management-key | --forget]"}
	if len(args) > 1 {
		return usage
	}
	if !vaultExists() {
		return errors.New("this machine has no vault: hi server vault init")
	}
	store, err := openVault()
	if err != nil {
		return err
	}
	if len(args) == 1 {
		switch args[0] {
		case "--management-key":
			key, err := readSecretLine(stdin, stdout, "The OpenRouter management key (openrouter.ai/settings/management-keys, read and write)")
			if err != nil {
				return err
			}
			said, err := openRouterSetManagement(store, key, vaultActor())
			if err != nil {
				return err
			}
			fmt.Fprintln(stdout, said)
			return nil
		case "--forget":
			if _, err := store.purge(openRouterManagementPath); err != nil {
				return err
			}
			vaultAudit(vaultActor(), "vault.openrouter-management", openRouterManagementPath, "forgotten")
			fmt.Fprintln(stdout, "The management key is forgotten; the keys hi made stay, as the apps' own keys. Delete it at OpenRouter too.")
			return nil
		}
		return usage
	}
	if openRouterManagementKey() == "" {
		fmt.Fprintln(stdout, "No OpenRouter management key: with one, hi makes each app a key of its own, with its budget as the key's limit. Make one at openrouter.ai/settings/management-keys (read and write), then: hi server openrouter --management-key")
		return nil
	}
	fmt.Fprintln(stdout, "The OpenRouter management key is in the vault. Keys hi made:")
	any := false
	for _, app := range machineAppNames() {
		config, err := loadTeam(app)
		if err != nil || config.OpenRouterManaged == "" {
			continue
		}
		any = true
		line := fmt.Sprintf("  %-16s %s", app, openRouterKeyName(app))
		if counts := appManagedKeyCounts(config); counts != nil {
			line += fmt.Sprintf("  %s this month", formatDollars(counts.UsageMonthly))
			if counts.Limit != nil {
				line += " of " + formatDollars(*counts.Limit)
			}
			if counts.Disabled {
				line += ", disabled"
			}
		}
		fmt.Fprintln(stdout, line)
	}
	if !any {
		fmt.Fprintln(stdout, "  none yet: hi app key <app> openrouter --managed")
	}
	return nil
}

// openRouterSetManagement checks a management key with OpenRouter (it may
// list keys), and keeps it in the vault.
func openRouterSetManagement(store *vaultStore, key, who string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("no key was entered")
	}
	if _, err := openRouterManage(key, "GET", "/keys", nil); err != nil {
		return "", fmt.Errorf("that isn't an OpenRouter management key that may list keys: %w", err)
	}
	if err := store.put(openRouterManagementPath, vaultData{Values: map[string]string{"management": key}}, who); err != nil {
		return "", err
	}
	vaultAudit(who, "vault.openrouter-management", openRouterManagementPath, "set")
	return "The OpenRouter management key is in the vault. hi app key <app> openrouter --managed makes an app a key of its own, with its budget as the limit; new apps get one by default.", nil
}
