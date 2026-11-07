// Fetches one file for a composite action. The limit is on silence, not on total time, so a large file on a slow link still succeeds.

import * as fs from 'node:fs';
import * as path from 'node:path';
import type { ActionGlobals } from './action-globals';
import { tokenSource } from './oidc.ts';

/** How long a transfer may go without a byte, before the headers or during the body. */
export const DefaultStallMs = 30_000;

/** A non-2xx answer. status says which, so a caller can tell a miss from a blip. */
export class HTTPError extends Error {
	readonly status: number;
	constructor(status: number, body: string) {
		super(`HTTP ${status}${body ? `: ${body.slice(0, 500)}` : ''}`);
		this.status = status;
	}
}

/**
 * Fetch url and hand each piece of the body to the writer that open returns for the response. It rejects with an HTTPError on a
 * non-2xx status, and when no byte arrives for stallMs.
 */
export async function fetchStream(
	url: string, headers: Record<string, string>,
	open: (response: Response) => (chunk: Uint8Array) => unknown,
	stallMs = DefaultStallMs,
): Promise<Response> {
	const controller = new AbortController();
	const stalled = () => controller.abort(new Error(`no bytes for ${stallMs / 1000}s`));
	let timer = setTimeout(stalled, stallMs);
	try {
		const response = await fetch(url, { headers, redirect: 'follow', signal: controller.signal });
		if (!response.ok) throw new HTTPError(response.status, await response.text());
		if (!response.body) throw new Error('response has no body');
		const write = open(response);
		const reader = response.body.getReader();
		for (;;) {
			clearTimeout(timer);
			timer = setTimeout(stalled, stallMs);
			const { done, value } = await reader.read();
			if (done) break;
			await write(value);
		}
		return response;
	} finally {
		clearTimeout(timer);
	}
}

/** Fetch url and return the whole body, on the terms of fetchStream. */
export async function fetchBody(url: string, headers: Record<string, string>, stallMs = DefaultStallMs): Promise<Buffer> {
	const chunks: Buffer[] = [];
	await fetchStream(url, headers, () => (chunk) => chunks.push(Buffer.from(chunk)), stallMs);
	return Buffer.concat(chunks);
}

const runnerArch: Record<string, string> = { X64: "amd64", ARM64: "arm64", X86: "386", ARM: "arm" };

/** The dl URL of one artifact. dl is a subdomain of the server's own host, and os and arch default to the runner's own platform. */
export function downloadURL(f: { server: string; project: string; version: string; branch: string; os: string; arch: string; format: string }, runnerOS = process.env.RUNNER_OS, runnerArchName = process.env.RUNNER_ARCH): string {
	const host = new URL(f.server).host;
	const query = new URLSearchParams({
		os: f.os || (runnerOS ?? "Linux").toLowerCase(),
		arch: f.arch || runnerArch[runnerArchName ?? "X64"] || "amd64",
	});
	for (const [key, value] of Object.entries({ v: f.version, branch: f.branch, fmt: f.format })) {
		if (value) query.set(key, value);
	}
	return `https://dl.${host}/${f.project}?${query}`;
}

/** How long download waits between tries of a transfer that stalled or failed on the server side. */
export const RetryMs = 5_000;

/** A 4xx answer is the registry's verdict, so a try again gets the same answer. */
function isFinal(e: unknown): boolean {
	return e instanceof HTTPError && e.status >= 400 && e.status < 500 && e.status !== 408 && e.status !== 429;
}

/**buildhost-download: writes the artifact and outputs path and downloaded. A stall, a network error or a 5xx gets
 * a new try every RetryMs until the job timeout stops it. With required false, a miss is a warning and downloaded=false,
 * so a caller can fall back. */
export async function download(g: ActionGlobals, wait = (ms: number) => new Promise((r) => setTimeout(r, ms))): Promise<void> {
	const { core, inputs } = g;
	const project = String(inputs.project);
	// The composite's secret-server step exports the read token secret-server vends the calling repository, so a private project needs no token input.
	const token = String(inputs.token) || process.env.BUILDHOST_READ_TOKEN || "";
	const url = downloadURL({
		server: String(inputs.server), project, version: String(inputs.version), branch: String(inputs.branch),
		os: String(inputs.os), arch: String(inputs.arch), format: String(inputs.format),
	});
	// A private project needs a credential. Without a token input, the job's OIDC token is it.
	const bearer = tokenSource(String(inputs.server), token);
	const destination: string = String(inputs.path) || path.join(process.env.RUNNER_TEMP!, project.split("/").pop()!);

	let lastError = "";
	for (let attempt = 1; ; attempt++) {
		try {
			const auth = await bearer.bearer();
			const body = await fetchBody(url, auth ? { Authorization: `Bearer ${auth}` } : {});
			fs.mkdirSync(path.dirname(destination), { recursive: true });
			fs.writeFileSync(destination, body);
			if (String(inputs.executable) === "true") fs.chmodSync(destination, 0o755);
			core.info(`Downloaded ${url} -> ${destination}`);
			core.setOutput("path", destination);
			core.setOutput("downloaded", "true");
			return;
		} catch (e) {
			lastError = e instanceof Error ? e.message : String(e);
			if (isFinal(e)) break;
			core.warning(`Try ${attempt} to download ${url} failed, next try in ${RetryMs / 1000}s: ${lastError}`);
			await wait(RetryMs);
		}
	}

	core.setOutput("path", "");
	core.setOutput("downloaded", "false");
	if (String(inputs.required) === "true") {
		core.setFailed(`Could not download ${url}: ${lastError}`);
	} else {
		core.warning(`Could not download ${url}: ${lastError}`);
	}
}
