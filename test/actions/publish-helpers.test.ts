// Behavior tests for the publish composite's helper modules under .github/actions/lib/.

const assert = require('node:assert');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');
const http = require('node:http');
const lib = `${process.env.GITHUB_WORKSPACE ?? process.cwd()}/.github/actions/lib`;
// Untyped on purpose: the module's types are checked where it is CALLED, in the publish composite.
const groups = require(`${lib}/artifact-groups.ts`);
const client = require(`${lib}/client.ts`);
const io = require(`${lib}/file-io.ts`);
const upload = require(`${lib}/upload.ts`);

function testNames() {
	assert.deepStrictEqual('log-streamer-client_linux_amd64'.match(groups.artifactRe)?.slice(1), ['log-streamer-client', 'linux', 'amd64']);
	const entry = (file: string, filename: string) => ({ file, filename, kind: 'binary', platforms: ['linux/amd64'] });
	assert.strictEqual(groups.binaryForPortable(entry('dir/tool_cosmo_any.exe', 'tool_cosmo_any.exe')), 'tool');
	assert.strictEqual(groups.binaryForPortable(entry('dir/tool.com', 'served')), 'served');
	assert.strictEqual(groups.binaryForPortable(entry('plain', 'plain')), 'plain');
}

async function testSlots() {
	const group = {
		slots: [{ file: 'a', os: 'linux', arch: 'amd64' }],
		portable: [{ file: 'ape', filename: 'ape', kind: 'binary', platforms: ['linux/arm64', 'darwin/arm64'] }],
	};
	const sums: Record<string, string> = { a: 'aa', ape: 'bb' };
	assert.deepStrictEqual(await groups.slotsOf(group, async (f: string) => sums[f]), [
		{ os: 'linux', arch: 'amd64', kind: 'binary', sha256: 'aa' },
		{ os: 'linux', arch: 'arm64', kind: 'binary', sha256: 'bb' },
		{ os: 'darwin', arch: 'arm64', kind: 'binary', sha256: 'bb' },
	]);
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

	// The putter asks the server for its direct limit once, then sends a small file as one body.
	const asked: string[] = [];
	const send = async (method: string, urlPath: string, body?: Buffer) => {
		asked.push(`${method} ${urlPath}${body ? ` ${body.toString()}` : ''}`);
		if (method === 'GET') return { status: 200, text: JSON.stringify({ max_direct_upload_bytes: 1 << 20 }) };
		return { status: 201, text: '{}' };
	};
	const core = { info: () => {}, warning: () => {} };
	const put = io.filePutter(send, core, upload, fs);
	const sum = crypto.createHash('sha256').update(bytes).digest('hex');
	assert.strictEqual((await put('/u/1', file, sum)).status, 201);
	assert.strictEqual((await put('/u/2', file, sum)).status, 201);
	assert.strictEqual(asked.filter((a) => a.startsWith('GET')).length, 1, 'the direct limit is read once');
	assert.ok(asked.includes('PUT /u/1 0123456789abcdef'), asked.join('\n'));
	fs.rmSync(dir, { recursive: true });
}

function testTransient() {
	assert.strictEqual(client.isTransient(503, '{}'), true, 'a 5xx is transient');
	assert.strictEqual(client.isTransient(404, ''), true, 'a proxy 404 with no body is transient');
	assert.strictEqual(client.isTransient(404, '{"error":"not found"}'), false, "the app's own 404 is not");
	assert.strictEqual(client.isTransient(400, 'bad'), false);
}

async function testOverHttp() {
	let calls = 0;
	const server = http.createServer((req: any, res: any) => {
		calls++;
		assert.strictEqual(req.headers.authorization, 'Bearer tok');
		res.writeHead(calls === 1 ? 503 : 200);
		res.end(calls === 1 ? 'busy' : '{"ok":true}');
	});
	await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
	const warned: string[] = [];
	const reply = await client.client(`http://127.0.0.1:${server.address().port}`, 'tok', { warning: (m: string) => warned.push(m) }, async () => {})('GET', '/api/v1/x');
	server.close();
	assert.deepStrictEqual(reply, { status: 200, text: '{"ok":true}' });
	assert.strictEqual(calls, 2);
	assert.match(warned[0], /HTTP 503 \(transient; attempt 1\/5\)/);
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
		const api = client.client('https://bh.example', 'tok', core, noSleep);
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
	testNames();
	await testSlots();
	await testFileIo();
	testTransient();
	await testApi();
	await testOverHttp();
	console.log('publish-helpers: all checks passed');
}

main().catch((err: unknown) => {
	console.error(err);
	process.exit(1);
});
