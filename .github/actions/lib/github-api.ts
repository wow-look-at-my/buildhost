// Every GitHub call these actions make goes through github-state-mirror, as buildhost's own calls do (auth.GitHubAPI).

import type { ActionGlobals } from './action-globals';

export const GITHUB_API = 'https://github-state-mirror.pazer.io';

type Octokit = ReturnType<typeof getOctokit>;

/** The job's own client, pointed at the mirror. It carries the token of the injected octokit. */
export async function githubAPI(g: ActionGlobals): Promise<Octokit> {
	const { token } = await g.octokit.auth() as { token: string };
	return g.getOctokit(token, { baseUrl: GITHUB_API });
}
