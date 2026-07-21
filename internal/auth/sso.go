package auth

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Cross-domain sign-in handoff.
//
// Project static sites can be served on a dedicated site domain
// ({project}.<SiteDomain>, e.g. myapp.pazer.site). The bh_session cookie is
// scoped to the primary apex (Domain=pazer.build) and the single GitHub OAuth
// App's callback lives there too, so a browser on the site domain can neither
// present an existing session nor complete OAuth locally. Instead it
// authenticates on the primary apex and the session is handed across:
//
//  1. An unauthenticated browser on a private x.<SiteDomain> resource is
//     303'd to https://<PrimaryDomain>/__signin?next=<original URL>.
//  2. Sign-in runs there -- the full GitHub OAuth flow, or instantly when the
//     request already carries a valid primary-apex bh_session.
//  3. The minted session VALUE is parked server-side under a random one-time
//     nonce, and the browser is 303'd to
//     https://x.<SiteDomain>/__sso?code=<signed nonce+next>&next=<original>.
//  4. /__sso (served only on site-domain hosts) verifies the code's HMAC,
//     expiry (<=60s), and single-use, sets the same session value as a
//     Domain=<SiteDomain> bh_session cookie, and 303s to the original URL.
//
// The session token itself never appears in a URL: the code is an opaque
// nonce+destination signed with the shared download-signing key, worthless
// without the server-side entry it names, dead after one redemption, and its
// destination cannot be swapped (the MAC covers it). Subsequent site-domain
// requests carry the site-domain cookie and flow through the ordinary
// requireProject / canAccessRepo authorization, which is host-independent.

const ssoPath = "/__sso"

// ssoHandoffTTL bounds how long a handoff code (and its parked session) may
// live between mint on the primary apex and redemption on the site domain --
// one cross-domain redirect, so a minute is generous. A var so tests can
// exercise expiry without sleeping.
var ssoHandoffTTL = 60 * time.Second

// ssoHandoff parks a minted session value between the sign-in redirect and its
// redemption, keyed by the code's one-time nonce (in-memory: a restart simply
// voids in-flight codes and the browser restarts sign-in).
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
	// tiny without a background janitor (mints only happen on completed sign-ins).
	for n, h := range ssoHandoffs {
		if now.After(h.exp) {
			delete(ssoHandoffs, n)
		}
	}
	ssoHandoffs[nonce] = ssoHandoff{session: session, exp: exp}
}

// takeSSOHandoff redeems a nonce: the entry is deleted on first access
// (single-use) and only returned while unexpired.
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
// site domain itself or exactly one DNS label under it, and "" otherwise. This
// is the apex-classifier for the site-domain scheme: myapp.pazer.site's apex is
// pazer.site, never pazer.build -- they are different registrable domains.
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
// (tests boot several servers per process; the router tolerates duplicates but
// there is no reason to accumulate them).
var ssoRegisteredDomains = map[string]bool{}

// registerSSOHandoffRoutes registers the /__sso redemption endpoint iff a site
// domain is configured -- with BUILDHOST_SITE_DOMAIN unset the route table is
// byte-identical to a build without the feature. Two registrations:
//
//   - {project}.<SiteDomain>/__sso: the {project}.<SiteDomain> host family
//     claims EVERY path on project subdomains (host-literal count outranks all
//     path terms), so host-agnostic routes never serve there and the redemption
//     endpoint must live inside the family. The literal /__sso outranks the
//     sites {path...} catch-all, which reserves the path: a site file literally
//     named /__sso is unreachable on this scheme (still served on
//     sites.{domain}/...).
//   - host-agnostic /__sso: the BARE site apex (2 labels) matches no
//     host-bearing route and falls through to host-agnostic routes, so a
//     handoff whose destination is https://<SiteDomain>/... redeems here. The
//     handler's own Host gate keeps it a 404 everywhere outside the site domain.
//
// Config-conditional routes cannot register in init() (config is only known at
// Init), so unlike the unconditional backends they are invisible to
// `buildhost routes`, which enumerates without booting a server.
func registerSSOHandoffRoutes() {
	sd := sharedSiteDomain
	if sd == "" || ssoRegisteredDomains[sd] {
		return
	}
	ssoRegisteredDomains[sd] = true
	HandleRaw("GET "+ssoPath, handleSSORedeem)
	SiteDomainHandleRaw(sd, "GET "+ssoPath, handleSSORedeem)
}

// mintSiteHandoff mints a single-use handoff code for a sign-in whose
// destination is on the configured site domain and returns the absolute
// /__sso redemption URL to redirect the browser to. It returns "" when the
// destination is not a site-domain URL (the caller then redirects to next
// directly, the ordinary same-domain flow). sessionValue is the signed
// bh_session value to hand across; it is parked server-side and never appears
// in the returned URL.
func mintSiteHandoff(sessionValue, next string) string {
	if SiteDomain() == "" {
		return ""
	}
	u, err := url.Parse(next)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || siteApexOf(u.Hostname()) == "" {
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
// validates the one-time code, sets the parked session as a
// Domain=<SiteDomain> cookie, and sends the browser on to its destination.
// Every response is no-store -- each one is part of a live credential exchange.
// Failures mirror the signin-callback rules: never a 5xx (Cloudflare masks
// those), and always a page with a way to restart at the primary apex.
func handleSSORedeem(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if requestSiteApex(r) == "" {
		// The host-agnostic registration answers on every unclaimed host; the
		// endpoint only exists on the site domain.
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
	// set Domain=<SiteDomain> -- one redemption signs the browser in for every
	// project site under the domain, mirroring the primary apex's domain-wide
	// cookie.
	setSessionCookie(w, r, session)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// ssoFailedHTML renders a terminal handoff failure with a restart link at the
// primary apex sign-in (the only place a new handoff can be minted).
func ssoFailedHTML(w http.ResponseWriter, r *http.Request, status int, reason, next string) {
	signinFailedPage(w, r, status, reason, primarySigninURL(r, next))
}

// primarySigninURL is the sign-in entrypoint on the primary domain, carrying an
// optional next. Falls back to this request's own apex when no primary domain
// is configured (then sign-in is a same-domain concern).
func primarySigninURL(r *http.Request, next string) string {
	base := apexRootURL(r)
	if pd := PrimaryDomain(); pd != "" {
		base = RequestScheme(r) + "://" + pd
	}
	u := base + signinStartPath
	if next != "" {
		u += "?next=" + url.QueryEscape(next)
	}
	return u
}
