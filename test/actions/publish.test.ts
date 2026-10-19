// Behavior tests for .github/actions/lib/publish.ts, against a fake fetch that answers the OIDC endpoint and the buildhost API.

import assert from 'node:assert';
import { publish, releaseBody, releaseVersionFrom, deploymentURL } from '../../.github/actions/lib/publish.ts';
import { tempDir, stepCore, oidcIssuer, globalsOf } from './fakes.ts';

type Answer = { status: number; body: string };

function globals(inputs: Record<string, string>) {
	const { core, out } = stepCore();
	const github = { repository: 'o/tool', ref_name: 'main', sha: 'c0ffee', run_id: '1' };
	const all = { server: 'https://bh.example', project: '', artifact_name: 'go-build', path: '', version: '', git_branch: '', git_commit: '', create_service: '', default_branch: '', apt_depends: '', depends: '', ...inputs };
	return { g: globalsOf({ core, github, context: {}, octokit: {}, exec: {}, inputs: all, steps: {} }), failed: out.failed, outputs: out.outputs };
}

// Answers each request by its method and path, and records it.
function fakeFetch(route: (method: string, urlPath: string) => Answer) {
	const seen: { method: string; url: string; body: unknown }[] = [];
	globalThis.fetch = (async (url: string, init?: { method?: string; body?: unknown }) => {
		const method = init?.method ?? 'GET';
		seen.push({ method, url, body: init?.body });
		if (url.startsWith('https://oidc.example')) return new Response(JSON.stringify({ value: 'tok' }));
		const a = route(method, new URL(url).pathname);
		return { status: a.status, text: async () => a.body, json: async () => JSON.parse(a.body) };
	}) as any;
	return seen;
}

(async () => {
	assert.strictEqual(releaseBody({ gitBranch: 'b', gitCommit: 'c', version: '', createService: '', defaultBranch: '', aptDepends: '', depends: ' ' }), '{"git_branch":"b","git_commit":"c"}', 'an empty input sends no field');
	assert.deepStrictEqual(JSON.parse(releaseBody({ gitBranch: 'b', gitCommit: 'c', version: '1.2.3', createService: 'false', defaultBranch: 'main', aptDepends: 'bubblewrap', depends: 'ascii-timeline, team/lib' })),
		{ git_branch: 'b', git_commit: 'c', version: '1.2.3', create_service: false, default_branch: 'main', apt_depends: 'bubblewrap', depends: ['ascii-timeline', 'team/lib'] });
	assert.deepStrictEqual(releaseVersionFrom('p', '', { status: 201, text: '{"version":"4"}' }), { rv: '4', existed: false });
	assert.deepStrictEqual(releaseVersionFrom('p', '1.0.0', { status: 409, text: '' }), { rv: '1.0.0', existed: true });
	assert.match(releaseVersionFrom('p', '', { status: 409, text: '' }) as string, /no explicit version/);
	assert.match(releaseVersionFrom('p', '', { status: 500, text: 'boom' }) as string, /failed \(500\): boom/);
	assert.strictEqual(deploymentURL('https://s', 'p', []), null);
	assert.strictEqual(deploymentURL('https://s', 'p', [{ proj: 'p/cli', rv: '3' }]), 'https://s/projects/p/cli/releases/3');
	assert.strictEqual(deploymentURL('https://s', 'p', [{ proj: 'p/cli', rv: '3' }, { proj: 'p', rv: '3' }]), 'https://s/projects/p', 'several projects name the repo-level one');

	const badService = globals({ create_service: 'yes' });
	await publish(badService.g);
	assert.match(badService.failed[0], /create_service must be/);

	const empty = globals({ path: tempDir('publish-', {}) });
	await publish(empty.g);
	assert.match(empty.failed[0], /is empty/);

	const files = { tool_linux_amd64: 'elf', tool_darwin_arm64: 'macho' };
	oidcIssuer();
	const noOidc = globals({ path: tempDir('publish-', files) });
	await publish(noOidc.g);
	assert.match(noOidc.failed[0], /OIDC not available/);

	oidcIssuer('https://oidc.example/token?x=1');

	const seen = fakeFetch((method, urlPath) => {
		if (urlPath === '/api/v1/server-info') return { status: 200, body: '{"max_direct_upload_bytes":1048576}' };
		if (method === 'POST' && urlPath === '/api/v1/projects/tool/releases') return { status: 201, body: '{"version":"5"}' };
		if (method === 'PUT') return { status: 201, body: '{}' };
		if (method === 'POST' && urlPath === '/api/v1/projects/tool/releases/5/publish') return { status: 200, body: '{}' };
		return { status: 404, body: '{"error":"unexpected"}' };
	});
	const full = globals({ path: tempDir('publish-', files) });
	await publish(full.g);
	assert.deepStrictEqual(full.failed, []);
	assert.strictEqual(full.outputs.deployment_url, 'https://bh.example/projects/tool/releases/5');
	const puts = seen.filter((s) => s.method === 'PUT').map((s) => new URL(s.url).pathname).sort();
	assert.deepStrictEqual(puts, ['/api/v1/projects/tool/releases/5/artifacts/darwin/arm64', '/api/v1/projects/tool/releases/5/artifacts/linux/amd64'], 'each platform uploads its own bytes');
	const create = seen.find((s) => s.method === 'POST' && s.url.endsWith('/releases'))!;
	assert.deepStrictEqual(JSON.parse(String(create.body)), { git_branch: 'main', git_commit: 'c0ffee' }, 'the branch and commit default to the run');

	fakeFetch((method, urlPath) => (method === 'POST' && urlPath.endsWith('/releases') ? { status: 409, body: '{}' } : { status: 200, body: '{}' }));
	const exists = globals({ path: tempDir('publish-', files) });
	await publish(exists.g);
	assert.match(exists.failed[0], /Release exists for tool, no explicit version/);

	console.log('publish: all checks passed');
})();
