# Every GitHub REST call goes through github-state-mirror (auth.GitHubAPI).
# A direct api.github.com call spends the org's quota and skips the cache.

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
