// Behavior tests for the publish composite's helper modules under .github/actions/lib/.

const assert = require('node:assert');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');
const lib = `${process.env.GITHUB_WORKSPACE ?? process.cwd()}/.github/actions/lib`;
// Untyped on purpose: the module's types are checked where it is CALLED, in the publish composite.
const manifest = require(`${lib}/portable-manifest.ts`);
const client = require(`${lib}/api-client.ts`);
const names = require(`${lib}/artifact-names.ts`);
const io = require(`${lib}/file-io.ts`);

function testManifest() {
	const has = (f: string) => f === 'tool.com';
	const doc = (artifacts: unknown) => JSON.stringify({ schema: 1, artifacts });
	assert.deepStrictEqual(manifest.parsePortableManifest(doc([{ file: 'tool.com', platforms: ['linux/amd64', 'darwin/arm64'] }]), has, path), [
		{ file: 'tool.com', filename: 'tool.com', kind: 'binary', platforms: ['linux/amd64', 'darwin/arm64'] },
	]);
	const bad: [string, string][] = [
		['{', 'not valid JSON'],
		[JSON.stringify({ schema: 2, artifacts: [] }), 'unsupported schema 2'],
		[JSON.stringify({ schema: 1 }), '"artifacts" must be an array'],
		[doc([{ file: '../x', platforms: ['linux/amd64'] }]), 'bad "file"'],
		[doc([{ file: '/abs', platforms: ['linux/amd64'] }]), 'bad "file"'],
		[doc([{ file: 'gone', platforms: ['linux/amd64'] }]), '"gone" does not exist'],
		[doc([{ file: 'tool.com', platforms: [] }]), 'non-empty "platforms"'],
		[doc([{ file: 'tool.com', platforms: ['linux'] }]), 'non-empty "platforms"'],
	];
	for (const [text, want] of bad) {
		const got = manifest.parsePortableManifest(text, has, path);
		assert.strictEqual(typeof got, 'string', `${text} must be refused`);
		assert.ok(got.startsWith('buildhost-artifacts.json: ') && got.includes(want), `${text}: got ${got}`);
	}
}

function testNames() {
	assert.strictEqual(names.projectForBinary('app', 'app'), 'app');
	assert.strictEqual(names.projectForBinary('app', 'app-cli'), 'app/cli');
	assert.strictEqual(names.projectForBinary('app', 'helper'), 'app/helper');
	assert.deepStrictEqual('log-streamer-client_linux_amd64'.match(names.ArtifactRe)?.slice(1), ['log-streamer-client', 'linux', 'amd64']);
	assert.strictEqual(names.binaryForPortable({ file: 'dir/tool_cosmo_any.exe', filename: 'tool_cosmo_any.exe' }, path.basename), 'tool');
	assert.strictEqual(names.binaryForPortable({ file: 'dir/tool.com', filename: 'served' }, path.basename), 'served');
	assert.strictEqual(names.binaryForPortable({ file: 'plain', filename: 'plain' }, path.basename), 'plain');
}

async function testFileIo() {
	const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'publish-helpers-'));
	const file = path.join(dir, 'blob');
	const bytes = Buffer.from('0123456789abcdef');
	fs.writeFileSync(file, bytes);
	assert.strictEqual(await io.fileSHA256(fs, crypto, file), crypto.createHash('sha256').update(bytes).digest('hex'));
	assert.strictEqual(io.readChunk(fs, file, 4, 6).toString(), '456789');
	// a filesystem that answers one byte per read still yields the whole chunk
	const trickle = { ...fs, readSync: (fd: number, buf: Buffer, off: number, _len: number, pos: number) => fs.readSync(fd, buf, off, 1, pos) };
	assert.strictEqual(io.readChunk(trickle, file, 2, 5).toString(), '23456');
	assert.throws(() => io.readChunk(fs, file, 12, 8), /wanted 8 bytes at 12, the file ended after 4/);
	fs.rmSync(dir, { recursive: true });
}

async function testApi() {
	const realFetch = globalThis.fetch;
	const warnings: string[] = [];
	const core = { warning: (m: string) => warnings.push(m) };
	const noSleep = async () => {};
	try {
		const answers = [{ status: 502, text: 'bad gateway' }, { status: 404, text: '' }, { status: 201, text: '{"ok":true}' }];
		const seen: { url: string; init: any }[] = [];
		globalThis.fetch = (async (url: string, init: any) => {
			seen.push({ url, init });
			const a = answers.shift()!;
			return { status: a.status, text: async () => a.text };
		}) as any;
		const api = client.makeApi('https://bh.example', 'tok', core, noSleep);
		assert.deepStrictEqual(await api('POST', '/api/v1/x', '{}'), { status: 201, text: '{"ok":true}' });
		assert.strictEqual(seen.length, 3, 'a 5xx and an empty 404 are retried');
		assert.strictEqual(seen[0].url, 'https://bh.example/api/v1/x');
		assert.strictEqual(seen[0].init.headers.Authorization, 'Bearer tok');
		assert.strictEqual(seen[0].init.headers['Content-Type'], 'application/json');
		assert.strictEqual(warnings.length, 2);

		globalThis.fetch = (async () => ({ status: 404, text: async () => '{"error":"project not found"}' })) as any;
		assert.strictEqual((await api('GET', '/p')).status, 404, "the app's own 404 returns at once");

		let calls = 0;
		globalThis.fetch = (async () => {
			calls++;
			throw new Error('ECONNRESET');
		}) as any;
		const failed = await api('PUT', '/b', Buffer.from('x'));
		assert.strictEqual(calls, client.MaxAttempts);
		assert.strictEqual(failed.status, 0);
		assert.ok(failed.text.includes('ECONNRESET'), failed.text);
	} finally {
		globalThis.fetch = realFetch;
	}
}

async function main() {
	testManifest();
	testNames();
	await testFileIo();
	await testApi();
	console.log('publish-helpers: all checks passed');
}

main().catch((err: unknown) => {
	console.error(err);
	process.exit(1);
});
