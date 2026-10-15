// Behavior tests for .github/actions/lib/deployment.ts. A rejected check is an unhandled rejection, which exits non-zero.

import assert from 'node:assert';
import { createPublishDeployment as createDeployment, finishPublishDeployment as finishDeployment, createDockerDeployment, finishDockerDeployment, dockerPublishOk } from '../../.github/actions/lib/deployment.ts';
import { globalsOf } from './fakes.ts';

function fakeGlobals(over: { inputs?: Record<string, string>; steps?: unknown; createDeployment?: (a: any) => any }) {
	const calls: { name: string; args: any }[] = [];
	const failed: string[] = [];
	const outputs: Record<string, string> = {};
	const core = {
		info: () => {},
		setFailed: (m: string) => failed.push(m),
		setOutput: (k: string, v: string) => { outputs[k] = v; },
	};
	const octokit = {
		rest: {
			repos: {
				createDeployment: async (a: any) => { calls.push({ name: 'createDeployment', args: a }); return over.createDeployment ? over.createDeployment(a) : { status: 201, data: { id: 7 } }; },
				createDeploymentStatus: async (a: any) => { calls.push({ name: 'createDeploymentStatus', args: a }); return { status: 201, data: {} }; },
			},
		},
	};
	const context = {
		sha: 'c0ffee', serverUrl: 'https://github.com', runId: 42,
		repo: { owner: 'o', repo: 'r' },
		payload: { repository: { default_branch: 'main' } },
	};
	const github = { repository: 'o/r', ref_name: 'main' };
	const inputs = { server: 'https://pazer.build', project: '', deployment_environment: '', deployment_ref: '', git_commit: '', git_branch: '', ...(over.inputs ?? {}) };
	return { g: globalsOf({ core, github, context, octokit, exec: {}, inputs, steps: over.steps ?? {} }), calls, failed, outputs };
}

const steps = (outcome: string) => ({ publish: { outcome, outputs: { deployment_url: 'https://pazer.build/projects/r' } }, deployment: { outputs: { deployment_id: '7' } } });

(async () => {
	const loopback = fakeGlobals({ inputs: { server: 'http://localhost:18080' } });
	await createDeployment(loopback.g);
	assert.deepStrictEqual(loopback.calls, [], 'a loopback server registers nothing');
	assert.deepStrictEqual(loopback.outputs, { deployment_id: '' });

	const plain = fakeGlobals({});
	await createDeployment(plain.g);
	assert.strictEqual(plain.calls[0].name, 'createDeployment');
	assert.strictEqual(plain.calls[0].args.environment, 'buildhost/r', 'the environment defaults to buildhost/<repo>');
	assert.strictEqual(plain.calls[0].args.ref, 'c0ffee', 'the ref defaults to the run sha');
	assert.strictEqual(plain.calls[0].args.production_environment, true, 'the default branch is production');
	assert.deepStrictEqual(plain.calls[0].args.required_contexts, []);
	assert.strictEqual(plain.calls[1].args.state, 'in_progress');
	assert.strictEqual(plain.calls[1].args.log_url, 'https://github.com/o/r/actions/runs/42');
	assert.strictEqual(plain.outputs.deployment_id, '7');
	assert.deepStrictEqual(plain.failed, []);

	const named = fakeGlobals({ inputs: { project: 'p', deployment_ref: 'feature', git_branch: 'feature' } });
	await createDeployment(named.g);
	assert.strictEqual(named.calls[0].args.environment, 'buildhost/p');
	assert.strictEqual(named.calls[0].args.ref, 'feature', 'deployment_ref wins over the commit');
	assert.strictEqual(named.calls[0].args.production_environment, false);

	const noId = fakeGlobals({ createDeployment: () => ({ status: 202, data: { message: 'merged' } }) });
	await createDeployment(noId.g);
	assert.match(noId.failed[0], /no deployment id/, 'a 2xx with no id fails');

	const refused = fakeGlobals({ createDeployment: () => { throw new Error('Resource not accessible by integration'); } });
	await createDeployment(refused.g);
	assert.match(refused.failed[0], /deployments: write/, 'a refused call names the missing permission');

	const ok = fakeGlobals({ steps: steps('success') });
	await finishDeployment(ok.g);
	assert.strictEqual(ok.calls[0].args.deployment_id, 7);
	assert.strictEqual(ok.calls[0].args.state, 'success');
	assert.strictEqual(ok.calls[0].args.environment_url, 'https://pazer.build/projects/r');

	const failed = fakeGlobals({ steps: steps('failure') });
	await finishDeployment(failed.g);
	assert.strictEqual(failed.calls[0].args.state, 'failure');
	assert.strictEqual(failed.calls[0].args.environment_url, undefined, 'a failed publish names no live URL');

	const docker = fakeGlobals({ inputs: { deployment_ref: '' } });
	(docker.g.context as any).ref = 'refs/heads/main';
	await createDockerDeployment(docker.g);
	assert.strictEqual(docker.calls[0].args.ref, 'c0ffee', 'a docker publish ignores git_commit');
	assert.strictEqual(docker.calls[0].args.production_environment, true, 'a push to the default branch is production');
	assert.match(docker.calls[0].args.description, /^buildhost docker publish of r/);
	assert.strictEqual(docker.calls[1].args.description, 'Publishing Docker image to buildhost');

	const ran = (outcome: string) => ({ outcome, conclusion: outcome, outputs: {} });
	const built = { refs: ran('success'), build: ran('success'), 'push-buildhost': ran('skipped'), 'push-foreign': ran('skipped') };
	assert.strictEqual(dockerPublishOk(built), true, 'a skipped push is fine');
	assert.strictEqual(dockerPublishOk({ ...built, build: ran('skipped') }), false, 'a build that never ran is not a success');
	assert.strictEqual(dockerPublishOk({ ...built, 'push-foreign': ran('failure') }), false);

	const dockerDone = fakeGlobals({ steps: { ...built, deployment: { outputs: { deployment_id: '9' } } } });
	await finishDockerDeployment(dockerDone.g);
	assert.strictEqual(dockerDone.calls[0].args.state, 'success');
	assert.strictEqual(dockerDone.calls[0].args.environment_url, 'https://pazer.build/projects/r');
	assert.strictEqual(dockerDone.calls[0].args.description, 'Docker image published to buildhost');

	console.log('deployment: all checks passed');
})();
