// Behavior tests for .github/actions/lib/portable-manifest.ts and .github/actions/lib/client.ts.

import assert from 'node:assert';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import * as http from 'node:http';
import * as crypto from 'node:crypto';
import { readPortableManifest, fileSHA256, manifestName } from '../../.github/actions/lib/portable-manifest.ts';
import { client } from '../../.github/actions/lib/client.ts';

function recorder() {
	const failed: string[] = [];
	const warned: string[] = [];
	return { failed, warned, core: { setFailed: (m: string) => failed.push(m), warning: (m: string) => warned.push(m) } };
}

async function main() {
	const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'publish-libs-'));

	{
		const rec = recorder();
		assert.deepStrictEqual(readPortableManifest(dir, rec.core), []);
		assert.deepStrictEqual(rec.failed, []);
	}

	fs.writeFileSync(path.join(dir, 'app.com'), 'ape');
	fs.writeFileSync(path.join(dir, manifestName), JSON.stringify({ schema: 1, artifacts: [{ file: 'app.com', platforms: ['linux/amd64', 'darwin/arm64'] }] }));
	{
		const rec = recorder();
		assert.deepStrictEqual(readPortableManifest(dir, rec.core), [{ file: 'app.com', filename: 'app.com', kind: 'binary', platforms: ['linux/amd64', 'darwin/arm64'] }]);
	}

	fs.writeFileSync(path.join(dir, manifestName), JSON.stringify({ schema: 1, artifacts: [{ file: '../escape', platforms: ['linux/amd64'] }] }));
	{
		const rec = recorder();
		assert.strictEqual(readPortableManifest(dir, rec.core), null);
		assert.match(rec.failed[0], /bad "file"/);
	}

	assert.strictEqual(await fileSHA256(path.join(dir, 'app.com')), crypto.createHash('sha256').update('ape').digest('hex'));

	let calls = 0;
	const server = http.createServer((req: any, res: any) => {
		calls++;
		assert.strictEqual(req.headers.authorization, 'Bearer tok');
		if (calls === 1) {
			res.writeHead(503);
			res.end('busy');
			return;
		}
		res.writeHead(200);
		res.end('{"ok":true}');
	});
	await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
	const base = `http://127.0.0.1:${(server.address() as import('node:net').AddressInfo).port}`;
	const rec = recorder();
	const reply = await client(base, 'tok', rec.core, async () => {})('GET', '/api/v1/x');
	server.close();
	assert.deepStrictEqual(reply, { status: 200, text: '{"ok":true}' });
	assert.strictEqual(calls, 2);
	assert.match(rec.warned[0], /HTTP 503 \(transient; attempt 1\/5\)/);

	console.log('publish-libs: all checks passed');
}

// Node runs this file directly, so the rejection has to become an exit code here.
main().catch((err: unknown) => {
	console.error(err);
	process.exit(1);
});
