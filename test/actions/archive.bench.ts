// Benchmark of `buildhost archive` against the host tar on the same trees. It
// asserts nothing; it prints a table.
//
//	BUILDHOST_CLI=build/buildhost node test/actions/archive.bench.ts [extra-tree-dir]
//
// Each case runs a few times and reports the median. The host tar pipes
// through node's zstd, as the actions did.

import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { pipeline } from 'node:stream/promises';
import { createZstdCompress, createZstdDecompress } from 'node:zlib';
import { runCli } from '../../.github/actions/lib/cli.ts';

const runs = 3;
const cli = process.env.BUILDHOST_CLI ?? '';
if (!cli) throw new Error('BUILDHOST_CLI must name the buildhost binary to measure');
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'archive-bench-'));

function makeSmall(dir: string): void {
	for (let d = 0; d < 200; d++) {
		const sub = path.join(dir, `d${d}`);
		fs.mkdirSync(sub, { recursive: true });
		for (let f = 0; f < 100; f++) fs.writeFileSync(path.join(sub, `f${f}.txt`), randomBytes(512).toString('hex'));
	}
}

function makeLarge(dir: string): void {
	fs.mkdirSync(dir, { recursive: true });
	const chunk = randomBytes(1 << 20);
	for (let f = 0; f < 4; f++) {
		const fd = fs.openSync(path.join(dir, `big${f}.bin`), 'w');
		for (let i = 0; i < 128; i++) fs.writeSync(fd, chunk);
		fs.closeSync(fd);
	}
}

function treeStats(dir: string): { files: number; bytes: number } {
	let files = 0;
	let bytes = 0;
	const walk = (p: string) => {
		for (const e of fs.readdirSync(p, { withFileTypes: true })) {
			const full = path.join(p, e.name);
			if (e.isDirectory()) walk(full);
			else if (e.isFile()) {
				files++;
				bytes += fs.statSync(full).size;
			}
		}
	};
	walk(dir);
	return { files, bytes };
}

function exited(child: ReturnType<typeof spawn>): Promise<void> {
	return new Promise((done, fail) => child.on('close', (code) => (code === 0 ? done() : fail(new Error(`tar exited ${code}`)))));
}

async function hostPack(dir: string, out: string): Promise<void> {
	const child = spawn('tar', ['-cf', '-', '-C', dir, '.'], { stdio: ['ignore', 'pipe', 'inherit'] });
	const done = exited(child);
	await pipeline(child.stdout!, createZstdCompress(), fs.createWriteStream(out));
	await done;
}

async function hostUnpack(file: string, dst: string): Promise<void> {
	fs.mkdirSync(dst, { recursive: true });
	const child = spawn('tar', ['-xf', '-', '-C', dst], { stdio: ['pipe', 'inherit', 'inherit'] });
	const done = exited(child);
	await pipeline(fs.createReadStream(file), createZstdDecompress(), child.stdin!);
	await done;
}

async function median(fn: () => Promise<void> | void, reset?: () => void): Promise<number> {
	const times: number[] = [];
	for (let i = 0; i < runs; i++) {
		reset?.();
		const t0 = performance.now();
		await fn();
		times.push((performance.now() - t0) / 1000);
	}
	return times.sort((a, b) => a - b)[Math.floor(runs / 2)];
}

type Row = { tree: string; op: string; host: number; ours: number };
const rows: Row[] = [];

async function bench(name: string, dir: string): Promise<void> {
	const stats = treeStats(dir);
	const label = `${name} (${stats.files} files, ${(stats.bytes / 2 ** 20).toFixed(0)} MiB)`;
	const hostArchive = path.join(root, `${name}.host.tar.zst`);
	const ourArchive = path.join(root, `${name}.ours.tar.zst`);
	const dst = path.join(root, `${name}-dst`);
	const clean = () => fs.rmSync(dst, { recursive: true, force: true });
	rows.push({
		tree: label, op: 'pack',
		host: await median(() => hostPack(dir, hostArchive)),
		ours: await median(() => runCli(cli, ['archive', 'pack', dir, ourArchive])),
	});
	rows.push({
		tree: label, op: 'unpack',
		host: await median(() => hostUnpack(hostArchive, dst), clean),
		ours: await median(() => runCli(cli, ['archive', 'unpack', ourArchive, dst]), clean),
	});
	const size = (p: string) => (fs.statSync(p).size / 2 ** 20).toFixed(1);
	console.error(`${label}: archive host ${size(hostArchive)} MiB, ours ${size(ourArchive)} MiB`);
	clean();
}

async function main() {
	const small = path.join(root, 'small');
	const large = path.join(root, 'large');
	makeSmall(small);
	makeLarge(large);
	await bench('small', small);
	await bench('large', large);
	if (process.argv[2]) await bench('extra', path.resolve(process.argv[2]));
	console.log('| tree | operation | host tar + node zstd (s) | buildhost archive (s) | ours / host |');
	console.log('|---|---|---:|---:|---:|');
	for (const r of rows) console.log(`| ${r.tree} | ${r.op} | ${r.host.toFixed(3)} | ${r.ours.toFixed(3)} | ${(r.ours / r.host).toFixed(2)}x |`);
	fs.rmSync(root, { recursive: true, force: true });
}

main().catch((e) => { console.error(e); process.exit(1); });
