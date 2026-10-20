# What `apt-get install` gets from a generated APT repository. The cases are a
# plain project, a slash-namespaced one whose Debian package name folds '/' to
# '-', and an APE. The APE's package must ship a launcher instead of a bare
# root-owned binary.
#
# Setup publishes and installs; the tests only ask questions about what landed.
# The apt client is a container the workflow builds and starts (APTBOX). The
# runner's own apt state is never touched and there is nothing to undo. It runs
# on the default bridge and reaches the server through host-gateway.
#
# $BUILDHOST_BIN, $ARTIFACT_BIN and $APTBOX come from the workflow.
#
# see docs/formats/apt.md, docs/project-names.md

shared:
	files:
		start.sh: |
			# Publish projects and install each of them through apt.
			set -eu
			WORK="$(dirname "$ENV_FILE")"
			BASE="http://127.0.0.1"
			KEYRING="/etc/apt/keyrings/buildhost-apt-e2e.gpg"
			test -x "$BUILDHOST_BIN" || { echo "not executable: $BUILDHOST_BIN" >&2; exit 1; }
			test -x "$ARTIFACT_BIN" || { echo "not executable: $ARTIFACT_BIN" >&2; exit 1; }
			test -n "${APTBOX:-}" || { echo "APTBOX is unset: the workflow must start the apt container" >&2; exit 1; }
			docker inspect "$APTBOX" >/dev/null 2>&1 \
				|| { echo "the apt container $APTBOX is not running" >&2; exit 1; }
			# box runs a command as root inside that container.
			box() { docker exec "$APTBOX" "$@"; }
			# --resolve is the documented override, and this is the address
			# to give it.
			GW="$(box getent hosts apt.localhost | awk '{print $1}')"
			test -n "$GW" || { echo "apt.localhost does not resolve inside $APTBOX" >&2; exit 1; }
			# An APE cannot be exec'd: its header is a shell script, and
			# no APE binfmt handler is registered here.
			if head -c 2 "$BUILDHOST_BIN" | grep -q MZ; then RUN="sh $BUILDHOST_BIN"; else RUN="$BUILDHOST_BIN"; fi
			BUILDHOST_DATA_DIR="$WORK/data"; export BUILDHOST_DATA_DIR
			BUILDHOST_DB_PATH="$WORK/data/buildhost.db"; export BUILDHOST_DB_PATH
			BUILDHOST_LISTEN_ADDR=":80"; export BUILDHOST_LISTEN_ADDR
			BUILDHOST_ADMIN_LISTEN_ADDR=""; export BUILDHOST_ADMIN_LISTEN_ADDR
			# Required. This suite addresses the server by several Hosts, so none is canonical: "*" is the explicit opt-in.
			BUILDHOST_PRIMARY_DOMAIN="*"; export BUILDHOST_PRIMARY_DOMAIN
			TOKEN="$($RUN bootstrap --name apt-e2e | tail -n1)"
			test -n "$TOKEN" || { echo "no token from bootstrap" >&2; exit 1; }
			# setsid keeps the server in its own process group so teardown
			# takes the whole tree down. An APE run as root registers its
			# loader for every APE on the host. APE_NOBINFMT stops that, or
			# the loader takes the shell-script fixture below.
			setsid sudo env \
				APE_NOBINFMT=1 \
				BUILDHOST_DATA_DIR="$BUILDHOST_DATA_DIR" \
				BUILDHOST_DB_PATH="$BUILDHOST_DB_PATH" \
				BUILDHOST_LISTEN_ADDR="$BUILDHOST_LISTEN_ADDR" \
				BUILDHOST_ADMIN_LISTEN_ADDR="$BUILDHOST_ADMIN_LISTEN_ADDR" \
				BUILDHOST_PRIMARY_DOMAIN="$BUILDHOST_PRIMARY_DOMAIN" \
				$RUN serve > "$WORK/server.log" 2>&1 &
			echo "$!" > "$WORK/server.pid"
			started=""
			# A short poll reaches the server log that says why it did not start, well before the hook times out.
			for _ in $(seq 50); do
				if curl -fsS "$BASE/healthz" >/dev/null 2>&1; then started=yes; break; fi
				sleep 0.2
			done
			test -n "$started" || { echo "server did not become healthy:" >&2; cat "$WORK/server.log" >&2; exit 1; }
			auth() { curl -fsS -H "Authorization: Bearer $TOKEN" "$@"; }

			# publish <project> <extra-release-json> <payload>: create a public
			# project, a release, upload the binary and publish. The extra JSON
			# is spliced into the release-create body, which is where the
			# declarative publish-path settings go. Prints the version.
			publish() {
				auth -X POST "$BASE/api/v1/projects" -H 'Content-Type: application/json' \
					-d "{\"name\":\"$1\",\"versioning\":\"auto\",\"is_private\":${4:-false},\"description\":\"APT endpoint e2e package\"}" >&2
				body='{"git_branch":"master"'
				if [ -n "$2" ]; then body="$body,$2"; fi
				body="$body}"
				version="$(auth -X POST "$BASE/api/v1/projects/$1/releases" \
					-H 'Content-Type: application/json' -d "$body" | jq -r .version)"
				test -n "$version" || { echo "no version for $1" >&2; exit 1; }
				auth -X PUT --data-binary "@$3" \
					"$BASE/api/v1/projects/$1/releases/$version/artifacts/linux/amd64?kind=binary" >&2
				auth -X POST "$BASE/api/v1/projects/$1/releases/$version/publish" >&2
				echo "$version"
			}

			# install <project> <package>: add the project's repository inside
			# the container and install the package apt derives from it. The
			# update refreshes THIS list only, so installs do not cost round
			# trips to the Ubuntu archive.
			install() {
				apt_base="http://apt.localhost/$1"
				box sh -c "install -d -m 0755 /etc/apt/keyrings \
					&& curl -fsSL --resolve 'apt.localhost:80:$GW' '$apt_base/key.asc' | gpg --batch --yes --dearmor > '$KEYRING' \
					&& echo 'deb [signed-by=$KEYRING] $apt_base stable main' > '/etc/apt/sources.list.d/$2.list' \
					&& apt-get update \
						-o Dir::Etc::sourcelist='sources.list.d/$2.list' \
						-o Dir::Etc::sourceparts='-' \
						-o APT::Get::List-Cleanup='0' \
					&& DEBIAN_FRONTEND=noninteractive apt-get install -y '$2'"
			}

			# The fixture is APE-SHAPED on purpose: no shebang, and it writes
			# to itself before printing its marker. A normal binary cannot
			# exercise this at all.
			{
				printf '%s\n' "MZqFpD='APE-shaped fixture: rewrites itself on first run'"
				printf '%s\n' ': >> "$0" || { echo "self-write failed" >&2; exit 1; }'
				printf '%s\n' 'echo buildhost-apt-ape-ok'
			} > "$WORK/ape-fixture"
			chmod +x "$WORK/ape-fixture"

			V1="$(publish buildhost-apt-e2e '"create_service":true' "$ARTIFACT_BIN")"
			install buildhost-apt-e2e buildhost-apt-e2e
			V2="$(publish buildhost-apt-e2e/tool '' "$ARTIFACT_BIN")"
			install buildhost-apt-e2e/tool buildhost-apt-e2e-tool
			V3="$(publish buildhost-apt-e2e-ape '' "$WORK/ape-fixture")"
			install buildhost-apt-e2e-ape buildhost-apt-e2e-ape
			# The app depends on another buildhost project. Its own install.sh must add the
			# dependency's repository, so one apt-get install pulls both in. curl maps every
			# *.localhost name to loopback, so a curlrc sends the script's fetches to the server.
			publish buildhost-apt-e2e-dep '' "$ARTIFACT_BIN" >/dev/null
			publish buildhost-apt-e2e-app '"depends":["buildhost-apt-e2e-dep"]' "$ARTIFACT_BIN" >/dev/null
			box sh -c "echo 'resolve = apt.localhost:80:$GW' > /root/.curlrc \
				&& curl -fsSL http://apt.localhost/buildhost-apt-e2e-app/install.sh | sh \
				&& DEBIAN_FRONTEND=noninteractive apt-get install -y buildhost-apt-e2e-app"

			# A public app that depends on a PRIVATE project. Without a token, and with a bad
			# one, its install.sh must fail. With a read token it must install both packages.
			# The tests read each attempt's exit status and output from $WORK.
			printf '%s\n' '#!/bin/sh' 'echo buildhost-apt-private-dep-ok' > "$WORK/private-dep"
			printf '%s\n' '#!/bin/sh' 'echo buildhost-apt-private-app-ok' > "$WORK/private-app"
			chmod +x "$WORK/private-dep" "$WORK/private-app"
			publish buildhost-apt-e2e-secret '' "$WORK/private-dep" true >/dev/null
			publish buildhost-apt-e2e-pubapp '"depends":["buildhost-apt-e2e-secret"]' "$WORK/private-app" >/dev/null
			READ_TOKEN="$(auth -X POST "$BASE/api/v1/tokens" -H 'Content-Type: application/json' \
				-d '{"name":"apt-e2e-reader","scopes":"read"}' | jq -r .token)"
			test -n "$READ_TOKEN" || { echo "no read token" >&2; exit 1; }
			# attempt <name> <token>: run the app's install.sh with that token and record the result.
			attempt() {
				if box sh -c "curl -fsSL http://apt.localhost/buildhost-apt-e2e-pubapp/install.sh | BUILDHOST_TOKEN='$2' sh" \
					> "$WORK/private-$1.out" 2>&1; then echo 0 > "$WORK/private-$1.rc"; else echo $? > "$WORK/private-$1.rc"; fi
			}
			attempt notoken ''
			attempt badtoken 'bh_not_a_real_token'
			for f in buildhost-buildhost-apt-e2e-secret.list buildhost-private.list; do
				box test ! -e "/etc/apt/sources.list.d/$f" \
					|| { echo "a failed install.sh still wrote $f" >&2; exit 1; }
			done
			# The tokenless attempt added the app's own repository. Remove it, so both packages must come from the one authenticated source.
			box rm -f /etc/apt/sources.list.d/buildhost-buildhost-apt-e2e-pubapp.list
			attempt token "$READ_TOKEN"
			box sh -c "DEBIAN_FRONTEND=noninteractive apt-get install -y buildhost-apt-e2e-pubapp" > "$WORK/private-install.out" 2>&1 \
				|| { cat "$WORK/private-token.out" "$WORK/private-install.out" >&2; exit 1; }
			{
				echo "WORK='$WORK'"
				echo "APTBOX='$APTBOX'"
				echo "V1=$V1"
				echo "V2=$V2"
				echo "V3=$V3"
			} > "$ENV_FILE"

		teardown.sh: |
			set -eu
			. "$1"
			kill -- "-$(cat "$WORK/server.pid")" 2>/dev/null || true
			# The container is the workflow's to remove. Nothing installed here
			# touched the runner, so there is no apt state to undo.
			true

# The dependency case runs the generated install.sh, whose apt-get update also reads the Ubuntu archive.
setup:
	- cmd: env ENV_FILE={shared.env} sh {shared.start.sh}
	  timeout: 5m
teardown: sh {shared.teardown.sh} {shared.env}

tests:
	- desc: the plain package installs at the published version and is executable
	  cmd: |
		set -eu
		. {shared.env}
		docker exec "$APTBOX" sh -c "dpkg-query -W -f='\${Status} \${Version}\n' buildhost-apt-e2e | grep -q 'install ok installed $V1'"
		docker exec "$APTBOX" test -x /usr/bin/buildhost-apt-e2e
		echo "plain-installed"
	  outputs:
		stdout:
			- "plain-installed"

	# create_service was declared on release-create. The deb must ship the
	# systemd USER unit and postinst must have enabled it globally. The
	# symlink is what makes it start at every user's next graphical login.
	- desc: apt-get install of a package installs the buildhost project it depends on
	  cmd: |
		set -eu
		. {shared.env}
		docker exec "$APTBOX" test -f /etc/apt/sources.list.d/buildhost-buildhost-apt-e2e-dep.list
		docker exec "$APTBOX" sh -c "dpkg-query -W -f='\${Depends}\n' buildhost-apt-e2e-app | grep -qx buildhost-apt-e2e-dep"
		docker exec "$APTBOX" sh -c "dpkg-query -W -f='\${Status}\n' buildhost-apt-e2e-dep | grep -q 'install ok installed'"
		docker exec "$APTBOX" test -x /usr/bin/buildhost-apt-e2e-dep
		echo "dependency-installed"
	  outputs:
		stdout:
			- "dependency-installed"

	- desc: install.sh with no token stops at a private dependency and names it
	  cmd: |
		set -eu
		. {shared.env}
		cat "$WORK/private-notoken.out"
		test "$(cat "$WORK/private-notoken.rc")" = 1
		grep -q "dependency 'buildhost-apt-e2e-secret' is a private project" "$WORK/private-notoken.out"
		echo "refused-without-token"
	  outputs:
		stdout:
			- "refused-without-token"

	- desc: install.sh with a wrong token fails
	  cmd: |
		set -eu
		. {shared.env}
		cat "$WORK/private-badtoken.out"
		test "$(cat "$WORK/private-badtoken.rc")" != 0
		echo "refused-with-bad-token"
	  outputs:
		stdout:
			- "refused-with-bad-token"

	- desc: with a read token, apt-get install of a public app installs its private dependency
	  cmd: |
		set -eu
		. {shared.env}
		test "$(cat "$WORK/private-token.rc")" = 0
		docker exec "$APTBOX" grep -qx 'deb \[signed-by=/etc/apt/keyrings/buildhost-private.asc\] http://apt.localhost/private stable main' /etc/apt/sources.list.d/buildhost-private.list
		docker exec "$APTBOX" grep -qx 'machine http://apt.localhost/private' /etc/apt/auth.conf.d/buildhost-private.conf
		docker exec "$APTBOX" test ! -e /etc/apt/sources.list.d/buildhost-buildhost-apt-e2e-secret.list
		for pkg in buildhost-apt-e2e-pubapp buildhost-apt-e2e-secret; do
			docker exec "$APTBOX" apt-cache policy "$pkg" | grep -q 'http://apt.localhost/private stable/main' \
				|| { echo "$pkg does not come from the authenticated source" >&2; docker exec "$APTBOX" apt-cache policy "$pkg" >&2; exit 1; }
		done
		docker exec "$APTBOX" sh -c "dpkg-query -W -f='\${Depends}\n' buildhost-apt-e2e-pubapp | grep -qx buildhost-apt-e2e-secret"
		docker exec "$APTBOX" sh -c "dpkg-query -W -f='\${Status}\n' buildhost-apt-e2e-secret | grep -q 'install ok installed'"
		docker exec "$APTBOX" buildhost-apt-e2e-secret
		docker exec "$APTBOX" buildhost-apt-e2e-pubapp
	  outputs:
		stdout:
			- "buildhost-apt-private-dep-ok"
			- "buildhost-apt-private-app-ok"

	- desc: create_service ships a user unit and enables it globally
	  cmd: |
		set -eu
		. {shared.env}
		unit=/usr/lib/systemd/user/buildhost-apt-e2e.service
		docker exec "$APTBOX" test -f "$unit" || { echo "missing $unit" >&2; exit 1; }
		docker exec "$APTBOX" grep -q '^ExecStart=/usr/bin/buildhost-apt-e2e$' "$unit"
		docker exec "$APTBOX" grep -q '^Restart=on-failure$' "$unit"
		docker exec "$APTBOX" grep -q '^WantedBy=graphical-session.target$' "$unit"
		docker exec "$APTBOX" test -L /etc/systemd/user/graphical-session.target.wants/buildhost-apt-e2e.service \
			|| { echo "postinst did not systemctl --global enable the unit" >&2; exit 1; }
		echo "unit-enabled"
	  outputs:
		stdout:
			- "unit-enabled"

	# dpkg rejects a '/' in a package name, so the fold is what makes apt
	# usable for a namespaced project at all.
	- desc: a slash-namespaced project installs under its folded package name
	  cmd: |
		set -eu
		. {shared.env}
		docker exec "$APTBOX" sh -c "dpkg-query -W -f='\${Status} \${Version}\n' buildhost-apt-e2e-tool | grep -q 'install ok installed $V2'"
		docker exec "$APTBOX" test -x /usr/bin/buildhost-apt-e2e-tool
		echo "folded-installed"
	  outputs:
		stdout:
			- "folded-installed"

	- desc: a project without create_service ships no unit and enables none
	  cmd: |
		set -eu
		. {shared.env}
		docker exec "$APTBOX" test ! -e /usr/lib/systemd/user/buildhost-apt-e2e-tool.service \
			|| { echo "flag-off project shipped a unit" >&2; exit 1; }
		docker exec "$APTBOX" test ! -e /etc/systemd/user/graphical-session.target.wants/buildhost-apt-e2e-tool.service \
			|| { echo "flag-off project enabled a unit" >&2; exit 1; }
		echo "no-unit"
	  outputs:
		stdout:
			- "no-unit"

	# A bare /usr/bin install is unrunnable for every user except root
	# ("cannot create /usr/bin/<pkg>: Permission denied"), which is what
	# `apt install go-toolchain` produced.
	- desc: an APE package installs under /usr/lib behind a launcher
	  cmd: |
		set -eu
		. {shared.env}
		docker exec "$APTBOX" test -f /usr/lib/buildhost-apt-e2e-ape/buildhost-apt-e2e-ape \
			|| { echo "the APE binary should install under /usr/lib" >&2; exit 1; }
		docker exec "$APTBOX" sh -c "head -n1 /usr/bin/buildhost-apt-e2e-ape | grep -q '^#!/bin/sh$'" \
			|| { echo "/usr/bin/buildhost-apt-e2e-ape is not the generated launcher" >&2; exit 1; }
		echo "launcher-installed"
	  outputs:
		stdout:
			- "launcher-installed"

	# aptuser is the image's non-root account: the user who cannot write
	# /usr/bin, which is the case that was broken. Root cannot exercise it.
	- desc: the apt-installed APE runs as a non-root user from a writable copy
	  cmd: |
		set -eu
		. {shared.env}
		docker exec -u aptuser -e HOME=/home/aptuser "$APTBOX" sh -c "
			set -eu
			test \"\$(id -u)\" != '0' || { echo 'expected a non-root user -- the case that was broken' >&2; exit 1; }
			/usr/bin/buildhost-apt-e2e-ape
			copy=\"\${XDG_CACHE_HOME:-\$HOME/.cache}/buildhost/buildhost-apt-e2e-ape/$V3/buildhost-apt-e2e-ape\"
			test -w \"\$copy\" || { echo \"expected a writable per-user copy at \$copy\" >&2; exit 1; }
			echo per-user-copy-ok"
	  outputs:
		stdout:
			- "buildhost-apt-ape-ok"
			- "per-user-copy-ok"
