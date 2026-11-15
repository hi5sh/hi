package cli

// What each model call an app makes through hi used and cost
// (docs/specs/approved/hi_model_spend.md). Every proxy that passes a call
// (an app's models socket, the lead's, a member's, the dev box's, a
// fallback to OpenRouter) writes a `call` line: who made it, the model,
// the tokens, the dollars, and which key paid, by fingerprint. The app's
// service sums them every minute, by month, day, who, and model.

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// modelCall is one call's line. No prompt, no answer, no key.
type modelCall struct {
	Who       string  `json:"who,omitempty"`
	Via       string  `json:"via"`
	Model     string  `json:"model,omitempty"`
	In        int64   `json:"in,omitempty"`
	Out       int64   `json:"out,omitempty"`
	Cached    int64   `json:"cached,omitempty"`
	Reasoning int64   `json:"reasoning,omitempty"`
	USD       float64 `json:"usd,omitempty"`
	// Key is which key paid: app (the app's key file: its own, a shared
	// key, or one hi made), machine (Hermes'), plan (a Claude or ChatGPT
	// sign-in), or caller (one the caller sent); KFP its fingerprint.
	Key string `json:"key,omitempty"`
	KFP string `json:"kfp,omitempty"`
	ID  string `json:"id,omitempty"`
}

func (c modelCall) json() string {
	data, _ := json.Marshal(c)
	return string(data)
}

// used says whether the answer said anything worth a line.
func (c modelCall) used() bool { return c.In > 0 || c.Out > 0 || c.USD > 0 }

// callKeyFingerprint is a key's first 8 hex of SHA-256, never the key.
func callKeyFingerprint(key string) string {
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:4])
}

// ---------------------------------------------------------------------------
// reading an answer's usage

// appCostWhole is how much of an answer that isn't streamed is kept to
// read its usage whole; past it, its end alone.
const appCostWhole = 8 << 20

// appCostReader passes an answer on and, when it ends, gives done what it
// cost, and call the call's usage: OpenRouter's and OpenAI's chat and
// Responses usage, and Anthropic's, streamed (each event's line) or not
// (the whole answer).
type appCostReader struct {
	body io.ReadCloser
	done func(float64)
	call func(modelCall)
	// seed is the call's who, via, and key, which the proxy knows.
	seed modelCall

	started bool
	stream  bool
	line    []byte
	whole   []byte
	over    bool
	tail    []byte
	got     modelCall
	once    sync.Once
}

func (r *appCostReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if n > 0 {
		r.feed(p[:n])
	}
	if err == io.EOF {
		r.finish()
	}
	return n, err
}

func (r *appCostReader) Close() error {
	r.finish()
	return r.body.Close()
}

func (r *appCostReader) feed(data []byte) {
	if !r.started {
		trimmed := strings.TrimLeft(string(data[:min(len(data), 64)]), " \t\r\n")
		if trimmed == "" {
			return
		}
		r.started = true
		r.stream = strings.HasPrefix(trimmed, "data:") || strings.HasPrefix(trimmed, "event:") || strings.HasPrefix(trimmed, ":")
	}
	r.tail = append(r.tail, data...)
	if len(r.tail) > 2*appCostTail {
		r.tail = append([]byte(nil), r.tail[len(r.tail)-appCostTail:]...)
	}
	if !r.stream {
		if !r.over {
			if len(r.whole)+len(data) > appCostWhole {
				r.over, r.whole = true, nil
			} else {
				r.whole = append(r.whole, data...)
			}
		}
		return
	}
	r.line = append(r.line, data...)
	for {
		i := strings.IndexByte(string(r.line), '\n')
		if i < 0 {
			break
		}
		r.event(r.line[:i])
		r.line = r.line[i+1:]
	}
	if len(r.line) > appCostWhole {
		r.line = nil // a line that long isn't usage
	}
}

// event reads one streamed line: the model once, and every usage.
func (r *appCostReader) event(line []byte) {
	text, ok := strings.CutPrefix(strings.TrimSpace(string(line)), "data:")
	if !ok {
		return
	}
	text = strings.TrimSpace(text)
	if !strings.Contains(text, `"usage"`) && (r.got.Model != "" || !strings.Contains(text, `"model"`)) {
		return
	}
	var object map[string]any
	if json.Unmarshal([]byte(text), &object) == nil {
		modelUsageFrom(object, &r.got)
	}
}

func (r *appCostReader) finish() {
	r.once.Do(func() {
		if r.stream {
			r.event(r.line)
		} else if !r.over && len(r.whole) > 0 {
			var object map[string]any
			if json.Unmarshal(r.whole, &object) == nil {
				modelUsageFrom(object, &r.got)
			}
		}
		if r.got.USD == 0 {
			r.got.USD = appAnswerCost(r.tail)
		}
		if r.done != nil {
			r.done(r.got.USD)
		}
		if r.call != nil && r.got.used() {
			call := r.got
			call.Who, call.Via, call.Key, call.KFP = r.seed.Who, r.seed.Via, r.seed.Key, r.seed.KFP
			r.call(call)
		}
	})
}

// modelUsageFrom takes what an answer, or one of its events, says: the
// model, OpenRouter's generation ID, and its usage, the highest of each
// count (Anthropic's stream counts up).
func modelUsageFrom(object map[string]any, c *modelCall) {
	for _, value := range []any{object, object["message"], object["response"]} {
		m, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if model, ok := m["model"].(string); ok && model != "" && c.Model == "" {
			c.Model = model
		}
		if id, ok := m["id"].(string); ok && strings.HasPrefix(id, "gen-") && c.ID == "" {
			c.ID = id
		}
		usage, ok := m["usage"].(map[string]any)
		if !ok {
			continue
		}
		number := func(m map[string]any, names ...string) int64 {
			for _, name := range names {
				if f, ok := m[name].(float64); ok {
					return int64(f)
				}
			}
			return 0
		}
		detail := func(outer string, name string) int64 {
			if m, ok := usage[outer].(map[string]any); ok {
				return number(m, name)
			}
			return 0
		}
		// Anthropic counts cache reads and writes apart from input_tokens;
		// OpenAI's prompt and input tokens hold the cached ones.
		in := number(usage, "prompt_tokens", "input_tokens") + number(usage, "cache_read_input_tokens") + number(usage, "cache_creation_input_tokens")
		cached := max(detail("prompt_tokens_details", "cached_tokens"), detail("input_tokens_details", "cached_tokens"), number(usage, "cache_read_input_tokens"))
		reasoning := max(detail("completion_tokens_details", "reasoning_tokens"), detail("output_tokens_details", "reasoning_tokens"))
		c.In = max(c.In, in)
		c.Out = max(c.Out, number(usage, "completion_tokens", "output_tokens"))
		c.Cached = max(c.Cached, cached)
		c.Reasoning = max(c.Reasoning, reasoning)
		if cost, ok := usage["cost"].(float64); ok && cost > c.USD {
			c.USD = cost
		}
	}
}

// ---------------------------------------------------------------------------
// writing the lines

// boxWhoEnv says who a box's proxy speaks for in its call lines: lead, a
// member's role (boxRoleEnv when this is empty), or dev.
const boxWhoEnv = "HI_BOX_WHO"

func boxCallWho() string {
	return firstNonEmpty(os.Getenv(boxWhoEnv), os.Getenv(boxRoleEnv), "box")
}

// boxRecordCall writes a proxy's call line: in the box's events file when
// it has one (a member's, the dev box's), else in its network log (the
// lead's), which the app's service reads.
func boxRecordCall(log *boxNetworkLog, events string, call modelCall) {
	if events != "" {
		file, err := os.OpenFile(events, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(file, "%s call %s\n", time.Now().UTC().Format(time.RFC3339), call.json())
			file.Close()
			return
		}
	}
	if log != nil {
		log.record("call", call.json())
	}
}

// boxKeyLabel says which of a proxy's key files a key came from: the
// first (the app's) or a later one (this machine's), or none (the
// caller's own).
func boxKeyLabel(files, key string) string {
	for i, file := range strings.Split(files, ",") {
		if value, err := readDotEnvValue(file, "OPENROUTER_API_KEY"); err == nil && value == key {
			if i == 0 && strings.Contains(file, "/secrets/team") {
				return "app"
			}
			return "machine"
		}
	}
	return "caller"
}

func appModelsDirOf(team string) string { return filepath.Join(appDir(team), "run", "models") }

func appCallsLogPath(team string, at time.Time) string {
	return filepath.Join(appModelsDirOf(team), "calls-"+at.UTC().Format("2006-01")+".log")
}

var appCallsMu sync.Mutex

// appRecordModelCall writes a call the app's models socket passed.
func appRecordModelCall(team string, call modelCall) {
	appCallsMu.Lock()
	defer appCallsMu.Unlock()
	now := time.Now().UTC()
	os.MkdirAll(appModelsDirOf(team), 0o700)
	file, err := os.OpenFile(appCallsLogPath(team, now), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintf(file, "%s call %s\n", now.Format(time.RFC3339), call.json())
}

// parseCallLine reads a line of any of the files: a call line, or an
// older proxy's spend line (time spend cost, or the models socket's
// time slot cost), which says only what it cost.
func parseCallLine(line, fallbackWho string) (time.Time, modelCall, bool) {
	at, rest, ok := strings.Cut(line, " ")
	if !ok {
		return time.Time{}, modelCall{}, false
	}
	when, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return time.Time{}, modelCall{}, false
	}
	kind, detail, _ := strings.Cut(rest, " ")
	switch kind {
	case "call":
		var call modelCall
		if json.Unmarshal([]byte(detail), &call) != nil {
			return when, call, false
		}
		if call.Who == "" {
			call.Who = fallbackWho
		}
		return when, call, true
	case "spend", appSlotProduction, appSlotPreview:
		cost, err := strconv.ParseFloat(strings.TrimSpace(detail), 64)
		if err != nil || cost <= 0 {
			return when, modelCall{}, false
		}
		who := fallbackWho
		if kind != "spend" {
			who = kind
		}
		return when, modelCall{Who: who, Via: "openrouter", USD: cost}, true
	}
	return when, modelCall{}, false
}

// ---------------------------------------------------------------------------
// the sums

// modelRow is one day's calls for one who, model, and key.
type modelRow struct {
	Day       string  `json:"day"`
	Who       string  `json:"who"`
	Model     string  `json:"model"`
	Via       string  `json:"via"`
	Key       string  `json:"key"`
	KFP       string  `json:"kfp,omitempty"`
	Calls     int     `json:"calls"`
	In        int64   `json:"in"`
	Out       int64   `json:"out"`
	Cached    int64   `json:"cached,omitempty"`
	Reasoning int64   `json:"reasoning,omitempty"`
	USD       float64 `json:"usd"`
}

// modelMonth is run/models/spend-YYYY-MM.json.
type modelMonth struct {
	Month string     `json:"month"`
	Rows  []modelRow `json:"rows"`
}

func appSpendMonthPath(team, month string) string {
	return filepath.Join(appModelsDirOf(team), "spend-"+month+".json")
}

func loadModelMonth(team, month string) modelMonth {
	m := modelMonth{Month: month}
	if data, err := os.ReadFile(appSpendMonthPath(team, month)); err == nil {
		json.Unmarshal(data, &m)
	}
	m.Month = month
	return m
}

func (m *modelMonth) add(day string, call modelCall) {
	model := firstNonEmpty(call.Model, "(not said)")
	for i := range m.Rows {
		r := &m.Rows[i]
		if r.Day == day && r.Who == call.Who && r.Model == model && r.Via == call.Via && r.Key == call.Key && r.KFP == call.KFP {
			r.Calls++
			r.In += call.In
			r.Out += call.Out
			r.Cached += call.Cached
			r.Reasoning += call.Reasoning
			r.USD += call.USD
			return
		}
	}
	m.Rows = append(m.Rows, modelRow{Day: day, Who: call.Who, Model: model, Via: call.Via, Key: call.Key, KFP: call.KFP,
		Calls: 1, In: call.In, Out: call.Out, Cached: call.Cached, Reasoning: call.Reasoning, USD: call.USD})
}

// modelHours is run/models/keys.json: what each key fingerprint spent,
// by hour, for the last two days, for a shared key's check.
type modelHours map[string]map[string]float64

func appSpendHoursPath(team string) string { return filepath.Join(appModelsDirOf(team), "keys.json") }

func loadModelHours(team string) modelHours {
	hours := modelHours{}
	if data, err := os.ReadFile(appSpendHoursPath(team)); err == nil {
		json.Unmarshal(data, &hours)
	}
	return hours
}

// appCallSources are the files the app's call lines are in, and who a
// line without one is: the models socket's, the lead's network log, and
// each box's events file.
func appCallSources(team string, now time.Time) map[string]string {
	sources := map[string]string{
		appCallsLogPath(team, now):                   "",
		appCallsLogPath(team, now.AddDate(0, -1, 0)): "",
		appSpendLogPath(team):                        "",
		appNetworkLogPath(team):                      "lead",
	}
	if entries, err := os.ReadDir(appEventsDir(team)); err == nil {
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".log") {
				who := strings.TrimSuffix(entry.Name(), ".log")
				if who == appDevBox(team) {
					who = "dev"
				}
				sources[filepath.Join(appEventsDir(team), entry.Name())] = who
			}
		}
	}
	return sources
}

// appSumModelCalls reads the call lines written since the last time, from
// where it left off in each file, into the month's sums and the hours'.
// It's the app's service's, each minute, and hi app budget's.
func appSumModelCalls(team string) error {
	dir := appModelsDirOf(team)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	offsetsPath := filepath.Join(dir, "read.json")
	offsets := map[string]int64{}
	if data, err := os.ReadFile(offsetsPath); err == nil {
		json.Unmarshal(data, &offsets)
	}
	now := time.Now().UTC()
	months := map[string]*modelMonth{}
	hours := loadModelHours(team)
	changed := false
	for path, who := range appCallSources(team, now) {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		from, seen := offsets[path]
		if info.Size() < from {
			from = 0 // the file started over
		}
		if !seen && path == appNetworkLogPath(team) && info.Size() > 8<<20 {
			from = info.Size() - 8<<20 // a long network log: its recent end
		}
		if info.Size() == from {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		file.Seek(from, io.SeekStart)
		reader := bufio.NewReaderSize(file, 64<<10)
		read := from
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break // a line still being written waits for the next time
			}
			read += int64(len(line))
			when, call, ok := parseCallLine(strings.TrimSpace(line), who)
			if !ok || call.Who == "" {
				continue
			}
			month := when.Format("2006-01")
			m := months[month]
			if m == nil {
				loaded := loadModelMonth(team, month)
				m = &loaded
				months[month] = m
			}
			m.add(when.Format(time.DateOnly), call)
			if call.KFP != "" && call.USD > 0 && now.Sub(when) < 48*time.Hour {
				hour := when.Truncate(time.Hour).Format(time.RFC3339)
				if hours[hour] == nil {
					hours[hour] = map[string]float64{}
				}
				hours[hour][call.KFP] += call.USD
			}
			changed = true
		}
		file.Close()
		offsets[path] = read
	}
	for hour := range hours {
		if at, err := time.Parse(time.RFC3339, hour); err != nil || now.Sub(at) > 48*time.Hour {
			delete(hours, hour)
		}
	}
	for month, m := range months {
		sort.Slice(m.Rows, func(i, j int) bool {
			a, b := m.Rows[i], m.Rows[j]
			if a.Day != b.Day {
				return a.Day < b.Day
			}
			if a.Who != b.Who {
				return a.Who < b.Who
			}
			return a.Model < b.Model
		})
		data, _ := json.MarshalIndent(m, "", " ")
		if err := writeFileAtomic(appSpendMonthPath(team, month), append(data, '\n'), 0o600); err != nil {
			return err
		}
	}
	if changed {
		data, _ := json.Marshal(hours)
		writeFileAtomic(appSpendHoursPath(team), data, 0o600)
	}
	data, _ := json.Marshal(offsets)
	if err := writeFileAtomic(offsetsPath, data, 0o600); err != nil {
		return err
	}
	// Call lines of the models socket older than last month go.
	if entries, err := os.ReadDir(dir); err == nil {
		keep := map[string]bool{filepath.Base(appCallsLogPath(team, now)): true, filepath.Base(appCallsLogPath(team, now.AddDate(0, -1, 0))): true}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "calls-") && !keep[entry.Name()] {
				os.Remove(filepath.Join(dir, entry.Name()))
				delete(offsets, filepath.Join(dir, entry.Name()))
			}
		}
	}
	// Sums older than 13 months go.
	if entries, err := os.ReadDir(dir); err == nil {
		oldest := now.AddDate(0, -13, 0).Format("2006-01")
		for _, entry := range entries {
			if month, ok := strings.CutSuffix(strings.TrimPrefix(entry.Name(), "spend-"), ".json"); ok && strings.HasPrefix(entry.Name(), "spend-") && month < oldest {
				os.Remove(filepath.Join(dir, entry.Name()))
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// a month, summed up

// modelSpend is a month of an app's calls, summed for people to read.
type modelSpend struct {
	App     string          `json:"app"`
	Month   string          `json:"month"`
	USD     float64         `json:"usd"`
	Calls   int             `json:"calls"`
	ByWho   []modelSpendRow `json:"by_who"`
	ByModel []modelSpendRow `json:"by_model"`
	ByDay   []modelSpendRow `json:"by_day"`
	// Plans are the calls a Claude or ChatGPT sign-in paid, by who.
	Plans  []modelSpendRow `json:"plans"`
	Months []string        `json:"months"`
}

type modelSpendRow struct {
	Name   string  `json:"name"`
	Calls  int     `json:"calls"`
	In     int64   `json:"in"`
	Out    int64   `json:"out"`
	Cached int64   `json:"cached,omitempty"`
	USD    float64 `json:"usd"`
}

func appModelSpend(team, month string) modelSpend {
	m := loadModelMonth(team, month)
	spend := modelSpend{App: team, Month: month, ByWho: []modelSpendRow{}, ByModel: []modelSpendRow{}, ByDay: []modelSpendRow{}, Plans: []modelSpendRow{}, Months: []string{}}
	group := func(rows *[]modelSpendRow, name string, r modelRow) {
		for i := range *rows {
			if (*rows)[i].Name == name {
				x := &(*rows)[i]
				x.Calls += r.Calls
				x.In += r.In
				x.Out += r.Out
				x.Cached += r.Cached
				x.USD += r.USD
				return
			}
		}
		*rows = append(*rows, modelSpendRow{Name: name, Calls: r.Calls, In: r.In, Out: r.Out, Cached: r.Cached, USD: r.USD})
	}
	for _, r := range m.Rows {
		spend.Calls += r.Calls
		spend.USD += r.USD
		if r.Key == "plan" {
			group(&spend.Plans, r.Who+" ("+r.Via+")", r)
			continue
		}
		group(&spend.ByWho, r.Who, r)
		group(&spend.ByModel, r.Model, r)
		group(&spend.ByDay, r.Day, r)
	}
	byUSD := func(rows []modelSpendRow) {
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].USD != rows[j].USD {
				return rows[i].USD > rows[j].USD
			}
			return rows[i].In+rows[i].Out > rows[j].In+rows[j].Out
		})
	}
	byUSD(spend.ByWho)
	byUSD(spend.ByModel)
	byUSD(spend.Plans)
	sort.Slice(spend.ByDay, func(i, j int) bool { return spend.ByDay[i].Name < spend.ByDay[j].Name })
	if entries, err := os.ReadDir(appModelsDirOf(team)); err == nil {
		for _, entry := range entries {
			if month, ok := strings.CutSuffix(strings.TrimPrefix(entry.Name(), "spend-"), ".json"); ok && strings.HasPrefix(entry.Name(), "spend-") {
				spend.Months = append(spend.Months, month)
			}
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(spend.Months)))
	return spend
}

// appMonthUSD is what an app spent this month through hi, from its sums.
func appMonthUSD(team string) float64 {
	total := 0.0
	for _, r := range loadModelMonth(team, time.Now().UTC().Format("2006-01")).Rows {
		total += r.USD
	}
	return total
}

// appBudgetCommand is hi app budget <name> [--month YYYY-MM]: the month,
// spent, by who and model, and the plans' calls in tokens.
func appBudgetCommand(args []string, stdout io.Writer) error {
	usage := usageError{"usage: hi app budget <name> [--month YYYY-MM]\n       hi app budget <name> [--monthly <usd>|off] [--daily <usd>|off]"}
	if len(args) == 0 {
		return usage
	}
	name := args[0]
	month := time.Now().UTC().Format("2006-01")
	var monthly, daily *float64
	for i := 1; i < len(args); i++ {
		arg := args[i]
		value := func() (string, bool) {
			if _, after, ok := strings.Cut(arg, "="); ok {
				return after, true
			}
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		flag, _, _ := strings.Cut(arg, "=")
		text, ok := value()
		if !ok {
			return usage
		}
		switch flag {
		case "--month":
			month = text
		case "--monthly", "--daily":
			n, err := parseBudget(text)
			if err != nil {
				return err
			}
			if flag == "--monthly" {
				monthly = &n
			} else {
				daily = &n
			}
		default:
			return usage
		}
	}
	if monthly != nil || daily != nil {
		if _, err := loadTeam(name); err != nil {
			return err
		}
		appSumModelCalls(name)
		said, err := appSetBudget(name, monthly, daily, vaultActor())
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, said)
		return nil
	}
	if _, err := time.Parse("2006-01", month); err != nil {
		return fmt.Errorf("%q isn't a month: YYYY-MM, such as %s", month, time.Now().UTC().Format("2006-01"))
	}
	if _, err := loadTeam(name); err != nil {
		return err
	}
	if err := appSumModelCalls(name); err != nil {
		return err
	}
	spend := appModelSpend(name, month)
	at, _ := time.Parse("2006-01", month)
	fmt.Fprintf(stdout, "App %s, %s: %s on models through hi, in %d calls.\n", name, at.Format("January 2006"), formatDollars(spend.USD), spend.Calls)
	if config, err := loadTeam(name); err == nil && month == time.Now().UTC().Format("2006-01") {
		state := appBudgetStateOf(config, time.Now())
		switch {
		case state.Monthly == 0 && state.Daily == 0:
			fmt.Fprintf(stdout, "No budget: hi app budget %s --monthly <usd> sets one.\n", name)
		default:
			if state.Monthly > 0 {
				fmt.Fprintf(stdout, "Budget: %s a month, %d%% spent.\n", formatDollars(state.Monthly), int(100*state.MonthUSD/state.Monthly))
			}
			if state.Daily > 0 {
				fmt.Fprintf(stdout, "Today: %s of %s a day.\n", formatDollars(state.DayUSD), formatDollars(state.Daily))
			}
			if state.Message != "" {
				fmt.Fprintln(stdout, state.Message)
			}
		}
		if counts := appManagedKeyCounts(config); counts != nil {
			line := fmt.Sprintf("OpenRouter counts %s this month on the key hi made for it", formatDollars(counts.UsageMonthly))
			if counts.Limit != nil {
				line += fmt.Sprintf(", of its %s limit", formatDollars(*counts.Limit))
			}
			fmt.Fprintln(stdout, line+".")
		}
	}
	table := func(title string, rows []modelSpendRow, dollars bool) {
		if len(rows) == 0 {
			return
		}
		fmt.Fprintf(stdout, "\n%s\n", title)
		for _, r := range rows {
			cost := ""
			if dollars {
				cost = fmt.Sprintf("%9s  ", formatDollars(r.USD))
			}
			fmt.Fprintf(stdout, "  %-34s %s%5d calls  %7s in  %7s out\n", r.Name, cost, r.Calls, formatTokens(int(r.In)), formatTokens(int(r.Out)))
		}
	}
	table("By who:", spend.ByWho, true)
	table("By model:", spend.ByModel, true)
	table("On a plan (Claude's or ChatGPT's sign-in, not dollars):", spend.Plans, false)
	if spend.Calls == 0 {
		fmt.Fprintln(stdout, "\nNo calls through hi yet this month. Agents' calls show from their boxes' next start on this hi.")
	}
	return nil
}

// appSpentWithKey is what each app on this machine spent this month with
// the key whose fingerprint is kfp.
func appSpentWithKey(kfp string) map[string]float64 {
	spent := map[string]float64{}
	if kfp == "" {
		return spent
	}
	month := time.Now().UTC().Format("2006-01")
	for _, app := range machineAppNames() {
		for _, r := range loadModelMonth(app, month).Rows {
			if r.KFP == kfp && r.USD > 0 {
				spent[app] += r.USD
			}
		}
	}
	return spent
}

// ---------------------------------------------------------------------------
// a shared key spending outside hi

// openRouterKeyUsage is what OpenRouter has counted on a key, all time,
// in dollars; tests replace it.
var openRouterKeyUsage = func(key string) (float64, error) {
	request, _ := http.NewRequest("GET", appOpenRouterUpstream+"/api/v1/key", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("OpenRouter answered %d", response.StatusCode)
	}
	var answer struct {
		Data struct {
			Usage float64 `json:"usage"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		return 0, err
	}
	return answer.Data.Usage, nil
}

// sharedOutside is vault/shared-outside.json: the last time each shared
// key spent outside hi's apps, and how much.
type sharedOutside struct {
	At  time.Time `json:"at"`
	USD float64   `json:"usd"`
}

func sharedOutsidePath() string { return filepath.Join(vaultDir(), "shared-outside.json") }

func loadSharedOutside() map[string]sharedOutside {
	outside := map[string]sharedOutside{}
	if data, err := os.ReadFile(sharedOutsidePath()); err == nil {
		json.Unmarshal(data, &outside)
	}
	return outside
}

type sharedKeySample struct {
	At    time.Time
	Usage float64
}

var sharedKeyWatch = struct {
	sync.Mutex
	samples map[string][]sharedKeySample
}{samples: map[string][]sharedKeySample{}}

// checkSharedKeys compares, for each shared key an app here uses as its
// OpenRouter key, what OpenRouter counted on it in about the last hour
// with what the apps here spent with it; more than $1 and 10% apart is
// told once in six hours. It says what it would tell, for the server to
// post.
func checkSharedKeys(now time.Time) []string {
	if !vaultExists() {
		return nil
	}
	names := map[string]bool{}
	for _, app := range machineAppNames() {
		if config, err := loadTeam(app); err == nil {
			if name, ok := appSharedName(config.OpenRouterFrom); ok {
				names[name] = true
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	store, err := openVault()
	if err != nil {
		return nil
	}
	var told []string
	outside := loadSharedOutside()
	changed := false
	for _, name := range sortedKeys(names) {
		data, _, found, err := store.get(vaultSharedPath(name))
		if err != nil || !found {
			continue
		}
		key := data.Values["value"]
		usage, err := openRouterKeyUsage(key)
		if err != nil {
			continue
		}
		sharedKeyWatch.Lock()
		samples := append(sharedKeyWatch.samples[name], sharedKeySample{At: now, Usage: usage})
		for len(samples) > 0 && now.Sub(samples[0].At) > 3*time.Hour {
			samples = samples[1:]
		}
		sharedKeyWatch.samples[name] = samples
		var then *sharedKeySample
		for i := range samples {
			if age := now.Sub(samples[i].At); age >= 45*time.Minute {
				then = &samples[i]
			}
		}
		sharedKeyWatch.Unlock()
		if then == nil {
			continue
		}
		spent := usage - then.Usage
		hi := 0.0
		kfp := callKeyFingerprint(key)
		from := then.At.Truncate(time.Hour)
		for _, app := range machineAppNames() {
			for hour, keys := range loadModelHours(app) {
				if at, err := time.Parse(time.RFC3339, hour); err == nil && !at.Before(from) {
					hi += keys[kfp]
				}
			}
		}
		gap := spent - hi
		if gap <= 1 || gap <= 0.1*spent {
			continue
		}
		if last, ok := outside[name]; ok && now.Sub(last.At) < 6*time.Hour {
			continue
		}
		outside[name] = sharedOutside{At: now.UTC(), USD: gap}
		changed = true
		vaultAudit("hi", "vault.shared-outside", vaultSharedPath(name), fmt.Sprintf("$%.2f outside hi's apps since %s", gap, then.At.UTC().Format(time.RFC3339)))
		told = append(told, fmt.Sprintf("shared/%s spent %s outside hi's apps since %s: something else has it. Change it (hi server vault shared set %s) and revoke the old one at OpenRouter.",
			name, formatDollars(gap), then.At.Local().Format("15:04"), name))
	}
	if changed {
		data, _ := json.Marshal(outside)
		writeFileAtomic(sharedOutsidePath(), data, 0o600)
	}
	return told
}
