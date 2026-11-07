// The GitHub Deployment a buildhost publish registers in the calling repo, for buildhost-publish, -publish-docker and -publish-site.

import type { ActionGlobals } from './action-globals';
import { githubAPI } from './github-api.ts';

/** What one publish registers: the ref, the environment it names when deployment_environment is empty, and how its statuses read. */
export interface DeploymentSpec {
	ref: string;
	environment: string;
	production: boolean;
	/** Whether GitHub may expect the environment to go away again. */
	transient: boolean;
	description: string;
	/** The description of the in-progress status. */
	inProgress: string;
}

/** The project a deployment is for: the project input, else the repository name. */
export function projectOf(g: ActionGlobals): string {
	return String(g.inputs.project || g.github.repository.split('/').pop()!);
}

export function siteBranch(branch: string): string {
	return branch.replace(/[^A-Za-z0-9._-]/g, '-');
}

/** Registers an in-progress GitHub Deployment, and sets the deployment_id output. A loopback or plain-http server registers nothing. */
export async function createDeployment(g: ActionGlobals, spec: DeploymentSpec): Promise<void> {
	const { core, context, inputs } = g;
	core.setOutput('deployment_id', '');
	const deployServer: string = String(inputs.server);
	const deployHost: string = new URL(deployServer).hostname;
	// this check finds a target nothing outside the runner can reach
	if (deployHost === "localhost" || deployHost === "127.0.0.1" || deployHost === "::1" || !deployServer.startsWith("https://")) {
		core.info(`Skipping the GitHub Deployment: ${deployServer} is not a reachable https server, so its deployment URL would point nowhere`);
		return;
	}
	try {
		const octokit = await githubAPI(g);
		const environment: string = String(inputs.deployment_environment || spec.environment);
		const runUrl = `${context.serverUrl}/${context.repo.owner}/${context.repo.repo}/actions/runs/${context.runId}`;
		// every publish to a real server registers -- there's no opt-out
		const dep = await octokit.rest.repos.createDeployment({
			owner: context.repo.owner,
			repo: context.repo.repo,
			ref: spec.ref,
			environment,
			// the API default would try to merge the default branch into ref
			auto_merge: false,
			// the API default is "every commit status", and the org's all-builds status is still pending on this sha mid-run -> 409 Conflict
			required_contexts: [],
			transient_environment: spec.transient,
			production_environment: spec.production,
			description: spec.description.slice(0, 140),
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
			description: spec.inProgress,
		});
		core.info(`Created GitHub Deployment ${id} in environment '${environment}'`);
	} catch (e) {
		const msg = e instanceof Error ? e.message : String(e);
		core.setFailed(`Could not create the GitHub Deployment (${msg}). Add 'deployments: write' to this job's permissions block -- job-level permissions REPLACE workflow-level ones, so add it alongside the ones the job already declares. Registering the publish is part of publishing and cannot be turned off.`);
	}
}

/** Posts the terminal status of a deployment createDeployment registered. */
export async function finishDeployment(g: ActionGlobals, ok: boolean, environmentUrl: string, descriptions: { success: string; failure: string }): Promise<void> {
	const { core, context, steps } = g;
	try {
		const octokit = await githubAPI(g);
		const runUrl = `${context.serverUrl}/${context.repo.owner}/${context.repo.repo}/actions/runs/${context.runId}`;
		await octokit.rest.repos.createDeploymentStatus({
			owner: context.repo.owner,
			repo: context.repo.repo,
			deployment_id: Number(steps.deployment.outputs.deployment_id),
			state: ok ? 'success' : 'failure',
			log_url: runUrl,
			...(ok && environmentUrl !== '' ? { environment_url: environmentUrl } : {}),
			description: ok ? descriptions.success : descriptions.failure,
		});
		core.info(`GitHub Deployment marked ${ok ? 'success' : 'failure'}`);
	} catch (e) {
		const msg = e instanceof Error ? e.message : String(e);
		core.setFailed(`Could not post the terminal GitHub Deployment status (${msg}); the deployment would be left stuck in_progress`);
	}
}

/** buildhost-publish's deployment. Its ref is deployment_ref, else git_commit, else the run sha, because a bare SHA belongs to no branch. */
export async function createPublishDeployment(g: ActionGlobals): Promise<void> {
	const { github, context, inputs } = g;
	const branch = String(inputs.git_branch || github.ref_name || '');
	const defaultBranch: string = context.payload.repository?.default_branch ?? '';
	const project = projectOf(g);
	await createDeployment(g, {
		ref: String(inputs.deployment_ref || inputs.git_commit || context.sha),
		environment: `buildhost/${project}`,
		production: defaultBranch !== '' && branch === defaultBranch,
		transient: false,
		description: `buildhost publish of ${project}`,
		inProgress: 'Publishing to buildhost',
	});
}

/** buildhost-publish's terminal status, from the outcome of its publish step and the deployment_url it set. */
export async function finishPublishDeployment(g: ActionGlobals): Promise<void> {
	const publish = g.steps.publish;
	await finishDeployment(g, publish.outcome === 'success', publish.outputs.deployment_url ?? '', { success: 'Published to buildhost', failure: 'buildhost publish failed' });
}

/** buildhost-publish-docker's deployment. Its ref is deployment_ref, else the run sha, and only a push to the default branch is production. */
export async function createDockerDeployment(g: ActionGlobals): Promise<void> {
	const { context, inputs } = g;
	const defaultBranch: string = context.payload.repository?.default_branch ?? '';
	const project = projectOf(g);
	await createDeployment(g, {
		ref: String(inputs.deployment_ref || context.sha),
		environment: `buildhost/${project}`,
		production: defaultBranch !== '' && context.ref === `refs/heads/${defaultBranch}`,
		transient: false,
		description: `buildhost docker publish of ${project}`,
		inProgress: 'Publishing Docker image to buildhost',
	});
}

/** Whether a docker publish succeeded: the build succeeded, and no step around it failed or was cancelled. A skipped push is fine. */
export function dockerPublishOk(steps: ActionGlobals['steps']): boolean {
	const build = steps.build?.outcome ?? 'skipped';
	const outcomes = [steps.refs?.outcome, build, steps['push-buildhost']?.outcome, steps['push-foreign']?.outcome];
	return build === 'success' && outcomes.every((o) => o !== 'failure' && o !== 'cancelled');
}

/** buildhost-publish-docker's terminal status. A success names the project page as the live URL. */
export async function finishDockerDeployment(g: ActionGlobals): Promise<void> {
	const ok = dockerPublishOk(g.steps);
	await finishDeployment(g, ok, ok ? `${String(g.inputs.server)}/projects/${projectOf(g)}` : '', { success: 'Docker image published to buildhost', failure: 'buildhost docker publish failed' });
}

/** buildhost-publish-site's deployment, one environment per site branch. A pr-<n> branch is transient and never production. */
export async function createSiteDeployment(g: ActionGlobals): Promise<void> {
	const { context, inputs } = g;
	const project = projectOf(g);
	const branch = siteBranch(String(inputs.branch));
	const transient = /^pr-\d+$/.test(branch);
	const defaultBranch: string = context.payload.repository?.default_branch ?? '';
	await createDeployment(g, {
		ref: String(inputs.deployment_ref || inputs.git_commit || context.sha),
		environment: `buildhost/${project}/${branch}`,
		production: !transient && defaultBranch !== '' && branch === defaultBranch,
		transient,
		description: `buildhost site publish of ${project} (branch ${branch})`,
		inProgress: 'Publishing site to buildhost',
	});
}

/** buildhost-publish-site's terminal status. A success names the published site as the live URL. */
export async function finishSiteDeployment(g: ActionGlobals): Promise<void> {
	const publish = g.steps.publish;
	await finishDeployment(g, publish.outcome === 'success', publish.outputs.site_url ?? '', { success: 'Site published to buildhost', failure: 'buildhost site publish failed' });
}
