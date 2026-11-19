package cli

// The web around the app, for the dev box's agents (hi dev web): where
// the owners see the agent and the app, the app's addresses and how
// they're reached, what answers at each name as the machine sees it, and
// what the dev page's Preview last couldn't show. The box sees none of
// it: its proxy reaches only what development needs, and a sign-in in
// front of the addresses answers the box 403. Without it, an agent asked
// about "the preview" or "the view window" didn't know what was meant.

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// appDevWebAddress is one of the app's addresses, as the machine sees it.
type appDevWebAddress struct {
	Name  string `json:"name"` // production, preview, dev page
	URL   string `json:"url"`
	Label string `json:"label,omitempty"`
	// Points is the name's DNS alias, when it has one: what answers there
	// is in front of hi. Front says who that is, when hi knows.
	Points string `json:"points,omitempty"`
	Front  string `json:"front,omitempty"`
	// What the name answered the machine: its status, where a redirect
	// sends to (an origin), and what it says about frames.
	Status int    `json:"status,omitempty"`
	Sends  string `json:"sends,omitempty"`
	Frames string `json:"frames,omitempty"`
	Error  string `json:"error,omitempty"`
}

// appDevFrameBlock is what the dev page's Preview couldn't show: the
// address sent the frame on to another origin, which the page doesn't
// frame.
type appDevFrameBlock struct {
	At   time.Time `json:"at"`
	Slot string    `json:"slot"`
	From string    `json:"from"`
	To   string    `json:"to"`
}

// appDevWebView is hi dev web's answer.
type appDevWebView struct {
	Reached   string             `json:"reached"`
	Addresses []appDevWebAddress `json:"addresses"`
	Frames    []string           `json:"frames"` // the origins the dev page's Preview may show
	Blocked   *appDevFrameBlock  `json:"blocked,omitempty"`
}

// appDevFrameBlocks are each app's last block the dev page saw, until
// the service starts again.
var appDevFrameBlocks sync.Map // team -> appDevFrameBlock

// appDevLookupCNAME and appDevWebClient are the machine's DNS and HTTP;
// tests replace them.
var (
	appDevLookupCNAME = net.DefaultResolver.LookupCNAME
	appDevWebClient   = &http.Client{
		Timeout:       6 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
)

// appDevReached says how the app's addresses are reached, in a sentence.
func appDevReached(config appConfig) string {
	switch config.Addresses {
	case "":
		return "On the machine's domain (hi net domain), over https: hi keeps the names in its DNS, pointing at the app's NetBird address, so only people on the NetBird network reach them."
	case appAddressesNetBird:
		return "On the app's NetBird name, over plain http: only people on the NetBird network reach them."
	case appAddressesNames:
		line := "Each at a name of its own, which the organisation's IT team keeps: pointing at the app's NetBird address, or at the IT team's reverse proxy (NetBird's, or their own), which passes each on to the app."
		if config.Names != nil && config.Names.HTTPS {
			line += " TLS ends at that proxy, in front of hi; hi serves http behind it."
		}
		return line
	}
	return "At " + config.Addresses + ", a name the organisation's IT team keeps, pointing at the app's NetBird address, over plain http."
}

// appDevFront is who answers at a DNS alias, when hi knows them.
func appDevFront(alias string) string {
	switch {
	case strings.Contains(alias, "netbird"):
		return "NetBird's reverse proxy, which may ask people to sign in (a NetBird login page) before they reach the app"
	case strings.Contains(alias, "cloudflare"):
		return "Cloudflare, which may ask people to sign in (Cloudflare Access) before they reach the app"
	}
	return "something the organisation's IT team keeps in front of hi"
}

// appDevFrameRule is what a response says about being shown in a frame:
// its X-Frame-Options and its CSP's frame-ancestors, or "".
func appDevFrameRule(header http.Header) string {
	var rules []string
	if value := header.Get("X-Frame-Options"); value != "" {
		rules = append(rules, "X-Frame-Options: "+value)
	}
	for _, policy := range header.Values("Content-Security-Policy") {
		for _, directive := range strings.Split(policy, ";") {
			if directive = strings.TrimSpace(directive); strings.HasPrefix(strings.ToLower(directive), "frame-ancestors") {
				rules = append(rules, "CSP "+directive)
			}
		}
	}
	return strings.Join(rules, "; ")
}

// probe looks at an address from the machine: its name's alias, and what
// it answers a browser's frame.
func (address *appDevWebAddress) probe(ctx context.Context) {
	parsed, err := url.Parse(address.URL)
	if err != nil || parsed.Hostname() == "" {
		address.Error = "not an address"
		return
	}
	host := parsed.Hostname()
	if net.ParseIP(host) == nil {
		if alias, err := appDevLookupCNAME(ctx, host); err == nil {
			alias = strings.TrimSuffix(strings.ToLower(alias), ".")
			if alias != "" && alias != strings.ToLower(host) {
				address.Points, address.Front = alias, appDevFront(alias)
			}
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address.URL, nil)
	if err != nil {
		address.Error = err.Error()
		return
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	request.Header.Set("Sec-Fetch-Dest", "iframe")
	request.Header.Set("Sec-Fetch-Mode", "navigate")
	response, err := appDevWebClient.Do(request)
	if err != nil {
		address.Error = appDevClip(err.Error(), 200)
		return
	}
	io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	response.Body.Close()
	address.Status = response.StatusCode
	address.Frames = appDevFrameRule(response.Header)
	if location := response.Header.Get("Location"); location != "" {
		if target, err := parsed.Parse(location); err == nil {
			address.Sends = appOrigin(target.String())
		}
	}
}

// devWeb is hi dev web: the addresses, looked at from the machine.
func (b *appBroker) devWeb(ctx context.Context) (appDevWebView, error) {
	config, err := loadTeam(b.team)
	if err != nil {
		return appDevWebView{}, err
	}
	view := appDevWebView{Reached: appDevReached(config), Frames: []string{}}
	if slot, ok := loadTeamSlot(b.team, appSlotProduction); ok && slot.URL != "" {
		view.Addresses = append(view.Addresses, appDevWebAddress{Name: "production", URL: slot.URL, Label: slot.Label})
	}
	if slot, ok := loadTeamSlot(b.team, appSlotPreview); ok && slot.URL != "" {
		view.Addresses = append(view.Addresses, appDevWebAddress{Name: "preview", URL: slot.URL, Label: slot.Label})
	}
	if page, ok := loadTeamDevPage(b.team); ok && page.URL != "" {
		// Its link's key stays out: the agent has no use for an owner's way in.
		view.Addresses = append(view.Addresses, appDevWebAddress{Name: "dev page", URL: appDevWebStripKey(appAddressLink(page.URL, appDevPagePrefix, nil))})
	}
	var wait sync.WaitGroup
	for i := range view.Addresses {
		wait.Add(1)
		go func(address *appDevWebAddress) {
			defer wait.Done()
			address.probe(ctx)
		}(&view.Addresses[i])
	}
	wait.Wait()
	if frames := strings.TrimPrefix(b.devPageFrames(), "frame-src "); frames != "'none'" {
		view.Frames = strings.Fields(frames)
	}
	if block, ok := appDevFrameBlocks.Load(b.team); ok {
		block := block.(appDevFrameBlock)
		view.Blocked = &block
	}
	return view, nil
}

// appDevWebStripKey is an address without the keys a link carries.
func appDevWebStripKey(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	query := parsed.Query()
	query.Del(netExposeUserParam)
	query.Del(netExposeKeyParam)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// devFrameBlocked keeps what the dev page's Preview couldn't show.
func (b *appBroker) devFrameBlocked(slot, to string) error {
	if slot != appSlotPreview && slot != appSlotProduction {
		return fmt.Errorf("%q isn't the preview or production", slot)
	}
	origin := appOrigin(to)
	if origin == "" || origin != strings.TrimSuffix(to, "/") || len(origin) > 300 {
		return fmt.Errorf("%q isn't an origin", to)
	}
	state, ok := loadTeamSlot(b.team, slot)
	if !ok {
		return fmt.Errorf("the %s has no address", slot)
	}
	appDevFrameBlocks.Store(b.team, appDevFrameBlock{At: time.Now().UTC(), Slot: slot, From: appOrigin(state.URL), To: origin})
	return nil
}

func printTeamDevWeb(view appDevWebView, now time.Time, w io.Writer) {
	fmt.Fprintln(w, "The owners and developers see you on the dev page: its Chat is this session, and its")
	fmt.Fprintln(w, "Terminal the same one. Its right-hand panel's Preview shows the preview or production")
	fmt.Fprintln(w, "inside the page, in a frame; ↗ there opens it in a tab of its own.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "How the addresses are reached: "+view.Reached)
	if len(view.Addresses) == 0 {
		fmt.Fprintln(w, "\nThe app has no addresses yet: it isn't up.")
	}
	for _, address := range view.Addresses {
		fmt.Fprintf(w, "\n%s: %s", strings.ToUpper(address.Name[:1])+address.Name[1:], address.URL)
		if address.Label != "" {
			fmt.Fprintf(w, " (%s)", address.Label)
		}
		fmt.Fprintln(w)
		if address.Points != "" {
			fmt.Fprintf(w, "  its name points at %s: %s.\n", address.Points, address.Front)
		}
		switch {
		case address.Error != "":
			fmt.Fprintf(w, "  from the machine: no answer (%s)\n", address.Error)
		case address.Sends != "":
			fmt.Fprintf(w, "  from the machine: %d, sent on to %s\n", address.Status, address.Sends)
		default:
			fmt.Fprintf(w, "  from the machine: %d %s\n", address.Status, http.StatusText(address.Status))
		}
		if address.Frames != "" {
			fmt.Fprintf(w, "  about frames: %s\n", address.Frames)
		}
	}
	fmt.Fprintln(w)
	if len(view.Frames) == 0 {
		fmt.Fprintln(w, "The dev page's Preview shows nothing yet: there is no address to show.")
	} else {
		fmt.Fprintf(w, "The dev page's Preview may show %s, and nothing they send it on to.\n", strings.Join(view.Frames, " and "))
	}
	if block := view.Blocked; block != nil {
		fmt.Fprintf(w, "It last couldn't show %s's address, %s: %s sent the frame on to %s,\n", block.Slot, appDevAgo(now, block.At), block.From, block.To)
		fmt.Fprintln(w, "which it doesn't frame. That is most likely a sign-in in front of the app. The")
		fmt.Fprintln(w, "Preview offers Sign in and come back: the whole tab goes through the app's addresses,")
		fmt.Fprintln(w, "signs in there, and comes back, and the frame shows the app after. If it still")
		fmt.Fprintln(w, "doesn't, the browser keeps the sign-in from the frame; ↗ opens it in a tab.")
	}
	fmt.Fprintln(w, "\nThe box itself can't open these addresses: run the app with its make run, and ask the")
	fmt.Fprintln(w, "people what their browser shows. The hi-dev-web skill says more.")
}

// appDevAgo is a time before now, in words.
func appDevAgo(now, at time.Time) string {
	since := now.Sub(at)
	switch {
	case since < time.Minute:
		return "just now"
	case since < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(since.Minutes()))
	case since < 48*time.Hour:
		return fmt.Sprintf("%d hours ago", int(since.Hours()))
	}
	return at.Format("Jan 2")
}
