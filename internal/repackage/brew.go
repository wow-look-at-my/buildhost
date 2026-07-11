package repackage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	neturl "net/url"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

var brewUnsafeChars = regexp.MustCompile(`[^a-zA-Z0-9 .,;:!?@&()/'+*=_-]`)

func sanitizeBrewString(s string) string {
	return brewUnsafeChars.ReplaceAllString(s, "")
}

func init() { Register(&Brew{}) }

type Brew struct{}

func (b *Brew) Format() Format { return FormatBrew }

func (b *Brew) Applicable(a db.Artifact) bool {
	if a.Kind == db.KindAssets || a.Kind.ServedViaDockerOnly() {
		return false
	}
	return a.OS == db.OSLinux || a.OS == db.OSDarwin
}

// brewTemplate always emits a TOP-LEVEL url/sha256 (the canonical resource,
// see brewCanonicalResource) in addition to the per-platform on_<os>/on_<arch>
// blocks. Homebrew's loader must find a stable URL on EVERY platform to import
// a formula at all (determine_active_spec raises "formula requires at least a
// URL" otherwise) -- and a formula that fails import poisons evaluation of the
// whole tap for that platform. With only on_* stanzas, a linux-only project's
// formula had no URL visible on macOS and broke the tap for every mac user
// (and vice versa). The on_* blocks still override url/sha256 on platforms
// they match, so multi-platform resolution is unchanged; on a platform with
// no matching block the canonical resource is what a (single-OS gated, see
// DependsOnOS) install would report. DependsOnOS is homebrew-core's
// single-platform pattern: `depends_on :linux` makes a foreign-platform
// install fail cleanly ("Linux is required...") instead of fetching a binary
// that cannot run.
var brewTemplate = template.Must(template.New("formula").Parse(`{{ if .Private }}require_relative "../lib/buildhost_private_download"

{{ end }}class {{ .ClassName }} < Formula
  desc "{{ .Description }}"
  homepage "{{ .Homepage }}"
  version "{{ .Version }}"
  license "{{ .License }}"

  url "{{ .Canonical.URL }}"{{ if .Private }}, using: BuildhostCurlDownloadStrategy{{ end }}
  sha256 "{{ .Canonical.SHA256 }}"
  {{- if .DependsOnOS }}
  depends_on :{{ .DependsOnOS }}
  {{- end }}

  {{- range .Resources }}
  on_{{ .OS }} do
    on_{{ .Arch }} do
      url "{{ .URL }}"{{ if $.Private }}, using: BuildhostCurlDownloadStrategy{{ end }}
      sha256 "{{ .SHA256 }}"
    end
  end
  {{- end }}

  def install
    {{- if eq .Kind "binary" }}
    bin.install "{{ .InstallName }}"
    {{- else if eq .Kind "library" }}
    lib.install "{{ .InstallName }}"
    {{- else }}
    prefix.install Dir["*"]
    {{- end }}
  end
end
`))

// brewInstallName returns the path the staged download exposes for install.
// The tar.gz artifact contains exactly one entry named after the project, so
// for a slash-namespaced project ("myrepo/myapp") the archive's only
// top-level entry is the "myrepo" directory -- and Homebrew's unpack step
// strips a lone top-level directory (the same normalization it applies to
// GitHub tarballs), leaving just "myapp" in the stage. Installing the full
// slashed path therefore ENOENTs; the basename is what actually exists. For
// single-segment projects the entry is a top-level file and the basename is
// the name itself, so this is universally correct.
func brewInstallName(project string) string {
	if i := strings.LastIndexByte(project, '/'); i >= 0 {
		return project[i+1:]
	}
	return project
}

// BrewPrivateStrategyPath is the path inside the generated tap repository that
// carries the download strategy for private-project formulas. Those formulas
// require_relative it (the "lib/" companion-file layout is Homebrew's standard
// private-tap pattern).
const BrewPrivateStrategyPath = "lib/buildhost_private_download.rb"

// BrewPrivateStrategy is the Ruby download strategy shipped in the generated
// tap. It never contains a token: the token comes from the user's environment
// at install time. The variable MUST be HOMEBREW_-prefixed -- Homebrew scrubs
// every other variable from the environment before formula code runs.
//
// The strategy only authenticates the INITIAL download request. buildhost's dl
// endpoint answers an authenticated private download with a redirect whose
// Location carries a short-lived signed token bound to that one artifact, so
// the followed cross-host redirect needs no Authorization header (curl drops
// the header on cross-host redirects by design, and brew inherits curl
// semantics).
const BrewPrivateStrategy = `# frozen_string_literal: true

# Download strategy for private buildhost projects: sends the token from
# HOMEBREW_BUILDHOST_TOKEN as a Bearer Authorization header on the download
# request. buildhost redirects private downloads with a short-lived signed
# token in the Location, so the followed redirect needs no header.
class BuildhostCurlDownloadStrategy < CurlDownloadStrategy
  def initialize(url, name, version, **meta)
    token = ENV["HOMEBREW_BUILDHOST_TOKEN"].to_s
    unless token.empty?
      meta = meta.merge(headers: Array(meta[:headers]) + ["Authorization: Bearer #{token}"])
    end
    super(url, name, version, **meta)
  end

  def fetch(timeout: nil)
    if ENV["HOMEBREW_BUILDHOST_TOKEN"].to_s.empty?
      raise "HOMEBREW_BUILDHOST_TOKEN is not set; export a buildhost token " \
            "with read access to this project, then retry."
    end
    super
  end
end
`

type brewData struct {
	ClassName   string
	Name        string
	InstallName string
	Description string
	Homepage    string
	Version     string
	License     string
	Kind        string
	Private     bool
	Canonical   BrewResource
	DependsOnOS string
	Resources   []BrewResource
}

// brewCanonicalResource picks the deterministic resource emitted as the
// formula's top-level url/sha256: linux/intel when present (the org's default
// platform), else the first in stable (OS, Arch) order. Deterministic choice
// matters -- the formula bytes feed the tap's content-addressed git objects, so
// an unstable pick would mint spurious tap commits.
func brewCanonicalResource(resources []BrewResource) BrewResource {
	sorted := make([]BrewResource, len(resources))
	copy(sorted, resources)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].OS != sorted[j].OS {
			return sorted[i].OS < sorted[j].OS
		}
		return sorted[i].Arch < sorted[j].Arch
	})
	for _, r := range sorted {
		if r.OS == "linux" && r.Arch == "intel" {
			return r
		}
	}
	return sorted[0]
}

// brewDependsOnOS returns "linux" or "macos" when every resource targets that
// one OS -- the formula then declares `depends_on :<os>` so installing on the
// other platform fails with a clean requirement error instead of downloading a
// foreign binary. Formulas spanning both OSes return "" (no gate).
func brewDependsOnOS(resources []BrewResource) string {
	osName := ""
	for _, r := range resources {
		if osName != "" && r.OS != osName {
			return ""
		}
		osName = r.OS
	}
	return osName
}

type BrewResource struct {
	OS     string
	Arch   string
	URL    string
	SHA256 string
}

type BrewFormula struct {
	ClassName   string
	Name        string
	Description string
	Homepage    string
	Version     string
	License     string
	Kind        string
	// Private marks a formula for a private project: it requires the tap's
	// BuildhostCurlDownloadStrategy (BrewPrivateStrategyPath) and downloads
	// with `using:` it, so the artifact fetch carries the user's token from
	// HOMEBREW_BUILDHOST_TOKEN. The formula itself never embeds a token.
	Private   bool
	Resources []BrewResource
}

func RenderBrewFormula(f BrewFormula) (*Output, error) {
	if len(f.Resources) == 0 {
		return nil, fmt.Errorf("formula %q has no resources", f.Name)
	}
	d := brewData{
		ClassName:   f.ClassName,
		Name:        sanitizeBrewString(f.Name),
		InstallName: sanitizeBrewString(brewInstallName(f.Name)),
		Description: sanitizeBrewString(f.Description),
		Homepage:    sanitizeBrewString(f.Homepage),
		Version:     sanitizeBrewString(f.Version),
		License:     sanitizeBrewString(f.License),
		Kind:        f.Kind,
		Private:     f.Private,
		Canonical:   brewCanonicalResource(f.Resources),
		DependsOnOS: brewDependsOnOS(f.Resources),
		Resources:   f.Resources,
	}

	var buf bytes.Buffer
	if err := brewTemplate.Execute(&buf, d); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	filename := f.Name + ".rb"
	return &Output{
		Reader:   io.NopCloser(&buf),
		Filename: filename,
		Size:     int64(buf.Len()),
	}, nil
}

func (b *Brew) Repackage(_ context.Context, input Input) (*Output, error) {
	if !BrewEligibleProjectName(input.Project.Name) {
		return nil, fmt.Errorf("project name %q cannot be a Homebrew formula (Ruby class names cannot start with a digit)", input.Project.Name)
	}
	h := sha256.New()
	if _, err := io.Copy(h, input.Reader); err != nil {
		return nil, fmt.Errorf("hash artifact: %w", err)
	}
	sha := fmt.Sprintf("%x", h.Sum(nil))

	version := strings.TrimPrefix(input.Release.Version, "v")
	if version == "" {
		version = fmt.Sprintf("%d", input.Release.VersionNum)
	}

	brewOS := "macos"
	if input.Artifact.OS == db.OSLinux {
		brewOS = "linux"
	}
	brewArch := "arm"
	if input.Artifact.Arch == db.ArchAMD64 {
		brewArch = "intel"
	}

	var url string
	if input.DownloadURL != nil {
		url = input.DownloadURL(input.Project.Name, version, input.Artifact.OS, input.Artifact.Arch, "tar.gz")
	} else {
		dlBase := dlServiceURL(input.BaseURL)
		q := neturl.Values{"os": {string(input.Artifact.OS)}, "arch": {string(input.Artifact.Arch)}}
		if version != "" {
			q.Set("v", "v"+version)
		}
		q.Set("fmt", "tar.gz")
		url = dlBase + "/" + input.Project.Name + "?" + q.Encode()
	}

	return RenderBrewFormula(BrewFormula{
		ClassName:   BrewClassName(input.Project.Name),
		Name:        sanitizeBrewString(input.Project.Name),
		Description: sanitizeBrewString(firstNonEmpty(input.Project.Description, input.Project.Name)),
		Homepage:    sanitizeBrewString(firstNonEmpty(input.Project.Homepage, input.BaseURL)),
		Version:     sanitizeBrewString(version),
		License:     sanitizeBrewString(firstNonEmpty(input.Project.License, "MIT")),
		Kind:        string(input.Artifact.Kind),
		Private:     input.Project.IsPrivate,
		Resources: []BrewResource{{
			OS:     brewOS,
			Arch:   brewArch,
			URL:    url,
			SHA256: sha,
		}},
	})
}

// BrewClassName derives the formula's Ruby class name from the project name.
// It MUST match what Homebrew derives from the formula FILENAME
// (Formulary.class_s of the folded name), or the tap's formulas fail to load
// with "expected to find class" -- and it must always be a valid Ruby
// constant, or brew dies with a ".rb: syntax error" while parsing the file.
// Brew's derivation treats '-', '_', and '.' as separators: it drops them and
// upcases the following character ("a.b-c_d" -> "ABCD", "go1.2.3" -> "Go123";
// measured against Formulary.class_s on Homebrew 6.0.9). '/' is buildhost's
// namespace fold (tapFormulaName turns it into '-'), so it separates the same
// way. Callers must gate on BrewEligibleProjectName first.
func BrewClassName(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_' || r == '/' || r == '.'
	})
	var b strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			b.WriteString(strings.ToUpper(p[:1]))
			b.WriteString(p[1:])
		}
	}
	return b.String()
}

// BrewEligibleProjectName reports whether a project name can be served as a
// Homebrew formula AT ALL. A name starting with a digit cannot: brew derives
// the expected class from the formula filename (Formulary.class_s("7zip") ==
// "7zip"), and a Ruby constant cannot start with a digit, so NO declaration
// satisfies the loader -- emitting `class 7zip < Formula` is a guaranteed
// ".rb:1: syntax errors found" that also breaks whole-tap evaluation, and any
// valid substitute class fails with TapFormulaClassUnavailableError (both
// measured against Homebrew 6.0.9). Such projects are excluded from the tap
// and 404 on the formula endpoints instead of poisoning the tap with
// unparseable Ruby. Project names are validator-constrained to lowercase
// [a-z0-9] starts, so checking the first byte suffices.
func BrewEligibleProjectName(name string) bool {
	return name != "" && name[0] >= 'a' && name[0] <= 'z'
}
