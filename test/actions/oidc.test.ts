// Behavior tests for .github/actions/lib/oidc.ts. Every publish composite mints
// through it, so a mistake here fails publishing for every repo in the org.

const assert = require('node:assert');
const lib = `${process.env.GITHUB_WORKSPACE ?? process.cwd()}/.github/actions/lib/oidc`;
const { mintOidcToken, OidcRefused } = require(`${lib}.ts`);

type Answer = { status: number; body: string } | Error;

const scripted = (answers: Answer[]) => {
	const seen: { url: string; auth: string }[] = [];
	const fetchImpl = async (url: string, init: { headers: Record<string, string> }) => {
		seen.push({ url, auth: init.headers.Authorization });
		const a = answers.shift();
		if (a === undefined) throw new Error('fetch called more times than scripted');
		if (a instanceof Error) throw a;
		return new Response(a.body, { status: a.status });
	};
	return { seen, fetchImpl };
};

const run = async (answers: Answer[]) => {
	const warned: string[] = [];
	const s = scripted(answers);
	const result = await mintOidcToken({
		url: 'https://token.actions.example/mint?x=1',
		bearer: 'req-bearer',
		audience: 'https://pazer.build',
		warning: (m: string) => warned.push(m),
		intervalMs: 1,
		fetchImpl: s.fetchImpl,
	}).then((token: string) => ({ token }), (error: Error) => ({ error }));
	return { ...result, warned, seen: s.seen };
};

(async () => {
	{
		const r = await run([{ status: 200, body: '{"value":"jwt-1"}' }]);
		assert.strictEqual(r.token, 'jwt-1');
		assert.deepStrictEqual(r.seen, [{ url: 'https://token.actions.example/mint?x=1&audience=https://pazer.build', auth: 'Bearer req-bearer' }]);
		assert.strictEqual(r.warned.length, 0);
	}

	// The failure seen in CI: a proxy error page in place of JSON.
	{
		const r = await run([
			{ status: 503, body: 'upstream connect error or disconnect/reset before headers' },
			new TypeError('fetch failed'),
			{ status: 200, body: 'upstream connect error' },
			{ status: 200, body: '{"value":""}' },
			{ status: 429, body: 'slow down' },
			{ status: 200, body: '{"value":"jwt-2"}' },
		]);
		assert.strictEqual(r.token, 'jwt-2');
		assert.strictEqual(r.warned.length, 5);
		assert.match(r.warned[0], /attempt 1.*HTTP 503: upstream connect error/);
		assert.match(r.warned[1], /attempt 2.*fetch failed/);
	}

	// A refusal does not change on retry, so it fails at the same time and says why.
	{
		const r = await run([{ status: 403, body: '{"message":"no id-token permission"}' }]);
		assert.ok(r.error instanceof OidcRefused, `expected OidcRefused, got ${r.error}`);
		assert.match(r.error.message, /HTTP 403: .*no id-token permission/);
		assert.strictEqual(r.seen.length, 1);
	}

	console.log('oidc: all checks passed');
})().catch((e) => { console.error(e); process.exit(1); });
