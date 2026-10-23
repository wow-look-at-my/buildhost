// The buildhost CLI the hand-off and cache actions pack and extract with. Its archive command is native Go: parallel file IO
// and multi-threaded zstd, and the same pax archive on every host.

import { execFileSync } from 'node:child_process';
import { copyFileSync, chmodSync, existsSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { downloadURL, fetchBody } from './download.ts';

let cached: string | undefined;

/** The argv that starts the CLI at path. An APE starts through its shell header on a posix host, and by its .exe name on NT. */
export function cliArgv(path: string, platform = process.platform): [string, string[]] {
	if (platform !== 'win32') return ['sh', [path]];
	const exe = path.endsWith('.exe') ? path : `${path}.exe`;
	if (!existsSync(exe)) copyFileSync(path, exe);
	return [exe, []];
}

/** Runs the CLI with args, and fails with its output when it exits non-zero. */
export function runCli(path: string, args: string[]): void {
	const [cmd, pre] = cliArgv(path);
	execFileSync(cmd, [...pre, ...args], { stdio: ['ignore', 'inherit', 'inherit'] });
}

/**
 * Finds the CLI: BUILDHOST_CLI when the job names one, else the newest build on server. A CLI with no archive command
 * fails here, by name, rather than at the first pack.
 */
export async function buildhostCli(server: string): Promise<string> {
	if (cached) return cached;
	let path = process.env.BUILDHOST_CLI ?? '';
	if (!path) {
		path = join(process.env.RUNNER_TEMP || '.', 'buildhost-archive-cli');
		const url = downloadURL({ server, project: 'buildhost', version: '', branch: '', os: '', arch: '', format: '' });
		writeFileSync(path, await fetchBody(url, {}));
		chmodSync(path, 0o755);
	}
	try {
		runCli(path, ['archive', '--help']);
	} catch (e) {
		throw new Error(`the buildhost CLI at ${path} cannot run its archive command: ${e instanceof Error ? e.message : String(e)}`);
	}
	cached = path;
	return path;
}
