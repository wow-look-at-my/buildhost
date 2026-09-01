# What the upload composites and the CLI must have done to a real server: one
# session for the chunked path and none for the direct one, bytes that survive
# the round trip, a release that ends up published, and an advertised site URL
# that serves rather than redirects.
#
# The workflow runs the composites -- an action only runs inside a workflow --
# and hands the results here: LOG is the server's log, BASE and SITES its two
# hosts, and the SHA/VERSION/SITE_URL values are what the steps reported.
#
# This half runs after both site publishes, so the session count covers
# the artifact upload and the chunked site together.
#
# see docs/uploads.md, docs/sites.md

tests:
	- desc: the big site opened its own session, the small one opened none
	  cmd: |
		set -eu
		echo "creates=$(grep -c 'method=POST path=/api/v1/uploads ' "$LOG" || true)"
		# 4 chunks from the artifact upload plus at least 3 for the ~3 MB site.
		chunks="$(grep -c 'method=PATCH path=/api/v1/uploads/' "$LOG" || true)"
		test "$chunks" -ge 7
		echo "chunks-at-least-7"
	  outputs:
		stdout:
			- "creates=2"
			- "chunks-at-least-7"

	# The advertised URL is what lands in preview comments and deployment
	# environment_urls, so it must SERVE, not redirect. Catches the action
	# falling back to a stale spelling, which is green otherwise.
	- desc: the advertised site URL serves the bytes with no redirect
	  cmd: |
		set -eu
		echo "site_url: $SITE_URL"
		case "$SITE_URL" in
			*"/branch/"*) echo "advertised the legacy /branch/ spelling" >&2; exit 1 ;;
		esac
		# No -L: the advertised URL must answer 200 itself.
		curl -fsS "${SITE_URL}index.html"
	  outputs:
		stdout:
			- "<h1>chunked site</h1>"
		!stdout:
			- "/branch/"

	- desc: every served site file matches the source bytes
	  cmd: |
		set -eu
		test "$(curl -fsSL "$SITES/upload-e2e/branch/direct/index.html")" = '<h1>direct site</h1>'
		test "$(curl -fsSL "$SITES/upload-e2e/branch/chunked/index.html")" = '<h1>chunked site</h1>'
		curl -fsSL -o blob.dl "$SITES/upload-e2e/branch/chunked/blob.bin"
		cmp "$BIG_SITE_BLOB" blob.dl
		echo "sites-match"
	  outputs:
		stdout:
			- "sites-match"

	# The legacy /branch/ URL is a 302 to the canonical URL for the same file.
	# Which canonical URL it names depends on which branch is the default, and
	# this project has no site on main/master, so the fallback is whichever of
	# the two was deployed last. Assert the redirect, then follow the target
	# the server actually named rather than hard-coding one of the two forms.
	- desc: the legacy branch URL redirects to the canonical one for the same file
	  cmd: |
		set -eu
		for b in direct chunked; do
			url="$SITES/upload-e2e/branch/$b/index.html"
			test "$(curl -fsS -o /dev/null -w '%{http_code}' "$url")" = '302'
			loc="$(curl -fsS -o /dev/null -w '%{redirect_url}' "$url")"
			case "$loc" in
				"$SITES/upload-e2e/index.html") ;;
				"$SITES/upload-e2e/@$b/index.html") ;;
				*) echo "unexpected redirect target for $b: $loc" >&2; exit 1 ;;
			esac
			test "$(curl -fsS "$loc")" = "<h1>$b site</h1>"
		done
		echo "redirects-canonical"
	  outputs:
		stdout:
			- "redirects-canonical"

	# Single-mode `buildhost publish` (no --manifest) must PUBLISH the release
	# it creates. The CLI package deliberately has no unit tests, so this is
	# where that promise is kept: an unpublished release is invisible to
	# latest/branch/brew/apt/npm/web and eventually swept as an abandoned
	# upload, with no CLI way to publish it later.
	- desc: a single-mode CLI publish leaves the release published
	  cmd: |
		set -eu
		head -c 4096 /dev/urandom > {outputs.cli.bin}
		if head -c 2 "$BIN" | grep -q MZ; then RUN="sh $BIN"; else RUN="$BIN"; fi
		$RUN project create --server "$BASE" --token "$TOKEN" --name cli-single-e2e
		$RUN publish --server "$BASE" --token "$TOKEN" --project cli-single-e2e \
			--os linux --arch amd64 --git-branch master --artifact {outputs.cli.bin}
		curl -fsS "$BASE/api/v1/projects/cli-single-e2e/releases/latest"
	  outputs:
		stdout:
			- '"published":true'
