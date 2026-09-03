package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// --- signed session + state (HMAC over the shared signing key) ---

// mintSession signs the user's login + GitHub token into the session value. The
func mintSession(login, token string, exp time.Time) string {
	return signValue("session", login+"\x00"+token, exp)
}

func verifySession(value string) (login, token string, ok bool) {
	payload, valid := verifySignedValue("session", value)
	if !valid {
		return "", "", false
	}
	l, t, found := strings.Cut(payload, "\x00")
	if !found {
		return "", "", false
	}
	return l, t, true
}

// signinState is the payload bound into the signed OAuth state parameter: the
type signinState struct {
	nonce   string
	next    string
	retried bool
}

func signState(st signinState, exp time.Time) string {
	flags := ""
	if st.retried {
		flags = "r"
	}
	return signValue("state", st.nonce+"\x00"+flags+"\x00"+st.next, exp)
}

// parseState authenticates an OAuth state parameter. ok reports a valid
// signature; everything in st is then trustworthy even when expired is set
// (the MAC covers the expiry too), so an expired flow can still be restarted
// to its own next URL. ok=false means forged or corrupt: use nothing from it.
func parseState(value string) (st signinState, expired, ok bool) {
	payload, expired, ok := parseSignedValue("state", value)
	if !ok {
		return signinState{}, false, false
	}
	nonce, rest, found := strings.Cut(payload, "\x00")
	if !found {
		return signinState{}, false, false
	}
	flags, next, found := strings.Cut(rest, "\x00")
	if !found {
		// A state minted before the retried flag existed (nonce\x00next): still
		return signinState{nonce: nonce, next: rest}, expired, true
	}
	return signinState{nonce: nonce, next: next, retried: flags == "r"}, expired, true
}

// signValue returns base64(payload).base64(mac) where mac is HMAC over the
// domain-separated (purpose, payload, exp). verifySignedValue checks the mac and
// expiry and returns the payload.
func signValue(purpose, payload string, exp time.Time) string {
	body := payload + "\x00" + strconv.FormatInt(exp.Unix(), 10)
	mac := valueMAC(purpose, body)
	return base64.RawURLEncoding.EncodeToString([]byte(body)) + "." + base64.RawURLEncoding.EncodeToString(mac)
}

func verifySignedValue(purpose, value string) (string, bool) {
	payload, expired, ok := parseSignedValue(purpose, value)
	return payload, ok && !expired
}

// parseSignedValue verifies the MAC and splits off the expiry. ok reports an
// authentic (correctly signed) value; expired is reported separately so a
// caller can distinguish "forged" from "genuine but past its expiry".
func parseSignedValue(purpose, value string) (payload string, expired, ok bool) {
	dot := strings.IndexByte(value, '.')
	if dot <= 0 {
		return "", false, false
	}
	body, err := base64.RawURLEncoding.DecodeString(value[:dot])
	if err != nil {
		return "", false, false
	}
	gotMAC, err := base64.RawURLEncoding.DecodeString(value[dot+1:])
	if err != nil || !hmac.Equal(gotMAC, valueMAC(purpose, string(body))) {
		return "", false, false
	}
	payload, expStr, ok := cutLast(string(body), '\x00')
	if !ok {
		return "", false, false
	}
	expUnix, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", false, false
	}
	return payload, time.Now().Unix() > expUnix, true
}

// cutLast splits on the final occurrence of sep, so a payload that itself
// contains sep (state's nonce\x00next) round-trips with the exp suffix.
func cutLast(s string, sep byte) (before, after string, found bool) {
	i := strings.LastIndexByte(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}

func valueMAC(purpose, body string) []byte {
	h := hmac.New(sha256.New, downloadSecretBytes())
	h.Write([]byte("ghlogin:" + purpose + "\x00"))
	h.Write([]byte(body))
	return h.Sum(nil)
}

func randToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// --- cookies ---

func sessionFromRequest(r *http.Request) (login, token string, ok bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", "", false
	}
	return verifySession(c.Value)
}

// setSessionCookie sets the session domain-wide (Domain=<apex>) so a login on
// the apex callback authenticates the user on every service subdomain.
func setSessionCookie(w http.ResponseWriter, r *http.Request, value string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: value, Path: "/", Domain: apexHost(r),
		MaxAge: sessionMaxAge, HttpOnly: true, Secure: RequestScheme(r) == "https", SameSite: http.SameSiteLaxMode,
	})
}

func clearCookie(w http.ResponseWriter, r *http.Request, name, path string) {
	domain := ""
	if name == sessionCookieName {
		domain = apexHost(r)
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: path, Domain: domain,
		MaxAge: -1, HttpOnly: true, Secure: RequestScheme(r) == "https", SameSite: http.SameSiteLaxMode,
	})
}

// apexHost is the registrable host the session cookie is scoped to: the request
// Host minus port, with a known leading service label (sites/dl/...) stripped --
// the same apex derivation as apexRootURL. On the apex, where /__signin and the
func apexHost(r *http.Request) string {
	host := hostNoPort(r.Host)
	if sd := siteApexOf(host); sd != "" {
		return sd
	}
	if dot := strings.IndexByte(host, '.'); dot > 0 && knownServiceLabels.Contains(host[:dot]) {
		host = host[dot+1:]
	}
	return host
}

// safeNextURL keeps post-login redirects inside this deployment: it accepts an
func safeNextURL(r *http.Request, next string) string {
	// Sign-in runs on the apex, so the request Host is the apex root.
	root := RequestBaseURL(r)
	if next == "" {
		return root + "/"
	}
	if next[0] == '/' && !strings.HasPrefix(next, "//") && !strings.HasPrefix(next, "/\\") {
		return next
	}
	u, err := url.Parse(next)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return root + "/"
	}
	apex := apexHost(r)
	host := u.Hostname()
	if host == apex || strings.HasSuffix(host, "."+apex) {
		return next
	}
	if siteApexOf(host) != "" {
		return next
	}
	return root + "/"
}
