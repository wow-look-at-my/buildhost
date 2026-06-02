package apt

import (
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

// installScriptTemplate is a POSIX sh installer that adds this project's signed
// APT repository and refreshes the package index. It is rendered per request so
// every URL is self-referential (derived from the Host the request came in on).
//
// APT reads an ASCII-armored key directly when it is referenced via signed-by,
// so the script does not need gpg/dearmor on the client -- it just saves the
// served key.asc as-is.
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
APT_URL='__APT_URL__'
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
# machine line is scoped to this project's path so the token is never sent
# to other repositories on the same host.
if [ -n "$TOKEN" ]; then
    mkdir -p /etc/apt/auth.conf.d
    {
        echo "machine ${APT_URL#*://}"
        echo "login token"
        echo "password $TOKEN"
    } > "$AUTH"
    chmod 0600 "$AUTH"
fi

apt-get update

echo ""
echo "buildhost: repository for '$PROJECT' added."
echo "Install it with:  sudo apt-get install $PROJECT"
`

// serveInstallScript renders the per-project apt installer. Auth (for private
// projects) has already been enforced by requireProject, so reaching here means
// the caller is allowed to read this project.
func (h *Handler) serveInstallScript(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())

	aptURL := strings.TrimRight(auth.RequestBaseURL(r), "/") + "/" + project.Name
	slug := "buildhost-" + strings.ReplaceAll(project.Name, "/", "-")

	script := strings.NewReplacer(
		"__PROJECT__", project.Name,
		"__APT_URL__", aptURL,
		"__SLUG__", slug,
	).Replace(installScriptTemplate)

	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(script))
}
