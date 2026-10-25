// buildhost-cache-restore and buildhost-cache-save: a directory kept across workflow runs under a key, through buildhost's cache.

import { existsSync, readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join, parse, relative, resolve, sep } from 'node:path';
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

/** The query that names the caller's repository, which the server checks against the token, and its default branch. Which the server reads when GitHub does not answer it. */
function repoQuery(env: NodeJS.ProcessEnv): URLSearchParams {
	const repository = env.GITHUB_REPOSITORY;
	if (!repository) throw new Error('GITHUB_REPOSITORY must be set: the cache belongs to a repository');
	const q = new URLSearchParams({ repository });
	const branch = eventDefaultBranch(env);
	if (branch) q.set('default_branch', branch);
	return q;
}

/** The default branch the run's event payload names, or '' when it names none. */
export function eventDefaultBranch(env: NodeJS.ProcessEnv): string {
	const file = env.GITHUB_EVENT_PATH;
	if (!file || !existsSync(file)) return '';
	const event = JSON.parse(readFileSync(file, 'utf8')) as { repository?: { default_branch?: unknown } };
	const branch = event.repository?.default_branch;
	return typeof branch === 'string' ? branch : '';
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
		const tree = cacheTree(String(g.inputs.path));
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
		await downloadAndUnpack(`${server}/api/v1/cache/${encodeURIComponent(found.key)}?${q}`, bearer, archive, tree.root);
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
