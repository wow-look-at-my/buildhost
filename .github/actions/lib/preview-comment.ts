// Handing the published site URL to the pull request it previews.
//
// A gallery nobody is handed is a gallery nobody opens. Publishing per branch
// only pays off when a reviewer looks before merging, so the publish itself
// posts the link rather than leaving each consumer to re-implement a sticky
// comment.
//
// Loaded by `require(".../preview-comment.ts")` (node strips the types) typed as
// `typeof import(".../preview-comment")`, the same as storage-record.ts.

export interface Core { info(m: string): void; warning(m: string): void; setFailed(m: string): void }
export interface Context { repo: { owner: string; repo: string } }

interface Comment { id: number; body?: string | null }
export interface Octokit {
	rest: {
		pulls: { list(p: Record<string, unknown>): Promise<{ data: readonly { number: number }[] }> };
		issues: {
			listComments(p: Record<string, unknown>): Promise<{ data: readonly Comment[] }>;
			createComment(p: Record<string, unknown>): Promise<unknown>;
			updateComment(p: Record<string, unknown>): Promise<unknown>;
		};
	};
}

/**
 * One sticky comment per published site. The marker carries the project and the
 * site branch, so a repo that publishes several sites off one commit keeps one
 * comment per site instead of overwriting itself.
 */
export function marker(project: string, branch: string): string {
	return `<!-- buildhost-preview:${project}/${branch} -->`;
}

export function commentBody(project: string, branch: string, siteURL: string): string {
	return `${marker(project, branch)}\n**Preview of \`${project}\` (\`${branch}\`):** ${siteURL}`;
}

/**
 * Resolve the pull request this publish previews. `prNumber` is the event's own
 * number where the event has one. Otherwise the branch names it: a push event
 * carries no pull request, and the open pull request whose head is this branch
 * is the one a reviewer is reading.
 */
async function resolvePR(octokit: Octokit, context: Context, params: {
	prNumber?: number; headRef?: string;
}): Promise<number> {
	if (params.prNumber !== undefined && params.prNumber > 0) return params.prNumber;
	if (!params.headRef) return 0;
	const { owner, repo } = context.repo;
	const open = await octokit.rest.pulls.list({ owner, repo, head: `${owner}:${params.headRef}`, state: 'open' });
	return open.data.length > 0 ? open.data[0].number : 0;
}

/**
 * Post or update the sticky comment. Returns false only after calling setFailed.
 * A branch with no open pull request is a no-op, not a failure: a push to the
 * default branch previews nothing a reviewer is waiting on.
 */
export async function commentPreview(octokit: Octokit, core: Core, context: Context, params: {
	project: string; branch: string; siteURL: string; prNumber?: number; headRef?: string;
}): Promise<boolean> {
	const { owner, repo } = context.repo;
	let issue_number: number;
	try {
		issue_number = await resolvePR(octokit, context, params);
	} catch (e) {
		core.setFailed(`Could not look up the pull request for '${params.headRef}' (${describe(e)}). ${remedy}`);
		return false;
	}
	if (issue_number === 0) {
		core.info(`No open pull request for '${params.headRef ?? ''}'; nothing to hand the preview URL to`);
		return true;
	}

	const mark = marker(params.project, params.branch);
	const body = commentBody(params.project, params.branch, params.siteURL);
	try {
		const seen = await octokit.rest.issues.listComments({ owner, repo, issue_number, per_page: 100 });
		const mine = seen.data.find((c) => (c.body ?? '').includes(mark));
		if (mine) {
			await octokit.rest.issues.updateComment({ owner, repo, comment_id: mine.id, body });
			core.info(`Updated the preview comment on #${issue_number}`);
		} else {
			await octokit.rest.issues.createComment({ owner, repo, issue_number, body });
			core.info(`Commented the preview URL on #${issue_number}`);
		}
	} catch (e) {
		core.setFailed(`Could not hand the preview URL to #${issue_number} (${describe(e)}). ${remedy}`);
		return false;
	}
	return true;
}

const remedy =
	"Add 'pull-requests: write' to this job's permissions block -- job-level permissions REPLACE workflow-level ones, so add it alongside the ones the job already declares. " +
	"Set the publish step's `comment` input to 'false' where a repository deliberately does not want the link posted.";

function describe(e: unknown): string {
	const status = (e as { status?: number }).status;
	const msg = e instanceof Error ? e.message : String(e);
	return status === undefined ? msg : `HTTP ${status}: ${msg}`;
}
