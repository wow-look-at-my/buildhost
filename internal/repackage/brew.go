package repackage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"text/template"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

func sanitizeBrewString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

type Brew struct{}

func (b *Brew) Format() Format { return FormatBrew }

func (b *Brew) Applicable(a model.Artifact) bool {
	if a.Kind == model.KindAssets {
		return false
	}
	return a.OS == model.OSLinux || a.OS == model.OSDarwin
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
	Resources   []brewResource
}

type brewResource struct {
	OS   string
	Arch string
	URL  string
	SHA256 string
}

func (b *Brew) Repackage(_ context.Context, input Input) (*Output, error) {
	data, err := io.ReadAll(input.Binary)
	if err != nil {
		return nil, fmt.Errorf("read binary: %w", err)
	}

	h := sha256.Sum256(data)
	sha := fmt.Sprintf("%x", h)

	version := strings.TrimPrefix(input.Release.Version, "v")
	if version == "" {
		version = fmt.Sprintf("%d", input.Release.VersionNum)
	}

	brewOS := "macos"
	if input.Artifact.OS == model.OSLinux {
		brewOS = "linux"
	}
	brewArch := "arm"
	if input.Artifact.Arch == model.ArchAMD64 {
		brewArch = "intel"
	}

	url := fmt.Sprintf("%s/dl/%s/v%s/%s/%s?format=tar.gz",
		input.BaseURL, input.Project.Name, version, input.Artifact.OS, input.Artifact.Arch)

	d := brewData{
		ClassName:   brewClassName(input.Project.Name),
		Name:        sanitizeBrewString(input.Project.Name),
		Description: sanitizeBrewString(firstNonEmpty(input.Project.Description, input.Project.Name)),
		Homepage:    sanitizeBrewString(firstNonEmpty(input.Project.Homepage, input.BaseURL)),
		Version:     version,
		License:     sanitizeBrewString(firstNonEmpty(input.Project.License, "MIT")),
		Kind:        string(input.Artifact.Kind),
		Resources: []brewResource{{
			OS:     brewOS,
			Arch:   brewArch,
			URL:    url,
			SHA256: sha,
		}},
	}

	var buf bytes.Buffer
	if err := brewTemplate.Execute(&buf, d); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	filename := input.Project.Name + ".rb"
	return &Output{
		Reader:   &buf,
		Filename: filename,
		Size:     int64(buf.Len()),
	}, nil
}

func brewClassName(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_'
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
