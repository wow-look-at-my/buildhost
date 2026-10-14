// Behavior tests for .github/actions/lib/publish-group.ts and .github/actions/lib/client.ts.

const assert = require('node:assert');
const root = `${process.env.GITHUB_WORKSPACE ?? process.cwd()}/.github/actions/lib`;
// Untyped on purpose: the modules' types are checked where they are CALLED, in the publish composite.
const { publishGroup } = require(`${root}/publish-group.ts`);
const { client } = require(`${root}/client.ts`);

const A = 'a'.repeat(64);
const B = 'b'.repeat(64);

function harness(sums: Record<string, string>, answer: (method: string, url: string) => number = () => 201) {
	const asked: string[] = [];
	const recorded: unknown[] = [];
	const failed: string[] = [];
	const respond = (method: string, url: string) => {
		asked.push(`${method} ${url}`);
		return { status: method === 'POST' ? 200 : answer(method, url), text: '' };
	};
	const deps = {
		send: async (method: string, url: string) => respond(method, url),
		put: async (url: string, file: string) => respond('PUT', `${url} <${file}>`),
		sha256: async (file: string) => sums[file],
		record: (slot: unknown) => { recorded.push(slot); },
		core: { info: () => {}, warning: () => {}, setFailed: (m: string) => { failed.push(m); } },
		server: 'https://bh',
	};
	return { deps, asked, recorded, failed };
}

const slot = (file: string, os: string, arch: string) => ({ file, os, arch });

(async () => {
	// Slots that are exactly the product of their platforms send one body for every row.
	{
		const h = harness({ l: A, d: A });
		const ok = await publishGroup(h.deps, 'tool', '7', { slots: [slot('l', 'linux', 'amd64'), slot('d', 'darwin', 'amd64')], portable: [] });
		assert.equal(ok, true);
		assert.deepEqual(h.asked, [
			'PUT /api/v1/projects/tool/releases/7/artifacts/linux,darwin/amd64?kind=binary <l>',
			'POST /api/v1/projects/tool/releases/7/publish',
		]);
		assert.equal(h.recorded.length, 2);
	}

	// A ragged slot set sends the bytes once and registers the rest by hash.
	{
		const h = harness({ a: A, b: A, c: A });
		await publishGroup(h.deps, 'tool', '7', { slots: [slot('a', 'linux', 'amd64'), slot('b', 'linux', 'arm64'), slot('c', 'darwin', 'arm64')], portable: [] });
		assert.deepEqual(h.asked.slice(0, 3), [
			'PUT /api/v1/projects/tool/releases/7/artifacts/linux/amd64?kind=binary <a>',
			`PUT /api/v1/projects/tool/releases/7/artifacts/linux/arm64?kind=binary&upload_sha256=${A}`,
			`PUT /api/v1/projects/tool/releases/7/artifacts/darwin/arm64?kind=binary&upload_sha256=${A}`,
		]);
	}

	// Different bytes transfer separately, and a portable entry is one upload that records each platform.
	{
		const h = harness({ l: A, w: B, ape: A });
		const portable = [{ file: 'ape', filename: 'tool', kind: 'binary', platforms: ['linux/amd64', 'darwin/arm64'] }];
		await publishGroup(h.deps, 'tool', '7', { slots: [slot('l', 'linux', 'amd64'), slot('w', 'windows', 'amd64')], portable });
		assert.equal(h.asked[0], 'PUT /api/v1/projects/tool/releases/7/artifacts/ape?platforms=linux/amd64,darwin/arm64&kind=binary <ape>');
		assert.equal(h.asked.filter((a) => a.startsWith('PUT')).length, 3);
		assert.equal(h.recorded.length, 4);
	}

	// A failed upload stops before the publish and names the slot.
	{
		const h = harness({ l: A }, () => 500);
		const ok = await publishGroup(h.deps, 'tool', '7', { slots: [slot('l', 'linux', 'amd64')], portable: [] });
		assert.equal(ok, false);
		assert.ok(!h.asked.some((a) => a.startsWith('POST')));
		assert.match(h.failed[0], /Upload tool linux\/amd64 failed \(500\)/);
	}

	// The client retries a transient answer and returns the first real one, with the bearer token.
	{
		const statuses = [502, 404, 201];
		const seen: string[] = [];
		const realFetch = globalThis.fetch;
		globalThis.fetch = (async (url: string, init: { headers: Record<string, string> }) => {
			seen.push(`${url} ${init.headers.Authorization}`);
			const status = statuses.shift()!;
			return { status, text: async () => (status === 404 ? '' : '{}') };
		}) as unknown as typeof fetch;
		const warnings: string[] = [];
		const send = client('https://bh', 'tok', { warning: (m: string) => warnings.push(m) }, async () => {});
		const r = await send('GET', '/x');
		globalThis.fetch = realFetch;
		assert.deepEqual(r, { status: 201, text: '{}' });
		assert.equal(seen.length, 3);
		assert.equal(seen[0], 'https://bh/x Bearer tok');
		assert.equal(warnings.length, 2);
	}

	console.log('publish-group: all checks passed');
})().catch((e) => { console.error(e); process.exit(1); });
