// buildhost-cache-restore and buildhost-cache-save: a directory kept across workflow runs under a key, through buildhost's cache.

import { homedir } from 'node:os';
import { join, resolve } from 'node:path';
import type { ActionGlobals } from './action-globals.ts';
import { downloadAndUnpack, withArchive, packAndUpload } from './handoff.ts';
import { tokenSource } from './oidc.ts';

const validKey = /^[A-Za-z0-9._-]{1,512}$/;

/** The answer of GET /api/v1/cache. key is absent on a miss. */
export interface CacheFind {
	key?: string;
	ref?: string;
	size?: number;
	exact?: boolean;
	scopes: string[];
	default_branch?: string;
}

/** The directory path names: one path, with a leading ~ read as the home directory. */
export function cachePath(path: string, home = homedir()): string {
	const lines = path.split('\n').map((l) => l.trim()).filter(Boolean);
	if (lines.length !== 1) throw new Error(`path must name one directory, got ${lines.length}: ${JSON.stringify(lines)}`);
	const p = lines[0];
	return resolve(p === '~' ? home : p.startsWith('~/') ? join(home, p.slice(2)) : p);
}

/** The restore keys, one per line, in the order the restore tries them. */
export function restoreKeyList(text: string): string[] {
	return text.split('\n').map((l) => l.trim()).filter(Boolean);
}

function checkKey(key: string, what: string): void {
	if (!validKey.test(key)) throw new Error(`invalid ${what} '${key}': want 1-512 of [A-Za-z0-9._-]`);
}

/** The query that names the caller's repository, which the server checks against the token. */
function repoQuery(env: NodeJS.ProcessEnv): URLSearchParams {
	const repository = env.GITHUB_REPOSITORY;
	if (!repository) throw new Error('GITHUB_REPOSITORY must be set: the cache belongs to a repository');
	return new URLSearchParams({ repository });
}

/** Asks the server which entry a restore of key and restoreKeys reads. */
export async function findCache(server: string, bearer: () => Promise<string>, key: string, restoreKeys: string[], env = process.env): Promise<CacheFind> {
	const q = repoQuery(env);
	q.set('key', key);
	for (const k of restoreKeys) q.append('restore_keys', k);
	const resp = await fetch(`${server}/api/v1/cache?${q}`, { headers: { Authorization: `Bearer ${await bearer()}` } });
	const text = await resp.text();
	if (resp.status !== 200 && resp.status !== 404) throw new Error(`cache lookup failed (HTTP ${resp.status}): ${text}`);
	return JSON.parse(text) as CacheFind;
}

/** buildhost-cache-restore: restores the entry key or restore-keys finds into path. A miss leaves path as it is. */
export async function restoreCache(g: ActionGlobals): Promise<void> {
	const server = String(g.inputs.server);
	const key = String(g.inputs.key);
	const restoreKeys = restoreKeyList(String(g.inputs['restore-keys'] ?? ''));
	const failOnMiss = String(g.inputs['fail-on-cache-miss'] ?? 'false');
	if (failOnMiss !== 'true' && failOnMiss !== 'false') { g.core.setFailed(`fail-on-cache-miss must be true or false, got '${failOnMiss}'`); return; }
	await withArchive(g, `restore ${key}`, async (archive) => {
		checkKey(key, 'key');
		restoreKeys.forEach((k) => checkKey(k, 'restore key'));
		const dir = cachePath(String(g.inputs.path));
		const { bearer } = tokenSource(server, String(g.inputs.token));
		g.core.setOutput('cache-primary-key', key);
		const found = await findCache(server, bearer, key, restoreKeys);
		const searched = `searched ${found.scopes.join(', ')}` + (found.default_branch ? `; the default branch was ${found.default_branch}` : '');
		if (!found.key) {
			g.core.setOutput('cache-hit', 'false');
			g.core.setOutput('cache-matched-key', '');
			const msg = `No cache entry for key ${key}` + (restoreKeys.length ? ` or restore keys ${restoreKeys.join(', ')}` : '') + ` (${searched})`;
			if (failOnMiss === 'true') { g.core.setFailed(msg); return; }
			g.core.info(msg);
			return;
		}
		const q = repoQuery(process.env);
		q.set('ref', found.ref!);
		await downloadAndUnpack(`${server}/api/v1/cache/${encodeURIComponent(found.key)}?${q}`, bearer, archive, dir);
		g.core.setOutput('cache-hit', found.exact ? 'true' : 'false');
		g.core.setOutput('cache-matched-key', found.key);
		g.core.info(`Restored cache key ${found.key} from ${found.ref} into ${dir} (${found.size} bytes; ${searched})`);
	});
}

/** buildhost-cache-save: stores path under key for the run's ref. A key the ref holds already stays as it is. */
export async function saveCache(g: ActionGlobals): Promise<void> {
	const server = String(g.inputs.server);
	const token = String(g.inputs.token);
	const key = String(g.inputs.key);
	await withArchive(g, `save ${key}`, async (archive) => {
		checkKey(key, 'key');
		const dir = cachePath(String(g.inputs.path));
		const found = await findCache(server, tokenSource(server, token).bearer, key, []);
		if (found.key === key && found.ref === process.env.GITHUB_REF) {
			g.core.info(`${found.ref} already holds cache key ${key}. A key is written once, so this save stores nothing.`);
			return;
		}
		const resp = await packAndUpload(g, server, token, dir, archive, `/api/v1/cache/${encodeURIComponent(key)}?${repoQuery(process.env)}`);
		if (resp.status === 409) { g.core.info(`Another job saved cache key ${key} first: ${resp.text}`); return; }
		if (resp.status !== 201) { g.core.setFailed(`save ${key} failed (HTTP ${resp.status}): ${resp.text}`); return; }
		g.core.info(`Saved ${dir} as cache key ${key}: ${resp.text}`);
	});
}
