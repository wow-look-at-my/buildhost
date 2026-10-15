// Behavior tests for .github/actions/lib/site.ts: the publish of a source_dir, directly and through an upload session, its record and its comment.

const assert = require('node:assert');
const nodeFs = require('node:fs') as typeof import('node:fs');
const site = require(`${process.env.GITHUB_WORKSPACE ?? process.cwd()}/.github/actions/lib/site.ts`);
const { tempDir } = require('./temp-dir.ts') as typeof import('./temp-dir');

type Call = { method: string; url: string; headers: Record<string, string> };

function stub(directLimit: number) {
	const calls: Call[] = [];
	let committed = 0;
	globalThis.fetch = (async (url: string, init: any = {}) => {
		const method = init.method ?? 'GET';
		calls.push({ method, url, headers: init.headers ?? {} });
		const u = new URL(url);
		const json = (status: number, v: unknown) => new Response(JSON.stringify(v), { status });
		if (u.pathname === '/api/v1/server-info') return json(200, { max_direct_upload_bytes: directLimit });
		if (method === 'POST' && u.pathname === '/api/v1/uploads') return json(201, { id: 's1' });
		if (method === 'PATCH') { committed += (init.body as Buffer).length; return json(200, { size: committed }); }
		if (method === 'PUT' && u.host === 'sites.bh.example') return json(201, { url: 'https://sites.bh.example/r/@main/' });
		return json(404, { error: 'unexpected' });
	}) as any;
	return calls;
}

function globals(inputs: Record<string, string>, pr?: number) {
	const out = { failed: [] as string[], outputs: {} as Record<string, string>, recorded: [] as any[], comments: [] as any[] };
	const g = {
		core: { info: () => {}, warning: () => {}, setFailed: (m: string) => out.failed.push(m), setOutput: (k: string, v: string) => { out.outputs[k] = v; } },
		github: { repository: 'o/r', sha: 'c0ffee', ref_name: 'main' },
		context: { repo: { owner: 'o', repo: 'r' }, payload: pr ? { pull_request: { number: pr } } : {} },
		octokit: {
			request: async (_route: string, p: unknown) => { out.recorded.push(p); },
			rest: { issues: { listComments: async () => ({ data: [] }), createComment: async (p: unknown) => { out.comments.push(p); } }, pulls: { list: async () => ({ data: [] }) } },
		},
		exec: { exec: async (_cmd: string, args: string[]) => { nodeFs.writeFileSync(args[1], 'archive-bytes'); return 0; } },
		steps: {},
		inputs: { server: 'https://bh.example', token: 'tok', project: '', branch: 'feature/x', source_dir: tempDir('site-', { 'index.html': 'hi' }), artifact_name: '', git_commit: '', public: 'false', chunk_size: '', comment: 'true', ...inputs },
	};
	return { g, out };
}

(async () => {
	assert.strictEqual(site.sitesBase('https://pazer.build'), 'https://sites.pazer.build');
	assert.strictEqual(site.chunkSizeFrom(''), 64 * 1024 * 1024);
	assert.strictEqual(site.chunkSizeFrom('0'), -1, 'chunk_size 0 is always direct');
	assert.match(site.chunkSizeFrom('-3'), /invalid chunk_size/);
	assert.strictEqual(site.siteURLFrom({}, 'https://sites.s', 'p', 'b'), 'https://sites.s/p/@b/', 'an older server gets the canonical URL');

	let calls = stub(1 << 20);
	let r = globals({}, 7);
	await site.publishSite(r.g);
	assert.deepStrictEqual(r.out.failed, []);
	const put = calls.find((c) => c.method === 'PUT')!;
	assert.strictEqual(put.url, 'https://sites.bh.example/r/branch/feature-x', 'the branch is flattened to one segment');
	assert.strictEqual(put.headers['Content-Type'], 'application/gzip');
	assert.strictEqual(put.headers['X-Git-Commit'], 'c0ffee');
	assert.strictEqual(put.headers['X-Public-Site'], 'false');
	assert.strictEqual(put.headers.Authorization, 'Bearer tok');
	assert.strictEqual(r.out.outputs.site_url, 'https://sites.bh.example/r/@main/');
	assert.strictEqual(r.out.recorded[0].path, 'branch/feature-x', 'the site is recorded');
	assert.match(r.out.comments[0].body, /buildhost-preview:r\/feature-x/, 'the URL is commented on the pull request');

	calls = stub(4);
	r = globals({ chunk_size: '5' });
	await site.publishSite(r.g);
	assert.deepStrictEqual(r.out.failed, []);
	assert.strictEqual(calls.filter((c) => c.method === 'PATCH').length, 3, 'a body over the direct limit goes in chunks');
	assert.match(calls.find((c) => c.method === 'PUT')!.url, /^https:\/\/sites\.bh\.example\/r\/branch\/feature-x\?upload_session=s1&upload_sha256=/);

	stub(1 << 20);
	r = globals({ comment: 'maybe' });
	await site.publishSite(r.g);
	assert.match(r.out.failed[0], /invalid comment 'maybe'/);

	r = globals({ artifact_name: 'site' });
	await site.publishSite(r.g);
	assert.match(r.out.failed[0], /not both/);

	console.log('site: all checks passed');
})();
