package apt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/go-containers/set"
)

// installScriptTemplate is a POSIX sh installer that adds this project's signed
const installScriptTemplate = `#!/bin/sh
#
# buildhost APT repository installer
#
# Project: __PROJECT__
#
# Adds the signed APT repository for this project and its dependencies, and
# refreshes the package index. With BUILDHOST_TOKEN it adds the one
# authenticated source instead, which lists every project the token can read.
# Re-running is safe; it overwrites the existing entries.
#
# Public project:
#   curl -fsSL __APT_URL__/install.sh | sudo sh
#
# Private project, or a private dependency (needs a read token):
#   curl -fsSL -H "Authorization: Bearer $TOKEN" __APT_URL__/install.sh \
#     | sudo BUILDHOST_TOKEN=$TOKEN sh
#
set -eu

PROJECT='__PROJECT__'
PKG='__PKG__'
APT_URL='__APT_URL__'
PRIVATE_URL='__PRIVATE_URL__'
KEYRING='/etc/apt/keyrings/__SLUG__.asc'
LIST='/etc/apt/sources.list.d/__SLUG__.list'
PRIVATE_KEYRING='/etc/apt/keyrings/buildhost-private.asc'
PRIVATE_LIST='/etc/apt/sources.list.d/buildhost-private.list'
PRIVATE_AUTH='/etc/apt/auth.conf.d/buildhost-private.conf'
TOKEN="${BUILDHOST_TOKEN:-}"

if [ "$(id -u)" -ne 0 ]; then
    echo "buildhost: this installer must run as root (pipe it to 'sudo sh')." >&2
    exit 1
fi

if ! command -v curl >/dev/null 2>&1; then
    echo "buildhost: curl is required but was not found on PATH." >&2
    exit 1
fi

mkdir -p /etc/apt/keyrings

# Each buildhost project this package depends on is its own APT repository.
# add_dep adds one the same way as this project's, so apt-get can resolve the Depends.
add_dep() {
    dep_name="$1"
    dep_url="$2"
    dep_slug="$3"
    dep_private="$4"
    if [ "$dep_private" = 1 ]; then
        echo "buildhost: dependency '$dep_name' is a private project; pass a read token in BUILDHOST_TOKEN." >&2
        exit 1
    fi
    curl -fsSL "$dep_url/key.asc" -o "/etc/apt/keyrings/$dep_slug.asc"
    chmod 0644 "/etc/apt/keyrings/$dep_slug.asc"
    echo "deb [signed-by=/etc/apt/keyrings/$dep_slug.asc] $dep_url stable main" > "/etc/apt/sources.list.d/$dep_slug.list"
}

if [ -n "$TOKEN" ]; then
    # With a token, one authenticated source lists this project, its
    # dependencies and every other project the token can read. Its one auth
    # entry covers every download: a private deb redirects with a signed link.
    # The machine line keeps its scheme, because apt applies a line with no
    # scheme to https only.
    curl -fsSL -H "Authorization: Bearer $TOKEN" "$PRIVATE_URL/key.asc" -o "$PRIVATE_KEYRING"
    chmod 0644 "$PRIVATE_KEYRING"
    echo "deb [signed-by=$PRIVATE_KEYRING] $PRIVATE_URL stable main" > "$PRIVATE_LIST"
    mkdir -p /etc/apt/auth.conf.d
    {
        echo "machine $PRIVATE_URL"
        echo "login token"
        echo "password $TOKEN"
    } > "$PRIVATE_AUTH"
    chmod 0600 "$PRIVATE_AUTH"
else
    curl -fsSL "$APT_URL/key.asc" -o "$KEYRING"
    chmod 0644 "$KEYRING"
    echo "deb [signed-by=$KEYRING] $APT_URL stable main" > "$LIST"
__DEPENDS__
fi

apt-get update

echo ""
echo "buildhost: repository for '$PROJECT' added."
echo "Install it with:  sudo apt-get install $PKG"
`

// serveInstallScript renders the per-project apt installer. Auth (for private
// projects) has already been enforced by requireProject, so reaching here means
// the caller is allowed to read this project.
func (h *Handler) serveInstallScript(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())

	deps, err := h.dependsClosure(r.Context(), project)
	if err != nil {
		slog.Error("apt install.sh depends", "project", project.Name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	base := strings.TrimRight(auth.RequestBaseURL(r), "/")
	var depLines strings.Builder
	for _, dep := range deps {
		private := 0
		if dep.IsPrivate {
			private = 1
		}
		fmt.Fprintf(&depLines, "    add_dep '%s' '%s/%s' '%s' %d\n", dep.Name, base, dep.Name, installSlug(dep.Name), private)
	}

	script := strings.NewReplacer(
		"__PROJECT__", project.Name,
		"__PKG__", repackage.DebPackageName(project.Name),
		"__APT_URL__", base+"/"+project.Name,
		"__PRIVATE_URL__", base+"/private",
		"__SLUG__", installSlug(project.Name),
		"__DEPENDS__", depLines.String(),
	).Replace(installScriptTemplate)

	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(script))
}

// installSlug names the keyring, source list and auth file of a project's repository.
func installSlug(project string) string {
	return "buildhost-" + strings.ReplaceAll(project, "/", "-")
}

// dependsClosure lists every project that project depends on, directly or
// through another dependency, sorted by name. apt resolves a Depends only
// from a configured repository, so install.sh adds the repository of each. A
// stored name that no longer resolves is an error: the install would fail on
// it.
func (h *Handler) dependsClosure(ctx context.Context, project *db.Project) ([]db.Project, error) {
	seen := set.Of[int64](project.ID)
	var deps []db.Project
	queue := project.DependsOn()
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		dep, _, err := h.DB.ResolveProject(ctx, name)
		if errors.Is(err, db.ErrNotFound) {
			return nil, fmt.Errorf("project %s depends on %q, which does not exist", project.Name, name)
		}
		if err != nil {
			return nil, err
		}
		if seen.Contains(dep.ID) {
			continue
		}
		seen.Add(dep.ID)
		deps = append(deps, *dep)
		queue = append(queue, dep.DependsOn()...)
	}
	slices.SortFunc(deps, func(a, b db.Project) int { return strings.Compare(a.Name, b.Name) })
	return deps, nil
}
