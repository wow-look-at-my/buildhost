// Behavior tests for .github/actions/lib/download.ts, against a real HTTP server.

const assert = require('node:assert');
const http = require('node:http') as typeof import('node:http');

const lib = `${process.env.GITHUB_WORKSPACE ?? process.cwd()}/.github/actions/lib/download`;
const { fetchBody } = require(`${lib}.ts`);

type Handler = (req: import('node:http').IncomingMessage, res: import('node:http').ServerResponse) => void;

async function serve(handler: Handler): Promise<{ url: string; close: () => Promise<void> }> {
	const server = http.createServer(handler);
	await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
	const port = (server.address() as import('node:net').AddressInfo).port;
	return {
		url: `http://127.0.0.1:${port}/f`,
		close: () => new Promise<void>((r) => { server.closeAllConnections(); server.close(() => r()); }),
	};
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

async function main() {
	const stallMs = 300;

	{
		const s = await serve((_req, res) => { res.writeHead(200); res.write('abc'); });
		const t = Date.now();
		await assert.rejects(fetchBody(s.url, {}, stallMs), /no bytes for 0.3s/, 'a body that stops mid-transfer is aborted');
		assert.ok(Date.now() - t < stallMs * 5, 'the abort comes from the stall limit');
		await s.close();
	}

	{
		const s = await serve(() => { /* never answers */ });
		await assert.rejects(fetchBody(s.url, {}, stallMs), /no bytes for 0.3s/, 'a server that never answers is aborted');
		await s.close();
	}

	{
		const s = await serve(async (_req, res) => {
			res.writeHead(200);
			for (let i = 0; i < 10; i++) {
				res.write(String(i));
				await sleep(stallMs / 3);
			}
			res.end();
		});
		const body = await fetchBody(s.url, {}, stallMs);
		assert.strictEqual(body.toString(), '0123456789', 'a slow transfer longer than the limit succeeds while bytes arrive');
		await s.close();
	}

	{
		const s = await serve((_req, res) => { res.writeHead(404); res.end('missing'); });
		await assert.rejects(fetchBody(s.url, {}, stallMs), /HTTP 404/);
		await s.close();
	}

	{
		const s = await serve((req, res) => { res.writeHead(200); res.end(req.headers.authorization ?? ''); });
		const body = await fetchBody(s.url, { Authorization: 'Bearer t' }, stallMs);
		assert.strictEqual(body.toString(), 'Bearer t', 'the caller headers are sent');
		await s.close();
	}

	console.log('download: all checks passed');
}

main().catch((e) => { console.error(e); process.exit(1); });
