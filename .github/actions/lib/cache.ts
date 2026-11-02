// buildhost-cache-restore and buildhost-cache-save: a directory kept across workflow runs under a key, through buildhost's cache.

import { existsSync } from 'node:fs';
import { homedir } from 'node:os';
import { join, parse, relative, resolve, sep } from 'node:path';
import type { ActionGlobals } from './action-globals.ts';
import { fetchRunFile, unpack, withArchive, packAndUpload } from './handoff.ts';
import { HTTPError } from './download.ts';
import { tokenSource } from './oidc.ts';

/** How long the lookup may take. */
const LookupTimeoutMs = 60_000;

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

/** The paths an entry holds, as members of one root. */
export interface CacheTree {
	root: string;
	members: string[];
}

/**
 * Reads the path input: one path per line, a relative path from the workspace, and a leading ~ as the home directory. A
 * restore must name the same paths as the save, because they decide where the files land.
 */
export function cacheTree(text: string, home = homedir(), workspace = process.env.GITHUB_WORKSPACE || process.cwd()): CacheTree {
	const lines = text.split('\n').map((l) => l.trim()).filter(Boolean);
	if (lines.length === 0) throw new Error('path names no path');
	const pattern = lines.find((l) => l.startsWith('!') || /[*?[\]]/.test(l));
	if (pattern) throw new Error(`path '${pattern}' is a pattern; the cache takes plain paths`);
	const ws = resolve(workspace);
	const abs = [...new Set(lines.map((p) => resolve(ws, p === '~' ? home : p.startsWith('~/') ? join(home, p.slice(2)) : p)))].sort();
	// A member is an archive name, so it is spelled with / on every host.
	const member = (from: string, p: string) => relative(from, p).split(sep).join('/') || '.';
	if (abs.every((p) => p === ws || p.startsWith(ws + sep))) {
		return { root: ws, members: abs.map((p) => member(ws, p)) };
	}
	const root = parse(abs[0]).root;
	const other = abs.find((p) => parse(p).root !== root);
	if (other) throw new Error(`paths ${abs[0]} and ${other} are on different drives; an entry holds paths of one drive`);
	return { root, members: abs.map((p) => member(root, p)) };
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
	const resp = await fetch(`${server}/api/v1/cache?${q}`, {
		headers: { Authorization: `Bearer ${await bearer()}` },
		signal: AbortSignal.timeout(LookupTimeoutMs),
	});
	const text = await resp.text();
	const failed = () => new Error(`cache lookup failed (HTTP ${resp.status}): ${text}`);
	if (resp.status !== 200 && resp.status !== 404) {
		const err = new HTTPError(resp.status, text);
		err.message = failed().message;
		throw err;
	}
	// Only the cache answers a lookup with its scopes.
	let found: CacheFind;
	try { found = JSON.parse(text) as CacheFind; } catch { throw failed(); }
	if (!Array.isArray(found?.scopes) || (resp.status === 200) !== Boolean(found.key)) throw failed();
	return found;
}

/** Whether e is a server or link outage. A restore reads an outage as a miss, as actions/cache does. A refusal stays a failure. */
export function serverOrLinkFailure(e: unknown): boolean {
	if (e instanceof HTTPError) return e.status >= 500 || [404, 408, 429].includes(e.status);
	if (e instanceof TypeError) return true;
	return e instanceof Error && (e.name === 'TimeoutError' || e.name === 'AbortError');
}

function errorText(e: unknown): string {
	return e instanceof Error ? e.message : String(e);
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
		const tree = cacheTree(String(g.inputs.path));
		const { bearer } = tokenSource(server, String(g.inputs.token));
		g.core.setOutput('cache-primary-key', key);
		const miss = (why: string) => {
			g.core.setOutput('cache-hit', 'false');
			g.core.setOutput('cache-matched-key', '');
			if (failOnMiss === 'true') { g.core.setFailed(why); return; }
			g.core.warning(why);
		};
		let found: CacheFind;
		try {
			found = await findCache(server, bearer, key, restoreKeys);
		} catch (e) {
			if (!serverOrLinkFailure(e)) throw e;
			miss(`cache restore of ${key} failed, so this run builds as a cache miss: ${errorText(e)}`);
			return;
		}
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
		const url = `${server}/api/v1/cache/${encodeURIComponent(found.key)}?${q}`;
		try {
			await fetchRunFile(url, bearer, archive);
		} catch (e) {
			// Past its retries, fetchRunFile throws a plain Error for a stall or a damaged body.
			if (e instanceof HTTPError && !serverOrLinkFailure(e)) throw e;
			miss(`cache restore of ${found.key} failed, so this run builds as a cache miss: ${errorText(e)}`);
			return;
		}
		// The download is whole and checked before this point, so a failed unpack is a defect, not an outage.
		await unpack(archive, tree.root, new URL(url).origin);
		g.core.setOutput('cache-hit', found.exact ? 'true' : 'false');
		g.core.setOutput('cache-matched-key', found.key);
		g.core.info(`Restored cache key ${found.key} from ${found.ref} into ${tree.members.join(', ')} under ${tree.root} (${found.size} bytes; ${searched})`);
	});
}

/** buildhost-cache-save: stores path under key for the run's ref. A key the ref holds already stays as it is. */
export async function saveCache(g: ActionGlobals): Promise<void> {
	const server = String(g.inputs.server);
	const token = String(g.inputs.token);
	const key = String(g.inputs.key);
	await withArchive(g, `save ${key}`, async (archive) => {
		checkKey(key, 'key');
		const tree = cacheTree(String(g.inputs.path));
		const members = tree.members.filter((m) => existsSync(join(tree.root, m)));
		for (const m of tree.members.filter((m) => !members.includes(m))) g.core.warning(`cache path ${join(tree.root, m)} does not exist, so the entry leaves it out`);
		if (members.length === 0) throw new Error(`no path to save exists: ${tree.members.map((m) => join(tree.root, m)).join(', ')}`);
		const found = await findCache(server, tokenSource(server, token).bearer, key, []);
		if (found.key === key && found.ref === process.env.GITHUB_REF) {
			g.core.info(`${found.ref} already holds cache key ${key}. A key is written once, so this save stores nothing.`);
			return;
		}
		const resp = await packAndUpload(g, server, token, tree.root, members, archive, `/api/v1/cache/${encodeURIComponent(key)}?${repoQuery(process.env)}`);
		if (resp.status === 409) { g.core.info(`Another job saved cache key ${key} first: ${resp.text}`); return; }
		if (resp.status !== 201) { g.core.setFailed(`save ${key} failed (HTTP ${resp.status}): ${resp.text}`); return; }
		g.core.info(`Saved ${members.join(', ')} under ${tree.root} as cache key ${key}: ${resp.text}`);
	});
}
