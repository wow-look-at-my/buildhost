// A GHA artifact of the current run, which buildhost-publish and buildhost-publish-site read with the GITHUB_TOKEN the step carries.

import { writeFileSync } from 'node:fs';
import type { ActionGlobals } from './action-globals';
import { GITHUB_API } from './github-api.ts';

/** Writes the named artifact of this run to zipPath as a zip. False after it reports that the run has no such artifact. */
export async function downloadRunArtifact(g: ActionGlobals, name: string, zipPath: string): Promise<boolean> {
	const oct = g.getOctokit(process.env.GITHUB_TOKEN!, { baseUrl: GITHUB_API });
	const [owner, repo] = g.github.repository.split('/');
	// the github context is rebuilt from environment variables, so run_id arrives as a string and octokit wants a number
	const { data: { artifacts } } = await oct.rest.actions.listWorkflowRunArtifacts({ owner, repo, run_id: Number(g.github.run_id) });
	const artifact = artifacts.find((a) => a.name === name);
	if (!artifact) { g.core.setFailed(`Artifact '${name}' not found in run`); return false; }
	const { data: zip } = await oct.rest.actions.downloadArtifact({ owner, repo, artifact_id: artifact.id, archive_format: 'zip' });
	writeFileSync(zipPath, Buffer.from(zip as ArrayBuffer));
	return true;
}
