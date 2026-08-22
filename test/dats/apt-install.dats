# What `apt-get install` gets from a generated APT repository: a plain project,
# a slash-namespaced one whose Debian package name folds '/' to '-', and an APE
# whose package must ship a launcher instead of a bare root-owned binary.
#
# Setup publishes and installs; the tests only ask questions about what landed.
# It needs the real apt-get, gpg, sudo and port 80 -- buildhost derives sibling
# service URLs without ports -- so a workflow runs this --no-sandbox.
#
# $BUILDHOST_BIN and $ARTIFACT_BIN come from the workflow.
#
# see docs/formats/apt.md, docs/project-names.md

shared:
	files:
		start.sh: |
			# Publish three projects and install all three through apt.
			set -eu
			WORK="$(dirname "$ENV_FILE")"
			BASE="http://127.0.0.1"
			KEYRING="/etc/apt/keyrings/buildhost-apt-e2e.gpg"
			test -x "$BUILDHOST_BIN" || { echo "not executable: $BUILDHOST_BIN" >&2; exit 1; }
			test -x "$ARTIFACT_BIN" || { echo "not executable: $ARTIFACT_BIN" >&2; exit 1; }
			# An APE cannot be exec'd: its header is a shell script, and
			# nothing here registers an APE binfmt handler.
			if head -c 2 "$BUILDHOST_BIN" | grep -q MZ; then RUN="sh $BUILDHOST_BIN"; else RUN="$BUILDHOST_BIN"; fi
			BUILDHOST_DATA_DIR="$WORK/data"; export BUILDHOST_DATA_DIR
			BUILDHOST_DB_PATH="$WORK/data/buildhost.db"; export BUILDHOST_DB_PATH
			BUILDHOST_LISTEN_ADDR=":80"; export BUILDHOST_LISTEN_ADDR
			BUILDHOST_ADMIN_LISTEN_ADDR=""; export BUILDHOST_ADMIN_LISTEN_ADDR
			TOKEN="$($RUN bootstrap --name apt-e2e | tail -n1)"
			test -n "$TOKEN" || { echo "no token from bootstrap" >&2; exit 1; }
			# Port 80 needs root. setsid keeps the server in its own process
			# group so teardown takes the whole tree down.
			setsid sudo env \
				BUILDHOST_DATA_DIR="$BUILDHOST_DATA_DIR" \
				BUILDHOST_DB_PATH="$BUILDHOST_DB_PATH" \
				BUILDHOST_LISTEN_ADDR="$BUILDHOST_LISTEN_ADDR" \
				BUILDHOST_ADMIN_LISTEN_ADDR="$BUILDHOST_ADMIN_LISTEN_ADDR" \
				$RUN serve > "$WORK/server.log" 2>&1 &
			echo "$!" > "$WORK/server.pid"
			started=""
			for _ in $(seq 50); do
				if curl -fsS "$BASE/healthz" >/dev/null 2>&1; then started=yes; break; fi
				sleep 1
			done
			test -n "$started" || { echo "server did not become healthy:" >&2; cat "$WORK/server.log" >&2; exit 1; }
			auth() { curl -fsS -H "Authorization: Bearer $TOKEN" "$@"; }

			# publish <project> <extra-release-json> <payload>: create a public
			# project, a release, upload the binary and publish. The extra JSON
			# is spliced into the release-create body, which is where the
			# declarative publish-path settings go. Prints the version.
			publish() {
				auth -X POST "$BASE/api/v1/projects" -H 'Content-Type: application/json' \
					-d "{\"name\":\"$1\",\"versioning\":\"auto\",\"is_private\":false,\"description\":\"APT endpoint e2e package\"}" >&2
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

			# install <project> <package>: add the project's repository and
			# install the package apt derives from it.
			install() {
				apt_base="http://apt.localhost/$1"
				sudo install -d -m 0755 /etc/apt/keyrings
				curl -fsSL "$apt_base/key.asc" | gpg --batch --yes --dearmor | sudo tee "$KEYRING" >/dev/null
				echo "deb [signed-by=$KEYRING] $apt_base stable main" \
					| sudo tee "/etc/apt/sources.list.d/$2.list" >/dev/null
				sudo apt-get update
				sudo DEBIAN_FRONTEND=noninteractive apt-get install -y "$2"
			}

			# An APE rewrites its own file the first time it runs, and dpkg
			# installs binaries root-owned 0755. The fixture is APE-SHAPED on
			# purpose: no shebang, and it writes to itself before printing its
			# marker. A normal binary cannot exercise this at all.
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
			{
				echo "WORK='$WORK'"
				echo "V1=$V1"
				echo "V2=$V2"
				echo "V3=$V3"
			} > "$ENV_FILE"

		teardown.sh: |
			set -eu
			. "$1"
			kill -- "-$(cat "$WORK/server.pid")" 2>/dev/null || true
			for p in buildhost-apt-e2e buildhost-apt-e2e-tool buildhost-apt-e2e-ape; do
				sudo apt-get remove -y "$p" >/dev/null 2>&1 || true
				sudo rm -f "/etc/apt/sources.list.d/$p.list"
			done
			sudo rm -f /etc/apt/keyrings/buildhost-apt-e2e.gpg
			true

setup: env ENV_FILE={shared.env} sh {shared.start.sh}
teardown: sh {shared.teardown.sh} {shared.env}

tests:
	- desc: the plain package installs at the published version and is executable
	  cmd: |
		set -eu
		. {shared.env}
		dpkg-query -W -f='${Status} ${Version}\n' buildhost-apt-e2e | grep -q "install ok installed $V1"
		test -x /usr/bin/buildhost-apt-e2e
		echo "plain-installed"
	  outputs:
		stdout:
			- "plain-installed"

	# create_service was declared on release-create, so the deb must ship the
	# systemd USER unit and postinst must have enabled it globally -- the
	# symlink is what makes it start at every user's next graphical login.
	- desc: create_service ships a user unit and enables it globally
	  cmd: |
		set -eu
		unit=/usr/lib/systemd/user/buildhost-apt-e2e.service
		test -f "$unit" || { echo "missing $unit" >&2; exit 1; }
		grep -q '^ExecStart=/usr/bin/buildhost-apt-e2e$' "$unit"
		grep -q '^Restart=on-failure$' "$unit"
		grep -q '^WantedBy=graphical-session.target$' "$unit"
		test -L /etc/systemd/user/graphical-session.target.wants/buildhost-apt-e2e.service \
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
		dpkg-query -W -f='${Status} ${Version}\n' buildhost-apt-e2e-tool | grep -q "install ok installed $V2"
		test -x /usr/bin/buildhost-apt-e2e-tool
		echo "folded-installed"
	  outputs:
		stdout:
			- "folded-installed"

	- desc: a project without create_service ships no unit and enables none
	  cmd: |
		set -eu
		test ! -e /usr/lib/systemd/user/buildhost-apt-e2e-tool.service \
			|| { echo "flag-off project shipped a unit" >&2; exit 1; }
		test ! -e /etc/systemd/user/graphical-session.target.wants/buildhost-apt-e2e-tool.service \
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
		test -f /usr/lib/buildhost-apt-e2e-ape/buildhost-apt-e2e-ape \
			|| { echo "the APE binary should install under /usr/lib" >&2; exit 1; }
		head -n1 /usr/bin/buildhost-apt-e2e-ape | grep -q '^#!/bin/sh$' \
			|| { echo "/usr/bin/buildhost-apt-e2e-ape is not the generated launcher" >&2; exit 1; }
		echo "launcher-installed"
	  outputs:
		stdout:
			- "launcher-installed"

	- desc: the apt-installed APE runs as a non-root user from a writable copy
	  cmd: |
		set -eu
		. {shared.env}
		test "$(id -u)" != "0" || { echo "expected a non-root user -- the case that was broken" >&2; exit 1; }
		/usr/bin/buildhost-apt-e2e-ape
		copy="${XDG_CACHE_HOME:-$HOME/.cache}/buildhost/buildhost-apt-e2e-ape/$V3/buildhost-apt-e2e-ape"
		test -w "$copy" || { echo "expected a writable per-user copy at $copy" >&2; exit 1; }
		echo "per-user-copy-ok"
	  outputs:
		stdout:
			- "buildhost-apt-ape-ok"
			- "per-user-copy-ok"
