// Fakes the tests of the action libs share: a temporary directory, and a core that records what a step reports.

import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import type { ActionGlobals } from '../../.github/actions/lib/action-globals.ts';

export type ServeHandler = (req: import('node:http').IncomingMessage, res: import('node:http').ServerResponse) => void;

/** A real HTTP server on a free loopback port. close drops its open connections too. */
export async function serve(handler: ServeHandler): Promise<{ url: string; close: () => Promise<void> }> {
	const http = await import('node:http');
	const server = http.createServer(handler);
	await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
	const port = (server.address() as import('node:net').AddressInfo).port;
	return {
		url: `http://127.0.0.1:${port}/f`,
		close: () => new Promise<void>((r) => { server.closeAllConnections(); server.close(() => r()); }),
	};
}

/** A fake carries only the globals the code under test reads, so it stands in for the whole set the typescript action injects. */
export function globalsOf(fake: object): ActionGlobals {
	return fake as ActionGlobals;
}

/** A temporary directory holding the given files. */
export function tempDir(prefix: string, files: Record<string, string>): string {
	const d = fs.mkdtempSync(path.join(os.tmpdir(), prefix));
	for (const [name, body] of Object.entries(files)) fs.writeFileSync(path.join(d, name), body);
	return d;
}

/** Lets the job mint OIDC tokens from issuer, or with no issuer takes that away. */
export function oidcIssuer(issuer?: string): void {
	if (issuer === undefined) {
		delete process.env.ACTIONS_ID_TOKEN_REQUEST_URL;
		return;
	}
	process.env.ACTIONS_ID_TOKEN_REQUEST_URL = issuer;
	process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN = 'bearer';
}

/** A core whose failures, warnings, outputs and secrets land in `out`. */
export function stepCore() {
	const out = { failed: [] as string[], warned: [] as string[], outputs: {} as Record<string, string>, secrets: [] as string[] };
	const core = {
		info: (_m: string) => {},
		warning: (m: string) => { out.warned.push(m); },
		setFailed: (m: string) => { out.failed.push(m); },
		setOutput: (k: string, v: string) => { out.outputs[k] = v; },
		setSecret: (s: string) => { out.secrets.push(s); },
	};
	return { core, out };
}
