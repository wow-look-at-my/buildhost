// buildhost-handoff: hands a directory from one job of a run to later jobs of the same run, through buildhost's run files.

import { createWriteStream, existsSync, mkdtempSync, rmSync, statSync, type WriteStream } from 'node:fs';
import { join, resolve } from 'node:path';
import { buildhostCli, runCli } from './cli.ts';
import type { ActionGlobals } from './action-globals.ts';
import { uploadFile } from './upload-artifact.ts';
import { fileSHA256 } from './portable-manifest.ts';
import { tokenSource } from './oidc.ts';
import { fetchStream, HTTPError, DefaultStallMs } from './download.ts';

const validName = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;

/** The run-file URL path of name, for the run this job belongs to. */
export function runFilePath(name: string, env: NodeJS.ProcessEnv = process.env): string {
	const repository = env.GITHUB_REPOSITORY;
	const runID = env.GITHUB_RUN_ID;
	if (!repository || !runID) throw new Error('GITHUB_REPOSITORY and GITHUB_RUN_ID must be set: a hand-off belongs to a workflow run');
	return `/api/v1/run-files/${encodeURIComponent(name)}?${new URLSearchParams({ repository, run_id: runID })}`;
}

/** Writes members of dir, by default all of it, to archive as a zstd-compressed tar. Modes, symlinks and hardlinks survive. */
export async function pack(dir: string, archive: string, members = ['.'], server = 'https://pazer.build'): Promise<void> {
	runCli(await buildhostCli(server), ['archive', 'pack', dir, archive, ...members]);
}

/** Extracts a pack archive into dir. Extraction stops at the end-of-archive marker, so bytes after it are no failure. */
export async function unpack(archive: string, dir: string, server = 'https://pazer.build'): Promise<void> {
	runCli(await buildhostCli(server), ['archive', 'unpack', archive, dir]);
}

/** The fixed pause between tries of a run file download. */
export const RunFileRetryMs = 5_000;

/**
 * Downloads url to file. A broken transfer resumes from the bytes it has with a Range request, and the whole file is checked
 * against the sha256 the server sends. A transport failure is tried again on a fixed cadence until the job timeout ends it,
 * so a server outage costs the time it lasts. A refusal, a miss, or a whole download that does not match is final.
 */
export async function fetchRunFile(
	url: string, bearer: () => Promise<string>, file: string,
	wait = (ms: number) => new Promise((r) => setTimeout(r, ms)), stallMs = DefaultStallMs,
): Promise<void> {
	rmSync(file, { force: true });
	let want = '';
	let failure = '';
	for (let attempt = 1; ; attempt++) {
		const have = existsSync(file) ? statSync(file).size : 0;
		const headers: Record<string, string> = { Authorization: `Bearer ${await bearer()}` };
		if (have > 0) headers.Range = `bytes=${have}-`;
		let sink: WriteStream | undefined;
		failure = '';
		try {
			await fetchStream(url, headers, (resp) => {
				want = resp.headers.get('X-Content-SHA256') ?? want;
				const out = createWriteStream(file, { flags: resp.status === 206 ? 'a' : 'w' });
				sink = out;
				return (chunk) => out.write(chunk) || new Promise((r) => out.once('drain', r));
			}, stallMs);
		} catch (e) {
			if (e instanceof HTTPError && [401, 403, 404].includes(e.status)) throw e;
			failure = e instanceof Error ? e.message : String(e);
		} finally {
			const out = sink;
			if (out) await new Promise<void>((r) => out.end(r));
		}
		if (!failure) {
			if (!want) throw new Error('the server sent no X-Content-SHA256 to check the download against');
			const got = await fileSHA256(file);
			if (got === want) return;
			rmSync(file, { force: true });
			failure = `downloaded sha256 ${got}, the server holds ${want}`;
			// A resumed file can join a couple of versions. A whole download that differs is what the server stores.
			if (have === 0) throw new Error(`run file download failed: ${failure}`);
		}
		console.warn(`run file download attempt ${attempt} failed: ${failure}; trying again in ${RunFileRetryMs / 1000}s`);
		await wait(RunFileRetryMs);
	}
}

/** buildhost-handoff: send packs path and stores it under name for this run. receive restores it into path. */
export async function handoff(g: ActionGlobals): Promise<void> {
	const mode = String(g.inputs.mode);
	const name = String(g.inputs.name);
	const server = String(g.inputs.server);
	const token = String(g.inputs.token);
	const dir = resolve(String(g.inputs.path) || '.');
	if (!validName.test(name)) { g.core.setFailed(`invalid name '${name}': want 1-128 of [A-Za-z0-9._-], starting with a letter or digit`); return; }
	if (mode !== 'send' && mode !== 'receive') { g.core.setFailed(`invalid mode '${mode}': want send or receive`); return; }
	const urlPath = runFilePath(name);

	await withArchive(g, `${mode} ${name}`, async (archive) => {
		if (mode === 'send') {
			if (!statSync(dir, { throwIfNoEntry: false })?.isDirectory()) throw new Error(`path ${dir} is not a directory`);
			const resp = await packAndUpload(g, server, token, dir, ['.'], archive, urlPath);
			if (resp.status !== 201) { g.core.setFailed(`send ${name} failed (HTTP ${resp.status}): ${resp.text}`); return; }
			g.core.info(`Sent ${dir} as run file ${name}: ${resp.text}`);
			return;
		}
		await downloadAndUnpack(`${server}${urlPath}`, tokenSource(server, token).bearer, archive, dir);
		g.core.info(`Received run file ${name} into ${dir}`);
		g.core.setOutput('path', dir);
	});
}

/**
 * Runs body with the path of a temporary archive, and deletes the archive afterwards. A throw fails the step, and the message
 * starts with what.
 */
export async function withArchive(g: ActionGlobals, what: string, body: (archive: string) => Promise<void>): Promise<void> {
	const archive = join(process.env.RUNNER_TEMP || mkdtempSync('buildhost-'), `buildhost-${process.pid}-${Date.now()}.tar.zst`);
	try {
		await body(archive);
	} catch (e) {
		g.core.setFailed(`${what}: ${e instanceof Error ? e.message : String(e)}`);
	} finally {
		rmSync(archive, { force: true });
	}
}

/** Downloads the archive url serves, checked against its sha256, and extracts it into dir. */
export async function downloadAndUnpack(url: string, bearer: () => Promise<string>, archive: string, dir: string): Promise<void> {
	await fetchRunFile(url, bearer, archive);
	await unpack(archive, dir, new URL(url).origin);
}

/** Packs members of dir into archive and uploads it to urlPath. */
export async function packAndUpload(
	g: ActionGlobals, server: string, token: string, dir: string, members: string[], archive: string, urlPath: string,
): Promise<import('./client').Response> {
	await pack(dir, archive, members, server);
	return uploadFile(g.core, server, token, urlPath, archive);
}
