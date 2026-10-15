// Behavior tests for .github/actions/lib/resources.ts, each action against a fake fetch that records what it was sent.

import assert from 'node:assert';
import * as mod from '../../.github/actions/lib/resources.ts';
import { stepCore, oidcIssuer, globalsOf } from './fakes.ts';

type Sent = { url: string; method: string; headers: Record<string, string>; body: unknown };

function serve(answer: (s: Sent) => { status: number; body: string }): Sent[] {
	const log: Sent[] = [];
	globalThis.fetch = (async (url: string, init: any = {}) => {
		const s: Sent = { url, method: init.method ?? 'GET', headers: init.headers ?? {}, body: init.body === undefined ? undefined : JSON.parse(init.body) };
		log.push(s);
		const a = url.startsWith('https://oidc.example') ? { status: 200, body: '{"value":"minted"}' } : answer(s);
		return { ok: a.status >= 200 && a.status < 300, status: a.status, text: async () => a.body, json: async () => JSON.parse(a.body) };
	}) as any;
	return log;
}

function run(inputs: Record<string, string>) {
	const { core, out } = stepCore();
	const github = { ref_name: 'main', sha: 'c0ffee' };
	return { g: globalsOf({ core, github, context: {}, octokit: {}, exec: {}, steps: {}, inputs: { server: 'https://bh.example', token: 'tok', ...inputs } }), out };
}

(async () => {
	oidcIssuer();
	assert.strictEqual(await mod.resolveToken('https://bh.example', 'given'), 'given', 'the token input wins');
	assert.strictEqual(await mod.resolveToken('https://bh.example', ''), '', 'no token and no OIDC is empty');
	oidcIssuer('https://oidc.example/t?a=1');
	const minted = serve(() => ({ status: 500, body: '' }));
	assert.strictEqual(await mod.resolveToken('https://bh.example', ''), 'minted');
	assert.strictEqual(minted[0].url, 'https://oidc.example/t?a=1&audience=https://bh.example', 'the OIDC token is minted for the server');

	let sent = serve(() => ({ status: 201, body: '{"id":3}' }));
	let r = run({ issuer: 'https://iss', subject_pattern: 'repo:o/*', scopes: 'read', audience: '', project_id: '9' });
	await mod.createOidcPolicy(r.g);
	assert.strictEqual(sent[0].url, 'https://bh.example/api/v1/oidc/policies');
	assert.deepStrictEqual(sent[0].body, { issuer: 'https://iss', subject_pattern: 'repo:o/*', scopes: 'read', audience: '', project_id: 9 });
	assert.strictEqual(sent[0].headers.Authorization, 'Bearer tok');
	assert.strictEqual(r.out.outputs.policy_id, '3');

	sent = serve(() => ({ status: 201, body: '{"token":"secret","details":{"id":4}}' }));
	r = run({ name: 'ci', scopes: 'read,write', project_id: '' });
	await mod.createToken(r.g);
	assert.deepStrictEqual(sent[0].body, { name: 'ci', scopes: 'read,write' }, 'no project_id is a global token');
	assert.strictEqual(r.out.outputs.token_value, 'secret');
	assert.strictEqual(r.out.outputs.token_id, '4');

	serve(() => ({ status: 403, body: 'denied' }));
	r = run({ name: 'ci', scopes: 'read', project_id: '' });
	await mod.createToken(r.g);
	assert.deepStrictEqual(r.out.failed, ['HTTP 403: denied']);

	sent = serve((s) => (s.method === 'POST' ? { status: 409, body: 'exists' } : { status: 200, body: '{"id":12,"name":"p"}' }));
	r = run({ name: 'p', description: 'd', homepage: '', license: 'MIT', private: 'true', versioning: 'auto' });
	await mod.createProject(r.g);
	assert.strictEqual(sent[0].body && (sent[0].body as any).is_private, true);
	assert.strictEqual(sent[1].url, 'https://bh.example/api/v1/projects/p', 'an existing project is read back');
	assert.strictEqual(r.out.outputs.id, '12');
	assert.deepStrictEqual(r.out.failed, []);

	serve((s) => (s.method === 'POST' ? { status: 409, body: 'exists' } : { status: 404, body: '' }));
	r = run({ name: 'p', description: '', homepage: '', license: '', private: 'false', versioning: 'auto' });
	await mod.createProject(r.g);
	assert.match(r.out.warned[0], /could not be read back/);
	assert.deepStrictEqual(r.out.failed, []);

	sent = serve(() => ({ status: 201, body: '{"version":"7"}' }));
	r = run({ project: 'p', version: '', git_branch: '', git_commit: '', notes: 'n', create_service: 'true', default_branch: '', apt_depends: 'bubblewrap | docker.io' });
	await mod.createRelease(r.g);
	assert.deepStrictEqual(sent[0].body, { git_branch: 'main', git_commit: 'c0ffee', notes: 'n', create_service: true, apt_depends: 'bubblewrap | docker.io' });
	assert.strictEqual(r.out.outputs.version, '7');

	r = run({ project: 'p', version: '', git_branch: '', git_commit: '', notes: '', create_service: 'maybe', default_branch: '', apt_depends: '' });
	await mod.createRelease(r.g);
	assert.match(r.out.failed[0], /create_service must be/);

	assert.strictEqual(mod.resourceURL('https://s', 'project', 'p', ''), 'https://s/api/v1/projects/p');
	assert.strictEqual(mod.resourceURL('https://s', 'release', 'p', '2'), 'https://s/api/v1/projects/p/releases/2');
	assert.deepStrictEqual(mod.resourceURL('https://s', 'release', 'p', ''), { error: 'version is required for resource=release' });
	assert.match((mod.resourceURL('https://s', 'tag', 'p', '') as { error: string }).error, /Unknown resource 'tag'/);

	sent = serve(() => ({ status: 200, body: '{"id":5}' }));
	r = run({ resource: 'release', project: 'p', version: '2' });
	await mod.getResource(r.g);
	assert.strictEqual(sent[0].url, 'https://bh.example/api/v1/projects/p/releases/2');
	assert.strictEqual(r.out.outputs.id, '5');

	sent = serve(() => ({ status: 200, body: '{"version":"2","artifacts":[{"os":"linux","arch":"amd64","sha256":"aa"}]}' }));
	r = run({ project: 'p', version: '2' });
	await mod.publishRelease(r.g);
	assert.strictEqual(sent[0].url, 'https://bh.example/api/v1/projects/p/releases/2/publish');
	assert.deepStrictEqual(r.out.failed, []);

	serve(() => ({ status: 200, body: '{"version":"2"}' }));
	r = run({ project: 'p', version: '2' });
	await mod.publishRelease(r.g);
	assert.match(r.out.failed[0], /returned no artifacts/, 'a publish that returns no artifacts fails');

	console.log('resources: all checks passed');
})();
