// Behavior tests for .github/actions/lib/storage-record.js, the module every
// publish composite calls to record what buildhost stored.
//
// This needs its own job because nothing else can reach the code: the publish
// composites' own e2e (upload-artifact-action-e2e) publishes to
// http://localhost:18080, where the unreachable-registry skip returns before a
// single record is posted. Every branch below therefore ships untested unless
// it is tested here -- and a mistake in it fails publishing for every repo in
// the org at once.
//
// Runs under the typescript action (`file:`), which wraps the body in an async
// function -- hence top-level await and require rather than ESM imports.

const assert = require('node:assert');

const workspace = process.env.GITHUB_WORKSPACE ?? process.cwd();
const { recordReleaseArtifacts, recordSite, recordImage } = require(`${workspace}/.github/actions/lib/storage-record`);

type Rec = Record<string, unknown>;
interface Log { info: string[]; failed: string[] }

const context = { repo: { owner: 'PazerOP', repo: 'UE553' } };
const SERVER = 'https://pazer.build';
const SHA = 'a'.repeat(64);

const mkCore = (): [{ info(m: string): void; setFailed(m: string): void }, Log] => {
	const log: Log = { info: [], failed: [] };
	return [{ info: (m) => log.info.push(m), setFailed: (m) => log.failed.push(m) }, log];
};

const httpError = (status: number): Error => Object.assign(new Error('Not Found'), { status });

// An octokit stand-in: `request` is the POST, `ownerType` is what
// GET /users/{owner} answers (an Error = the lookup itself failed).
const fakeOctokit = (request: (route: string, params: Rec) => Promise<unknown>, ownerType?: string | Error) => ({
	request,
	rest: {
		users: {
			getByUsername: async () => {
				if (ownerType instanceof Error) throw ownerType;
				return { data: { type: ownerType } };
			},
		},
	},
});

const collect = (sent: Rec[]) => fakeOctokit(async (_route, p) => { sent.push(p); });
const mustNotPost = async (): Promise<never> => { throw new Error('posted a record it should have skipped'); };
const artifact = (over: Rec = {}) => ({ os: 'linux', arch: 'amd64', sha256: SHA, ...over });

async function main(): Promise<void> {
	// --- what each kind derives -------------------------------------------

	// A release artifact: server-derived download origin, debug=1 (the only
	// download returning the uploaded bytes, so the URL and digest agree).
	{
		const [core, log] = mkCore(); const sent: Rec[] = [];
		const ok = await recordReleaseArtifacts(collect(sent), core, context, {
			server: SERVER, project: 'ue553', version: 'v7', artifacts: [artifact()],
		});
		assert.strictEqual(ok, true);
		assert.deepStrictEqual(sent[0], {
			org: 'PazerOP',
			name: 'ue553',
			version: 'v7',
			digest: `sha256:${SHA}`,
			registry_url: SERVER,
			repository: 'ue553',
			path: 'linux/amd64',
			artifact_url: 'https://dl.pazer.build/ue553?v=v7&os=linux&arch=amd64&debug=1',
			github_repository: 'UE553',
			status: 'active',
			return_records: false,
		});
		assert.match(log.info[0], /^Recorded ue553 linux\/amd64 sha256:a{64} on PazerOP's linked artifacts page$/);
	}

	// A namespaced fan-out: per-entry project/version override the call's.
	{
		const [core] = mkCore(); const sent: Rec[] = [];
		await recordReleaseArtifacts(collect(sent), core, context, {
			server: SERVER,
			artifacts: [
				artifact({ project: 'repo/client', version: 'v1' }),
				artifact({ project: 'repo/server', version: 'v2', arch: 'arm64' }),
			],
		});
		assert.deepStrictEqual(sent.map((r) => [r.name, r.version, r.path]), [
			['repo/client', 'v1', 'linux/amd64'],
			['repo/server', 'v2', 'linux/arm64'],
		]);
		assert.strictEqual(sent[1].artifact_url, 'https://dl.pazer.build/repo/server?v=v2&os=linux&arch=arm64&debug=1');
	}

	// A published artifact with no sha256 is a failed publish, not a record.
	{
		const [core, log] = mkCore();
		const ok = await recordReleaseArtifacts(fakeOctokit(mustNotPost), core, context, {
			server: SERVER, project: 'ue553', version: 'v7', artifacts: [artifact({ sha256: undefined })],
		});
		assert.strictEqual(ok, false);
		assert.match(log.failed[0], /carried no sha256/);
	}

	// A site carries NO artifact_url: it is served unpacked, so no URL returns
	// the archive bytes this digest covers.
	{
		const [core, log] = mkCore(); const sent: Rec[] = [];
		const ok = await recordSite(collect(sent), core, context, {
			server: SERVER, project: 'ue553', branch: 'pr-326', version: 'deadbeef', sha256: SHA,
		});
		assert.strictEqual(ok, true);
		assert.strictEqual(sent[0].artifact_url, undefined);
		assert.strictEqual(sent[0].path, 'branch/pr-326');
		assert.strictEqual(sent[0].version, 'deadbeef');
		assert.match(log.info[0], /^Recorded ue553 branch\/pr-326 /);
	}

	// An image: the reference names the registry, repository and tag.
	{
		const [core, log] = mkCore(); const sent: Rec[] = [];
		const ok = await recordImage(collect(sent), core, context, {
			reference: 'oci.pazer.build/ue553:v7', digest: `sha256:${SHA}`,
		});
		assert.strictEqual(ok, true);
		assert.strictEqual(sent[0].registry_url, 'https://oci.pazer.build');
		assert.strictEqual(sent[0].name, 'ue553');
		assert.strictEqual(sent[0].version, 'v7');
		assert.strictEqual(sent[0].artifact_url, `https://oci.pazer.build/v2/ue553/manifests/sha256:${SHA}`);
		assert.match(log.info[0], /^Recorded ue553 sha256:/);
	}

	// An unparseable reference records nothing rather than something wrong.
	{
		const [core, log] = mkCore();
		const ok = await recordImage(fakeOctokit(mustNotPost), core, context, { reference: '', digest: `sha256:${SHA}` });
		assert.strictEqual(ok, false);
		assert.match(log.failed[0], /Could not parse the buildhost image reference/);
	}

	// --- what every kind shares -------------------------------------------

	const record = (octokit: unknown, core: unknown) => recordReleaseArtifacts(octokit, core, context, {
		server: SERVER, project: 'ue553', version: 'v7', artifacts: [artifact()],
	});

	// Nothing to record is not a failure.
	{
		const [core] = mkCore();
		assert.strictEqual(await recordReleaseArtifacts(fakeOctokit(mustNotPost), core, context, { server: SERVER, artifacts: [] }), true);
	}

	// A registry nothing can fetch from is skipped -- a property of the target.
	for (const server of ['http://localhost:18080', 'https://127.0.0.1', 'http://pazer.build']) {
		const [core, log] = mkCore();
		const ok = await recordReleaseArtifacts(fakeOctokit(mustNotPost), core, context, {
			server, project: 'ue553', version: 'v7', artifacts: [artifact()],
		});
		assert.strictEqual(ok, true, server);
		assert.match(log.info[0], /^Skipping the storage record: /);
		assert.deepStrictEqual(log.failed, [], server);
	}

	// A user-account owner has no linked artifacts page: skip, publish survives.
	{
		const [core, log] = mkCore();
		const ok = await recordReleaseArtifacts(fakeOctokit(async () => { throw httpError(404); }, 'User'), core, context, {
			server: SERVER, project: 'ue553', version: 'v7', artifacts: [artifact(), artifact({ arch: 'arm64' })],
		});
		assert.strictEqual(ok, true);
		assert.strictEqual(log.info[0], 'Skipping 2 storage records: PazerOP is a user account, which has no linked artifacts page');
		assert.deepStrictEqual(log.failed, []);
	}

	// An organization's 404 still fails the publish, and says what it observed.
	{
		const [core, log] = mkCore();
		const ok = await record(fakeOctokit(async () => { throw httpError(404); }, 'Organization'), core);
		assert.strictEqual(ok, false);
		assert.match(log.failed[0], /HTTP 404/);
		assert.match(log.failed[0], /GET \/users\/PazerOP reported 'Organization'/);
		assert.match(log.failed[0], /missing 'artifact-metadata: write'/);
	}

	// Fail closed: an unreadable owner type leaves the failure standing.
	{
		const [core, log] = mkCore();
		const ok = await record(fakeOctokit(async () => { throw httpError(404); }, new Error('network')), core);
		assert.strictEqual(ok, false);
		assert.match(log.failed[0], /GET \/users\/PazerOP failed, so the owner type is unknown/);
	}

	// A 403 is a plain refusal -- named as such, and it costs no extra lookup.
	{
		const [core, log] = mkCore();
		let probed = false;
		const ok = await record({
			request: async () => { throw httpError(403); },
			rest: { users: { getByUsername: async () => { probed = true; return { data: { type: 'User' } }; } } },
		}, core);
		assert.strictEqual(ok, false);
		assert.strictEqual(probed, false, 'a 403 must not cost an owner-type lookup');
		assert.match(log.failed[0], /HTTP 403/);
		assert.match(log.failed[0], /must grant 'artifact-metadata: write'/);
	}

	// The common case -- an org, records posted -- pays no owner-type lookup.
	{
		const [core] = mkCore();
		let probed = false;
		await record({
			request: async () => ({}),
			rest: { users: { getByUsername: async () => { probed = true; return { data: {} }; } } },
		}, core);
		assert.strictEqual(probed, false, 'a successful POST must not probe the owner');
	}

	// An over-long artifact_url fails rather than dropping the only link back
	// to the bytes the digest covers.
	{
		const [core, log] = mkCore();
		const ok = await recordReleaseArtifacts(fakeOctokit(mustNotPost), core, context, {
			server: SERVER, project: `deep/${'n'.repeat(140)}`, version: 'v7', artifacts: [artifact()],
		});
		assert.strictEqual(ok, false);
		assert.match(log.failed[0], /over the API's 152-char limit/);
	}

	// A non-HTTP failure keeps its bare message (no invented cause).
	{
		const [core, log] = mkCore();
		const ok = await record(fakeOctokit(async () => { throw new Error('socket hang up'); }), core);
		assert.strictEqual(ok, false);
		assert.match(log.failed[0], /\(socket hang up\)\.$/);
	}

	console.log('storage-record: all checks passed');
}

await main();
