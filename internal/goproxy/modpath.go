package goproxy

import (
	"regexp"
	"strings"

	"golang.org/x/mod/module"
)

// repoRef is a module path resolved onto a GitHub repository: which repo holds
// it, which directory inside that repo is the module root, and therefore which
// tag prefix its versions carry.
type repoRef struct {
	Owner string
	Repo  string
	// Dir is the module root relative to the repo root ("" for a repo-root
	Dir string
	// Major is the major-version suffix ("" for v0/v1, else "v2", "v3", ...).
	Major string
}

// TagPrefix is what a semver tag for this module root is prefixed with. Go tags
func (r repoRef) TagPrefix() string {
	if r.Dir == "" {
		return ""
	}
	return r.Dir + "/"
}

var majorSuffixRE = regexp.MustCompile(`^v([2-9]|[1-9][0-9]+)$`)

// parseModulePath maps a module path onto the GitHub repo that serves it.
//
// Only github.com paths are handled: this proxy's private-module source is the
// GitHub API, and a path it cannot map is reported as such rather than being
// guessed at. Everything else reaches the upstream public proxy instead.
//
// A "/vN" major-version suffix does not by itself say where the code lives: it
// may sit at the module root (go.mod declares the /vN path) or in a "vN"
func parseModulePath(path string) ([]repoRef, error) {
	if err := module.CheckPath(path); err != nil {
		return nil, invalidErr(path, "", "not a valid module path: "+err.Error())
	}
	rest, ok := strings.CutPrefix(path, "github.com/")
	if !ok {
		return nil, invalidErr(path, "", "only github.com module paths are served from the private source")
	}

	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return nil, invalidErr(path, "", "github.com module paths need an owner and a repository")
	}
	owner, repo := parts[0], parts[1]
	sub := parts[2:]

	major := ""
	if n := len(sub); n > 0 && majorSuffixRE.MatchString(sub[n-1]) {
		major = sub[n-1]
		sub = sub[:n-1]
	}
	dir := strings.Join(sub, "/")

	if major == "" {
		return []repoRef{{Owner: owner, Repo: repo, Dir: dir}}, nil
	}

	// With a major suffix the module root is either the same directory (go.mod
	majorDir := major
	if dir != "" {
		majorDir = dir + "/" + major
	}
	return []repoRef{
		{Owner: owner, Repo: repo, Dir: dir, Major: major},
		{Owner: owner, Repo: repo, Dir: majorDir, Major: major},
	}, nil
}

func matchesPrefix(path string, prefixes []string) bool {
	for _, p := range prefixes {
		p = strings.Trim(strings.TrimSpace(p), "/")
		if p == "" {
			continue
		}
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
