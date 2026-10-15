// Behavior tests for .github/actions/lib/docker.ts: the default tags, the digest a layout names, and the push call.

import assert from 'node:assert';
import * as docker from '../../.github/actions/lib/docker.ts';
import { tempDir, oidcIssuer, globalsOf } from './fakes.ts';

(async () => {
	assert.deepStrictEqual(docker.defaultTagList('main', 'refs/heads/main', 'c0ffee', 'main'), ['c0ffee', 'main', 'latest'], 'the default branch also tags latest');
	assert.deepStrictEqual(docker.defaultTagList('claude/Foo', 'refs/heads/claude/Foo', 'c0ffee', 'main'), ['c0ffee', 'claude-foo'], 'a branch name becomes a bare docker tag');
	assert.deepStrictEqual(docker.defaultTagList('.hidden', 'refs/tags/.hidden', 'c0ffee', 'main'), ['c0ffee', 'hidden']);

	const layout = tempDir('layout-', { 'index.json': '{"manifests":[{"digest":"sha256:abc"}]}' });
	assert.strictEqual(docker.layoutDigest(layout), 'sha256:abc');
	assert.strictEqual(docker.layoutDigest(tempDir('layout-', { 'index.json': '{}' })), '', 'a layout with no manifest names no digest');

	const ran: { cmd: string; args: string[]; token: string | undefined }[] = [];
	const out = { failed: [] as string[], secrets: [] as string[], outputs: {} as Record<string, string> };
	const fake = {
		core: { info: () => {}, setFailed: (m: string) => out.failed.push(m), setSecret: (s: string) => out.secrets.push(s), setOutput: (k: string, v: string) => { out.outputs[k] = v; } },
		exec: { exec: async (cmd: string, args: string[], opts: { env: Record<string, string> }) => { ran.push({ cmd, args, token: opts.env.BUILDHOST_TOKEN }); return 0; } },
		github: {}, context: {}, octokit: {}, steps: {},
		inputs: { server: 'https://pazer.build', token: 'tok', image: layout, refs: 'oci.pazer.build/p:v1\n\n oci.pazer.build/p:latest \n', record: 'true' },
	};
	process.env.RUNNER_TEMP = '/runner/temp';
	await docker.dockerPush(globalsOf(fake));
	assert.deepStrictEqual(out.failed, []);
	assert.deepStrictEqual(ran, [{ cmd: '/runner/temp/buildhost-cli', args: ['docker-push', '--server', 'https://pazer.build', '--image', layout, 'oci.pazer.build/p:v1', 'oci.pazer.build/p:latest'], token: 'tok' }]);
	assert.deepStrictEqual(out.secrets, ['tok'], 'the token is masked');
	assert.strictEqual(out.outputs.digest, 'sha256:abc');

	oidcIssuer();
	await docker.dockerPush(globalsOf({ ...fake, inputs: { ...fake.inputs, token: '' } }));
	assert.match(out.failed[0], /OIDC is unavailable/, 'no token and no OIDC fails');

	await docker.dockerPush(globalsOf({ ...fake, inputs: { ...fake.inputs, refs: '\n' } }));
	assert.match(out.failed[1], /refs is empty/);

	console.log('docker: all checks passed');
})();
