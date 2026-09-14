# A docker credential here is a GitHub OIDC token, and it lives minutes. One
# large pull outlasts it, so a step that logs in once and then loops answers the
# second reference with a 401 on the manifest HEAD. The login belongs inside the
# loop, immediately before each docker operation.

tests:
	- desc: a loop that pulls or pushes logs in inside the loop
	  cmd: |
		set -eu
		awk '
		FNR == 1 { loop = 0; login = 0 }
		/while IFS=/ { loop = FNR; login = 0 }
		/docker-login\.sh/ { login = FNR }
		/docker (pull|push) / {
			if (loop && login < loop) {
				printf "%s:%d: the loop pulls, but its login is outside it\n", FILENAME, FNR
				bad = 1
			}
		}
		END {
			if (bad) exit 1
			print "login-per-operation"
		}
		' .github/actions/*/action.yml
	  outputs:
		stdout:
			- "login-per-operation"
