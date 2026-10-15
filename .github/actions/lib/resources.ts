// The single-request buildhost actions: create an OIDC policy, a token, a project or a release, read one resource, and publish a release.

import type { ActionGlobals } from './action-globals.ts';
import { mintOidcToken } from './oidc.ts';

/** The token a request sends: the token input, else an OIDC token minted for the server when the job may mint one, else empty. */
export async function resolveToken(server: string, token: string): Promise<string> {
	if (token) return token;
	return (await mintOidcToken(server)) ?? '';
}

/** POSTs a JSON body with the bearer token, and answers the response status and text. */
async function postJSON(url: string, token: string, body: unknown): Promise<{ ok: boolean; status: number; text: string }> {
	const resp = await fetch(url, {
		method: 'POST',
		headers: { 'Authorization': `Bearer ${token}`, 'Content-Type': 'application/json' },
		body: JSON.stringify(body),
	});
	return { ok: resp.ok, status: resp.status, text: await resp.text() };
}

/** buildhost-create-oidc-policy: outputs response and policy_id. */
export async function createOidcPolicy(g: ActionGlobals): Promise<void> {
	const { core, inputs } = g;
	const server = String(inputs.server);
	const token = await resolveToken(server, String(inputs.token));
	const projectId = String(inputs.project_id);
	const body: Record<string, string | number> = {
		issuer: String(inputs.issuer),
		subject_pattern: String(inputs.subject_pattern),
		scopes: String(inputs.scopes),
		audience: String(inputs.audience),
	};
	if (projectId) body['project_id'] = Number(projectId);
	const resp = await postJSON(`${server}/api/v1/oidc/policies`, token, body);
	if (!resp.ok) { core.setFailed(`HTTP ${resp.status}: ${resp.text}`); return; }
	const data = JSON.parse(resp.text);
	core.info(JSON.stringify(data, null, 2));
	core.setOutput('response', resp.text);
	core.setOutput('policy_id', String(data.id));
}

/** buildhost-create-token: outputs response, token_value and token_id. */
export async function createToken(g: ActionGlobals): Promise<void> {
	const { core, inputs } = g;
	const server = String(inputs.server);
	const token = await resolveToken(server, String(inputs.token));
	const projectId = String(inputs.project_id);
	const body: Record<string, string | number> = { name: String(inputs.name), scopes: String(inputs.scopes) };
	if (projectId) body['project_id'] = Number(projectId);
	const resp = await postJSON(`${server}/api/v1/tokens`, token, body);
	if (!resp.ok) { core.setFailed(`HTTP ${resp.status}: ${resp.text}`); return; }
	const data = JSON.parse(resp.text);
	core.info(JSON.stringify(data, null, 2));
	core.setOutput('response', resp.text);
	core.setOutput('token_value', data.token);
	core.setOutput('token_id', String(data.details.id));
}

/** buildhost-create-project: outputs response and id. A project that already exists is a success, read back for the outputs. */
export async function createProject(g: ActionGlobals): Promise<void> {
	const { core, inputs } = g;
	const server = String(inputs.server);
	const token = await resolveToken(server, String(inputs.token));
	const name = String(inputs.name);
	const resp = await postJSON(`${server}/api/v1/projects`, token, {
		name,
		description: String(inputs.description),
		homepage: String(inputs.homepage),
		license: String(inputs.license),
		is_private: String(inputs.private) === 'true',
		versioning: String(inputs.versioning),
	});
	if (resp.status === 409) {
		core.info(`Project "${name}" already exists; treating as success.`);
		const getResp = await fetch(`${server}/api/v1/projects/${name}`, { headers: { 'Authorization': `Bearer ${token}` } });
		if (!getResp.ok) {
			core.warning(`Project exists but could not be read back (HTTP ${getResp.status}); id output left empty.`);
			return;
		}
		const existing = await getResp.text();
		core.setOutput('response', existing);
		core.setOutput('id', String(JSON.parse(existing).id));
		return;
	}
	if (!resp.ok) { core.setFailed(`HTTP ${resp.status}: ${resp.text}`); return; }
	const data = JSON.parse(resp.text);
	core.info(JSON.stringify(data, null, 2));
	core.setOutput('response', resp.text);
	core.setOutput('id', String(data.id));
}

/** buildhost-create-release: outputs response and version. create_service, default_branch and apt_depends go only when set. */
export async function createRelease(g: ActionGlobals): Promise<void> {
	const { core, github, inputs } = g;
	const server = String(inputs.server);
	const project = String(inputs.project);
	const version = String(inputs.version);
	const body: Record<string, string | boolean> = {
		git_branch: String(inputs.git_branch || github.ref_name),
		git_commit: String(inputs.git_commit || github.sha),
		notes: String(inputs.notes),
	};
	if (version) body['version'] = version;
	const brewService = String(inputs.create_service);
	if (brewService === 'true' || brewService === 'false') {
		body['create_service'] = brewService === 'true';
	} else if (brewService !== '') {
		core.setFailed(`create_service must be 'true', 'false', or empty; got '${brewService}'`);
		return;
	}
	const defaultBranch = String(inputs.default_branch);
	if (defaultBranch) body['default_branch'] = defaultBranch;
	const aptDepends = String(inputs.apt_depends);
	if (aptDepends) body['apt_depends'] = aptDepends;
	const token = await resolveToken(server, String(inputs.token));
	// an older server ignores a field it predates
	const resp = await postJSON(`${server}/api/v1/projects/${project}/releases`, token, body);
	if (!resp.ok) { core.setFailed(`HTTP ${resp.status}: ${resp.text}`); return; }
	const data = JSON.parse(resp.text);
	core.info(JSON.stringify(data, null, 2));
	core.setOutput('response', resp.text);
	core.setOutput('version', data.version);
}

/** The API path of a resource the get action reads, or the reason it cannot. */
export function resourceURL(server: string, resource: string, project: string, version: string): string | { error: string } {
	if (resource === 'project') return `${server}/api/v1/projects/${project}`;
	if (resource !== 'release') return { error: `Unknown resource '${resource}'. Valid: project, release` };
	if (!version) return { error: 'version is required for resource=release' };
	return `${server}/api/v1/projects/${project}/releases/${version}`;
}

/** buildhost-get: outputs response and id. */
export async function getResource(g: ActionGlobals): Promise<void> {
	const { core, inputs } = g;
	const server = String(inputs.server);
	const url = resourceURL(server, String(inputs.resource), String(inputs.project), String(inputs.version));
	if (typeof url !== 'string') { core.setFailed(url.error); return; }
	const token = await resolveToken(server, String(inputs.token));
	const headers: Record<string, string> = {};
	if (token) headers['Authorization'] = `Bearer ${token}`;
	const resp = await fetch(url, { headers });
	const text = await resp.text();
	if (!resp.ok) { core.setFailed(`HTTP ${resp.status}: ${text}`); return; }
	const data = JSON.parse(text);
	core.info(JSON.stringify(data, null, 2));
	core.setOutput('response', text);
	core.setOutput('id', String(data.id));
}

/** The published release as the publish endpoint answers it. */
export interface PublishedRelease {
	version?: string;
	artifacts?: { os?: string; arch?: string; sha256?: string }[];
}

/** buildhost-publish-release: publishes the release and outputs response. A publish that returns no artifacts fails, because nothing can be recorded for it. */
export async function publishRelease(g: ActionGlobals): Promise<void> {
	const { core, inputs } = g;
	const server = String(inputs.server);
	const token = await resolveToken(server, String(inputs.token));
	const project = String(inputs.project);
	const version = String(inputs.version);
	const resp = await fetch(`${server}/api/v1/projects/${project}/releases/${version}/publish`, {
		method: 'POST',
		headers: { 'Authorization': `Bearer ${token}` },
	});
	const text = await resp.text();
	if (!resp.ok) { core.setFailed(`HTTP ${resp.status}: ${text}`); return; }
	core.info(JSON.stringify(JSON.parse(text), null, 2));
	core.setOutput('response', text);
	// the publish response carries the artifacts it made public; each becomes one storage record
	const published = JSON.parse(text) as PublishedRelease;
	if ((published.artifacts ?? []).length === 0) {
		core.setFailed(`Publishing ${project}/${version} returned no artifacts, so nothing can be recorded on the org's linked artifacts page. The publish endpoint returns them as of the buildhost release that added artifact-metadata recording -- this server is older than that, which is a version to upgrade rather than a condition to wait out. A publish that records nothing must not report success.`);
	}
}
