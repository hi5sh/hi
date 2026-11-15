package cli

// An app's model budget (docs/specs/approved/hi_model_spend.md, release
// 2): monthly and daily, in dollars, of what its calls cost through hi.
// The app's service writes budget.json after each minute's sums, beside
// the key file every proxy reads per call; at 100% the proxies refuse the
// calls that cost dollars, in each API's own error shape, and calls a
// plan pays go on. The ops channel hears at 80%, at 100%, and when calls
// go through again.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// appModelBudget is app.json's model_budget.
type appModelBudget struct {
	Monthly float64 `json:"monthly,omitempty"`
	Daily   float64 `json:"daily,omitempty"`
}

// appBudgetState is budget.json: what the proxies read.
type appBudgetState struct {
	Month    string  `json:"month"`
	Day      string  `json:"day"`
	MonthUSD float64 `json:"month_usd"`
	DayUSD   float64 `json:"day_usd"`
	Monthly  float64 `json:"monthly,omitempty"`
	Daily    float64 `json:"daily,omitempty"`
	// Stopped is "monthly" or "daily" when that budget is spent; Message
	// is what a refused call says.
	Stopped string `json:"stopped,omitempty"`
	Message string `json:"message,omitempty"`
}

const appBudgetFile = "budget.json"

// appBudgetPath is budget.json in the app's keys folder, which every
// proxy of the app has mounted at /secrets/team.
func appBudgetPath(team string) string { return filepath.Join(appKeysDir(team), appBudgetFile) }

// appBudgetTold is run/models/budget-told.json: which notices went out.
type appBudgetTold struct {
	Month80  string `json:"month80,omitempty"`
	Month100 string `json:"month100,omitempty"`
	Day80    string `json:"day80,omitempty"`
	Day100   string `json:"day100,omitempty"`
	Stopped  string `json:"stopped,omitempty"`
}

func appBudgetToldPath(team string) string {
	return filepath.Join(appModelsDirOf(team), "budget-told.json")
}

// appBudgetStateOf is the app's budget against its sums now.
func appBudgetStateOf(config appConfig, now time.Time) appBudgetState {
	now = now.UTC()
	state := appBudgetState{Month: now.Format("2006-01"), Day: now.Format(time.DateOnly)}
	for _, r := range loadModelMonth(config.Name, state.Month).Rows {
		state.MonthUSD += r.USD
		if r.Day == state.Day {
			state.DayUSD += r.USD
		}
	}
	if config.ModelBudget != nil {
		state.Monthly, state.Daily = config.ModelBudget.Monthly, config.ModelBudget.Daily
	}
	switch {
	case state.Monthly > 0 && state.MonthUSD >= state.Monthly:
		state.Stopped = "monthly"
		state.Message = fmt.Sprintf("App %s spent its %s for %s on models: calls that cost money stop until next month, or until an admin raises the budget (admin page, the app's Spend; or hi app budget %s --monthly <usd>).",
			config.Name, formatDollars(state.Monthly), now.Format("January"), config.Name)
	case state.Daily > 0 && state.DayUSD >= state.Daily:
		state.Stopped = "daily"
		state.Message = fmt.Sprintf("App %s spent its %s for today on models: calls that cost money stop until tomorrow (UTC), or until an admin raises the budget (admin page, the app's Spend; or hi app budget %s --daily <usd>).",
			config.Name, formatDollars(state.Daily), config.Name)
	}
	return state
}

// appWriteBudget writes budget.json for the proxies and tells the ops
// channel what changed: 80%, 100%, and calls going through again.
func appWriteBudget(team string, now time.Time) error {
	config, err := loadTeam(team)
	if err != nil {
		return err
	}
	state := appBudgetStateOf(config, now)
	if err := os.MkdirAll(appKeysDir(team), 0o700); err != nil {
		return err
	}
	data, _ := json.Marshal(state)
	old, _ := os.ReadFile(appBudgetPath(team))
	if string(old) != string(data) {
		if err := writeFileAtomic(appBudgetPath(team), data, 0o644); err != nil {
			return err
		}
	}
	var told appBudgetTold
	if data, err := os.ReadFile(appBudgetToldPath(team)); err == nil {
		json.Unmarshal(data, &told)
	}
	before := told
	say := func(text string) { appOpsSay(config, text) }
	if state.Monthly > 0 {
		if state.MonthUSD >= 0.8*state.Monthly && state.MonthUSD < state.Monthly && told.Month80 != state.Month {
			told.Month80 = state.Month
			say(fmt.Sprintf(":warning: App %s has spent %s of its %s for %s on models (%d%%). At %s its calls that cost money stop until next month, or until an admin raises it.",
				team, formatDollars(state.MonthUSD), formatDollars(state.Monthly), now.UTC().Format("January"), int(100*state.MonthUSD/state.Monthly), formatDollars(state.Monthly)))
		}
		if state.Stopped == "monthly" && told.Month100 != state.Month {
			told.Month100 = state.Month
			say(":octagonal_sign: " + state.Message + " Its agents on a Claude or ChatGPT plan go on.")
		}
	}
	if state.Daily > 0 {
		if state.DayUSD >= 0.8*state.Daily && state.DayUSD < state.Daily && told.Day80 != state.Day {
			told.Day80 = state.Day
			say(fmt.Sprintf(":warning: App %s has spent %s of its %s for today on models (%d%%).", team, formatDollars(state.DayUSD), formatDollars(state.Daily), int(100*state.DayUSD/state.Daily)))
		}
		if state.Stopped == "daily" && told.Day100 != state.Day {
			told.Day100 = state.Day
			say(":octagonal_sign: " + state.Message + " Its agents on a Claude or ChatGPT plan go on.")
		}
	}
	if told.Stopped != "" && state.Stopped == "" {
		say(fmt.Sprintf(":white_check_mark: App %s's model calls go through again: %s of %s this month.", team, formatDollars(state.MonthUSD), firstNonEmpty(map[bool]string{true: formatDollars(state.Monthly)}[state.Monthly > 0], "no budget")))
	}
	told.Stopped = state.Stopped
	if told != before {
		data, _ := json.Marshal(told)
		os.MkdirAll(appModelsDirOf(team), 0o700)
		writeFileAtomic(appBudgetToldPath(team), data, 0o600)
	}
	return nil
}

// appBudgetStop is what a refused paid call says, or "" while the budget
// allows it: read from budget.json beside the key file, per call. A file
// of another day or month is old: the new one is on its way.
func appBudgetStop(path string, now time.Time) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var state appBudgetState
	if json.Unmarshal(data, &state) != nil || state.Stopped == "" {
		return ""
	}
	now = now.UTC()
	if state.Stopped == "monthly" && state.Month != now.Format("2006-01") || state.Stopped == "daily" && state.Day != now.Format(time.DateOnly) {
		return ""
	}
	return state.Message
}

// boxBudgetPath is budget.json beside a proxy's first key file, the
// app's, or "" for a box that isn't an app's.
func boxBudgetPath(keys string) string {
	first, _, _ := strings.Cut(keys, ",")
	if !strings.Contains(first, "/secrets/team") {
		return ""
	}
	return filepath.Join(filepath.Dir(first), appBudgetFile)
}

// appPaidCall is whether a request is a model call that costs money.
func appPaidCall(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	path := r.URL.Path
	return strings.HasSuffix(path, "/chat/completions") || strings.HasSuffix(path, "/completions") || strings.HasSuffix(path, "/responses") ||
		strings.HasSuffix(path, "/messages") || strings.HasSuffix(path, "/embeddings")
}

// appBudgetRefuse answers a refused call in its API's error shape:
// Anthropic's for /messages, OpenAI's and OpenRouter's otherwise.
func appBudgetRefuse(w http.ResponseWriter, r *http.Request, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)
	if strings.HasSuffix(r.URL.Path, "/messages") {
		json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]any{"type": "billing_error", "message": message}})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": http.StatusPaymentRequired, "type": "insufficient_quota", "message": message}})
}

// boxBudgetGate refuses paid calls once the app's budget is spent.
func boxBudgetGate(next http.Handler, budget string, log *boxNetworkLog) http.Handler {
	if budget == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if appPaidCall(r) {
			if message := appBudgetStop(budget, time.Now()); message != "" {
				io.Copy(io.Discard, io.LimitReader(r.Body, 64<<20))
				if log != nil {
					log.record("budget-refused", r.URL.Path)
				}
				appBudgetRefuse(w, r, message)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// appSetBudget sets an app's model budget: a number of dollars, or off
// (0). It says what it did.
func appSetBudget(team string, monthly, daily *float64, who string) (string, error) {
	config, err := loadTeam(team)
	if err != nil {
		return "", err
	}
	budget := appModelBudget{}
	if config.ModelBudget != nil {
		budget = *config.ModelBudget
	}
	if monthly != nil {
		budget.Monthly = *monthly
	}
	if daily != nil {
		budget.Daily = *daily
	}
	if budget.Monthly < 0 || budget.Daily < 0 || budget.Monthly > 1e6 || budget.Daily > 1e6 {
		return "", fmt.Errorf("a budget is dollars, 0 to 1000000")
	}
	if budget.Monthly > 0 && budget.Daily > budget.Monthly {
		return "", fmt.Errorf("a daily budget of %s is more than the monthly %s", formatDollars(budget.Daily), formatDollars(budget.Monthly))
	}
	if budget == (appModelBudget{}) {
		config.ModelBudget = nil
	} else {
		config.ModelBudget = &budget
	}
	if err := saveTeam(config); err != nil {
		return "", err
	}
	if err := appWriteBudget(team, time.Now()); err != nil {
		return "", err
	}
	adminRecord(who, team, "set the model budget to "+appBudgetWords(budget))
	if config.OpenRouterManaged != "" {
		if err := appManagedKeyLimit(config); err != nil {
			return "", fmt.Errorf("the budget is set, but OpenRouter's limit on the app's key isn't: %w", err)
		}
	}
	return fmt.Sprintf("App %s's model budget: %s. hi stops its calls that cost money at it; its ops channel hears at 80%% and 100%%.", team, appBudgetWords(budget)), nil
}

func appBudgetWords(budget appModelBudget) string {
	var parts []string
	if budget.Monthly > 0 {
		parts = append(parts, formatDollars(budget.Monthly)+" a month")
	}
	if budget.Daily > 0 {
		parts = append(parts, formatDollars(budget.Daily)+" a day")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " and ")
}

// parseBudget reads a budget's dollars, or off.
func parseBudget(text string) (float64, error) {
	if text == "off" || text == "none" {
		return 0, nil
	}
	n, err := strconv.ParseFloat(strings.TrimPrefix(text, "$"), 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%q isn't dollars, such as 200, or off", text)
	}
	return n, nil
}
