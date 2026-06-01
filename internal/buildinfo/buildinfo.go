// Package buildinfo exposes the version-control metadata that the Go toolchain
// stamps into the binary at build time: the commit it was built from, the
// commit time, and whether the working tree was dirty. Both the `version`
// command and the GET /healthz endpoint report it, so the running build can be
// identified without a separate version endpoint (for example, to confirm that
// a rollout has landed on the server).
package buildinfo

import (
	"fmt"
	"runtime/debug"
	"sync"
	"time"
)

// Info holds the VCS stamps embedded at build time. Its fields are empty when
// the binary carries no VCS information (for example a `go run` outside a
// checkout, or a build with -buildvcs=false).
type Info struct {
	Revision string // full git commit SHA
	Time     string // commit time in RFC3339
	Modified bool   // built from a dirty working tree
}

var (
	once   sync.Once
	cached Info
)

// Get returns the embedded VCS metadata. It is read from the binary once and
// cached, and is safe for concurrent use.
func Get() Info {
	once.Do(func() {
		bi, ok := debug.ReadBuildInfo()
		if !ok {
			return
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				cached.Revision = s.Value
			case "vcs.time":
				cached.Time = s.Value
			case "vcs.modified":
				cached.Modified = s.Value == "true"
			}
		}
	})
	return cached
}

// Commit returns the full git commit SHA the binary was built from, or
// "unknown" when no VCS stamp is present.
func Commit() string {
	if r := Get().Revision; r != "" {
		return r
	}
	return "unknown"
}

// Version returns a synthetic version derived from the commit time
// ("v0.0.<unix-seconds>"), or "dev" when no commit time is available.
func Version() string {
	if ts := Get().Time; ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			return fmt.Sprintf("v0.0.%d", t.Unix())
		}
	}
	return "dev"
}

// Date returns the commit time in RFC3339, or "" when unavailable.
func Date() string {
	return Get().Time
}
