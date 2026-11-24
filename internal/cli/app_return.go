package cli

// Signing in at the top, for the dev page's Preview. An IT team's proxy
// in front of the app's addresses (NetBird's reverse proxy, say) signs
// people in by sending the browser to its sign-in page, and keeps the
// session in a cookie of each address's own. A frame can't follow it
// there: the dev page frames only the app's addresses. So the Preview
// sends the whole tab through the address instead, where the proxy signs
// it in and sets its cookie, and the address sends it back here; the
// frame then has the cookie, which the browser sends with it, the dev
// page and the address being one site. The route is always on, the
// helper or not.

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
)

// appReturnPrefix is the path on the app's addresses that sends the
// browser back to the dev page (or through another of the app's
// addresses first).
const appReturnPrefix = appHelperPrefix + "return/"

// appOwnAddress says whether a URL is on one of the app's own addresses:
// the dev page's, production's, or the preview's.
func appOwnAddress(team, raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return false
	}
	origin := appOrigin(raw)
	if origin == "" {
		return false
	}
	var own []string
	if page, ok := loadTeamDevPage(team); ok {
		own = append(own, page.URL)
	}
	for _, slot := range []string{appSlotProduction, appSlotPreview} {
		if state, ok := loadTeamSlot(team, slot); ok {
			own = append(own, state.URL)
		}
	}
	for _, address := range own {
		if appOrigin(address) == origin {
			return true
		}
	}
	return false
}

// returnToPage is GET /__hi/return/<the address, base64url>/, or
// /__hi/return/?to=<the address> from dev pages before v2.6.244. The
// address is one of the app's own. It rides in the path because NetBird's
// reverse proxy, after its sign-in, sends the browser back to the path it
// came for without the query.
func (b *appBroker) returnToPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	to := r.URL.Query().Get("to")
	if encoded := strings.Trim(strings.TrimPrefix(r.URL.Path, appReturnPrefix), "/"); encoded != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(encoded, "="))
		if err != nil {
			http.Error(w, "That isn't one of this app's addresses.", http.StatusBadRequest)
			return
		}
		to = string(decoded)
	}
	if !appOwnAddress(b.team, to) {
		http.Error(w, "That isn't one of this app's addresses.", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}
