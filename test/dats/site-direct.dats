# A tiny site fits under the server's advertised direct-upload limit, so the
# publish composite must PUT it whole. Runs between the two site publishes, so
# the only session in the log is still the artifact upload's.
#
# The workflow runs the composite and hands the server log here as $LOG.
#
# see docs/sites.md, docs/uploads.md

tests:
	- desc: a small site publishes with no new upload session
	  cmd: |
		set -eu
		echo "creates=$(grep -c 'method=POST path=/api/v1/uploads ' "$LOG" || true)"
	  outputs:
		stdout:
			- "creates=1"
