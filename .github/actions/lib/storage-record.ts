// Recording what buildhost stores on GitHub's linked artifacts page.
//
// buildhost stores three kinds of thing, so this exports three functions and
// each publish composite calls the one matching what it published. Callers pass
// what they PUBLISHED; every record field, URL and message is derived here --
// the artifact_url/digest agreement is a correctness rule, and composites
// deriving it independently is how it drifts.
//
// Loaded by `require(".../storage-record.ts")` (node strips the types) typed as
// `typeof import(".../storage-record")`. See docs/artifact-storage-records.md.

const MAX_ARTIFACT_URL = 152;

export interface Core { info(m: string): void; setFailed(m: string): void }
export interface Context { repo: { owner: string; repo: string } }
export interface Octokit {
	request(route: string, params: Record<string, unknown>): Promise<unknown>;
	rest: { users: { getByUsername(p: { username: string }): Promise<{ data: { type?: string } }> } };
}

/** One published slot. `project`/`version` default to the call's. */
export interface PublishedArtifact {
	os?: string; arch?: string;
	/** Bare hex sha256 of the bytes as UPLOADED; missing = a failed publish. */
	sha256?: string;
	project?: string; version?: string;
}

interface Record_ {
	name: string; version: string; digest: string; registry_url: string;
	repository: string; path?: string; artifact_url?: string;
}

/** Release artifacts (buildhost-publish, buildhost-publish-release). */
export async function recordReleaseArtifacts(octokit: Octokit, core: Core, context: Context, params: {
	server: string; project?: string; version?: string; artifacts: readonly PublishedArtifact[];
}): Promise<boolean> {
	// Downloads live on the dl.<host> subdomain -- buildhost dispatches services
	// by the first label of the request Host.
	const dl = (() => { const u = new URL(params.server); u.host = `dl.${u.host}`; return u.origin; })();
	const records: Record_[] = [];
	for (const a of params.artifacts) {
		const project = a.project ?? params.project ?? '';
		const version = a.version ?? params.version ?? '';
		const path = `${a.os ?? ''}/${a.arch ?? ''}`;
		if (!a.sha256) {
			core.setFailed(`Published artifact ${project} ${path} carried no sha256; cannot record what buildhost stored`);
			return false;
		}
		records.push({
			name: project, version, digest: `sha256:${a.sha256}`, registry_url: params.server,
			repository: project, path,
			// debug=1 is the only download returning the uploaded bytes verbatim
			// -- buildhost strips and repackages on demand -- so it is the one
			// URL whose bytes hash to the recorded digest.
			artifact_url: `${dl}/${project}?v=${encodeURIComponent(version)}&os=${a.os ?? ''}&arch=${a.arch ?? ''}&debug=1`,
		});
	}
	return post(octokit, core, context, records, (r) => `${r.name} ${r.path}`);
}

/** A static site archive (buildhost-publish-site). */
export async function recordSite(octokit: Octokit, core: Core, context: Context, params: {
	server: string; project: string; branch: string; version: string; sha256: string;
}): Promise<boolean> {
	// No artifact_url: a site is served unpacked, so no URL returns the archive
	// bytes this digest covers, and a record must never point at bytes that hash
	// to something else.
	return post(octokit, core, context, [{
		name: params.project, version: params.version, digest: `sha256:${params.sha256}`,
		registry_url: params.server, repository: params.project, path: `branch/${params.branch}`,
	}], (r) => `${r.name} ${r.path}`);
}

/**
 * A pushed image (buildhost-publish-docker), named by its buildhost-bound
 * reference `<oci-host>/<repository>:<tag>`. Foreign references are another
 * registry's inventory to account for and are never passed here.
 */
export async function recordImage(octokit: Octokit, core: Core, context: Context, params: {
	reference: string; digest: string;
}): Promise<boolean> {
	const { reference: ref, digest } = params;
	const slash = ref.indexOf('/');
	const colon = ref.lastIndexOf(':');
	if (slash < 0 || colon < slash) {
		core.setFailed(`Could not parse the buildhost image reference '${ref}'; cannot record what was stored`);
		return false;
	}
	const repository = ref.slice(slash + 1, colon);
	const registry = `https://${ref.slice(0, slash)}`;
	return post(octokit, core, context, [{
		name: repository, version: ref.slice(colon + 1), digest, registry_url: registry,
		repository, artifact_url: `${registry}/v2/${repository}/manifests/${digest}`,
	}], (r) => r.repository);
}

// A loopback server is not a registry anything can fetch from (buildhost's own
// e2e spawns one), so a record pointing at it would be an unreachable row. A
// property of the target, not a caller-facing switch.
function unreachable(url: string): string {
	let host: string;
	try { host = new URL(url).hostname; } catch { return `${url} is not a valid URL`; }
	return host === 'localhost' || host === '127.0.0.1' || host === '::1' || !url.startsWith('https://')
		? `${url} is not a reachable https registry (the API accepts https URLs only)`
		: '';
}

// What a failing POST actually tells you. A 404 is NOT proof of a missing
// grant: the endpoint is org-scoped, so it also 404s when the owner names no
// organization this token can see. The owner-type probe's answer separates them.
function failureDetail(status: number | undefined, msg: string, owner: string, ownerType: string): string {
	const cannotTurnOff = 'Recording what buildhost stores is part of publishing and cannot be turned off.';
	if (status === 403) {
		return `(HTTP 403: ${msg}). The token was refused for artifact metadata: this job must grant 'artifact-metadata: write' -- job-level permissions REPLACE workflow-level ones, so a job-level block must list it alongside the ones it already declares. ${cannotTurnOff}`;
	}
	if (status === 404) {
		const probe = ownerType === '' ? 'failed, so the owner type is unknown' : `reported '${ownerType}'`;
		return `(HTTP 404: ${msg}). POST /orgs/${owner}/artifacts/metadata/storage-record found no organization named ${owner} that this token can see, and GET /users/${owner} ${probe} -- a user-account owner is skipped rather than recorded. If ${owner} is an organization, the token is missing 'artifact-metadata: write' (job-level permissions REPLACE workflow-level ones). ${cannotTurnOff}`;
	}
	return `(${msg}).`;
}

// Returns false only after calling setFailed, so a caller's `if (!ok) return`
// needs no message of its own.
async function post(octokit: Octokit, core: Core, context: Context, records: Record_[], label: (r: Record_) => string): Promise<boolean> {
	if (records.length === 0) return true;
	const plural = records.length === 1 ? 'the storage record' : `${records.length} storage records`;

	const skip = unreachable(records[0].registry_url);
	if (skip !== '') {
		core.info(`Skipping ${plural}: ${skip}`);
		return true;
	}

	const owner = context.repo.owner;
	for (const rec of records) {
		// A record without the link back to the bytes its digest covers is a
		// silent downgrade, so an over-long URL fails rather than dropping it.
		if (rec.artifact_url !== undefined && rec.artifact_url.length > MAX_ARTIFACT_URL) {
			core.setFailed(`Cannot record ${label(rec)}: its artifact_url is ${rec.artifact_url.length} chars, over the API's ${MAX_ARTIFACT_URL}-char limit. Recording it without the URL would silently drop the only link back to the bytes the digest covers; shorten the project name or change the download URL scheme.`);
			return false;
		}
		try {
			// github_repository takes the repo NAME only -- an owner/repo value
			// is rejected, and the owner is already the path parameter.
			await octokit.request('POST /orgs/{org}/artifacts/metadata/storage-record', {
				org: owner, ...rec, github_repository: context.repo.repo, status: 'active', return_records: false,
			});
			core.info(`Recorded ${label(rec)} ${rec.digest} on ${owner}'s linked artifacts page`);
		} catch (e) {
			const status = (e as { status?: number }).status;
			const msg = e instanceof Error ? e.message : String(e);
			// A personal account has no linked artifacts page, so the org-scoped
			// endpoint 404s no matter what the token grants. Probed only on a 404
			// (orgs pay no extra call) and fails closed: an unreadable owner type
			// leaves the failure standing, so a genuine permissions 404 on an org
			// still fails the publish.
			const ownerType = status === 404
				? await octokit.rest.users.getByUsername({ username: owner }).then((r) => r.data.type ?? '').catch(() => '')
				: '';
			if (ownerType === 'User') {
				core.info(`Skipping ${plural}: ${owner} is a user account, which has no linked artifacts page`);
				return true;
			}
			core.setFailed(`Could not record ${label(rec)} on ${owner}'s linked artifacts page ${failureDetail(status, msg, owner, ownerType)}`);
			return false;
		}
	}
	return true;
}
