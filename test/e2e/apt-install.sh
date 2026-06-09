#!/usr/bin/env bash
# End-to-end test: publish a linux/amd64 binary to buildhost, add the generated
# APT repository to a Debian/Ubuntu client, and install the package with apt-get.
#
# Usage: apt-install.sh <buildhost-binary> <artifact-binary>
set -euo pipefail

BUILDHOST_BIN="${1:-${BUILDHOST_BIN:-}}"
ARTIFACT_BIN="${2:-${ARTIFACT_BIN:-}}"
PROJECT="buildhost-apt-e2e"
PORT="${PORT:-8089}"
APTHOST="apt.buildhost.test"
STATICHOST="static.buildhost.test"
APT_BASE="http://${APTHOST}:${PORT}/${PROJECT}"
BASE="http://127.0.0.1:${PORT}"

[ -x "$BUILDHOST_BIN" ] || { echo "buildhost binary not found/executable: '$BUILDHOST_BIN'" >&2; exit 2; }
[ -x "$ARTIFACT_BIN" ] || { echo "artifact binary not found/executable: '$ARTIFACT_BIN'" >&2; exit 2; }
command -v apt-get >/dev/null 2>&1 || { echo "apt-get not found" >&2; exit 2; }
command -v gpg >/dev/null 2>&1 || { echo "gpg not found" >&2; exit 2; }

WORK="$(mktemp -d)"
KEYRING="/etc/apt/keyrings/${PROJECT}.gpg"
SOURCE_LIST="/etc/apt/sources.list.d/${PROJECT}.list"
export BUILDHOST_DATA_DIR="$WORK/data"
export BUILDHOST_DB_PATH="$WORK/data/buildhost.db"
export BUILDHOST_LISTEN_ADDR=":${PORT}"
export BUILDHOST_ADMIN_LISTEN_ADDR=""
SERVER_PID=""
cleanup() {
	[ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
	sudo rm -f "$SOURCE_LIST" "$KEYRING"
	sudo apt-get remove -y "$PROJECT" >/dev/null 2>&1 || true
	rm -rf "$WORK"
}
trap cleanup EXIT

echo "== bootstrap admin token =="
TOKEN="$("$BUILDHOST_BIN" bootstrap --name apt-e2e | tail -n1)"
[ -n "$TOKEN" ] || { echo "no token from bootstrap" >&2; exit 1; }

echo "== start buildhost serve =="
"$BUILDHOST_BIN" serve >"$WORK/server.log" 2>&1 &
SERVER_PID=$!
for i in $(seq 1 50); do
	curl -fsS "$BASE/healthz" >/dev/null 2>&1 && break
	kill -0 "$SERVER_PID" 2>/dev/null || { echo "server exited early:" >&2; cat "$WORK/server.log" >&2; exit 1; }
	[ "$i" = 50 ] && { echo "server did not become healthy:" >&2; cat "$WORK/server.log" >&2; exit 1; }
	sleep 0.2
done

auth=(-H "Authorization: Bearer $TOKEN")
echo "== create public project '$PROJECT' =="
curl -fsS "${auth[@]}" -H "Content-Type: application/json" \
	-d "{\"name\":\"$PROJECT\",\"versioning\":\"auto\",\"is_private\":false,\"description\":\"APT endpoint e2e package\"}" \
	"$BASE/api/v1/projects" >/dev/null

echo "== create release =="
VERSION="$(curl -fsS "${auth[@]}" -H "Content-Type: application/json" -d '{}' \
	"$BASE/api/v1/projects/$PROJECT/releases" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')"
[ -n "$VERSION" ] || { echo "could not determine release version" >&2; exit 1; }
echo "   version=$VERSION"

echo "== upload linux/amd64 binary =="
curl -fsS "${auth[@]}" -X PUT --data-binary "@$ARTIFACT_BIN" \
	"$BASE/api/v1/projects/$PROJECT/releases/$VERSION/artifacts/linux/amd64?kind=binary" >/dev/null

echo "== publish release =="
curl -fsS "${auth[@]}" -X POST "$BASE/api/v1/projects/$PROJECT/releases/$VERSION/publish" >/dev/null

# The apt repository and package download redirect are host-routed.
for host in "$APTHOST" "$STATICHOST"; do
	grep -Eq "[[:space:]]${host}([[:space:]]|$)" /etc/hosts || echo "127.0.0.1 $host" | sudo tee -a /etc/hosts >/dev/null
done

echo "== configure apt source =="
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL "$APT_BASE/key.asc" | gpg --batch --yes --dearmor | sudo tee "$KEYRING" >/dev/null
echo "deb [signed-by=$KEYRING] $APT_BASE stable main" | sudo tee "$SOURCE_LIST" >/dev/null

echo "== apt update =="
sudo apt-get update

echo "== apt install $PROJECT =="
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y "$PROJECT"

echo "== verify installed package =="
dpkg-query -W -f='${Status} ${Version}\n' "$PROJECT" | grep -q "install ok installed $VERSION"
[ -x "/usr/bin/$PROJECT" ] || { echo "installed binary missing or not executable" >&2; exit 1; }

echo "== E2E OK: apt endpoint installed $PROJECT $VERSION =="
