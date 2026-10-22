// Behavior tests for .github/actions/lib/handoff.ts: the archive round trip, and a download that breaks and resumes.

import assert from 'node:assert';
import { createHash } from 'node:crypto';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { pack, unpack, fetchRunFile, runFilePath, tarCommand } from '../../.github/actions/lib/handoff.ts';
import { HTTPError } from '../../.github/actions/lib/download.ts';
import { serve, tempDir } from './fakes.ts';

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
	await resumes(tmp);
	await missIsFinal(tmp);
	await corruptFails(tmp);
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
