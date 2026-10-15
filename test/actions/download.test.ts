// Behavior tests for the buildhost-download entry in .github/actions/lib/download.ts. This covers the URL it asks dl for, the file it writes,
// and a miss.

const assert = require('node:assert');
const { readFileSync, statSync, mkdtempSync } = require('node:fs') as typeof import('node:fs');
const { tmpdir } = require('node:os') as typeof import('node:os');
const { join } = require('node:path') as typeof import('node:path');
const { download, downloadURL } = require(`${process.env.GITHUB_WORKSPACE ?? process.cwd()}/.github/actions/lib/download.ts`);

const none = { version: '', branch: '', os: '', arch: '', format: '' };

function answer(status: number, text: string) {
	const asked: { url: string; auth: string | undefined }[] = [];
	globalThis.fetch = (async (url: string, init: { headers: Record<string, string> }) => {
		asked.push({ url, auth: init.headers.Authorization });
		return new Response(text, { status });
	}) as any;
	return asked;
}

function step(inputs: Record<string, string>) {
	const out = { failed: [] as string[], warned: [] as string[], outputs: {} as Record<string, string> };
	const core = { info: () => {}, warning: (m: string) => out.warned.push(m), setFailed: (m: string) => out.failed.push(m), setOutput: (k: string, v: string) => { out.outputs[k] = v; } };
	const all = { server: 'https://pazer.build', token: '', project: 'org/tool', ...none, path: '', executable: 'true', required: 'true', ...inputs };
	return { g: { core, github: {}, context: {}, octokit: {}, exec: {}, steps: {}, inputs: all }, out };
}

(async () => {
	assert.strictEqual(downloadURL({ server: 'https://pazer.build', project: 'dats', ...none }, 'Linux', 'X64'), 'https://dl.pazer.build/dats?os=linux&arch=amd64', 'os and arch default to the runner');
	assert.strictEqual(downloadURL({ server: 'https://builds.example:8443', project: 'p', ...none, version: 'v3', format: 'tar.gz' }, 'macOS', 'ARM64'),
		'https://dl.builds.example:8443/p?os=macos&arch=arm64&v=v3&fmt=tar.gz', 'dl follows the server host, and a set input is sent');

	const dir = mkdtempSync(join(tmpdir(), 'download-'));
	const asked = answer(200, 'binary');
	const ok = step({ token: 'tok', path: join(dir, 'sub', 'tool'), os: 'linux', arch: 'amd64' });
	await download(ok.g, async () => {});
	assert.strictEqual(asked[0].auth, 'Bearer tok');
	assert.strictEqual(readFileSync(join(dir, 'sub', 'tool'), 'utf8'), 'binary');
	assert.strictEqual(statSync(join(dir, 'sub', 'tool')).mode & 0o111, 0o111, 'executable is the default');
	assert.deepStrictEqual(ok.out.outputs, { path: join(dir, 'sub', 'tool'), downloaded: 'true' });

	const tries = answer(404, '');
	const missing = step({ path: join(dir, 'gone'), required: 'false' });
	await download(missing.g, async () => {});
	assert.strictEqual(tries.length, 3, 'a miss is retried');
	assert.deepStrictEqual(missing.out.outputs, { path: '', downloaded: 'false' });
	assert.match(missing.out.warned[0], /HTTP 404/);
	assert.deepStrictEqual(missing.out.failed, [], 'required false reports the miss without failing');

	answer(404, '');
	const required = step({ path: join(dir, 'gone') });
	await download(required.g, async () => {});
	assert.match(required.out.failed[0], /Could not download https:\/\/dl\.pazer\.build\/org\/tool/);

	console.log('download-action: all checks passed');
})();
