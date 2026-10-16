// Behavior tests for the buildhost-download entry in .github/actions/lib/download.ts. This covers the URL it asks dl for, the file it writes,
// and a miss.

import assert from 'node:assert';
import { readFileSync, statSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { download, downloadURL } from '../../.github/actions/lib/download.ts';
import { globalsOf, oidcIssuer } from './fakes.ts';

// A runner can mint OIDC tokens. These checks start from a job that cannot.
oidcIssuer();

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
	return { g: globalsOf({ core, github: {}, context: {}, octokit: {}, exec: {}, steps: {}, inputs: all }), out };
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

	// With no token input, a job that may mint one sends its OIDC token, which is what reads a private project.
	oidcIssuer('https://issuer.example/token?api-version=2.0');
	const minted: { url: string; auth: string | undefined }[] = [];
	globalThis.fetch = (async (url: string, init: { headers: Record<string, string> }) => {
		minted.push({ url, auth: init.headers.Authorization });
		if (url.startsWith('https://issuer.example/')) return new Response(JSON.stringify({ value: 'oidc-jwt' }), { status: 200 });
		return new Response('binary', { status: 200 });
	}) as any;
	const viaOidc = step({ path: join(dir, 'private') });
	await download(viaOidc.g, async () => {});
	oidcIssuer();
	assert.strictEqual(minted[0].url, 'https://issuer.example/token?api-version=2.0&audience=https://pazer.build');
	assert.strictEqual(minted[1].auth, 'Bearer oidc-jwt', 'the download carries the minted token');
	assert.deepStrictEqual(viaOidc.out.failed, []);

	console.log('download-action: all checks passed');
})();
