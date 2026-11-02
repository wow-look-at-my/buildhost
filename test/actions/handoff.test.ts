// Behavior tests for .github/actions/lib/handoff.ts and the cache on top of it: the archive round trip. A download that
// breaks and resumes, and a cache restore and save.

import assert from 'node:assert';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { pipeline } from 'node:stream/promises';
import { createZstdCompress } from 'node:zlib';
import { pack, unpack, fetchRunFile, runFilePath, RunFileRetryMs } from '../../.github/actions/lib/handoff.ts';
import { HTTPError } from '../../.github/actions/lib/download.ts';
import { fetchCli } from '../../.github/actions/lib/cli.ts';
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
	// NTFS holds no exec bit, so node reports none on Windows.
	const exec = process.platform === 'win32' ? 0 : 0o111;
	assert.strictEqual(fs.statSync(path.join(dst, 'bin', 'go')).mode & 0o111, exec, 'the exec bit survives where the host holds one');
	assert.strictEqual(fs.readlinkSync(path.join(dst, 'compile')).split(path.sep).join('/'), '../bin/go', 'a symlink stays a symlink');
}

// The metadata a tree keeps through a pack: the parts a host tar dropped or crashed on.
async function metadataRoundTrip(tmp: string) {
	const src = path.join(tmp, 'meta-src');
	const long = `${'d'.repeat(120)}/unicod\u00e9-\u65e5\u672c-${'n'.repeat(110)}.txt`;
	fs.mkdirSync(path.join(src, path.dirname(long)), { recursive: true });
	fs.mkdirSync(path.join(src, 'deep', 'empty'), { recursive: true });
	fs.writeFileSync(path.join(src, long), 'long name');
	fs.writeFileSync(path.join(src, 'a.bin'), Buffer.alloc(70_000, 7));
	fs.linkSync(path.join(src, 'a.bin'), path.join(src, 'b.bin'));
	fs.writeFileSync(path.join(src, 'ro.txt'), 'read-only');
	fs.symlinkSync('deep', path.join(src, 'dir-link'), 'dir');
	fs.symlinkSync('no-such-target', path.join(src, 'dangling'), 'file');
	const when = 981173106.5;
	for (const f of [long, 'a.bin', 'ro.txt']) fs.utimesSync(path.join(src, f), when, when);
	fs.utimesSync(path.join(src, 'deep', 'empty'), when, when);
	fs.chmodSync(path.join(src, 'ro.txt'), 0o444);
	const archive = path.join(tmp, 'meta.tar.zst');
	await pack(src, archive);
	const dst = path.join(tmp, 'meta-dst');
	fs.mkdirSync(dst);
	fs.writeFileSync(path.join(dst, 'ro.txt'), 'stale');
	fs.chmodSync(path.join(dst, 'ro.txt'), 0o444);
	await unpack(archive, dst);
	const ms = (p: string) => fs.lstatSync(path.join(dst, p)).mtimeMs;
	assert.strictEqual(fs.readFileSync(path.join(dst, long), 'utf8'), 'long name', 'a name past 100 bytes and outside ASCII survives');
	assert.strictEqual(ms(long), when * 1000, 'a sub-second mtime survives');
	assert.strictEqual(ms(path.join('deep', 'empty')), when * 1000, 'a directory keeps its mtime');
	const a = fs.statSync(path.join(dst, 'a.bin'));
	const b = fs.statSync(path.join(dst, 'b.bin'));
	assert.strictEqual(a.ino, b.ino, 'a hardlink stays one inode');
	assert.strictEqual(fs.readFileSync(path.join(dst, 'ro.txt'), 'utf8'), 'read-only', 'a read-only file in the way is replaced');
	assert.strictEqual(fs.statSync(path.join(dst, 'ro.txt')).mode & 0o200, 0, 'and the replacement is read-only again');
	assert.ok(fs.statSync(path.join(dst, 'dir-link')).isDirectory(), 'a directory symlink resolves to the directory');
	assert.strictEqual(fs.readlinkSync(path.join(dst, 'dangling')), 'no-such-target', 'a dangling symlink is kept');
}

// A cache path outside the workspace roots the entry at the filesystem root, which is a drive root on Windows.
async function restoreIntoTheFilesystemRoot(tmp: string) {
	const file = path.join(tmp, 'rooted', 'entry.txt');
	fs.mkdirSync(path.dirname(file), { recursive: true });
	fs.writeFileSync(file, 'rooted');
	const root = path.parse(file).root;
	const member = path.relative(root, file).split(path.sep).join('/');
	const archive = path.join(tmp, 'rooted.tar.zst');
	await pack(root, archive, [member]);
	fs.rmSync(path.dirname(file), { recursive: true });
	await unpack(archive, root);
	assert.strictEqual(fs.readFileSync(file, 'utf8'), 'rooted', 'an entry rooted at the filesystem root restores');
}

// A cache entry an older action version saved was written by the host tar. It must still restore.
function hostTar(): string {
	return process.platform === 'win32' ? path.join(process.env.SystemRoot ?? 'C:\\Windows', 'System32', 'tar.exe') : 'tar';
}

async function bytesAfterTheEndMarker(tmp: string) {
	const src = path.join(tmp, 'padded-src');
	fs.mkdirSync(path.join(src, 'sub'), { recursive: true });
	fs.writeFileSync(path.join(src, 'f'), 'payload');
	fs.writeFileSync(path.join(src, 'sub', `${'g'.repeat(150)}.txt`), 'gnu long name');
	const plain = path.join(tmp, 'padded.tar');
	execFileSync(hostTar(), ['-cf', plain, '-C', src, '.']);
	fs.appendFileSync(plain, Buffer.alloc(4_000_000));
	const archive = path.join(tmp, 'padded.tar.zst');
	await pipeline(fs.createReadStream(plain), createZstdCompress(), fs.createWriteStream(archive));
	const dst = path.join(tmp, 'padded-dst');
	await unpack(archive, dst);
	assert.strictEqual(fs.readFileSync(path.join(dst, 'f'), 'utf8'), 'payload', 'tar stops at its end marker, and the bytes after it are no failure');
	assert.strictEqual(fs.readFileSync(path.join(dst, 'sub', `${'g'.repeat(150)}.txt`), 'utf8'), 'gnu long name', "the host tar's long name reads back");
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
	await pack(tmp, archive, ["cache-dst"]);
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

// A that is not the cache's own answer is a wrong server, not an outage: it fails with its status and body.
async function cacheLookupFailureIsNoMiss(tmp: string, status: number, body: string) {
	const s = await serve((_req, res) => { res.writeHead(status); res.end(body); });
	const { core, out } = stepCore();
	await restoreCache(globalsOf({ core, inputs: cacheInputs(s.url, { path: path.join(tmp, 'cache-err'), key: 'k', 'fail-on-cache-miss': 'false' }) }));
	await s.close();
	assert.strictEqual(out.failed.length, 1, `HTTP ${status} fails the restore`);
	assert.match(out.failed[0], new RegExp(`cache lookup failed \\(HTTP ${status}\\)`));
	assert.doesNotMatch(out.failed[0], /No cache entry/);
}

// An outage of the server or the link is a miss with a warning, so the run builds without the cache. fail-on-cache-miss still fails it.
async function cacheOutageIsAWarnedMiss(tmp: string, failOnMiss: string, lookupStatus: number, downloadStatus = 0) {
	const s = await serve((req, res) => {
		if (new URL(req.url ?? '', 'http://x').pathname === '/api/v1/cache') {
			if (lookupStatus !== 200) { res.writeHead(lookupStatus); res.end('busy'); return; }
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify({ key: 'k', ref: 'refs/heads/master', size: 1, exact: true, scopes: ['refs/heads/master'] }));
			return;
		}
		res.writeHead(downloadStatus);
		res.end('gone');
	});
	const { core, out } = stepCore();
	const dst = path.join(tmp, `cache-outage-${failOnMiss}-${lookupStatus}-${downloadStatus}`);
	await restoreCache(globalsOf({ core, inputs: cacheInputs(s.url, { path: dst, key: 'k', 'fail-on-cache-miss': failOnMiss }) }));
	await s.close();
	const what = `lookup ${lookupStatus}, download ${downloadStatus}, fail-on-cache-miss ${failOnMiss}`;
	assert.strictEqual(out.outputs['cache-hit'], 'false', what);
	assert.strictEqual(out.outputs['cache-matched-key'], '', what);
	assert.strictEqual(fs.existsSync(dst), false, `an outage restores nothing (${what})`);
	const said = failOnMiss === 'true' ? out.failed : out.warned;
	assert.strictEqual(said.length, 1, `exactly one ${failOnMiss === 'true' ? 'failure' : 'warning'} (${what})`);
	assert.match(said[0], /builds as a cache miss/, what);
	if (failOnMiss === 'false') assert.deepStrictEqual(out.failed, [], `an outage never fails the step (${what})`);
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
	// The root is a host path: /w/repo resolves to D:\w\repo on Windows. The members are archive names, with / on every host.
	const ws = path.resolve('/w/repo');
	assert.deepStrictEqual(cacheTree('pkg/obj/go-build\n\nbin\nbin\n', '/home/u', '/w/repo'), { root: ws, members: ['bin', 'pkg/obj/go-build'] },
		'paths inside the workspace are members of it, sorted once each, so another workspace directory restores them');
	assert.deepStrictEqual(cacheTree('~/.cache/wasmtime\nout', '/home/u', '/w/repo'), { root: path.parse(ws).root, members: ['home/u/.cache/wasmtime', 'w/repo/out'] },
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
	let calls = 0;
	const s = await serve((_req, res) => { calls++; res.writeHead(200, { 'X-Content-SHA256': '0'.repeat(64) }); res.end('corrupt'); });
	await assert.rejects(fetchRunFile(s.url, bearer, path.join(tmp, 'bad'), noWait), /downloaded sha256 .* the server holds 0{64}/);
	assert.strictEqual(calls, 1, 'a whole download that does not match is what the server stores, so it is not asked again');
	await s.close();
}

async function outlastsAnOutage(tmp: string) {
	// The server cuts transfers, then sends nothing, for more tries than a capped retry allows.
	const body = Buffer.from('the dependency cache');
	const sum = createHash('sha256').update(body).digest('hex');
	let requests = 0;
	const waits: number[] = [];
	const s = await serve((req, res) => {
		requests++;
		if (requests <= 4) {
			res.writeHead(200, { 'X-Content-SHA256': sum });
			res.write(body.subarray(0, 4), () => setTimeout(() => res.destroy(), 20));
			return;
		}
		if (requests <= 8) return;
		const range = req.headers.range ?? '';
		res.writeHead(range ? 206 : 200, { 'X-Content-SHA256': sum });
		res.end(body.subarray(range ? Number(/bytes=(\d+)-/.exec(range)![1]) : 0));
	});
	const file = path.join(tmp, 'outage');
	await fetchRunFile(s.url, bearer, file, async (ms: number) => { waits.push(ms); }, 100);
	assert.deepStrictEqual(fs.readFileSync(file), body, 'the download completes once the server answers again');
	assert.strictEqual(requests, 9);
	assert.deepStrictEqual(waits, Array(8).fill(RunFileRetryMs), 'every try waits the same fixed interval');
	await s.close();
}

async function cliDownloadSurvivesACutTransfer(tmp: string) {
	const body = Buffer.from('the buildhost cli');
	let requests = 0;
	const s = await serve((_req, res) => {
		requests++;
		res.writeHead(200);
		if (requests < 3) {
			res.write(body.subarray(0, 4), () => setTimeout(() => res.destroy(), 100));
			return;
		}
		res.end(body);
	});
	const file = path.join(tmp, 'cli');
	await fetchCli(s.url, file, noWait);
	assert.deepStrictEqual(fs.readFileSync(file), body, 'a cut CLI download is tried again until it completes');
	assert.strictEqual(requests, 3);
	await s.close();
}

async function cliDownloadMissIsFinal(tmp: string) {
	let requests = 0;
	const s = await serve((_req, res) => { requests++; res.writeHead(404); res.end('no such project'); });
	await assert.rejects(fetchCli(s.url, path.join(tmp, 'cli-miss'), noWait), (e: unknown) => e instanceof HTTPError && e.status === 404);
	assert.strictEqual(requests, 1, 'a 404 is not asked again');
	await s.close();
}

async function run() {
	const tmp = tempDir('handoff-', {});
	await roundTrip(tmp);
	await metadataRoundTrip(tmp);
	await restoreIntoTheFilesystemRoot(tmp);
	await bytesAfterTheEndMarker(tmp);
	await corruptArchiveFails(tmp);
	await resumes(tmp);
	await missIsFinal(tmp);
	await corruptFails(tmp);
	await outlastsAnOutage(tmp);
	await cliDownloadSurvivesACutTransfer(tmp);
	await cliDownloadMissIsFinal(tmp);
	await cacheInputsAreChecked(tmp);
	await cacheRestoreHitReadsTheMatchedScope(tmp);
	await cacheMissLeavesPathAlone(tmp, 'false', 0);
	await cacheMissLeavesPathAlone(tmp, 'true', 1);
	await cacheOutageIsAWarnedMiss(tmp, 'false', 500);
	await cacheOutageIsAWarnedMiss(tmp, 'false', 429);
	await cacheOutageIsAWarnedMiss(tmp, 'true', 503);
	await cacheOutageIsAWarnedMiss(tmp, 'false', 200, 404);
	await cacheLookupFailureIsNoMiss(tmp, 404, '404 page not found');
	await cacheLookupFailureIsNoMiss(tmp, 404, '{"error":"not found"}');
	await cacheSaveOfAHeldKeySendsNothing(tmp);
	assert.strictEqual(
		runFilePath('toolchain-Linux', { GITHUB_REPOSITORY: 'wow-look-at-my/gosmopolitan', GITHUB_RUN_ID: '42' }),
		'/api/v1/run-files/toolchain-Linux?repository=wow-look-at-my%2Fgosmopolitan&run_id=42');
	assert.throws(() => runFilePath('x', {}), /GITHUB_RUN_ID/);
	fs.rmSync(tmp, { recursive: true, force: true });
	console.log('handoff: all checks passed');
}

run().catch((e) => { console.error(e); process.exit(1); });
