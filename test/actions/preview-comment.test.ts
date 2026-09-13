// Behavior tests for .github/actions/lib/preview-comment.ts.
//
// Nothing else reaches this code: a publish e2e runs against a local server with
// no pull request to comment on. A mistake here posts the wrong link, or none,
// on every repo in the org at once.

const assert = require('node:assert');
const lib = `${process.env.GITHUB_WORKSPACE ?? process.cwd()}/.github/actions/lib/preview-comment`;
// Untyped on purpose: the module's types are checked where it is CALLED, in the
// composite; here the assertions are the contract.
const { commentPreview, marker } = require(`${lib}.ts`);

type Sent = Record<string, unknown>;
const ctx = { repo: { owner: 'wow-look-at-my', repo: 'js-snippets' } };
const URL_ = 'https://sites.pazer.build/js-snippets/branch/claude-x/';

let info: string[] = [], warned: string[] = [], failed: string[] = [], created: Sent[] = [], updated: Sent[] = [], listed: Sent[] = [];
const core = {
	info: (m: string) => info.push(m),
	warning: (m: string) => warned.push(m),
	setFailed: (m: string) => failed.push(m),
};
const reset = () => { info = []; warned = []; failed = []; created = []; updated = []; listed = []; };

// `pulls` is what GET /pulls answers, `comments` what listComments answers.
const gh = (pulls: unknown, comments: readonly unknown[] = [], write?: Error) => ({
	rest: {
		pulls: { list: async (p: Sent) => {
			listed.push(p);
			if (pulls instanceof Error) throw pulls;
			return { data: pulls };
		} },
		issues: {
			listComments: async () => ({ data: comments }),
			createComment: async (p: Sent) => { if (write) throw write; created.push(p); },
			updateComment: async (p: Sent) => { if (write) throw write; updated.push(p); },
		},
	},
});
const run = (octokit: unknown, over: Sent = {}) => commentPreview(octokit, core, ctx, {
	project: 'js-snippets', branch: 'claude-x', siteURL: URL_, headRef: 'claude/x', ...over,
});

async function main(): Promise<void> {
	const mark = marker('js-snippets', 'claude-x');

	// --- the branch names its pull request, so a push comments too ---------
	reset();
	assert.strictEqual(await run(gh([{ number: 74 }])), true);
	assert.deepStrictEqual(listed[0], {
		owner: 'wow-look-at-my', repo: 'js-snippets', head: 'wow-look-at-my:claude/x', state: 'open',
	});
	assert.strictEqual(created[0].issue_number, 74);
	assert.match(String(created[0].body), new RegExp(`^${mark}\\n`));
	assert.ok(String(created[0].body).includes(URL_), 'the comment carries the published URL');

	// The event's own number wins, and costs no lookup.
	reset();
	await run(gh(new Error('must not be called')), { prNumber: 99 });
	assert.deepStrictEqual(listed, []);
	assert.strictEqual(created[0].issue_number, 99);

	// --- sticky: the second publish edits the first comment ---------------
	reset();
	await run(gh([{ number: 74 }], [{ id: 5, body: 'unrelated' }, { id: 8, body: `${mark}\nold` }]));
	assert.deepStrictEqual(created, []);
	assert.strictEqual(updated[0].comment_id, 8);

	// A different site branch is a different comment, not an overwrite.
	reset();
	await run(gh([{ number: 74 }], [{ id: 8, body: `${marker('js-snippets', 'library-claude-x')}\nold` }]));
	assert.deepStrictEqual(updated, []);
	assert.strictEqual(created.length, 1);

	// --- no pull request is a no-op, never a failure -----------------------
	reset();
	assert.strictEqual(await run(gh([])), true);
	assert.deepStrictEqual(failed, []);
	assert.deepStrictEqual(created, []);
	assert.match(info[0], /No open pull request/);

	// A publish with no branch to look up cannot name one either.
	reset();
	assert.strictEqual(await run(gh(new Error('must not be called')), { headRef: '' }), true);
	assert.deepStrictEqual(listed, []);
	assert.deepStrictEqual(failed, []);

	// --- a refused write fails loudly, and names the grant -----------------
	reset();
	const forbidden = Object.assign(new Error('Resource not accessible by integration'), { status: 403 });
	assert.strictEqual(await run(gh([{ number: 74 }], [], forbidden)), false);
	assert.match(failed[0], /HTTP 403.*'pull-requests: write'/s);
	assert.match(failed[0], /`comment` input to 'false'/);

	// So does a failed lookup: silence there would drop the link with no trace.
	reset();
	assert.strictEqual(await run(gh(Object.assign(new Error('nope'), { status: 403 }))), false);
	assert.match(failed[0], /Could not look up the pull request for 'claude\/x'.*HTTP 403/s);

	console.log('preview-comment: all checks passed');
}

// Node runs this file directly, so the rejection has to become an exit code
// here: an unhandled one prints a warning and still exits 0 on some versions.
main().catch((err: unknown) => {
	console.error(err);
	process.exit(1);
});
