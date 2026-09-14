package auth

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/wow-look-at-my/go-containers/set"
)

// Cross-domain sign-in handoff.

const ssoPath = "/__sso"

// ssoHandoffTTL bounds how long a handoff code (and its parked session) may
var ssoHandoffTTL = 60 * time.Second

// ssoHandoff parks a minted session value between the sign-in redirect and its
type ssoHandoff struct {
	session string
	exp     time.Time
}

var (
	ssoMu       sync.Mutex
	ssoHandoffs = map[string]ssoHandoff{}
)

func storeSSOHandoff(nonce, session string, exp time.Time) {
	now := time.Now()
	ssoMu.Lock()
	defer ssoMu.Unlock()
	// Opportunistic sweep: entries live at most ssoHandoffTTL, so the map stays
	for n, h := range ssoHandoffs {
		if now.After(h.exp) {
			delete(ssoHandoffs, n)
		}
	}
	ssoHandoffs[nonce] = ssoHandoff{session: session, exp: exp}
}

func takeSSOHandoff(nonce string) (string, bool) {
	ssoMu.Lock()
	defer ssoMu.Unlock()
	h, ok := ssoHandoffs[nonce]
	if !ok {
		return "", false
	}
	delete(ssoHandoffs, nonce)
	if time.Now().After(h.exp) {
		return "", false
	}
	return h.session, true
}

// siteApexOf returns the configured site domain when host (no port) is the
func siteApexOf(host string) string {
	sd := SiteDomain()
	if sd == "" {
		return ""
	}
	host = strings.ToLower(host)
	if host == sd {
		return sd
	}
	if label, ok := strings.CutSuffix(host, "."+sd); ok && label != "" && !strings.Contains(label, ".") {
		return sd
	}
	return ""
}

// requestSiteApex is siteApexOf for a request's Host header.
func requestSiteApex(r *http.Request) string {
	return siteApexOf(hostNoPort(r.Host))
}

// ssoRegisteredDomains keeps registration idempotent across repeated Init calls
var ssoRegisteredDomains = set.New[string]()

var ssoBareRegistered bool

func init() {
	OnSiteDomain(registerSSOHandoffRoutes)
}

// registerSSOHandoffRoutes registers the /__sso redemption endpoint iff a site
// domain is configured -- with BUILDHOST_SITE_DOMAIN unset the route table is
func registerSSOHandoffRoutes(sd string) {
	if sd == "" || !ssoRegisteredDomains.Add(sd) {
		return
	}
	if !ssoBareRegistered {
		ssoBareRegistered = true
		HandleRaw("GET "+ssoPath, handleSSORedeem)
	}
	SiteDomainHandleRaw(sd, "GET "+ssoPath, handleSSORedeem)
}

// siteHandoffDest returns the parsed destination when next is a URL on the
// configured site domain (the shape a cross-domain handoff exists for), and
// nil otherwise.
func siteHandoffDest(next string) *url.URL {
	if SiteDomain() == "" {
		return nil
	}
	u, err := url.Parse(next)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || siteApexOf(u.Hostname()) == "" {
		return nil
	}
	return u
}

// mintSiteHandoff mints a single-use handoff code for a sign-in whose
// destination is on the configured site domain and returns the absolute
// /__sso redemption URL to redirect the browser to. It returns "" when the
// destination is not a site-domain URL (the caller then redirects to next
// directly, the ordinary same-domain flow). sessionValue is the signed
func mintSiteHandoff(sessionValue, next string) string {
	u := siteHandoffDest(next)
	if u == nil {
		return ""
	}
	nonce := randToken()
	exp := time.Now().Add(ssoHandoffTTL)
	storeSSOHandoff(nonce, sessionValue, exp)
	code := signValue("sso", nonce+"\x00"+next, exp)
	q := url.Values{"code": {code}, "next": {next}}
	return u.Scheme + "://" + u.Host + ssoPath + "?" + q.Encode()
}

// handleSSORedeem completes the cross-domain sign-in on the site domain: it
func handleSSORedeem(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if requestSiteApex(r) == "" {
		// The host-agnostic registration answers on every unclaimed host; the
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	payload, expired, ok := parseSignedValue("sso", q.Get("code"))
	if !ok {
		ssoFailedHTML(w, r, http.StatusBadRequest, "This sign-in link is invalid. It may have been truncated or altered.", "")
		return
	}
	nonce, next, found := strings.Cut(payload, "\x00")
	if !found || nonce == "" || next == "" {
		ssoFailedHTML(w, r, http.StatusBadRequest, "This sign-in link is invalid. It may have been truncated or altered.", "")
		return
	}
	// next is MAC-verified from here on, so failure pages may carry it into
	// their restart link.
	if expired {
		ssoFailedHTML(w, r, http.StatusBadRequest, "This sign-in link has expired.", next)
		return
	}
	// The destination rides inside the signed code; the query next exists only
	// for transparency. If they disagree, someone edited the link.
	if qn := q.Get("next"); qn != "" && qn != next {
		ssoFailedHTML(w, r, http.StatusBadRequest, "This sign-in link is invalid. It may have been truncated or altered.", "")
		return
	}
	// Re-validate the (mint-time-checked, MAC-covered) destination shape.
	if u, err := url.Parse(next); err != nil || (u.Scheme != "http" && u.Scheme != "https") || siteApexOf(u.Hostname()) == "" {
		ssoFailedHTML(w, r, http.StatusBadRequest, "This sign-in link is invalid. It may have been truncated or altered.", "")
		return
	}
	session, ok := takeSSOHandoff(nonce)
	if !ok {
		ssoFailedHTML(w, r, http.StatusBadRequest, "This sign-in link was already used or has expired.", next)
		return
	}
	// apexHost resolves a site-domain request to the site apex, so the cookie is
	setSessionCookie(w, r, session)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// ssoFailedHTML renders a terminal handoff failure with a restart link at the
// primary apex sign-in (the only place a new handoff can be minted).
func ssoFailedHTML(w http.ResponseWriter, r *http.Request, status int, reason, next string) {
	signinFailedPage(w, r, status, reason, primarySigninURL(r, next))
}

// primarySigninURL is the sign-in entrypoint on the primary domain, carrying an
// optional next. Falls back to this request's own apex when no single primary
// domain is pinned -- unset, or the "*" serve-every-Host opt-in -- because then
// sign-in is a same-domain concern and "*" is not an addressable host.
func primarySigninURL(r *http.Request, next string) string {
	base := apexRootURL(r)
	if pd := PinnedPrimaryDomain(); pd != "" {
		base = RequestScheme(r) + "://" + pd
	}
	u := base + signinStartPath
	if next != "" {
		u += "?next=" + url.QueryEscape(next)
	}
	return u
}
