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

# publish_project <project>: create a public project, a release, upload the
# linux/amd64 binary, and publish. Logs go to stderr; the resolved version is
# echoed to stdout for command substitution.
publish_project() {
	local project="$1"
	echo "== create public project '$project' ==" >&2
	curl -fsS "${auth[@]}" -H "Content-Type: application/json" \
		-d "{\"name\":\"$project\",\"versioning\":\"auto\",\"is_private\":false,\"description\":\"APT endpoint e2e package\"}" \
		"$BASE/api/v1/projects" >/dev/null
	local version
	version="$(curl -fsS "${auth[@]}" -H "Content-Type: application/json" -d '{"git_branch":"master"}' \
		"$BASE/api/v1/projects/$project/releases" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')"
	[ -n "$version" ] || { echo "could not determine release version for '$project'" >&2; exit 1; }
	echo "   version=$version" >&2
	curl -fsS "${auth[@]}" -X PUT --data-binary "@$ARTIFACT_BIN" \
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

# 1) Plain single-segment project: package name == project name.
V1="$(publish_project "buildhost-apt-e2e")"
install_via_apt "buildhost-apt-e2e" "buildhost-apt-e2e" "$V1"

# 2) Slash-namespaced project: the Debian package name folds '/' to '-'.
V2="$(publish_project "buildhost-apt-e2e/tool")"
install_via_apt "buildhost-apt-e2e/tool" "buildhost-apt-e2e-tool" "$V2"

echo "== E2E OK: apt endpoint installed plain and namespaced packages =="
