// buildhost-upload-artifact: uploads one file to a release slot, directly or through an upload session, and outputs its sha256.

import * as fsMod from 'node:fs';
import type { ActionGlobals } from './action-globals.ts';
import { client } from './client.ts';
import { putFile, chunkSizeFrom } from './upload.ts';
import { fileBody } from './file-io.ts';
import { fileSHA256 } from './portable-manifest.ts';
import { tokenSource } from './oidc.ts';

/**
 * The sha256 of an upload answer. A non-APE fan-out answers a row per platform, all sharing one blob, so any row carries it.
 * An APE answers the one row covering every platform.
 */
export function uploadedSha256(answer: unknown): string {
	const row = (Array.isArray(answer) ? answer[0] : answer) as { sha256?: string } | undefined;
	return row?.sha256 ?? '';
}

/** buildhost-upload-artifact: outputs response and sha256. A failure is reported through core.setFailed. */
export async function uploadArtifact(g: ActionGlobals): Promise<void> {
	const { core, inputs } = g;
	const chunkSize = chunkSizeFrom(String(inputs.chunk_size));
	if (typeof chunkSize === 'string') { core.setFailed(chunkSize); return; }
	const filePath = String(inputs.path);
	const platform = `${String(inputs.os)}/${String(inputs.arch)}`;
	const headers: Record<string, string> = { 'Content-Type': 'application/octet-stream' };
	if (inputs.filename) headers['X-Artifact-Filename'] = String(inputs.filename);

	const base = String(inputs.server);
	const send = client(base, tokenSource(base, String(inputs.token)), core);
	const body = await fileBody(fsMod, filePath, await fileSHA256(filePath));
	core.info(`Uploading ${body.size} bytes (${platform})`);
	let r: import('./client').Response;
	try {
		r = await putFile(send, core, {
			urlPath: `/api/v1/projects/${String(inputs.project)}/releases/${String(inputs.version)}/artifacts/${platform}?kind=${String(inputs.kind)}`,
			body, headers, chunkSize,
		});
	} catch (e) {
		core.setFailed(e instanceof Error ? e.message : String(e));
		return;
	}
	if (r.status < 200 || r.status >= 300) { core.setFailed(`HTTP ${r.status}: ${r.text}`); return; }
	const answer = JSON.parse(r.text);
	core.info(JSON.stringify(answer, null, 2));
	core.setOutput('response', r.text);
	const sum = uploadedSha256(answer);
	if (!sum) { core.setFailed(`upload response carried no sha256: ${r.text}`); return; }
	core.setOutput('sha256', sum);
}
