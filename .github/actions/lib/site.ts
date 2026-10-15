// buildhost-publish-site: archives a site, uploads it to one site branch, records it and hands the URL to the pull request.

import * as nodeFs from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createRequire } from 'node:module';
import type { ActionGlobals } from './action-globals';
import type { Response } from './client';

const need = createRequire(import.meta.url);
const { tokenSource } = need('./oidc.ts') as typeof import('./oidc');
const { siteBranch, projectOf } = need('./deployment.ts') as typeof import('./deployment');
const { client } = need('./client.ts') as typeof import('./client');
const { putFile, chunkSizeFrom } = need('./upload.ts') as typeof import('./upload');
const { fileBody } = need('./file-io.ts') as typeof import('./file-io');
const { fileSHA256 } = need('./portable-manifest.ts') as typeof import('./portable-manifest');
const { recordSite } = need('./storage-record.ts') as typeof import('./storage-record');
const { commentPreview } = need('./preview-comment.ts') as typeof import('./preview-comment');
const { downloadRunArtifact } = need('./run-artifact.ts') as typeof import('./run-artifact');

/** The sites endpoint: buildhost dispatches each service by the first Host label, so it is sites.<domain>. */
export function sitesBase(apex: string): string {
	const su = new URL(apex);
	return `${su.protocol}//sites.${su.host}`;
}

/** The URL the server reports for the site, else the canonical one for a server predating that field. The server owns the URL grammar. */
export function siteURLFrom(reply: { url?: unknown }, base: string, project: string, branch: string): string {
	return typeof reply.url === 'string' && reply.url !== '' ? reply.url : `${base}/${project}/@${branch}/`;
}

/** The facts of one site publish that the upload and the record share. */
interface Site { apex: string; project: string; branch: string; gitCommit: string; sha256: string }

/** Reports the upload's answer, records the site, and comments its URL on the pull request unless comment is false. */
async function finish(g: ActionGlobals, r: Response, site: Site): Promise<void> {
	if (r.status < 200 || r.status >= 300) { g.core.setFailed(`HTTP ${r.status}: ${r.text}`); return; }
	const reply = JSON.parse(r.text);
	const siteURL = siteURLFrom(reply, sitesBase(site.apex), site.project, site.branch);
	g.core.info(`Published site to ${siteURL}`);
	g.core.info(JSON.stringify(reply, null, 2));
	g.core.setOutput('response', r.text);
	g.core.setOutput('site_url', siteURL);
	if (!await recordSite(g.octokit, g.core, g.context, { server: site.apex, project: site.project, branch: site.branch, version: site.gitCommit, sha256: site.sha256 })) return;
	// every `with:` value reaches a composite action as a string, so a bool arrives spelled out
	const comment = String(g.inputs.comment);
	if (comment !== 'true' && comment !== 'false') {
		g.core.setFailed(`invalid comment '${comment}': expected true or false. A value that is neither would silently read as off, which hides the preview link the publish exists to hand over.`);
		return;
	}
	if (comment === 'false') {
		g.core.info('Not commenting the preview URL: comment is false for this publish');
		return;
	}
	const pr = g.context.payload.pull_request as { number?: number; head?: { ref?: string } } | undefined;
	await commentPreview(g.octokit, g.core, g.context, {
		project: site.project, branch: site.branch, siteURL,
		prNumber: pr?.number ?? 0,
		headRef: pr?.head?.ref ?? (g.github.ref_name || ''),
	});
}

/** buildhost-publish-site: outputs response and site_url. A failure is reported through core.setFailed. */
export async function publishSite(g: ActionGlobals): Promise<void> {
	const sourceDir = String(g.inputs.source_dir);
	const artifactName = String(g.inputs.artifact_name);
	if (sourceDir && artifactName) { g.core.setFailed('Specify source_dir or artifact_name, not both'); return; }
	if (!sourceDir && !artifactName) { g.core.setFailed('Either source_dir or artifact_name is required'); return; }
	const chunkSize = chunkSizeFrom(String(g.inputs.chunk_size));
	if (typeof chunkSize === 'string') { g.core.setFailed(chunkSize); return; }

	const apex = String(g.inputs.server);
	const project = projectOf(g);
	const branch = siteBranch(String(g.inputs.branch));
	const gitCommit = String(g.inputs.git_commit || g.github.sha);

	// built to disk, from source_dir or from the run's artifact
	const archivePath = join(tmpdir(), artifactName ? 'site.zip' : 'site.tar.gz');
	if (artifactName) {
		if (!await downloadRunArtifact(g, artifactName, archivePath)) return;
	} else {
		await g.exec.exec('tar', ['-czf', archivePath, '-C', sourceDir, '.']);
	}
	const size = nodeFs.statSync(archivePath).size;
	g.core.info(`Archive size: ${size} bytes`);
	// the chunked finalize verifies the assembled spool against this sha256, and the storage record is keyed by it
	const sha256 = await fileSHA256(archivePath);

	// session requests go to the server, and the upload itself to the sites endpoint
	const api = client('', tokenSource(apex, String(g.inputs.token)), g.core);
	const send = (method: string, url: string, body?: string | Buffer | null, headers?: Record<string, string>) =>
		api(method, url.startsWith('/') ? `${apex}${url}` : url, body, headers);
	let resp: Response;
	try {
		resp = await putFile(send, g.core, {
			urlPath: `${sitesBase(apex)}/${project}/branch/${branch}`,
			body: await fileBody(nodeFs, archivePath, sha256),
			headers: {
				'Content-Type': artifactName ? 'application/zip' : 'application/gzip',
				'X-Git-Commit': gitCommit,
				// serves this site without a token, even under a private project, when true
				'X-Public-Site': String(g.inputs.public) === 'true' ? 'true' : 'false',
			},
			chunkSize,
		});
	} catch (e) {
		g.core.setFailed(e instanceof Error ? e.message : String(e));
		return;
	}
	await finish(g, resp, { apex, project, branch, gitCommit, sha256 });
}
