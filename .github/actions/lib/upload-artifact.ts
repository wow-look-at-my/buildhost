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

/** Sends the file at filePath to urlPath on server, directly or through an upload session, as the token or the job's OIDC token. */
export async function uploadFile(
	core: import('./upload').Core, server: string, token: string, urlPath: string, filePath: string,
	headers: Record<string, string> = {}, chunkSize?: number,
): Promise<import('./client').Response> {
	const send = client(server, tokenSource(server, token), core);
	const body = await fileBody(fsMod, filePath, await fileSHA256(filePath));
	core.info(`Uploading ${body.size} bytes to ${urlPath}`);
	return putFile(send, core, { urlPath, body, headers, chunkSize });
}

/** buildhost-upload-artifact: outputs response and sha256. A failure is reported through core.setFailed. */
export async function uploadArtifact(g: ActionGlobals): Promise<void> {
	const { core, inputs } = g;
	const chunkSize = chunkSizeFrom(String(inputs.chunk_size));
	if (typeof chunkSize === 'string') { core.setFailed(chunkSize); return; }
	const platform = `${String(inputs.os)}/${String(inputs.arch)}`;
	const headers: Record<string, string> = { 'Content-Type': 'application/octet-stream' };
	if (inputs.filename) headers['X-Artifact-Filename'] = String(inputs.filename);

	let r: import('./client').Response;
	try {
		r = await uploadFile(core, String(inputs.server), String(inputs.token),
			`/api/v1/projects/${String(inputs.project)}/releases/${String(inputs.version)}/artifacts/${platform}?kind=${String(inputs.kind)}`,
			String(inputs.path), headers, chunkSize);
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
