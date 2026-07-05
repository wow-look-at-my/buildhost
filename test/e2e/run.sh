#!/usr/bin/env bash
# End-to-end test: publish a binary to buildhost, then load the OCI image buildhost
# synthesizes from it using a real registry client (crane / go-containerregistry) and
# run its entrypoint. Proves the synthesized image is a valid, pullable container image
# AND that the CA bundle baked into it works for real outbound HTTPS -- the whole point
# of the essentials layer.
#
# crane (not docker) is used deliberately: buildhost's layers are zstd-compressed
# (application/vnd.oci.image.layer.v1.tar+zstd), which go-containerregistry pulls and
# decompresses, but GitHub-hosted Docker (without the containerd image store) may not.
# crane is also daemon-free, so the same script runs in CI and locally.
#
# Usage: run.sh <buildhost-binary> <netcheck-binary>
set -euo pipefail

BUILDHOST_BIN="${1:-${BUILDHOST_BIN:-}}"
NETCHECK_BIN="${2:-${NETCHECK_BIN:-}}"
PROJECT="netcheck"
PORT="${PORT:-8088}"
HOST="oci.buildhost.test"
REG="${HOST}:${PORT}"
REF="${REG}/${PROJECT}:latest"
BASE="http://127.0.0.1:${PORT}"

[ -x "$BUILDHOST_BIN" ] || { echo "buildhost binary not found/executable: '$BUILDHOST_BIN'" >&2; exit 2; }
[ -x "$NETCHECK_BIN" ]  || { echo "netcheck binary not found/executable: '$NETCHECK_BIN'" >&2; exit 2; }
command -v crane >/dev/null 2>&1 || { echo "crane not found on PATH" >&2; exit 2; }

WORK="$(mktemp -d)"
export BUILDHOST_DATA_DIR="$WORK/data"
export BUILDHOST_DB_PATH="$WORK/data/buildhost.db"   # DBPath is independent of DataDir
export BUILDHOST_LISTEN_ADDR=":${PORT}"
export BUILDHOST_ADMIN_LISTEN_ADDR=""                # disable admin server (avoid extra port)
SERVER_PID=""
cleanup() { [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true; rm -rf "$WORK"; }
trap cleanup EXIT

echo "== bootstrap admin token =="
TOKEN="$("$BUILDHOST_BIN" bootstrap --name e2e | tail -n1)"
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
	-d "{\"name\":\"$PROJECT\",\"versioning\":\"auto\",\"is_private\":false}" \
	"$BASE/api/v1/projects" >/dev/null

echo "== create release =="
VERSION="$(curl -fsS "${auth[@]}" -H "Content-Type: application/json" -d '{"git_branch":"master"}' \
	"$BASE/api/v1/projects/$PROJECT/releases" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')"
[ -n "$VERSION" ] || { echo "could not determine release version" >&2; exit 1; }
echo "   version=$VERSION"

echo "== upload linux/amd64 binary =="
curl -fsS "${auth[@]}" -X PUT --data-binary "@$NETCHECK_BIN" \
	"$BASE/api/v1/projects/$PROJECT/releases/$VERSION/artifacts/linux/amd64?kind=binary" >/dev/null

echo "== publish release =="
curl -fsS "${auth[@]}" -X POST "$BASE/api/v1/projects/$PROJECT/releases/$VERSION/publish" >/dev/null

# The OCI endpoint is host-routed (first Host label must be "oci"); map it to the server.
grep -q "$HOST" /etc/hosts || echo "127.0.0.1 $HOST" | sudo tee -a /etc/hosts >/dev/null

echo "== inspect synthesized image config (crane config) =="
CONFIG="$(crane config --insecure "$REF")"
echo "$CONFIG"
echo "$CONFIG" | grep -q '"Entrypoint":\["/netcheck"\]' || { echo "FAIL: entrypoint not /netcheck" >&2; exit 1; }
echo "$CONFIG" | grep -q 'SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt' || { echo "FAIL: SSL_CERT_FILE env missing" >&2; exit 1; }
# Two ordered diff_ids: the essentials base layer + the per-binary layer.
[ "$(echo "$CONFIG" | grep -o 'sha256:' | wc -l)" -ge 2 ] || { echo "FAIL: expected >=2 diff_ids" >&2; exit 1; }

echo "== load image: crane export -> flatten rootfs =="
ROOT="$WORK/rootfs"; mkdir -p "$ROOT"
crane export --insecure "$REF" - | tar -x -C "$ROOT"
[ -s "$ROOT/etc/ssl/certs/ca-certificates.crt" ] || { echo "FAIL: CA bundle missing/empty in image" >&2; exit 1; }
[ -f "$ROOT/etc/passwd" ] || { echo "FAIL: /etc/passwd missing in image" >&2; exit 1; }
grep -q '^nonroot:x:65532:65532:' "$ROOT/etc/passwd" || { echo "FAIL: nonroot user missing from /etc/passwd" >&2; exit 1; }
[ -x "$ROOT/netcheck" ] || { echo "FAIL: entrypoint binary missing from image" >&2; exit 1; }

echo "== run the image's entrypoint (HTTPS via the image's CA bundle) =="
SSL_CERT_FILE="$ROOT/etc/ssl/certs/ca-certificates.crt" "$ROOT/netcheck"

echo "== E2E OK: synthesized image loaded and ran; CA bundle validated a live HTTPS request =="
