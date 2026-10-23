# The shared modules the publish composites import. Nothing else reaches this
# code: upload-artifact-action-e2e publishes to http://localhost:18080, where
# the unreachable-registry skip returns before a record is posted. A mistake
# here fails publishing for every repo in the org at the same time.
#
# The record-shape assertions need octokit fakes, so they live in a node test
# the suite invokes; node runs TypeScript directly.
#
# see docs/artifact-storage-records.md

tests:
	- desc: storage-record derives and posts the right record for every kind
	  cmd: node test/actions/storage-record.test.ts
	  outputs:
		stdout:
			- "storage-record: all checks passed"

	- desc: preview-comment posts one sticky comment per site, and fails loudly when refused
	  cmd: node test/actions/preview-comment.test.ts
	  outputs:
		stdout:
			- "preview-comment: all checks passed"

	- desc: oidc retries a transient token failure and fails at once on a refusal
	  cmd: node test/actions/oidc.test.ts
	  outputs:
		stdout:
			- "oidc: all checks passed"

	- desc: upload sends a body past the advertised direct limit through a session
	  cmd: node test/actions/upload-chunked.test.ts
	  outputs:
		stdout:
			- "upload: all checks passed"

	- desc: an already published release is a no-op for the same bytes, and a failure naming each slot for other bytes
	  cmd: node test/actions/existing-release.test.ts
	  outputs:
		stdout:
			- "existing-release: all checks passed"

	- desc: artifact-groups finds what a publish uploads and the project each file goes to, and fails on a bad manifest
	  cmd: node test/actions/artifact-groups.test.ts
	  outputs:
		stdout:
			- "artifact-groups: all checks passed"

	- desc: publish-group sends each body once, registers the rest by hash, and stops before the publish on a failure
	  cmd: node test/actions/publish-group.test.ts
	  outputs:
		stdout:
			- "publish-group: all checks passed"
	- desc: the publish helpers parse the manifest, map names, read files and retry the API
	  cmd: node test/actions/publish-helpers.test.ts
	  outputs:
		stdout:
			- "publish-helpers: all checks passed"
	- desc: publish reads the portable manifest, hashes a file, and retries a transient API answer
	  cmd: node test/actions/publish-libs.test.ts
	  outputs:
		stdout:
			- "publish-libs: all checks passed"

	- desc: the publish step creates a release per project, uploads each platform and publishes, and fails on a bad input or a refused release
	  cmd: node test/actions/publish.test.ts
	  outputs:
		stdout:
			- "publish: all checks passed"

	- desc: the single-request actions send the right request with an OIDC or given token, and report what the server answers
	  cmd: node test/actions/resources.test.ts
	  outputs:
		stdout:
			- "resources: all checks passed"

	- desc: a docker publish defaults its tags from the ref, and the push sends every ref with the token and outputs the layout's digest
	  cmd: node test/actions/docker.test.ts
	  outputs:
		stdout:
			- "docker: all checks passed"

	- desc: a site publish uploads to its one-segment branch, directly or in chunks, then records the site and comments its URL
	  cmd: node test/actions/site.test.ts
	  outputs:
		stdout:
			- "site: all checks passed"

	- desc: an artifact upload goes direct or in chunks, re-mints an OIDC token the server refuses mid-upload, and outputs the stored sha256
	  cmd: node test/actions/upload-artifact.test.ts
	  outputs:
		stdout:
			- "upload-artifact: all checks passed"

	- desc: the GitHub Deployment registers for a real server only, and its terminal status follows the publish
	  cmd: node test/actions/deployment.test.ts
	  outputs:
		stdout:
			- "deployment: all checks passed"

	- desc: download aborts a transfer that goes silent, and finishes a slow one that keeps sending
	  cmd: node test/actions/download-stall.test.ts
	  outputs:
		stdout:
			- "download: all checks passed"

	- desc: download asks dl for the runner's platform, writes the file, and reports a miss as an output when it is not required
	  cmd: node test/actions/download.test.ts
	  outputs:
		stdout:
			- "download-action: all checks passed"

	- desc: the portable manifest reads every entry, and fails a manifest it cannot use
	  cmd: node test/actions/portable-manifest.test.ts
	  outputs:
		stdout:
			- "portable-manifest: all checks passed"

	- desc: the publish client retries a transient failure with the bearer token
	  cmd: node test/actions/publish-client.test.ts
	  outputs:
		stdout:
			- "publish-client: all checks passed"

	- desc: no script that holds an expression passes the length GitHub allows one expression, each typescript step passes the typescript action's type-check, and every action lib and its tests pass a strict tsc
	  cmd: node test/actions/action-scripts.test.ts
	  outputs:
		stdout:
			- "action-scripts: all checks passed"
