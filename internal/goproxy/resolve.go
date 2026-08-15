package goproxy

import (
	"context"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// resolved is a module version pinned to the exact commit that serves it.
type resolved struct {
	Ref     repoRef
	Version string
	Commit  string
	Time    time.Time
	// GoMod is the module's go.mod at Commit (synthesized when the module has
	// none, which the protocol still requires us to serve).
	GoMod []byte
}

// resolveRef picks which repo directory actually holds modPath.
//
// A "/vN" module may live at the repo root (its go.mod declares the /vN path)
// or in a "vN" subdirectory, and only the go.mod settles it -- so each candidate
// is checked against the path it declares. A module with no go.mod at all is
// accepted as-is: that is legal for pre-modules code, and there is nothing to
// contradict.
func (s *Service) resolveRef(ctx context.Context, modPath, rev string) (repoRef, []byte, error) {
	candidates, err := parseModulePath(modPath)
	if err != nil {
		return repoRef{}, nil, err
	}

	var firstErr error
	for _, ref := range candidates {
		content, declared, err := s.github.goModAt(ctx, ref, rev, modPath, "")
		if err != nil {
			// Keep the first failure: if no candidate resolves, that error is a far
			// better report than "not found" (it may be the credential).
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if declared == "" || declared == modPath {
			return ref, content, nil
		}
		if firstErr == nil {
			firstErr = notFoundErr(modPath, "", upstreamGitHub, 200,
				"go.mod in "+refDesc(ref)+" declares module "+declared)
		}
	}
	if firstErr != nil {
		return repoRef{}, nil, firstErr
	}
	return repoRef{}, nil, notFoundErr(modPath, "", upstreamGitHub, 0, "no module root matched this path")
}

func refDesc(r repoRef) string {
	if r.Dir == "" {
		return r.Owner + "/" + r.Repo
	}
	return r.Owner + "/" + r.Repo + "/" + r.Dir
}

// versionTags returns the module's semver tags, newest first, paired with the
// tag that carries each. Tags outside this module's directory prefix, and tags
// whose major version disagrees with the module path's suffix, are excluded --
// a v2 tag on the repo root is not a version of the root module.
func (s *Service) versionTags(ctx context.Context, modPath string, ref repoRef) ([]taggedVersion, error) {
	tags, err := s.github.listTags(ctx, ref, modPath)
	if err != nil {
		return nil, err
	}
	prefix := ref.TagPrefix()
	wantMajor := ref.Major
	if wantMajor == "" {
		wantMajor = "v1" // semver.Major reports v0 and v1 separately; both are allowed here.
	}

	var out []taggedVersion
	for _, t := range tags {
		v, ok := strings.CutPrefix(t.Name, prefix)
		if !ok || !semver.IsValid(v) || v != semver.Canonical(v) {
			continue
		}
		maj := semver.Major(v)
		if ref.Major == "" {
			if maj != "v0" && maj != "v1" {
				continue
			}
		} else if maj != ref.Major {
			continue
		}
		out = append(out, taggedVersion{Version: v, Tag: t})
	}
	sort.Slice(out, func(i, j int) bool {
		return semver.Compare(out[i].Version, out[j].Version) > 0
	})
	return out, nil
}

type taggedVersion struct {
	Version string
	Tag     tagRef
}

// listVersions serves @v/list: the module's tagged versions. Pseudo-versions are
// excluded per the protocol; pre-releases are included, as the go command
// filters those itself.
func (s *Service) listVersions(ctx context.Context, modPath string) ([]string, error) {
	ref, _, err := s.resolveRef(ctx, modPath, "HEAD")
	if err != nil {
		return nil, err
	}
	tagged, err := s.versionTags(ctx, modPath, ref)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(tagged))
	for _, t := range tagged {
		out = append(out, t.Version)
	}
	return out, nil
}

// resolveVersion pins an exact requested version to its commit.
func (s *Service) resolveVersion(ctx context.Context, modPath, version string) (*resolved, error) {
	if !semver.IsValid(version) {
		return nil, invalidErr(modPath, version, "not a valid semantic version")
	}

	if module.IsPseudoVersion(version) {
		return s.resolvePseudo(ctx, modPath, version)
	}

	ref, _, err := s.resolveRef(ctx, modPath, "HEAD")
	if err != nil {
		return nil, err
	}
	tagged, err := s.versionTags(ctx, modPath, ref)
	if err != nil {
		return nil, err
	}
	canonical := semver.Canonical(version)
	for _, t := range tagged {
		if t.Version != canonical {
			continue
		}
		sha, err := s.github.commitSHA(ctx, ref, t.Tag, modPath, version)
		if err != nil {
			return nil, err
		}
		return s.pin(ctx, ref, modPath, t.Version, sha)
	}
	return nil, notFoundErr(modPath, version, upstreamGitHub, 0,
		"no tag "+ref.TagPrefix()+canonical+" in "+refDesc(ref))
}

// resolvePseudo resolves a pseudo-version by the commit embedded in it. The
// revision is authoritative: the timestamp in the pseudo-version is not
// re-derived, only checked for being a real commit.
func (s *Service) resolvePseudo(ctx context.Context, modPath, version string) (*resolved, error) {
	rev, err := module.PseudoVersionRev(version)
	if err != nil || rev == "" {
		return nil, invalidErr(modPath, version, "pseudo-version carries no revision")
	}
	ref, _, err := s.resolveRef(ctx, modPath, rev)
	if err != nil {
		return nil, err
	}
	commit, err := s.github.getCommit(ctx, ref, rev, modPath, version)
	if err != nil {
		return nil, err
	}
	return s.pin(ctx, ref, modPath, version, commit.SHA)
}

// pin fills in the commit time and go.mod for an already-chosen revision.
func (s *Service) pin(ctx context.Context, ref repoRef, modPath, version, sha string) (*resolved, error) {
	commit, err := s.github.getCommit(ctx, ref, sha, modPath, version)
	if err != nil {
		return nil, err
	}
	gomod, declared, err := s.github.goModAt(ctx, ref, commit.SHA, modPath, version)
	if err != nil {
		return nil, err
	}
	if declared != "" && declared != modPath {
		return nil, notFoundErr(modPath, version, upstreamGitHub, 0,
			"go.mod at this version declares module "+declared)
	}
	return &resolved{Ref: ref, Version: version, Commit: commit.SHA, Time: commit.Time, GoMod: gomod}, nil
}

// latest serves @latest: the highest release version, else the highest
// pre-release, else a pseudo-version of the default branch head. The last case
// is the normal one for this org's untagged first-party modules, so it is a
// first-class path rather than a fallback that reports "nothing here".
func (s *Service) latest(ctx context.Context, modPath string) (*resolved, error) {
	ref, _, err := s.resolveRef(ctx, modPath, "HEAD")
	if err != nil {
		return nil, err
	}
	tagged, err := s.versionTags(ctx, modPath, ref)
	if err != nil {
		return nil, err
	}

	var best *taggedVersion
	for i, t := range tagged {
		if semver.Prerelease(t.Version) == "" {
			best = &tagged[i]
			break
		}
	}
	if best == nil && len(tagged) > 0 {
		best = &tagged[0]
	}
	if best != nil {
		sha, err := s.github.commitSHA(ctx, ref, best.Tag, modPath, best.Version)
		if err != nil {
			return nil, err
		}
		return s.pin(ctx, ref, modPath, best.Version, sha)
	}

	branch, err := s.github.defaultBranch(ctx, ref, modPath)
	if err != nil {
		return nil, err
	}
	commit, err := s.github.getCommit(ctx, ref, branch, modPath, "")
	if err != nil {
		return nil, err
	}
	major := ref.Major
	if major == "" {
		major = "v0"
	}
	version := module.PseudoVersion(major, "", commit.Time, shortRev(commit.SHA))
	return s.pin(ctx, ref, modPath, version, commit.SHA)
}

func shortRev(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
