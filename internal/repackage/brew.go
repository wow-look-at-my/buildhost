package repackage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	neturl "net/url"
	"regexp"
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

var brewTemplate = template.Must(template.New("formula").Parse(`class {{ .ClassName }} < Formula
  desc "{{ .Description }}"
  homepage "{{ .Homepage }}"
  version "{{ .Version }}"
  license "{{ .License }}"

  {{- range .Resources }}
  on_{{ .OS }} do
    on_{{ .Arch }} do
      url "{{ .URL }}"
      sha256 "{{ .SHA256 }}"
    end
  end
  {{- end }}

  def install
    {{- if eq .Kind "binary" }}
    bin.install "{{ .Name }}"
    {{- else if eq .Kind "library" }}
    lib.install "{{ .Name }}"
    {{- else }}
    prefix.install Dir["*"]
    {{- end }}
  end
end
`))

type brewData struct {
	ClassName   string
	Name        string
	Description string
	Homepage    string
	Version     string
	License     string
	Kind        string
	Resources   []BrewResource
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
	Resources   []BrewResource
}

func RenderBrewFormula(f BrewFormula) (*Output, error) {
	d := brewData{
		ClassName:   f.ClassName,
		Name:        sanitizeBrewString(f.Name),
		Description: sanitizeBrewString(f.Description),
		Homepage:    sanitizeBrewString(f.Homepage),
		Version:     sanitizeBrewString(f.Version),
		License:     sanitizeBrewString(f.License),
		Kind:        f.Kind,
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
		Resources: []BrewResource{{
			OS:     brewOS,
			Arch:   brewArch,
			URL:    url,
			SHA256: sha,
		}},
	})
}

func BrewClassName(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_' || r == '/'
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
