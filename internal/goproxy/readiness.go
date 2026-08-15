package goproxy

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// Health is the proxy's own readiness, independent of the registry's /healthz.
//
// The failure this exists for: a module proxy with no upstream credential serves
// every PUBLIC module perfectly and no private one at all, so uptime checks,
// dashboards and smoke tests all read green while every build that depends on a
// private first-party module is broken. Nothing in "is the process up" can see
// that. So readiness here is a statement about the credential, re-checked
// periodically and reported as its own thing.
type Health struct {
	// Healthy is false when the proxy cannot serve the private modules it is
	// configured to serve.
	Healthy bool `json:"healthy"`
	// Reason is why, in one line, when Healthy is false.
	Reason string `json:"reason,omitempty"`
	// CredentialConfigured reports whether a GitHub App or static token is set.
	CredentialConfigured bool `json:"credential_configured"`
	// CredentialKind is "github-app", "token" or "none".
	CredentialKind string `json:"credential_kind"`
	// PrivatePrefixes is what this proxy considers private.
	PrivatePrefixes []string `json:"private_prefixes"`
	// Upstream is the configured public mirror ("" when passthrough is off).
	Upstream string `json:"upstream"`
	// ReadinessModule is the private module resolved as the live proof, "" when
	// none is configured -- in which case Probed is false and the check can only
	// report whether a credential exists.
	ReadinessModule string    `json:"readiness_module"`
	Probed          bool      `json:"probed"`
	ProbeVersion    string    `json:"probe_version,omitempty"`
	ProbeError      string    `json:"probe_error,omitempty"`
	ProbeErrorKind  string    `json:"probe_error_kind,omitempty"`
	CheckedAt       time.Time `json:"checked_at"`
}

type health struct {
	mu   sync.RWMutex
	last Health
}

func newHealth() *health { return &health{} }

func (h *health) get() Health {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.last
}

func (h *health) set(v Health) {
	h.mu.Lock()
	h.last = v
	h.mu.Unlock()
}

// Health returns the last readiness result.
func (s *Service) Health() Health { return s.health.get() }

const readinessInterval = 15 * time.Minute

// startReadiness evaluates readiness now and keeps it current.
//
// With no private prefixes there is nothing a credential is needed for, so the
// result cannot change: the check runs once, inline, and no background loop
// starts. That is the shape every test that calls auth.Init has -- a ticker per
// call would leak for the life of the test binary, and a probe would reach
// api.github.com from a unit test the moment someone passed real orgs.
//
// Otherwise the first run goes on its own goroutine (immediate, so a
// misconfigured proxy says so in the startup logs rather than on the first
// failing build) and repeats on the interval.
func (s *Service) startReadiness(ctx context.Context) {
	if len(s.cfg.PrivatePrefixes) == 0 {
		s.checkHealth(ctx)
		return
	}
	go func() {
		s.checkHealth(ctx)
		t := time.NewTicker(readinessInterval)
		defer t.Stop()
		for range t.C {
			s.checkHealth(ctx)
		}
	}()
}

// checkHealth re-evaluates readiness and logs a failure loudly.
func (s *Service) checkHealth(ctx context.Context) Health {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	h := Health{
		PrivatePrefixes: s.cfg.PrivatePrefixes,
		Upstream:        s.cfg.Upstream,
		ReadinessModule: s.cfg.ReadinessModule,
		CheckedAt:       time.Now().UTC(),
		Healthy:         true,
	}
	h.CredentialKind, h.CredentialConfigured = s.credentialKind(ctx)

	switch {
	case len(s.cfg.PrivatePrefixes) == 0:
		// Nothing private is claimed, so there is nothing a credential is needed
		// for; passthrough-only is a legitimate configuration.
		h.Reason = ""

	case !h.CredentialConfigured:
		h.Healthy = false
		h.Reason = "no GitHub credential is configured, so every PRIVATE module under " +
			joinPrefixes(s.cfg.PrivatePrefixes) + " will fail while public modules keep working"

	case s.cfg.ReadinessModule == "":
		// A credential that authenticates but is not authorized for the org looks
		// identical to a working one from here. Say so rather than claiming a
		// proof this check cannot make.
		h.Reason = "no BUILDHOST_GOPROXY_READINESS_MODULE is set, so the credential's " +
			"ACCESS to private modules is unproven (only its presence is checked)"

	default:
		h.Probed = true
		if res, err := s.latest(ctx, s.cfg.ReadinessModule); err != nil {
			e := asError(s.cfg.ReadinessModule, "", err)
			h.Healthy = false
			h.ProbeErrorKind = e.Kind.String()
			h.ProbeError = e.Error()
			h.Reason = "the readiness module " + s.cfg.ReadinessModule + " did not resolve"
		} else {
			h.ProbeVersion = res.Version
		}
	}

	s.health.set(h)
	if !h.Healthy {
		slog.Error("goproxy is NOT ready",
			"reason", h.Reason,
			"credential", h.CredentialKind,
			"private_prefixes", s.cfg.PrivatePrefixes,
			"probe_error", h.ProbeError)
	} else if h.Reason != "" {
		slog.Warn("goproxy readiness is unproven", "reason", h.Reason)
	} else {
		slog.Info("goproxy ready",
			"credential", h.CredentialKind,
			"private_prefixes", s.cfg.PrivatePrefixes,
			"probe_module", h.ReadinessModule,
			"probe_version", h.ProbeVersion)
	}
	return h
}

// credentialKind reports what credential the proxy would present. It asks for a
// bearer against a repo in the first configured private prefix, because that is
// the question that matters -- a GitHub App only mints a token for an org it is
// actually installed on.
func (s *Service) credentialKind(ctx context.Context) (string, bool) {
	owner, repo := "", ""
	if len(s.cfg.PrivatePrefixes) > 0 {
		if refs, err := parseModulePath(s.cfg.PrivatePrefixes[0] + "/probe"); err == nil && len(refs) > 0 {
			owner, repo = refs[0].Owner, refs[0].Repo
		}
	}
	if owner == "" {
		return "none", false
	}
	if tok := s.github.tokenFor(ctx, owner, repo); tok != "" {
		if auth.HasGitHubApp() {
			return "github-app", true
		}
		return "token", true
	}
	return "none", false
}

func joinPrefixes(p []string) string {
	if len(p) == 0 {
		return "(none)"
	}
	out := p[0]
	for _, s := range p[1:] {
		out += ", " + s
	}
	return out
}

// publicHealth is what an unauthenticated caller sees: the verdict and nothing
// that names a module.
type publicHealth struct {
	Healthy   bool      `json:"healthy"`
	CheckedAt time.Time `json:"checked_at"`
	Detail    string    `json:"detail"`
}

// serveHealth exposes readiness on the proxy's own subdomain, so an external
// check can ask this proxy whether it can serve private modules rather than
// only whether it is listening.
//
// The status code and the healthy flag are unauthenticated, because a monitor
// that must authenticate is a monitor nobody wires up. Everything else needs a
// read token: the private prefixes, the readiness module and a probe error all
// NAME private repositories, and serve() gates even public modules so that "is
// this module private?" is not a question anybody can ask anonymously. Handing
// out the prefix list here would answer it for free.
func (s *Service) serveHealth(w http.ResponseWriter, r *http.Request) {
	h := s.Health()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if !h.Healthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	if s.authorized(r) {
		_ = json.NewEncoder(w).Encode(h)
		return
	}
	_ = json.NewEncoder(w).Encode(publicHealth{
		Healthy:   h.Healthy,
		CheckedAt: h.CheckedAt,
		Detail:    "authenticate with a read-scoped token for the reason, the credential state and the configured prefixes",
	})
}

// cachedFromResolved converts a resolution into a cache row.
func cachedFromResolved(r *resolved) *db.GoproxyCached {
	return &db.GoproxyCached{
		Version:     r.Version,
		CommitSHA:   r.Commit,
		CommittedAt: r.Time,
		GoMod:       r.GoMod,
	}
}
