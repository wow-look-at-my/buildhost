// Behavior tests for .github/actions/lib/handoff.ts and the cache on top of it: the archive round trip. A download that
// breaks and resumes, and a cache restore and save.

import assert from 'node:assert';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { pipeline } from 'node:stream/promises';
import { createZstdCompress } from 'node:zlib';
import { pack, unpack, fetchRunFile, runFilePath, tarCommand } from '../../.github/actions/lib/handoff.ts';
import { HTTPError } from '../../.github/actions/lib/download.ts';
import { cacheTree, restoreKeyList, restoreCache, saveCache } from '../../.github/actions/lib/cache.ts';
import { globalsOf, serve, stepCore, tempDir } from './fakes.ts';

const noWait = async () => {};
const bearer = async () => 't';

async function roundTrip(tmp: string) {
	const src = path.join(tmp, 'src');
	fs.mkdirSync(path.join(src, 'bin'), { recursive: true });
	fs.writeFileSync(path.join(src, 'bin', 'go'), 'binary');
	fs.chmodSync(path.join(src, 'bin', 'go'), 0o755);
	fs.symlinkSync('../bin/go', path.join(src, 'compile'));
	const archive = path.join(tmp, 'a.tar.zst');
	await pack(src, archive);
	const dst = path.join(tmp, 'dst');
	await unpack(archive, dst);
	assert.strictEqual(fs.readFileSync(path.join(dst, 'bin', 'go'), 'utf8'), 'binary', 'the contents survive');
	assert.strictEqual(fs.statSync(path.join(dst, 'bin', 'go')).mode & 0o111, 0o111, 'the exec bit survives');
	assert.strictEqual(fs.readlinkSync(path.join(dst, 'compile')), '../bin/go', 'a symlink stays a symlink');
}

async function bytesAfterTheEndMarker(tmp: string) {
	const src = path.join(tmp, 'padded-src');
	fs.mkdirSync(src, { recursive: true });
	fs.writeFileSync(path.join(src, 'f'), 'payload');
	const plain = path.join(tmp, 'padded.tar');
	execFileSync(tarCommand(), ['-cf', plain, '-C', src, '.']);
	fs.appendFileSync(plain, Buffer.alloc(4_000_000));
	const archive = path.join(tmp, 'padded.tar.zst');
	await pipeline(fs.createReadStream(plain), createZstdCompress(), fs.createWriteStream(archive));
	const dst = path.join(tmp, 'padded-dst');
	await unpack(archive, dst);
	assert.strictEqual(fs.readFileSync(path.join(dst, 'f'), 'utf8'), 'payload', 'tar stops at its end marker, and the bytes after it are no failure');
}

async function corruptArchiveFails(tmp: string) {
	const archive = path.join(tmp, 'garbage.tar.zst');
	fs.writeFileSync(archive, 'not zstd');
	await assert.rejects(unpack(archive, path.join(tmp, 'garbage-dst')));
}

// The cache actions run as a pull request's job in workspace tmp, so a restore can match an entry of the default branch.
function cacheInputs(server: string, extra: Record<string, string>) {
	process.env.GITHUB_REPOSITORY = 'wow-look-at-my/consumer';
	process.env.GITHUB_REF = 'refs/pull/7/merge';
	return { server: server.replace(/\/f$/, ''), token: 't', ...extra };
}

async function cacheRestoreHitReadsTheMatchedScope(tmp: string) {
	process.env.GITHUB_WORKSPACE = tmp;
	fs.mkdirSync(path.join(tmp, 'cache-dst'));
	fs.writeFileSync(path.join(tmp, 'cache-dst', 'entry'), 'cached');
	const archive = path.join(tmp, 'cache-entry.tar.zst');
	await pack(tmp, archive, tarCommand(), ['cache-dst']);
	fs.rmSync(path.join(tmp, 'cache-dst'), { recursive: true });
	const body = fs.readFileSync(archive);
	const sum = createHash('sha256').update(body).digest('hex');
	const asked: string[] = [];
	const s = await serve((req, res) => {
		asked.push(req.url ?? '');
		if (new URL(req.url ?? '', 'http://x').pathname === '/api/v1/cache') {
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify({ key: 'deps-old', ref: 'refs/heads/master', size: body.length, exact: false, scopes: ['refs/pull/7/merge', 'refs/heads/master'] }));
			return;
		}
		res.writeHead(200, { 'X-Content-SHA256': sum });
		res.end(body);
	});
	const { core, out } = stepCore();
	await restoreCache(globalsOf({ core, inputs: cacheInputs(s.url, { path: 'cache-dst', key: 'deps-new', 'restore-keys': 'nothing-\ndeps-\n' }) }));
	await s.close();
	assert.deepStrictEqual(out.failed, []);
	assert.strictEqual(fs.readFileSync(path.join(tmp, 'cache-dst', 'entry'), 'utf8'), 'cached', 'the matched entry lands at its path under the workspace');
	assert.strictEqual(out.outputs['cache-hit'], 'false', 'a prefix match is not a hit');
	assert.strictEqual(out.outputs['cache-matched-key'], 'deps-old');
	assert.deepStrictEqual(new URL(asked[0], 'http://x').searchParams.getAll('restore_keys'), ['nothing-', 'deps-'], 'the restore keys go in order');
	assert.strictEqual(new URL(asked[1], 'http://x').searchParams.get('ref'), 'refs/heads/master', 'the download names the scope that matched');
}

async function cacheMissLeavesPathAlone(tmp: string, failOnMiss: string, wantFailed: number) {
	const s = await serve((_req, res) => {
		res.writeHead(404, { 'Content-Type': 'application/json' });
		res.end(JSON.stringify({ scopes: ['refs/pull/7/merge'], default_branch: 'not read: GitHub did not answer the default branch' }));
	});
	const { core, out } = stepCore();
	const dst = path.join(tmp, `cache-miss-${failOnMiss}`);
	await restoreCache(globalsOf({ core, inputs: cacheInputs(s.url, { path: dst, key: 'k', 'fail-on-cache-miss': failOnMiss }) }));
	await s.close();
	assert.strictEqual(out.outputs['cache-hit'], 'false');
	assert.strictEqual(fs.existsSync(dst), false, 'a miss creates nothing');
	assert.strictEqual(out.failed.length, wantFailed);
	if (wantFailed) assert.match(out.failed[0], /GitHub did not answer/, 'the miss says why the default branch was not searched');
}

async function cacheSaveOfAHeldKeySendsNothing(tmp: string) {
	const asked: string[] = [];
	const s = await serve((req, res) => {
		asked.push(`${req.method} ${new URL(req.url ?? '', 'http://x').pathname}`);
		res.writeHead(200, { 'Content-Type': 'application/json' });
		res.end(JSON.stringify({ key: 'k', ref: 'refs/pull/7/merge', size: 1, exact: true, scopes: ['refs/pull/7/merge'] }));
	});
	const { core, out } = stepCore();
	await saveCache(globalsOf({ core, inputs: cacheInputs(s.url, { path: tmp, key: 'k' }) }));
	await s.close();
	assert.deepStrictEqual(out.failed, []);
	assert.deepStrictEqual(asked, ['GET /api/v1/cache'], 'the save asks once and sends no body');
}

async function cacheInputsAreChecked(tmp: string) {
	assert.deepStrictEqual(cacheTree('pkg/obj/go-build\n\nbin\nbin\n', '/home/u', '/w/repo'), { root: '/w/repo', members: ['bin', 'pkg/obj/go-build'] },
		'paths inside the workspace are members of it, sorted once each, so another workspace directory restores them');
	assert.deepStrictEqual(cacheTree('~/.cache/wasmtime\nout', '/home/u', '/w/repo'), { root: '/', members: ['home/u/.cache/wasmtime', 'w/repo/out'] },
		'a path outside the workspace roots every path at the filesystem root');
	assert.throws(() => cacheTree('src/**/*.o', '/home/u', '/w/repo'), /is a pattern/);
	assert.throws(() => cacheTree('!skip', '/home/u', '/w/repo'), /is a pattern/);
	assert.throws(() => cacheTree('\n', '/home/u', '/w/repo'), /names no path/);
	assert.deepStrictEqual(restoreKeyList(' a-\n\nb- \n'), ['a-', 'b-']);
	const { core, out } = stepCore();
	await restoreCache(globalsOf({ core, inputs: cacheInputs('http://127.0.0.1:1/f', { path: tmp, key: 'bad key' }) }));
	assert.match(out.failed[0], /invalid key 'bad key'/);
}

async function resumes(tmp: string) {
	const body = Buffer.from('0123456789abcdefghij');
	const sum = createHash('sha256').update(body).digest('hex');
	const ranges: string[] = [];
	const s = await serve((req, res) => {
		const range = req.headers.range ?? '';
		ranges.push(range);
		res.writeHead(range ? 206 : 200, { 'X-Content-SHA256': sum });
		if (ranges.length === 1) {
			res.write(body.subarray(0, 8), () => setTimeout(() => res.destroy(), 100));
			return;
		}
		res.end(body.subarray(range ? Number(/bytes=(\d+)-/.exec(range)![1]) : 0));
	});
	const file = path.join(tmp, 'dl');
	await fetchRunFile(s.url, bearer, file, noWait);
	assert.deepStrictEqual(fs.readFileSync(file), body, 'a broken transfer resumes into the whole file');
	assert.deepStrictEqual(ranges, ['', 'bytes=8-'], 'the second request asks only for the missing bytes');
	await s.close();
}

async function missIsFinal(tmp: string) {
	let calls = 0;
	const s = await serve((_req, res) => { calls++; res.writeHead(404); res.end('{"error":"run o/r#1 holds no file named x; it holds no files"}'); });
	await assert.rejects(fetchRunFile(s.url, bearer, path.join(tmp, 'miss'), noWait),
		(e: unknown) => e instanceof HTTPError && e.status === 404 && /holds no file named x/.test(e.message));
	assert.strictEqual(calls, 1, 'a miss is final and is not asked again');
	await s.close();
}

async function corruptFails(tmp: string) {
	const s = await serve((_req, res) => { res.writeHead(200, { 'X-Content-SHA256': '0'.repeat(64) }); res.end('corrupt'); });
	await assert.rejects(fetchRunFile(s.url, bearer, path.join(tmp, 'bad'), noWait, 2), /downloaded sha256 .* the server holds 0{64}/);
	await s.close();
}

async function run() {
	const tmp = tempDir('handoff-', {});
	await roundTrip(tmp);
	await bytesAfterTheEndMarker(tmp);
	await corruptArchiveFails(tmp);
	await resumes(tmp);
	await missIsFinal(tmp);
	await corruptFails(tmp);
	await cacheInputsAreChecked(tmp);
	await cacheRestoreHitReadsTheMatchedScope(tmp);
	await cacheMissLeavesPathAlone(tmp, 'false', 0);
	await cacheMissLeavesPathAlone(tmp, 'true', 1);
	await cacheSaveOfAHeldKeySendsNothing(tmp);
	assert.strictEqual(
		runFilePath('toolchain-Linux', { GITHUB_REPOSITORY: 'wow-look-at-my/gosmopolitan', GITHUB_RUN_ID: '42' }),
		'/api/v1/run-files/toolchain-Linux?repository=wow-look-at-my%2Fgosmopolitan&run_id=42');
	assert.throws(() => runFilePath('x', {}), /GITHUB_RUN_ID/);
	assert.strictEqual(tarCommand('win32', { SystemRoot: 'C:\\Windows' }), 'C:\\Windows\\System32\\tar.exe');
	assert.strictEqual(tarCommand('linux', {}), 'tar');
	fs.rmSync(tmp, { recursive: true, force: true });
	console.log('handoff: all checks passed');
}

run().catch((e) => { console.error(e); process.exit(1); });
