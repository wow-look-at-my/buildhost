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
# Adds the signed APT repository for this project and refreshes the package
# index. Re-running is safe; it overwrites the existing entry.
#
# Public project:
#   curl -fsSL __APT_URL__/install.sh | sudo sh
#
# Private project (needs a read token):
#   curl -fsSL -H "Authorization: Bearer $TOKEN" __APT_URL__/install.sh \
#     | sudo BUILDHOST_TOKEN=$TOKEN sh
#
set -eu

PROJECT='__PROJECT__'
PKG='__PKG__'
APT_URL='__APT_URL__'
STATIC_HOST='__STATIC_HOST__'
KEYRING='/etc/apt/keyrings/__SLUG__.asc'
LIST='/etc/apt/sources.list.d/__SLUG__.list'
AUTH='/etc/apt/auth.conf.d/__SLUG__.conf'
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

if [ -n "$TOKEN" ]; then
    curl -fsSL -H "Authorization: Bearer $TOKEN" "$APT_URL/key.asc" -o "$KEYRING"
else
    curl -fsSL "$APT_URL/key.asc" -o "$KEYRING"
fi
chmod 0644 "$KEYRING"

echo "deb [signed-by=$KEYRING] $APT_URL stable main" > "$LIST"

# Private repositories: persist the token so apt-get can authenticate. The
# first machine line is scoped to this project's path so the token is never
# sent to other repositories on the same host; the second covers the static
# host, because the .deb pool download redirects there.
if [ -n "$TOKEN" ]; then
    mkdir -p /etc/apt/auth.conf.d
    {
        echo "machine ${APT_URL#*://}"
        echo "login token"
        echo "password $TOKEN"
        echo "machine $STATIC_HOST"
        echo "login token"
        echo "password $TOKEN"
    } > "$AUTH"
    chmod 0600 "$AUTH"
fi

# Each buildhost project this package depends on is its own APT repository.
# add_dep adds one the same way as above, so apt-get can resolve the Depends.
add_dep() {
    dep_name="$1"
    dep_url="$2"
    dep_slug="$3"
    dep_private="$4"
    if [ "$dep_private" = 1 ] && [ -z "$TOKEN" ]; then
        echo "buildhost: dependency '$dep_name' is a private project; pass a read token in BUILDHOST_TOKEN." >&2
        exit 1
    fi
    dep_keyring="/etc/apt/keyrings/$dep_slug.asc"
    if [ -n "$TOKEN" ]; then
        curl -fsSL -H "Authorization: Bearer $TOKEN" "$dep_url/key.asc" -o "$dep_keyring"
    else
        curl -fsSL "$dep_url/key.asc" -o "$dep_keyring"
    fi
    chmod 0644 "$dep_keyring"
    echo "deb [signed-by=$dep_keyring] $dep_url stable main" > "/etc/apt/sources.list.d/$dep_slug.list"
    if [ -n "$TOKEN" ]; then
        mkdir -p /etc/apt/auth.conf.d
        {
            echo "machine ${dep_url#*://}"
            echo "login token"
            echo "password $TOKEN"
            echo "machine $STATIC_HOST"
            echo "login token"
            echo "password $TOKEN"
        } > "/etc/apt/auth.conf.d/$dep_slug.conf"
        chmod 0600 "/etc/apt/auth.conf.d/$dep_slug.conf"
    fi
}
__DEPENDS__
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
		fmt.Fprintf(&depLines, "add_dep '%s' '%s/%s' '%s' %d\n", dep.Name, base, dep.Name, installSlug(dep.Name), private)
	}

	script := strings.NewReplacer(
		"__PROJECT__", project.Name,
		"__PKG__", repackage.DebPackageName(project.Name),
		"__APT_URL__", base+"/"+project.Name,
		"__STATIC_HOST__", auth.DeriveServiceURL(r, "static").Host,
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
