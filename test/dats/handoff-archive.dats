# The hand-off and cache archive on the host it runs on. CI runs this file on Linux, macOS and Windows,
# because the archive is what each host packs and extracts with its own filesystem.
tests:
	- desc: handoff packs a tree with its modes, mtimes, symlinks and hardlinks, resumes a broken receive, and the cache restores the scope that matched
	  cmd: node test/actions/handoff.test.ts
	  outputs:
		stdout:
			- "handoff: all checks passed"
