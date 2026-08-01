// Behavior tests for .github/actions/lib/storage-record.js, the module every
// publish composite imports to post storage records.
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
const { postStorageRecords } = require(`${workspace}/.github/actions/lib/storage-record`);

type Rec = Record<string, unknown>;
interface Log { info: string[]; failed: string[] }

const mkCore = (): [{ info(m: string): void; setFailed(m: string): void }, Log] => {
	const log: Log = { info: [], failed: [] };
	return [{ info: (m) => log.info.push(m), setFailed: (m) => log.failed.push(m) }, log];
};

const rec = (over: Rec = {}): Rec => ({
	name: 'p', version: 'v1', digest: `sha256:${'a'.repeat(64)}`,
	registry_url: 'https://pazer.build', repository: 'p', path: 'linux/amd64', ...over,
});

const opts = {
	owner: 'PazerOP',
	githubRepository: 'UE553',
	label: (r: { name: string; path?: string }) => `${r.name} ${r.path}`,
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

const mustNotPost = async (): Promise<never> => { throw new Error('posted a record it should have skipped'); };

async function main(): Promise<void> {
	// A record carries the caller's fields plus the ones the module owns.
	{
		const [core, log] = mkCore();
		let sent: Rec = {};
		const ok = await postStorageRecords(fakeOctokit(async (_r, p) => { sent = p; }), core, [rec()], opts);
		assert.strictEqual(ok, true);
		assert.strictEqual(sent.org, 'PazerOP');
		assert.strictEqual(sent.github_repository, 'UE553');
		assert.strictEqual(sent.status, 'active');
		assert.strictEqual(sent.return_records, false);
		assert.strictEqual(sent.name, 'p');
		assert.match(log.info[0], /^Recorded p linux\/amd64 sha256:a{64} on PazerOP's linked artifacts page$/);
		assert.deepStrictEqual(log.failed, []);
	}

	// Nothing to record is not a failure.
	{
		const [core] = mkCore();
		assert.strictEqual(await postStorageRecords(fakeOctokit(mustNotPost), core, [], opts), true);
	}

	// A registry nothing can fetch from is skipped -- a property of the target.
	for (const url of ['http://localhost:18080', 'https://127.0.0.1', 'http://pazer.build']) {
		const [core, log] = mkCore();
		const ok = await postStorageRecords(fakeOctokit(mustNotPost), core, [rec({ registry_url: url })], opts);
		assert.strictEqual(ok, true, url);
		assert.match(log.info[0], /^Skipping the storage record: /);
		assert.deepStrictEqual(log.failed, [], url);
	}

	// A user-account owner has no linked artifacts page: skip, publish survives.
	{
		const [core, log] = mkCore();
		const ok = await postStorageRecords(fakeOctokit(async () => { throw httpError(404); }, 'User'), core, [rec(), rec()], opts);
		assert.strictEqual(ok, true);
		assert.strictEqual(log.info[0], 'Skipping 2 storage records: PazerOP is a user account, which has no linked artifacts page');
		assert.deepStrictEqual(log.failed, []);
	}

	// An organization's 404 still fails the publish, and says what it observed.
	{
		const [core, log] = mkCore();
		const ok = await postStorageRecords(fakeOctokit(async () => { throw httpError(404); }, 'Organization'), core, [rec()], opts);
		assert.strictEqual(ok, false);
		assert.match(log.failed[0], /HTTP 404/);
		assert.match(log.failed[0], /GET \/users\/PazerOP reported 'Organization'/);
		assert.match(log.failed[0], /missing 'artifact-metadata: write'/);
	}

	// Fail closed: an unreadable owner type leaves the failure standing.
	{
		const [core, log] = mkCore();
		const ok = await postStorageRecords(fakeOctokit(async () => { throw httpError(404); }, new Error('network')), core, [rec()], opts);
		assert.strictEqual(ok, false);
		assert.match(log.failed[0], /GET \/users\/PazerOP failed, so the owner type is unknown/);
	}

	// A 403 is a plain refusal -- named as such, and it costs no extra lookup.
	{
		const [core, log] = mkCore();
		let probed = false;
		const octokit = {
			request: async () => { throw httpError(403); },
			rest: { users: { getByUsername: async () => { probed = true; return { data: { type: 'User' } }; } } },
		};
		const ok = await postStorageRecords(octokit, core, [rec()], opts);
		assert.strictEqual(ok, false);
		assert.strictEqual(probed, false, 'a 403 must not cost an owner-type lookup');
		assert.match(log.failed[0], /HTTP 403/);
		assert.match(log.failed[0], /must grant 'artifact-metadata: write'/);
	}

	// The common case -- an org, records posted -- pays no owner-type lookup.
	{
		const [core] = mkCore();
		let probed = false;
		const octokit = {
			request: async () => ({}),
			rest: { users: { getByUsername: async () => { probed = true; return { data: {} }; } } },
		};
		await postStorageRecords(octokit, core, [rec()], opts);
		assert.strictEqual(probed, false, 'a successful POST must not probe the owner');
	}

	// An over-long artifact_url fails rather than dropping the only link back
	// to the bytes the digest covers.
	{
		const [core, log] = mkCore();
		const ok = await postStorageRecords(fakeOctokit(mustNotPost), core, [rec({ artifact_url: `https://dl.pazer.build/${'x'.repeat(200)}` })], opts);
		assert.strictEqual(ok, false);
		assert.match(log.failed[0], /over the API's 152-char limit/);
	}

	// A non-HTTP failure keeps its bare message (no invented cause).
	{
		const [core, log] = mkCore();
		const ok = await postStorageRecords(fakeOctokit(async () => { throw new Error('socket hang up'); }), core, [rec()], opts);
		assert.strictEqual(ok, false);
		assert.match(log.failed[0], /\(socket hang up\)\.$/);
	}

	console.log('storage-record: all checks passed');
}

await main();
