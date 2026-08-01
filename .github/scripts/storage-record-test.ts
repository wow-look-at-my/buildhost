// Behavior tests for .github/actions/lib/storage-record.ts.
//
// Its own job because nothing else reaches the code: upload-artifact-action-e2e
// publishes to http://localhost:18080, where the unreachable-registry skip
// returns before a record is posted. A mistake here fails publishing for every
// repo in the org at once.

const assert = require('node:assert');
const lib = `${process.env.GITHUB_WORKSPACE ?? process.cwd()}/.github/actions/lib/storage-record`;
// Untyped on purpose: the module's types are checked where it is CALLED, in
// the four composites; here the assertions are the contract.
const { recordReleaseArtifacts, recordSite, recordImage } = require(`${lib}.ts`);

type Sent = Record<string, unknown>;
const ctx = { repo: { owner: 'PazerOP', repo: 'UE553' } };
const SERVER = 'https://pazer.build';
const SHA = 'a'.repeat(64);

let info: string[] = [], failed: string[] = [], sent: Sent[] = [];
const core = { info: (m: string) => info.push(m), setFailed: (m: string) => failed.push(m) };
const reset = () => { info = []; failed = []; sent = []; };

// `post` is what POST /orgs/.../storage-record does; `ownerType` is what
// GET /users/{owner} answers (an Error = the lookup itself failed).
const gh = (post: () => Promise<unknown> = async () => ({}), ownerType?: string | Error) => ({
	request: async (_route: string, p: Sent) => { sent.push(p); return post(); },
	rest: { users: { getByUsername: async () => {
		if (ownerType instanceof Error) throw ownerType;
		return { data: { type: ownerType } };
	} } },
});
const fails = (status: number) => async () => { throw Object.assign(new Error('Not Found'), { status }); };
const slot = (over: Sent = {}) => ({ os: 'linux', arch: 'amd64', sha256: SHA, ...over });
const release = (octokit: unknown, over: Sent = {}) => recordReleaseArtifacts(octokit, core, ctx, {
	server: SERVER, project: 'ue553', version: 'v7', artifacts: [slot()], ...over,
});

async function main(): Promise<void> {
	// --- each kind derives its own record ---------------------------------

	reset();
	assert.strictEqual(await release(gh()), true);
	assert.deepStrictEqual(sent[0], {
		org: 'PazerOP', name: 'ue553', version: 'v7', digest: `sha256:${SHA}`,
		registry_url: SERVER, repository: 'ue553', path: 'linux/amd64',
		artifact_url: 'https://dl.pazer.build/ue553?v=v7&os=linux&arch=amd64&debug=1',
		github_repository: 'UE553', status: 'active', return_records: false,
	});
	assert.match(info[0], /^Recorded ue553 linux\/amd64 sha256:a{64} on PazerOP's linked artifacts page$/);

	// A namespaced fan-out: per-entry project/version override the call's.
	reset();
	await release(gh(), { artifacts: [slot({ project: 'repo/client', version: 'v1' }), slot({ project: 'repo/server', version: 'v2', arch: 'arm64' })] });
	assert.deepStrictEqual(sent.map((r) => [r.name, r.version, r.path]), [['repo/client', 'v1', 'linux/amd64'], ['repo/server', 'v2', 'linux/arm64']]);
	assert.strictEqual(sent[1].artifact_url, 'https://dl.pazer.build/repo/server?v=v2&os=linux&arch=arm64&debug=1');

	// A published artifact with no sha256 is a failed publish, not a record.
	reset();
	assert.strictEqual(await release(gh(), { artifacts: [slot({ sha256: undefined })] }), false);
	assert.match(failed[0], /carried no sha256/);
	assert.deepStrictEqual(sent, []);

	// A site carries NO artifact_url: served unpacked, so no URL returns the
	// archive bytes its digest covers.
	reset();
	assert.strictEqual(await recordSite(gh(), core, ctx, { server: SERVER, project: 'ue553', branch: 'pr-326', version: 'deadbeef', sha256: SHA }), true);
	assert.strictEqual(sent[0].artifact_url, undefined);
	assert.deepStrictEqual([sent[0].path, sent[0].version], ['branch/pr-326', 'deadbeef']);

	// An image: the reference names registry, repository and tag.
	reset();
	assert.strictEqual(await recordImage(gh(), core, ctx, { reference: 'oci.pazer.build/ue553:v7', digest: `sha256:${SHA}` }), true);
	assert.deepStrictEqual([sent[0].registry_url, sent[0].name, sent[0].version, sent[0].artifact_url],
		['https://oci.pazer.build', 'ue553', 'v7', `https://oci.pazer.build/v2/ue553/manifests/sha256:${SHA}`]);

	// An unparseable reference records nothing rather than something wrong.
	reset();
	assert.strictEqual(await recordImage(gh(), core, ctx, { reference: '', digest: `sha256:${SHA}` }), false);
	assert.match(failed[0], /Could not parse the buildhost image reference/);

	// --- shared posting behavior ------------------------------------------

	// Nothing to record is not a failure.
	reset();
	assert.strictEqual(await release(gh(), { artifacts: [] }), true);
	assert.deepStrictEqual(sent, []);

	// A registry nothing can fetch from is skipped -- a property of the target.
	for (const server of ['http://localhost:18080', 'https://127.0.0.1', 'http://pazer.build']) {
		reset();
		assert.strictEqual(await release(gh(), { server }), true, server);
		assert.match(info[0], /^Skipping the storage record: /);
		assert.deepStrictEqual([failed, sent], [[], []], server);
	}

	// A user-account owner has no linked artifacts page: skip, publish survives.
	reset();
	assert.strictEqual(await release(gh(fails(404), 'User'), { artifacts: [slot(), slot({ arch: 'arm64' })] }), true);
	assert.strictEqual(info[0], 'Skipping 2 storage records: PazerOP is a user account, which has no linked artifacts page');
	assert.deepStrictEqual(failed, []);

	// An organization's 404 still fails, and says what it observed.
	reset();
	assert.strictEqual(await release(gh(fails(404), 'Organization')), false);
	assert.match(failed[0], /HTTP 404.*GET \/users\/PazerOP reported 'Organization'.*missing 'artifact-metadata: write'/s);

	// Fail closed: an unreadable owner type leaves the failure standing.
	reset();
	assert.strictEqual(await release(gh(fails(404), new Error('network'))), false);
	assert.match(failed[0], /GET \/users\/PazerOP failed, so the owner type is unknown/);

	// A 403 is a plain refusal, and neither it nor a success costs a lookup.
	for (const [post, refused] of [[fails(403), true], [async () => ({}), false]] as const) {
		reset();
		let probed = false;
		await release({ request: async (_r: string, p: Sent) => { sent.push(p); return post(); },
			rest: { users: { getByUsername: async () => { probed = true; return { data: { type: 'User' } }; } } } });
		assert.strictEqual(probed, false, 'owner-type lookup must be a 404-only cost');
		if (refused) assert.match(failed[0], /HTTP 403.*must grant 'artifact-metadata: write'/s);
		else assert.deepStrictEqual(failed, []);
	}

	// An over-long artifact_url fails rather than dropping the only link back
	// to the bytes the digest covers.
	reset();
	assert.strictEqual(await release(gh(), { project: `deep/${'n'.repeat(140)}` }), false);
	assert.match(failed[0], /over the API's 152-char limit/);

	// A non-HTTP failure keeps its bare message (no invented cause).
	reset();
	assert.strictEqual(await release(gh(async () => { throw new Error('socket hang up'); })), false);
	assert.match(failed[0], /\(socket hang up\)\.$/);

	console.log('storage-record: all checks passed');
}

await main();
