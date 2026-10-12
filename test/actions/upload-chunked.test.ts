// Behavior tests for .github/actions/lib/upload.ts.
//
// Its own job because nothing else reaches the code: the 413 it exists to
// prevent only appears against a body over 100 MB through Cloudflare, which no
// CI job sends. A mistake here fails publishing for every repo in the org that
// ships a binary larger than the advertised direct limit.
//
// The server here speaks the real session protocol from docs/uploads.md, so the
// assertions are about what buildhost actually receives.

const assert = require('node:assert');
const http = require('node:http') as typeof import('node:http');
const fs = require('node:fs') as typeof import('node:fs');
const os = require('node:os') as typeof import('node:os');
const pathMod = require('node:path') as typeof import('node:path');
const crypto = require('node:crypto') as typeof import('node:crypto');

const lib = `${process.env.GITHUB_WORKSPACE ?? process.cwd()}/.github/actions/lib/upload`;
// Untyped on purpose: the module's types are checked where it is CALLED, in the
// publish composite; here the assertions are the contract.
const { putFile, directLimit, DefaultDirectLimit } = require(`${lib}.ts`);

type Req = { method: string; url: string; headers: Record<string, string>; body: Buffer };

interface ServerOptions {
	/** Advertised max_direct_upload_bytes; 0 makes server-info answer 404. */
	maxDirect: number;
	/** Bytes to drop from the Nth PATCH (1-based), simulating a cut transfer. */
	truncateChunk?: { nth: number; keep: number };
	/** Status the finalize request answers with. */
	finalizeStatus?: number;
}

interface Harness {
	send: (method: string, urlPath: string, body?: Buffer | null, headers?: Record<string, string>) => Promise<{ status: number; text: string }>;
	requests: Req[];
	spools: Map<string, Buffer>;
	finalized: Req | null;
	close: () => Promise<void>;
}

async function start(opts: ServerOptions): Promise<Harness> {
	const requests: Req[] = [];
	const spools = new Map<string, Buffer>();
	let finalized: Req | null = null;
	let patches = 0;
	let nextID = 0;

	const server = http.createServer((req, res) => {
		const chunks: Buffer[] = [];
		req.on('data', (c: Buffer) => chunks.push(c));
		req.on('end', () => {
			const body = Buffer.concat(chunks);
			const url = req.url ?? '';
			const entry: Req = { method: req.method ?? '', url, headers: req.headers as Record<string, string>, body };
			requests.push(entry);
			const json = (status: number, obj: unknown) => {
				res.writeHead(status, { 'Content-Type': 'application/json' });
				res.end(JSON.stringify(obj));
			};

			if (url === '/api/v1/server-info') {
				if (opts.maxDirect <= 0) { res.writeHead(404); res.end('no such route'); return; }
				json(200, { max_direct_upload_bytes: opts.maxDirect, upload_sessions: true });
				return;
			}
			if (req.method === 'POST' && url === '/api/v1/uploads') {
				const id = `sess${++nextID}`;
				spools.set(id, Buffer.alloc(0));
				json(201, { id });
				return;
			}
			const session = /^\/api\/v1\/uploads\/([^/?]+)/.exec(url);
			if (session) {
				const id = decodeURIComponent(session[1]);
				const spool = spools.get(id);
				if (spool === undefined) { json(404, { error: 'no such session' }); return; }
				if (req.method === 'GET') { json(200, { size: spool.length }); return; }
				if (req.method === 'DELETE') { spools.delete(id); res.writeHead(204); res.end(); return; }
				const offset = Number(new URL(url, 'http://x').searchParams.get('offset'));
				if (offset !== spool.length) { json(409, { size: spool.length }); return; }
				patches++;
				const t = opts.truncateChunk;
				const kept = t && t.nth === patches ? body.subarray(0, t.keep) : body;
				spools.set(id, Buffer.concat([spool, kept]));
				json(200, { size: spool.length + kept.length });
				return;
			}
			// Anything else is the artifact endpoint the publish would call.
			finalized = entry;
			res.writeHead(opts.finalizeStatus ?? 201, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify({ ok: true }));
		});
	});

	await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
	const port = (server.address() as { port: number }).port;
	const base = `http://127.0.0.1:${port}`;

	const send = async (method: string, urlPath: string, body?: Buffer | null, headers?: Record<string, string>) => {
		const h: Record<string, string> = { Authorization: 'Bearer t', ...(headers ?? {}) };
		if (body) h['Content-Type'] = 'application/octet-stream';
		const resp = await fetch(`${base}${urlPath}`, { method, headers: h, body: body ?? undefined });
		return { status: resp.status, text: await resp.text() };
	};

	return {
		send, requests, spools,
		get finalized() { return finalized; },
		close: () => new Promise<void>((resolve) => server.close(() => resolve())),
	} as Harness;
}

const warnings: string[] = [];
const core = { info: () => {}, warning: (m: string) => warnings.push(m) };

// A fixture on disk, read back through the same fd-per-chunk Body the publish
// composite builds, so the test exercises that shape rather than a fake one.
function writeFixture(name: string, size: number) {
	const dir = fs.mkdtempSync(pathMod.join(os.tmpdir(), 'upload-test-'));
	const bytes = crypto.randomBytes(size);
	const p = pathMod.join(dir, name);
	fs.writeFileSync(p, bytes);
	const sum = crypto.createHash('sha256').update(bytes).digest('hex');
	const body = {
		size: fs.statSync(p).size,
		sha256: sum,
		read: (offset: number, length: number): Buffer => {
			const buf = Buffer.allocUnsafe(length);
			const fd = fs.openSync(p, 'r');
			try { fs.readSync(fd, buf, 0, length, offset); } finally { fs.closeSync(fd); }
			return buf;
		},
	};
	return { path: p, sum, bytes, body };
}

async function main(): Promise<void> {
	// --- a file inside the advertised limit keeps the single direct PUT ------

	{
		const h = await start({ maxDirect: 1 << 20 });
		const f = writeFixture('small_linux_amd64', 4096);
		const r = await putFile(h.send, core, { urlPath: '/api/v1/a?kind=binary', body: f.body, directLimit: 1 << 20 });
		assert.strictEqual(r.status, 201);
		assert.strictEqual(h.requests.length, 1, 'a small file opens no session');
		assert.strictEqual(h.requests[0].method, 'PUT');
		assert.strictEqual(h.requests[0].url, '/api/v1/a?kind=binary');
		assert.ok(h.requests[0].body.equals(f.bytes), 'the direct PUT carries the file');
		await h.close();
	}

	// --- a file past the limit is assembled through a session ---------------

	{
		const h = await start({ maxDirect: 1000 });
		const f = writeFixture('big_linux_amd64', 5000);
		const r = await putFile(h.send, core, {
			urlPath: '/api/v1/a?kind=binary', body: f.body, chunkSize: 1024,
			headers: { 'X-Artifact-Filename': 'go-toolchain' },
		});
		assert.strictEqual(r.status, 201);

		const patches = h.requests.filter((q) => q.method === 'PATCH');
		assert.strictEqual(patches.length, 5, '5000 bytes in 1024-byte chunks');
		assert.deepStrictEqual(patches.map((q) => new URL(q.url, 'http://x').searchParams.get('offset')),
			['0', '1024', '2048', '3072', '4096']);

		const spool = [...h.spools.values()][0];
		assert.ok(spool.equals(f.bytes), 'the reassembled spool is the file');

		const fin = h.finalized!;
		assert.strictEqual(fin.method, 'PUT');
		assert.strictEqual(fin.body.length, 0, 'finalize sends an empty body');
		const q = new URL(fin.url, 'http://x').searchParams;
		assert.strictEqual(q.get('kind'), 'binary', 'the original query survives');
		assert.strictEqual(q.get('upload_session'), [...h.spools.keys()][0]);
		assert.strictEqual(q.get('upload_sha256'), f.sum);
		assert.strictEqual(fin.headers['x-artifact-filename'], 'go-toolchain', 'per-artifact headers survive');
		await h.close();
	}

	// --- a cut chunk resumes from the size the server committed -------------

	{
		const h = await start({ maxDirect: 1000, truncateChunk: { nth: 2, keep: 300 } });
		const f = writeFixture('resume_linux_amd64', 5000);
		const r = await putFile(h.send, core, { urlPath: '/api/v1/a', body: f.body, chunkSize: 1024 });
		assert.strictEqual(r.status, 201);
		const spool = [...h.spools.values()][0];
		assert.ok(spool.equals(f.bytes), 'the partially delivered chunk is completed, not restarted');
		const offsets = h.requests.filter((q) => q.method === 'PATCH')
			.map((q) => Number(new URL(q.url, 'http://x').searchParams.get('offset')));
		assert.ok(offsets.includes(1324), `resumes at the committed size, got ${offsets.join(',')}`);
		await h.close();
	}

	// --- a refused finalize releases the session ----------------------------

	{
		const h = await start({ maxDirect: 1000, finalizeStatus: 409 });
		const f = writeFixture('refused_linux_amd64', 3000);
		const r = await putFile(h.send, core, { urlPath: '/api/v1/a', body: f.body, chunkSize: 1024 });
		assert.strictEqual(r.status, 409, 'the endpoint status is returned, not thrown');
		assert.strictEqual(h.spools.size, 0, 'the spool is aborted rather than left to the sweeper');
		await h.close();
	}

	// --- a Body that under-delivers is refused, never appended -------------

	{
		const h = await start({ maxDirect: 1000 });
		const f = writeFixture('short_linux_amd64', 5000);
		// Drops the last byte of every chunk. Appending it would shift every
		// later offset, so the spool would silently be the wrong bytes.
		const short = { ...f.body, read: (o: number, n: number) => f.body.read(o, n).subarray(0, n - 1) };
		await assert.rejects(
			putFile(h.send, core, { urlPath: '/api/v1/a', body: short, chunkSize: 1024 }),
			/read 1023 bytes at offset 0, wanted 1024/,
		);
		assert.strictEqual(h.requests.filter((q) => q.method === 'PATCH').length, 0, 'nothing is appended');
		assert.strictEqual(h.spools.size, 0, 'the session is aborted');
		await h.close();
	}

	// --- an unreadable server-info falls back, it never guesses unlimited ---

	{
		const h = await start({ maxDirect: 0 });
		warnings.length = 0;
		assert.strictEqual(await directLimit(h.send, core), DefaultDirectLimit);
		assert.strictEqual(DefaultDirectLimit, 95 << 20, 'matches the server default');
		assert.strictEqual(warnings.length, 1, 'the fallback is reported, never silent');
		await h.close();
	}

	console.log('upload: all checks passed');
}

main().catch((e) => { console.error(e); process.exit(1); });
