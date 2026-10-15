// The GitHub Deployment a buildhost publish registers in the calling repo.

import type { ActionGlobals } from './action-globals';

/** Registers the publish as an in-progress GitHub Deployment, and sets the deployment_id output. A loopback or plain-http server registers nothing. */
export async function createDeployment(g: ActionGlobals): Promise<void> {
	const { core, github, context, octokit, inputs } = g;
	core.setOutput('deployment_id', '');
	const deployServer: string = String(inputs.server);
	const deployHost: string = new URL(deployServer).hostname;
	// this check finds a target nothing outside the runner can reach
	if (deployHost === "localhost" || deployHost === "127.0.0.1" || deployHost === "::1" || !deployServer.startsWith("https://")) {
		// a loopback or plain-http target has nothing true to register as live
		core.info(`Skipping the GitHub Deployment: ${deployServer} is not a reachable https server, so its deployment URL would point nowhere`);
		return;
	}
	try {
		const project: string = String(inputs.project || github.repository.split('/').pop()!);
		const environment: string = String(inputs.deployment_environment || `buildhost/${project}`);
		// a bare SHA belongs to no branch.
		const ref: string = String(inputs.deployment_ref || inputs.git_commit || context.sha);
		const branch: string = String(inputs.git_branch || github.ref_name || '');
		const defaultBranch: string = context.payload.repository?.default_branch ?? '';
		const runUrl = `${context.serverUrl}/${context.repo.owner}/${context.repo.repo}/actions/runs/${context.runId}`;
		// every publish to a real server registers -- there's no opt-out
		const dep = await octokit.rest.repos.createDeployment({
			owner: context.repo.owner,
			repo: context.repo.repo,
			ref,
			environment,
			// the API default would try to merge the default branch into ref
			auto_merge: false,
			// the API default is "every commit status", and the org's all-builds status is still pending on this sha mid-run -> 409 Conflict
			required_contexts: [],
			transient_environment: false,
			production_environment: defaultBranch !== '' && branch === defaultBranch,
			description: `buildhost publish of ${project}`.slice(0, 140),
		});
		const id = (dep.data as any)?.id;
		if (typeof id !== 'number') {
			core.setFailed(`GitHub Deployment not created (HTTP ${dep.status}): the API returned no deployment id`);
			return;
		}
		core.setOutput('deployment_id', String(id));
		await octokit.rest.repos.createDeploymentStatus({
			owner: context.repo.owner,
			repo: context.repo.repo,
			deployment_id: id,
			state: 'in_progress',
			log_url: runUrl,
			description: 'Publishing to buildhost',
		});
		core.info(`Created GitHub Deployment ${id} in environment '${environment}'`);
	} catch (e) {
		const msg = e instanceof Error ? e.message : String(e);
		core.setFailed(`Could not create the GitHub Deployment (${msg}). Add 'deployments: write' to this job's permissions block -- job-level permissions REPLACE workflow-level ones, so add it alongside the ones the job already declares. Registering the publish is part of publishing and cannot be turned off.`);
	}
}

/** Posts the terminal status of the deployment createDeployment registered, from the outcome of the publish step. */
export async function finishDeployment(g: ActionGlobals): Promise<void> {
	const { core, context, octokit, steps } = g;
	try {
		const ok = steps.publish.outcome === 'success';
		const environmentUrl: string = steps.publish.outputs.deployment_url ?? '';
		const runUrl = `${context.serverUrl}/${context.repo.owner}/${context.repo.repo}/actions/runs/${context.runId}`;
		await octokit.rest.repos.createDeploymentStatus({
			owner: context.repo.owner,
			repo: context.repo.repo,
			deployment_id: Number(steps.deployment.outputs.deployment_id),
			state: ok ? 'success' : 'failure',
			log_url: runUrl,
			...(ok && environmentUrl !== '' ? { environment_url: environmentUrl } : {}),
			description: ok ? 'Published to buildhost' : 'buildhost publish failed',
		});
		core.info(`GitHub Deployment marked ${ok ? 'success' : 'failure'}`);
	} catch (e) {
		const msg = e instanceof Error ? e.message : String(e);
		core.setFailed(`Could not post the terminal GitHub Deployment status (${msg}); the deployment would be left stuck in_progress`);
	}
}
