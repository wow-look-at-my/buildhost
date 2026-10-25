# Every GitHub REST call goes through github-state-mirror: auth.GitHubAPI in Go,
# githubAPI(g) in the action libs. A direct call spends the org's quota and skips the cache.

tests:
	- desc: no shipped Go file calls api.github.com directly
	  cmd: |
		set -eu
		if grep -rn --include='*.go' --exclude='*_test.go' 'https://api\.github\.com' cmd internal; then
			echo "call auth.GitHubAPI instead"
			exit 1
		fi
		echo "github-via-mirror"
	  outputs:
		stdout:
			- "github-via-mirror"

	- desc: an action lib uses the injected octokit only to hand its token to githubAPI
	  cmd: |
		set -eu
		bad=0
		if grep -n 'api\.github\.com' .github/actions/lib/*.ts; then bad=1; fi
		if grep -n 'g\.octokit\b' .github/actions/lib/*.ts | grep -v 'g\.octokit\.auth()'; then bad=1; fi
		if grep -nE '\{[^}]*\boctokit\b[^}]*\} = g\b' .github/actions/lib/*.ts; then bad=1; fi
		if [ "$bad" = 1 ]; then
			echo "build the client with githubAPI(g)"
			exit 1
		fi
		echo "actions-via-mirror"
	  outputs:
		stdout:
			- "actions-via-mirror"
