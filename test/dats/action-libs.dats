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
