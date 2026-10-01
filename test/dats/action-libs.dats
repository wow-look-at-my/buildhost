# The shared modules the publish composites import. Nothing else reaches this
# code: upload-artifact-action-e2e publishes to http://localhost:18080, where
# the unreachable-registry skip returns before a record is posted. A mistake
# here fails publishing for every repo in the org at once.
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

	- desc: upload sends a body past the advertised direct limit through a session
	  cmd: node test/actions/upload-chunked.test.ts
	  outputs:
		stdout:
			- "upload: all checks passed"

	- desc: download aborts a transfer that goes silent, and finishes a slow one that keeps sending
	  cmd: node test/actions/download-stall.test.ts
	  outputs:
		stdout:
			- "download: all checks passed"

	- desc: download finds its lib from a container job, where the action path names the host
	  cmd: node test/actions/download-action-path.test.ts
	  outputs:
		stdout:
			- "download-action-path: all checks passed"
