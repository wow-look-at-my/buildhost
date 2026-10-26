# buildhost-publish-docker hands the build only the secrets the caller names.
# A name with no value fails the publish, because a build without the secret it mounts fails later and further from the cause.

tests:
	- desc: each named secret maps to the variable of the same name, and no value is printed
	  cmd: |
		set -eu
		export PRIVATE_ORG_REPO_READ=hunter2-value OTHER_TOKEN=other-value
		bash .github/actions/buildhost-publish-docker/build-secrets.sh "$(printf 'PRIVATE_ORG_REPO_READ\n\n  OTHER_TOKEN \n')"
	  outputs:
		stdout:
			- "PRIVATE_ORG_REPO_READ=PRIVATE_ORG_REPO_READ"
			- "OTHER_TOKEN=OTHER_TOKEN"
		!stdout:
			- "hunter2-value"
			- "other-value"

	- desc: no names print nothing
	  cmd: bash .github/actions/buildhost-publish-docker/build-secrets.sh ""
	  outputs:
		!stdout:
			- "."

	- desc: a name the environment does not hold fails and names the secret
	  cmd: env -u MISSING_SECRET bash .github/actions/buildhost-publish-docker/build-secrets.sh MISSING_SECRET
	  exit: 1
	  outputs:
		stderr:
			- "no MISSING_SECRET, so the build cannot mount it"

	- desc: a value that is not a secret name is refused
	  cmd: bash .github/actions/buildhost-publish-docker/build-secrets.sh 'A=B'
	  exit: 1
	  outputs:
		stderr:
			- "'A=B' is not a secret name"
