#!/usr/bin/env bash
# End-to-end test: publish linux/amd64 binaries to buildhost, add the generated
# APT repositories to a Debian/Ubuntu client, and install the packages with
# apt-get. Covers both a plain single-segment project and a slash-namespaced
# project, whose Debian package name folds '/' to '-' (dpkg rejects a '/' in a
# package name, so this is what makes apt usable for namespaced projects).
#
# Usage: apt-install.sh <buildhost-binary> <artifact-binary>
set -euo pipefail

BUILDHOST_BIN="${1:-${BUILDHOST_BIN:-}}"
ARTIFACT_BIN="${2:-${ARTIFACT_BIN:-}}"
PORT="${PORT:-80}"
APTHOST="apt.localhost"
STATICHOST="static.localhost"
BASE="http://127.0.0.1"
KEYRING="/etc/apt/keyrings/buildhost-apt-e2e.gpg"

[ -x "$BUILDHOST_BIN" ] || { echo "buildhost binary not found/executable: '$BUILDHOST_BIN'" >&2; exit 2; }
[ -x "$ARTIFACT_BIN" ] || { echo "artifact binary not found/executable: '$ARTIFACT_BIN'" >&2; exit 2; }
command -v apt-get >/dev/null 2>&1 || { echo "apt-get not found" >&2; exit 2; }
command -v gpg >/dev/null 2>&1 || { echo "gpg not found" >&2; exit 2; }
[ "$PORT" = "80" ] || { echo "apt install e2e requires PORT=80 because buildhost derives sibling service URLs without ports" >&2; exit 2; }

WORK="$(mktemp -d)"
export BUILDHOST_DATA_DIR="$WORK/data"
export BUILDHOST_DB_PATH="$WORK/data/buildhost.db"
export BUILDHOST_LISTEN_ADDR=":${PORT}"
export BUILDHOST_ADMIN_LISTEN_ADDR=""
export BUILDHOST_PRIMARY_DOMAIN="*"   # required; "*" = serve every Host (localhost + apt.localhost here)
SERVER_PID=""
SOURCE_LISTS=()
INSTALLED_PKGS=()
cleanup() {
	[ -n "$SERVER_PID" ] && sudo kill "$SERVER_PID" 2>/dev/null || true
	for f in "${SOURCE_LISTS[@]:-}"; do [ -n "$f" ] && sudo rm -f "$f"; done
	sudo rm -f "$KEYRING"
	for p in "${INSTALLED_PKGS[@]:-}"; do [ -n "$p" ] && sudo apt-get remove -y "$p" >/dev/null 2>&1 || true; done
	sudo rm -rf "$WORK"
}
trap cleanup EXIT

echo "== bootstrap admin token =="
TOKEN="$("$BUILDHOST_BIN" bootstrap --name apt-e2e | tail -n1)"
[ -n "$TOKEN" ] || { echo "no token from bootstrap" >&2; exit 1; }
auth=(-H "Authorization: Bearer $TOKEN")

echo "== start buildhost serve =="
sudo env \
	BUILDHOST_DATA_DIR="$BUILDHOST_DATA_DIR" \
	BUILDHOST_DB_PATH="$BUILDHOST_DB_PATH" \
	BUILDHOST_LISTEN_ADDR="$BUILDHOST_LISTEN_ADDR" \
	BUILDHOST_ADMIN_LISTEN_ADDR="$BUILDHOST_ADMIN_LISTEN_ADDR" \
	BUILDHOST_PRIMARY_DOMAIN="$BUILDHOST_PRIMARY_DOMAIN" \
	"$BUILDHOST_BIN" serve >"$WORK/server.log" 2>&1 &
SERVER_PID=$!
for i in $(seq 1 50); do
	curl -fsS "$BASE/healthz" >/dev/null 2>&1 && break
	kill -0 "$SERVER_PID" 2>/dev/null || { echo "server exited early:" >&2; cat "$WORK/server.log" >&2; exit 1; }
	[ "$i" = 50 ] && { echo "server did not become healthy:" >&2; cat "$WORK/server.log" >&2; exit 1; }
	sleep 0.2
done

# The apt repository and package download redirect are host-routed.
for host in "$APTHOST" "$STATICHOST"; do
	grep -Eq "[[:space:]]${host}([[:space:]]|$)" /etc/hosts || echo "127.0.0.1 $host" | sudo tee -a /etc/hosts >/dev/null
done

# publish_project <project> [extra-release-json]: create a public project, a
# release, upload the linux/amd64 binary, and publish. The optional second
# argument is spliced into the release-create JSON body (e.g.
# '"create_service":true' -- the declarative publish-path settings). Logs go
# to stderr; the resolved version is echoed to stdout for command
# substitution.
publish_project() {
	local project="$1" extra="${2:-}" payload="${3:-$ARTIFACT_BIN}"
	echo "== create public project '$project' ==" >&2
	curl -fsS "${auth[@]}" -H "Content-Type: application/json" \
		-d "{\"name\":\"$project\",\"versioning\":\"auto\",\"is_private\":false,\"description\":\"APT endpoint e2e package\"}" \
		"$BASE/api/v1/projects" >/dev/null
	local body='{"git_branch":"master"'
	[ -n "$extra" ] && body="${body},${extra}"
	body="${body}}"
	local version
	version="$(curl -fsS "${auth[@]}" -H "Content-Type: application/json" -d "$body" \
		"$BASE/api/v1/projects/$project/releases" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')"
	[ -n "$version" ] || { echo "could not determine release version for '$project'" >&2; exit 1; }
	echo "   version=$version" >&2
	curl -fsS "${auth[@]}" -X PUT --data-binary "@$payload" \
		"$BASE/api/v1/projects/$project/releases/$version/artifacts/linux/amd64?kind=binary" >/dev/null
	curl -fsS "${auth[@]}" -X POST "$BASE/api/v1/projects/$project/releases/$version/publish" >/dev/null
	echo "$version"
}

# install_via_apt <project> <package> <version>: add the project's APT repo and
# install <package>, then verify dpkg state and the installed binary. <package>
# is the Debian package name buildhost derives from <project> (slashes -> '-').
install_via_apt() {
	local project="$1" pkg="$2" version="$3"
	local apt_base="http://${APTHOST}/${project}"
	local source_list="/etc/apt/sources.list.d/${pkg}.list"
	SOURCE_LISTS+=("$source_list")
	INSTALLED_PKGS+=("$pkg")

	echo "== configure apt source for '$project' (package '$pkg') =="
	sudo install -d -m 0755 /etc/apt/keyrings
	curl -fsSL "$apt_base/key.asc" | gpg --batch --yes --dearmor | sudo tee "$KEYRING" >/dev/null
	echo "deb [signed-by=$KEYRING] $apt_base stable main" | sudo tee "$source_list" >/dev/null

	echo "== apt update =="
	sudo apt-get update

	echo "== apt install $pkg =="
	sudo DEBIAN_FRONTEND=noninteractive apt-get install -y "$pkg"

	echo "== verify installed package '$pkg' =="
	dpkg-query -W -f='${Status} ${Version}\n' "$pkg" | grep -q "install ok installed $version"
	[ -x "/usr/bin/$pkg" ] || { echo "installed binary /usr/bin/$pkg missing or not executable" >&2; exit 1; }
	echo "== OK: installed '$pkg' $version from project '$project' =="
}

# 1) Plain single-segment project, declaring create_service on release-create
#    (the declarative publish-path flow): the installed deb must ship the
#    systemd USER unit with the documented content.
V1="$(publish_project "buildhost-apt-e2e" '"create_service":true')"
install_via_apt "buildhost-apt-e2e" "buildhost-apt-e2e" "$V1"

echo "== verify create_service systemd user unit landed =="
UNIT="/usr/lib/systemd/user/buildhost-apt-e2e.service"
[ -f "$UNIT" ] || { echo "missing $UNIT (create_service was declared on release-create)" >&2; exit 1; }
grep -q '^ExecStart=/usr/bin/buildhost-apt-e2e$' "$UNIT"
grep -q '^Restart=on-failure$' "$UNIT"
grep -q '^WantedBy=graphical-session.target$' "$UNIT"

echo "== verify postinst auto-enabled the unit (global enable symlink) =="
WANTS="/etc/systemd/user/graphical-session.target.wants/buildhost-apt-e2e.service"
[ -L "$WANTS" ] || { echo "missing enable symlink $WANTS -- postinst did not run systemctl --global enable" >&2; exit 1; }
echo "== OK: unit present (crash-only restart), auto-enabled for every user's next graphical login =="

# 2) Slash-namespaced project, flag off: the Debian package name folds '/'
#    to '-', and the flag-off deb must NOT ship a unit.
V2="$(publish_project "buildhost-apt-e2e/tool")"
install_via_apt "buildhost-apt-e2e/tool" "buildhost-apt-e2e-tool" "$V2"
[ ! -e /usr/lib/systemd/user/buildhost-apt-e2e-tool.service ] || { echo "flag-off project unexpectedly shipped a unit" >&2; exit 1; }
[ ! -e /etc/systemd/user/graphical-session.target.wants/buildhost-apt-e2e-tool.service ] || { echo "flag-off project unexpectedly enabled a unit" >&2; exit 1; }

# 3) A Cosmopolitan APE artifact. An APE rewrites its own file the first time it
#    runs, and dpkg installs binaries root-owned 0755 -- so a plain /usr/bin
#    install is unrunnable for every user except root ("cannot create
#    /usr/bin/<pkg>: Permission denied"), which is what `apt install
#    go-toolchain` produced. The package must instead install the binary under
#    /usr/lib and ship a launcher that keeps a per-user writable copy.
#
#    The fixture is APE-SHAPED on purpose: no shebang, and it writes to itself
#    before printing its marker. A normal binary cannot exercise this at all.
APE_BIN="$WORK/ape-fixture"
{
	printf '%s\n' "MZqFpD='APE-shaped fixture: rewrites itself on first run'"
	printf '%s\n' ': >> "$0" || { echo "self-write failed" >&2; exit 1; }'
	printf '%s\n' 'echo buildhost-apt-ape-ok'
} >"$APE_BIN"
chmod +x "$APE_BIN"

V3="$(publish_project "buildhost-apt-e2e-ape" "" "$APE_BIN")"
install_via_apt "buildhost-apt-e2e-ape" "buildhost-apt-e2e-ape" "$V3"

echo "== verify the APE package ships a launcher, not a bare root-owned binary =="
REAL="/usr/lib/buildhost-apt-e2e-ape/buildhost-apt-e2e-ape"
[ -f "$REAL" ] || { echo "missing $REAL -- the APE binary should install under /usr/lib" >&2; exit 1; }
head -n1 /usr/bin/buildhost-apt-e2e-ape | grep -q '^#!/bin/sh$' || {
	echo "/usr/bin/buildhost-apt-e2e-ape is not the generated launcher" >&2; exit 1; }

echo "== run it as the CURRENT (non-root) user -- the case that was broken =="
id -u | grep -qv '^0$' || { echo "expected to be running as a non-root user" >&2; exit 1; }
OUT="$(/usr/bin/buildhost-apt-e2e-ape)"
[ "$OUT" = "buildhost-apt-ape-ok" ] || { echo "APE launcher output was '$OUT'" >&2; exit 1; }

COPY="${XDG_CACHE_HOME:-$HOME/.cache}/buildhost/buildhost-apt-e2e-ape/$V3/buildhost-apt-e2e-ape"
[ -w "$COPY" ] || { echo "expected a writable per-user copy at $COPY" >&2; exit 1; }
echo "== OK: apt-installed APE runs as a non-root user, via a writable per-user copy =="

echo "== E2E OK: apt endpoint installed plain (with service unit), namespaced (without), and APE (via launcher) packages =="
