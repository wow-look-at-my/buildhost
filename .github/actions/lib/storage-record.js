// Recording what buildhost stores on the org's linked artifacts page.
//
// buildhost stores exactly three kinds of thing, so this module exports three
// functions -- one per kind -- and each publish composite calls the one that
// matches what it just published. Callers pass what they PUBLISHED (project,
// version, slots, digests); every record field, URL and message is derived
// here, so a caller never restates the record shape. That is deliberate: the
// artifact_url/digest agreement below is a correctness rule, and four
// composites deriving it independently is how it drifts.
//
// See docs/artifact-storage-records.md.

const MAX_ARTIFACT_URL = 152;

// ---------------------------------------------------------------- public API

/**
 * Release artifacts (buildhost-publish, buildhost-publish-release). Entries may
 * carry their own project/version, else the top-level ones apply -- one call
 * covers a single release or a whole namespaced fan-out.
 */
async function recordReleaseArtifacts(octokit, core, context, params) {
	const records = [];
	for (const a of params.artifacts) {
		const project = a.project ?? params.project ?? '';
		const version = a.version ?? params.version ?? '';
		const slot = `${a.os ?? ''}/${a.arch ?? ''}`;
		if (!a.sha256) {
			core.setFailed(`Published artifact ${project} ${slot} carried no sha256; cannot record what buildhost stored`);
			return false;
		}
		records.push({
			name: project,
			version,
			digest: `sha256:${a.sha256}`,
			registry_url: params.server,
			repository: project,
			path: slot,
			// debug=1 is the only download that returns the uploaded bytes
			// verbatim -- buildhost strips and repackages on demand -- so it is
			// the one URL whose bytes hash to the recorded digest.
			artifact_url: `${downloadOrigin(params.server)}/${project}?v=${encodeURIComponent(version)}&os=${a.os ?? ''}&arch=${a.arch ?? ''}&debug=1`,
		});
	}
	return post(octokit, core, context, records, (rec) => `${rec.name} ${rec.path}`);
}

/** A static site archive (buildhost-publish-site). */
async function recordSite(octokit, core, context, params) {
	return post(
		octokit, core, context,
		[{
			name: params.project,
			version: params.version,
			digest: `sha256:${params.sha256}`,
			registry_url: params.server,
			repository: params.project,
			path: `branch/${params.branch}`,
			// Deliberately no artifact_url: a site is served unpacked, so no URL
			// returns the archive bytes this digest covers, and a record must
			// never point at bytes that hash to something else.
		}],
		() => `${params.project} branch/${params.branch}`,
	);
}

/**
 * A pushed image (buildhost-publish-docker), named by its buildhost-bound
 * reference `<oci-host>/<repository>:<tag>`. Foreign references are another
 * registry's inventory to account for and are never passed here.
 */
async function recordImage(octokit, core, context, params) {
	const ref = params.reference;
	const slash = ref.indexOf('/');
	const colon = ref.lastIndexOf(':');
	if (slash < 0 || colon < slash) {
		core.setFailed(`Could not parse the buildhost image reference '${ref}'; cannot record what was stored`);
		return false;
	}
	const repository = ref.slice(slash + 1, colon);
	const registryURL = `https://${ref.slice(0, slash)}`;
	return post(
		octokit, core, context,
		[{
			name: repository,
			version: ref.slice(colon + 1),
			digest: params.digest,
			registry_url: registryURL,
			repository,
			artifact_url: `${registryURL}/v2/${repository}/manifests/${params.digest}`,
		}],
		(rec) => rec.repository,
	);
}

// --------------------------------------------------------------- the posting

// Downloads live on the dl.<host> subdomain -- buildhost dispatches services by
// the first label of the request Host.
function downloadOrigin(server) {
	const u = new URL(server);
	u.host = `dl.${u.host}`;
	return u.origin;
}

function unreachableRegistry(registryURL) {
	let host;
	try {
		host = new URL(registryURL).hostname;
	} catch {
		return `${registryURL} is not a valid URL`;
	}
	if (host === 'localhost' || host === '127.0.0.1' || host === '::1' || !registryURL.startsWith('https://')) {
		return `${registryURL} is not a reachable https registry (the API accepts https URLs only)`;
	}
	return '';
}

// What a failing POST actually tells you. A 404 is NOT proof of a missing
// grant: the endpoint is org-scoped, so it also 404s when the owner names no
// organization this token can see. The owner-type probe's own answer is what
// separates the two.
function failureDetail(status, msg, owner, ownerType) {
	if (status === 403) {
		return `(HTTP 403: ${msg}). The token was refused for artifact metadata: this job must grant 'artifact-metadata: write' -- job-level permissions REPLACE workflow-level ones, so a job-level block must list it alongside the ones it already declares. Recording what buildhost stores is part of publishing and cannot be turned off.`;
	}
	if (status === 404) {
		const probe = ownerType === '' ? 'failed, so the owner type is unknown' : `reported '${ownerType}'`;
		return `(HTTP 404: ${msg}). POST /orgs/${owner}/artifacts/metadata/storage-record found no organization named ${owner} that this token can see, and GET /users/${owner} ${probe} -- a user-account owner is skipped rather than recorded. If ${owner} is an organization, the token is missing 'artifact-metadata: write' (job-level permissions REPLACE workflow-level ones). Recording what buildhost stores is part of publishing and cannot be turned off.`;
	}
	return `(${msg}).`;
}

// Post every record, in order. Returns false only after calling core.setFailed,
// so a caller's `if (!ok) return` needs no message of its own.
async function post(octokit, core, context, records, label) {
	if (records.length === 0) {
		return true;
	}
	const plural = records.length === 1 ? 'the storage record' : `${records.length} storage records`;

	// A loopback server is not a registry anything can fetch from (buildhost's
	// own e2e spawns one), so a record pointing at it would be an unreachable
	// row on the org's page. A property of the target, not a caller-facing
	// switch.
	const unreachable = unreachableRegistry(records[0].registry_url);
	if (unreachable !== '') {
		core.info(`Skipping ${plural}: ${unreachable}`);
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
			await octokit.request('POST /orgs/{org}/artifacts/metadata/storage-record', {
				org: owner,
				...rec,
				// The API takes the repo NAME only -- an owner/repo value is
				// rejected outright, and the owner is already the path param.
				github_repository: context.repo.repo,
				status: 'active',
				return_records: false,
			});
			core.info(`Recorded ${label(rec)} ${rec.digest} on ${owner}'s linked artifacts page`);
		} catch (e) {
			const status = e.status;
			const msg = e instanceof Error ? e.message : String(e);
			// A personal account has no linked artifacts page, so the org-scoped
			// endpoint 404s no matter what the token grants -- a property of the
			// target, like the loopback skip above. Probed only on a 404 (orgs
			// pay no extra call) and fails closed: an unreadable owner type
			// leaves the failure standing, so a genuine permissions 404 on an
			// org still fails the publish.
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

module.exports = { recordReleaseArtifacts, recordSite, recordImage };
