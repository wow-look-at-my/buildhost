// Behavior tests for .github/actions/lib/portable-manifest.ts.

const assert = require('node:assert');
const fs = require('node:fs') as typeof import('node:fs');
const path = require('node:path') as typeof import('node:path');
const lib = `${process.env.GITHUB_WORKSPACE ?? process.cwd()}/.github/actions/lib/portable-manifest`;
const { readPortableManifest, manifestName } = require(`${lib}.ts`);
const { tempDir } = require('./temp-dir.ts') as typeof import('./temp-dir');

const dir = (files: Record<string, string>): string => tempDir('portable-', files);

function read(d: string): { out: unknown; failed: string[] } {
	const failed: string[] = [];
	const out = readPortableManifest(d, { setFailed: (m: string) => failed.push(m) }, fs, path);
	return { out, failed };
}

{
	const { out, failed } = read(dir({}));
	assert.deepStrictEqual(out, [], 'no manifest is an empty list');
	assert.deepStrictEqual(failed, []);
}

{
	const manifest = { schema: 1, artifacts: [{ file: 'tool.com', platforms: ['linux/amd64', 'darwin/arm64'] }] };
	const { out, failed } = read(dir({ [manifestName]: JSON.stringify(manifest), 'tool.com': 'x' }));
	assert.deepStrictEqual(failed, []);
	assert.deepStrictEqual(out, [{ file: 'tool.com', filename: 'tool.com', kind: 'binary', platforms: ['linux/amd64', 'darwin/arm64'] }]);
}

{
	const { out, failed } = read(dir({ [manifestName]: '{' }));
	assert.strictEqual(out, null, 'a manifest that is not JSON fails');
	assert.match(failed[0], /not valid JSON/);
}

{
	const manifest = { schema: 1, artifacts: [{ file: '../escape', platforms: ['linux/amd64'] }] };
	const { out, failed } = read(dir({ [manifestName]: JSON.stringify(manifest) }));
	assert.strictEqual(out, null, 'a path out of the directory fails');
	assert.match(failed[0], /bad "file"/);
}

{
	const manifest = { schema: 1, artifacts: [{ file: 'tool.com', platforms: [] }] };
	const { out, failed } = read(dir({ [manifestName]: JSON.stringify(manifest), 'tool.com': 'x' }));
	assert.strictEqual(out, null, 'an entry with no platform fails');
	assert.match(failed[0], /non-empty "platforms"/);
}

console.log('portable-manifest: all checks passed');
