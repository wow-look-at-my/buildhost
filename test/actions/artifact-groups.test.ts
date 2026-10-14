// Behavior tests for .github/actions/lib/artifact-groups.ts.

const assert = require('node:assert');
const lib = `${process.env.GITHUB_WORKSPACE ?? process.cwd()}/.github/actions/lib/artifact-groups`;
// Untyped on purpose: the module's types are checked where it is CALLED, in the publish composite.
const { findArtifacts, projectForBinary } = require(`${lib}.ts`);

const dir = (files: string[], manifest: unknown = null, present: string[] = files) => ({
	files,
	manifest: manifest === null ? null : typeof manifest === 'string' ? manifest : JSON.stringify(manifest),
	exists: (f: string) => present.includes(f),
});

// A binary named after the repo publishes flat, a "<repo>-" prefix is dropped, and any other binary nests.
assert.equal(projectForBinary('tool', 'tool'), 'tool');
assert.equal(projectForBinary('tool', 'tool-client'), 'tool/client');
assert.equal(projectForBinary('tool', 'other'), 'tool/other');

// Per-platform files group by project. Checksums, zips and names without the suffix stay out.
{
	const found = findArtifacts(dir(['tool_linux_amd64', 'tool_windows_amd64.exe', 'tool-client_darwin_arm64', 'checksums.txt', 'x.zip', 'README']), 'tool');
	assert.ok(found.ok);
	assert.deepEqual([...found.groups.keys()].sort(), ['tool', 'tool/client']);
	assert.deepEqual(found.groups.get('tool').slots, [
		{ file: 'tool_linux_amd64', os: 'linux', arch: 'amd64' },
		{ file: 'tool_windows_amd64.exe', os: 'windows', arch: 'amd64' },
	]);
}

// A manifest entry is one portable artifact, and its file does not also publish as a per-platform slot.
{
	const manifest = { schema: 1, artifacts: [{ file: 'tool_cosmo_any', platforms: ['linux/amd64', 'darwin/arm64'] }] };
	const found = findArtifacts(dir(['tool_cosmo_any', 'buildhost-artifacts.json'], manifest), 'tool');
	assert.ok(found.ok);
	assert.equal(found.files.length, 0);
	assert.deepEqual(found.groups.get('tool').portable, [{ file: 'tool_cosmo_any', filename: 'tool_cosmo_any', kind: 'binary', platforms: ['linux/amd64', 'darwin/arm64'] }]);
}

// A served filename names the project.
{
	const manifest = { schema: 1, artifacts: [{ file: 'out/x.com', filename: 'tool-cli', platforms: ['linux/amd64'] }] };
	const found = findArtifacts(dir(['buildhost-artifacts.json'], manifest, ['out/x.com']), 'tool');
	assert.ok(found.ok);
	assert.deepEqual([...found.groups.keys()], ['tool/cli']);
}

// Each bad manifest fails, and the message names the problem.
const bad: [unknown, RegExp][] = [
	['{', /not valid JSON/],
	[{ schema: 2, artifacts: [] }, /unsupported schema 2/],
	[{ schema: 1 }, /"artifacts" must be an array/],
	[{ schema: 1, artifacts: [{ file: '../x', platforms: ['linux/amd64'] }] }, /bad "file"/],
	[{ schema: 1, artifacts: [{ file: '/abs', platforms: ['linux/amd64'] }] }, /bad "file"/],
	[{ schema: 1, artifacts: [{ file: 'gone', platforms: ['linux/amd64'] }] }, /"gone" does not exist/],
	[{ schema: 1, artifacts: [{ file: 'tool_cosmo_any', platforms: ['linux'] }] }, /non-empty "platforms"/],
];
for (const [manifest, want] of bad) {
	const found = findArtifacts(dir(['tool_cosmo_any'], manifest), 'tool');
	assert.equal(found.ok, false);
	assert.match(found.message, want);
}

// A directory with nothing to publish fails and lists what it holds.
{
	const found = findArtifacts(dir(['README', 'checksums.txt']), 'tool');
	assert.equal(found.ok, false);
	assert.match(found.message, /No matrix artifacts.*Files: README, checksums.txt/);
}

console.log('artifact-groups: all checks passed');
