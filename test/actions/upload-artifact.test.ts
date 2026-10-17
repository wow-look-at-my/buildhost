// Behavior tests for buildhost-upload-artifact (.github/actions/lib/upload-artifact.ts) and the OIDC token source it authenticates with.

import check from 'node:assert';
import { uploadArtifact, uploadedSha256 } from '../../.github/actions/lib/upload-artifact.ts';
import { jwtExpiry, mintOidcToken, tokenSource } from '../../.github/actions/lib/oidc.ts';
import { tempDir, stepCore, oidcIssuer, globalsOf } from './fakes.ts';

const jwt = (exp: number) => `h.${Buffer.from(JSON.stringify({ exp })).toString('base64url')}.s`;

// Answers the OIDC issuer with a fresh JWT each time, and every other request through the handler.
function registry(handler: (method: string, path: string) => { status: number; body: unknown }) {
	const log: { method: string; path: string; auth: string; type: string }[] = [];
	let mints = 0;
	globalThis.fetch = (async (url: string, init: any = {}) => {
		if (url.startsWith('https://oidc.example')) return new Response(JSON.stringify({ value: jwt(4_000_000_000 + ++mints) }));
		const u = new URL(url);
		log.push({ method: init.method ?? 'GET', path: u.pathname + u.search, auth: init.headers?.Authorization ?? '', type: init.headers?.['Content-Type'] ?? '' });
		const a = handler(init.method ?? 'GET', u.pathname);
		return new Response(JSON.stringify(a.body), { status: a.status });
	}) as any;
	return { log, mints: () => mints };
}

function upload(inputs: Record<string, string>) {
	const { core, out } = stepCore();
	const file = `${tempDir('artifact-', { tool: 'abcdefghij' })}/tool`;
	return { out, g: globalsOf({ core, github: {}, context: {}, octokit: {}, exec: {}, steps: {}, inputs: { server: 'https://bh.example', token: 'tok', project: 'p', version: '3', os: 'linux', arch: 'amd64', path: file, kind: 'binary', filename: '', chunk_size: '', ...inputs } }) };
}

(async () => {
	check.strictEqual(jwtExpiry(jwt(123)), 123);
	check.strictEqual(jwtExpiry('opaque'), 0, 'a token that is not a JWT names no expiry');
	check.strictEqual(uploadedSha256([{ sha256: 'aa' }, { sha256: 'aa' }]), 'aa', 'a fan-out answers a row per platform');
	check.strictEqual(uploadedSha256({ sha256: 'bb' }), 'bb');
	check.strictEqual(uploadedSha256({}), '');

	// The issuer's proxy answers with a text error page, then the token: the mint asks again rather than parse the page.
	{
		oidcIssuer('https://oidc.example/t?a=1');
		const answers = [new Response('upstream connect error', { status: 503 }), new Response('upstream connect error'), new Response(JSON.stringify({ value: 'minted' }))];
		let asked = 0;
		let waited = 0;
		const realFetch = globalThis.fetch;
		globalThis.fetch = (async () => answers[asked++]) as any;
		check.strictEqual(await mintOidcToken('https://bh.example', async () => { waited++; }), 'minted');
		check.strictEqual(asked, 3);
		check.strictEqual(waited, 2);
		globalThis.fetch = (async () => new Response('no', { status: 403 })) as any;
		await check.rejects(mintOidcToken('https://bh.example', async () => {}), /refused the token request \(403\)/);
		globalThis.fetch = realFetch;
	}

	oidcIssuer();
	const fixed = tokenSource('https://bh.example', 'given');
	check.strictEqual(await fixed.bearer(), 'given');
	check.strictEqual(fixed.expire(), false, 'a given token cannot be minted again');

	let reg = registry((_method, path) => (path === '/api/v1/server-info' ? { status: 200, body: { max_direct_upload_bytes: 1 << 20 } } : { status: 201, body: { sha256: 'cafe' } }));
	const direct = upload({ filename: 'tool-linux' });
	await uploadArtifact(direct.g);
	check.strictEqual(direct.out.failed.length, 0, direct.out.failed.join('\n'));
	const put = reg.log.find((c) => c.method === 'PUT')!;
	check.strictEqual(put.path, '/api/v1/projects/p/releases/3/artifacts/linux/amd64?kind=binary');
	check.strictEqual(put.auth, 'Bearer tok');
	check.strictEqual(put.type, 'application/octet-stream');
	check.strictEqual(direct.out.outputs.sha256, 'cafe');

	oidcIssuer('https://oidc.example/t?a=1');
	let committed = 0;
	let refused = false;
	reg = registry((method, path) => {
		if (path === '/api/v1/server-info') return { status: 200, body: { max_direct_upload_bytes: 4 } };
		if (method === 'POST') return { status: 201, body: { id: 'u1' } };
		// the first token dies mid-upload, at the second chunk
		if (method === 'PATCH' && committed === 4 && !refused) { refused = true; return { status: 401, body: { error: 'expired' } }; }
		if (method === 'PATCH') { committed = Math.min(committed + 4, 10); return { status: 200, body: { size: committed } }; }
		return { status: 201, body: [{ sha256: 'beef' }] };
	});
	const chunked = upload({ token: '', chunk_size: '4' });
	await uploadArtifact(chunked.g);
	check.strictEqual(chunked.out.failed.length, 0, chunked.out.failed.join('\n'));
	check.strictEqual(reg.mints(), 2, 'a 401 mints a fresh token and the chunk is sent again');
	check.match(reg.log.find((c) => c.method === 'PUT')!.path, /\?kind=binary&upload_session=u1&upload_sha256=[0-9a-f]{64}$/);
	check.strictEqual(chunked.out.outputs.sha256, 'beef');

	const bad = upload({ chunk_size: 'x' });
	await uploadArtifact(bad.g);
	check.match(bad.out.failed[0], /invalid chunk_size 'x'/);

	console.log('upload-artifact: all checks passed');
})();
