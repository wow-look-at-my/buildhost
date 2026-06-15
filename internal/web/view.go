package web

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// siteName is the product name shown in the header and titles.
const siteName = "buildhost"

var templateFuncs = template.FuncMap{
	// nonEmpty reports whether s is a non-blank string, for {{if}} guards on
	// optional metadata fields.
	"nonEmpty": func(s string) bool { return strings.TrimSpace(s) != "" },
}

// ----- index (home) --------------------------------------------------------

type indexView struct {
	SiteName string
	Rows     []projectListRow
}

type projectCard struct {
	Name          string
	Label         string
	URL           string
	Description   string
	ReleaseCount  int64
	ArtifactCount int64
	Updated       string
	Private       bool
}

type projectListRow struct {
	Kind    string
	Depth   int
	Folder  string
	Project projectCard
}

func buildIndexView(rows []db.ProjectSummary) indexView {
	root := newProjectNode("")
	for _, p := range rows {
		root.add(projectCard{
			Name:          p.Name,
			Label:         lastSegment(p.Name),
			URL:           projectPath(p.Name),
			Description:   p.Description,
			ReleaseCount:  p.ReleaseCount,
			ArtifactCount: p.ArtifactCount,
			Updated:       timeAgo(p.UpdatedAt),
			Private:       p.IsPrivate,
		})
	}
	return indexView{SiteName: siteName, Rows: root.rows(0)}
}

type projectNode struct {
	name     string
	project  *projectCard
	children map[string]*projectNode
}

func newProjectNode(name string) *projectNode {
	return &projectNode{name: name, children: map[string]*projectNode{}}
}

func (n *projectNode) add(card projectCard) {
	cur := n
	for _, part := range strings.Split(card.Name, "/") {
		child := cur.children[part]
		if child == nil {
			child = newProjectNode(part)
			cur.children[part] = child
		}
		cur = child
	}
	cur.project = &card
}

func (n *projectNode) rows(depth int) []projectListRow {
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]projectListRow, 0, len(names))
	for _, name := range names {
		child := n.children[name]
		hasChildren := len(child.children) > 0
		if hasChildren {
			out = append(out, projectListRow{Kind: "folder", Depth: depth, Folder: name})
		}
		if child.project != nil {
			out = append(out, projectListRow{Kind: "project", Depth: depth, Project: *child.project})
		}
		out = append(out, child.rows(depth+1)...)
	}
	return out
}

// ----- project -------------------------------------------------------------

type projectView struct {
	SiteName    string
	Name        string
	Description string
	Homepage    string
	License     string
	Versioning  string
	Private     bool
	Created     string
	Updated     string
	Releases    []releaseRow
	Sites       []siteRow
	Install     *installInfo
}

type releaseRow struct {
	Version       string
	URL           string
	Branch        string
	Commit        string
	Published     string
	ArtifactCount int64
	Latest        bool
}

type siteRow struct {
	Branch  string
	URL     string
	Files   int64
	Updated string
}

// installInfo holds copy-pasteable commands for fetching a project. Commands
// are gated on what the latest published release actually contains: a
// docker-only release exposes just `docker pull`, anything with a real binary
// exposes the download/apt/brew/npm forms too.
type installInfo struct {
	HasBinary bool
	Curl      string
	Brew      string
	Npm       string
	Apt       string
	Docker    string
}

func buildProjectView(r *http.Request, p *db.Project, rels []db.ReleaseSummary, sites []db.Site, latestHasBinary bool, latestVersion string) projectView {
	v := projectView{
		SiteName:    siteName,
		Name:        p.Name,
		Description: p.Description,
		Homepage:    p.Homepage,
		License:     p.License,
		Versioning:  string(p.Versioning),
		Private:     p.IsPrivate,
		Created:     formatDate(p.CreatedAt),
		Updated:     timeAgo(p.UpdatedAt),
	}

	// Only published releases are downloadable, so only those are shown. The
	// first published row (the list is ordered newest-first) is the latest.
	latestShown := false
	for _, rel := range rels {
		if !rel.Published {
			continue
		}
		row := releaseRow{
			Version:       rel.Version,
			URL:           releasePath(p.Name, rel.Version),
			Branch:        rel.GitBranch,
			Commit:        shortCommit(rel.GitCommit),
			Published:     publishedWhen(rel.PublishedAt, rel.CreatedAt),
			ArtifactCount: rel.ArtifactCount,
		}
		if !latestShown {
			row.Latest = true
			latestShown = true
		}
		v.Releases = append(v.Releases, row)
	}

	for _, s := range sites {
		v.Sites = append(v.Sites, siteRow{
			Branch:  s.Branch,
			URL:     serviceURL(r, "sites", p.Name+"/branch/"+s.Branch+"/"),
			Files:   s.FileCount,
			Updated: timeAgo(s.UpdatedAt),
		})
	}

	if latestVersion != "" {
		v.Install = buildInstallInfo(r, p.Name, latestVersion, latestHasBinary)
	}
	return v
}

// buildInstallInfo assembles copy-paste fetch commands. `docker pull` is always
// offered (buildhost synthesizes an image even from a bare binary); the
// download/apt/brew/npm forms are offered only when the latest release actually
// has a non-docker artifact to repackage.
func buildInstallInfo(r *http.Request, project, version string, hasBinary bool) *installInfo {
	info := &installInfo{
		HasBinary: hasBinary,
		Docker:    "docker pull oci." + r.Host + "/" + project + ":" + version,
	}
	if hasBinary {
		info.Curl = fmt.Sprintf("curl -LO %q", dlURL(r, project, "", "linux", "amd64", "raw"))
		info.Brew = "brew tap pazer/build " + serviceURL(r, "brew", "tap.git") + "\nbrew install pazer/build/" + project
		info.Npm = "npm install @buildhost/" + project + " --registry " + serviceBase(r, "npm")
		info.Apt = fmt.Sprintf("echo \"deb [signed-by=/etc/apt/keyrings/%s.gpg] %s stable main\" | sudo tee /etc/apt/sources.list.d/%s.list",
			lastSegment(project), serviceURL(r, "apt", project), lastSegment(project))
	}
	return info
}

// ----- release -------------------------------------------------------------

type releaseView struct {
	SiteName    string
	ProjectName string
	ProjectURL  string
	Version     string
	Branch      string
	Commit      string
	Notes       string
	Published   string
	Artifacts   []artifactRow
}

type artifactRow struct {
	OS         string
	Arch       string
	Kind       string
	Filename   string
	Size       string
	SHA256     string
	Downloads  []downloadLink
	Docker     bool
	DockerPull string
}

type downloadLink struct {
	Label string
	URL   string
}

// archiveFormats are the repackaged download formats offered for every
// non-docker artifact, matching the fmt values the dl/static endpoints accept.
var archiveFormats = []string{"tar.gz", "tar.xz", "tar.zst", "zip"}

func buildReleaseView(r *http.Request, p *db.Project, rel *db.Release, arts []db.Artifact) releaseView {
	v := releaseView{
		SiteName:    siteName,
		ProjectName: p.Name,
		ProjectURL:  projectPath(p.Name),
		Version:     rel.Version,
		Branch:      rel.GitBranch,
		Commit:      shortCommit(rel.GitCommit),
		Notes:       rel.Notes,
		Published:   publishedWhen(rel.PublishedAt, rel.CreatedAt),
	}

	for _, a := range arts {
		row := artifactRow{
			OS:       string(a.OS),
			Arch:     string(a.Arch),
			Kind:     string(a.Kind),
			Filename: a.Filename,
			Size:     humanSize(a.Size),
			SHA256:   a.SHA256,
		}
		if a.Kind.ServedViaDockerOnly() {
			row.Docker = true
			row.DockerPull = "docker pull " + "oci." + r.Host + "/" + p.Name + ":" + rel.Version
			v.Artifacts = append(v.Artifacts, row)
			continue
		}
		row.Downloads = append(row.Downloads, downloadLink{
			Label: "raw",
			URL:   dlURL(r, p.Name, rel.Version, string(a.OS), string(a.Arch), "raw"),
		})
		for _, f := range archiveFormats {
			row.Downloads = append(row.Downloads, downloadLink{
				Label: f,
				URL:   dlURL(r, p.Name, rel.Version, string(a.OS), string(a.Arch), f),
			})
		}
		v.Artifacts = append(v.Artifacts, row)
	}
	return v
}

// ----- URL helpers ---------------------------------------------------------

// serviceBase returns the scheme://host base for a service subdomain, derived
// from the main-domain request (e.g. example.com -> https://dl.example.com).
// It deliberately does not use auth.DeriveServiceURL, which is meant for
// subdomain-origin requests and strips the first host label.
func serviceBase(r *http.Request, service string) string {
	return auth.RequestScheme(r) + "://" + service + "." + r.Host
}

// serviceURL builds a full URL on a service subdomain for the given path
// (no leading slash), with proper escaping.
func serviceURL(r *http.Request, service, path string) string {
	u, err := url.Parse(serviceBase(r, service))
	if err != nil {
		return ""
	}
	u.Path = "/" + path
	return u.String()
}

// dlURL builds a canonical download URL on the dl subdomain. An empty version
// downloads the latest; an empty/"raw" format downloads the original binary.
func dlURL(r *http.Request, project, version, os, arch, format string) string {
	q := url.Values{}
	q.Set("os", os)
	q.Set("arch", arch)
	if version != "" {
		q.Set("v", version)
	}
	if format != "" && format != "raw" {
		q.Set("fmt", format)
	}
	u, err := url.Parse(serviceBase(r, "dl"))
	if err != nil {
		return ""
	}
	u.Path = "/" + project
	u.RawQuery = q.Encode()
	return u.String()
}

func projectPath(project string) string {
	u := &url.URL{Path: "/projects/" + project}
	return u.String()
}

func releasePath(project, version string) string {
	u := &url.URL{Path: "/projects/" + project + "/releases/" + version}
	return u.String()
}

// ----- formatting helpers --------------------------------------------------

func lastSegment(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

func publishedWhen(publishedAt *time.Time, created time.Time) string {
	if publishedAt != nil {
		return timeAgo(*publishedAt)
	}
	return timeAgo(created)
}

func formatDate(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour")
	case d < 30*24*time.Hour:
		return plural(int(d.Hours()/24), "day")
	default:
		return t.UTC().Format("2006-01-02")
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit + " ago"
	}
	return fmt.Sprintf("%d %ss ago", n, unit)
}
