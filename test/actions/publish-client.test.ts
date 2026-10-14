// Behavior tests for .github/actions/lib/publish-client.ts.

const assert = require('node:assert');
const fs = require('node:fs') as typeof import('node:fs');
const lib = `${process.env.GITHUB_WORKSPACE ?? process.cwd()}/.github/actions/lib/publish-client`;
const { newClient, isTransient } = require(`${lib}.ts`);

assert.strictEqual(isTransient(503, '{}'), true, 'a 5xx is transient');
assert.strictEqual(isTransient(404, ''), true, 'a proxy 404 with no body is transient');
assert.strictEqual(isTransient(404, '{"error":"not found"}'), false, "the app's own 404 is not");
assert.strictEqual(isTransient(400, 'bad'), false);

(async () => {
	const answers = [{ status: 502, body: '' }, { status: 201, body: '{"ok":true}' }];
	const asked: { url: string; auth: string }[] = [];
	(globalThis as any).fetch = async (url: string, init: { headers: Record<string, string> }) => {
		asked.push({ url, auth: init.headers.Authorization });
		const a = answers.shift()!;
		return { status: a.status, text: async () => a.body };
	};
	const warnings: string[] = [];
	const core = { info: () => {}, warning: (m: string) => warnings.push(m) };
	const { api } = newClient({ server: 'https://bh.example', token: 'tok', core, upload: {}, fs });
	const r = await api('POST', '/api/v1/projects/p/releases', '{}');
	assert.deepStrictEqual(r, { status: 201, text: '{"ok":true}' }, 'a transient failure is retried');
	assert.strictEqual(asked.length, 2);
	assert.strictEqual(asked[0].url, 'https://bh.example/api/v1/projects/p/releases');
	assert.strictEqual(asked[0].auth, 'Bearer tok');
	assert.strictEqual(warnings.length, 1, 'the retry is logged');
	console.log('publish-client: all checks passed');
})();
