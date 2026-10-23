// Benchmark of .github/actions/lib/tar.ts against the host tar on the same trees. It asserts nothing; it prints a table.
//
//	node test/actions/tar.bench.ts [extra-tree-dir]
//
// Each case runs a few times and reports the median. zstd is node's on both sides, so a difference is the tar alone.

import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { Readable, Writable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { createZstdCompress, createZstdDecompress } from 'node:zlib';
import { tarOf, untar } from '../../.github/actions/lib/tar.ts';

const runs = 3;
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'tar-bench-'));

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

const sink = () => new Writable({ write(_c, _e, done) { done(); } });

function exited(child: ReturnType<typeof spawn>): Promise<void> {
	return new Promise((done, fail) => child.on('close', (code) => (code === 0 ? done() : fail(new Error(`tar exited ${code}`)))));
}

async function hostPackTo(dir: string, out: Writable, zstd: boolean): Promise<void> {
	const child = spawn('tar', ['-cf', '-', '-C', dir, '.'], { stdio: ['ignore', 'pipe', 'inherit'] });
	const done = exited(child);
	await (zstd ? pipeline(child.stdout!, createZstdCompress(), out) : pipeline(child.stdout!, out));
	await done;
}

async function hostUnpack(file: string, dst: string, zstd: boolean): Promise<void> {
	fs.mkdirSync(dst, { recursive: true });
	const child = spawn('tar', ['-xf', '-', '-C', dst], { stdio: ['pipe', 'inherit', 'inherit'] });
	const done = exited(child);
	await (zstd ? pipeline(fs.createReadStream(file), createZstdDecompress(), child.stdin!) : pipeline(fs.createReadStream(file), child.stdin!));
	await done;
}

async function ourUnpack(file: string, dst: string, zstd: boolean): Promise<void> {
	const src = fs.createReadStream(file);
	await untar(zstd ? src.pipe(createZstdDecompress()) : src, dst);
}

async function median(fn: () => Promise<void>, reset?: () => void): Promise<number> {
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
	console.error(`${name}: ${stats.files} files, ${(stats.bytes / 2 ** 20).toFixed(1)} MiB`);
	const plainHost = path.join(root, `${name}.host.tar`);
	const plainOurs = path.join(root, `${name}.ours.tar`);
	const zstHost = path.join(root, `${name}.host.tar.zst`);
	const zstOurs = path.join(root, `${name}.ours.tar.zst`);
	const dst = path.join(root, `${name}-dst`);
	const clean = () => fs.rmSync(dst, { recursive: true, force: true });
	await hostPackTo(dir, fs.createWriteStream(plainHost), false);
	await pipeline(Readable.from(tarOf(dir)), fs.createWriteStream(plainOurs));
	await hostPackTo(dir, fs.createWriteStream(zstHost), true);
	await pipeline(Readable.from(tarOf(dir)), createZstdCompress(), fs.createWriteStream(zstOurs));
	const add = (op: string, host: number, ours: number) => rows.push({ tree: `${name} (${stats.files} files, ${(stats.bytes / 2 ** 20).toFixed(0)} MiB)`, op, host, ours });
	add('archive', await median(() => hostPackTo(dir, sink(), false)), await median(() => pipeline(Readable.from(tarOf(dir)), sink())));
	add('extract', await median(() => hostUnpack(plainHost, dst, false), clean), await median(() => ourUnpack(plainOurs, dst, false), clean));
	add('pack (tar+zstd)', await median(() => hostPackTo(dir, sink(), true)), await median(() => pipeline(Readable.from(tarOf(dir)), createZstdCompress(), sink())));
	add('unpack (zstd+tar)', await median(() => hostUnpack(zstHost, dst, true), clean), await median(() => ourUnpack(zstOurs, dst, true), clean));
	const size = (p: string) => (fs.statSync(p).size / 2 ** 20).toFixed(1);
	console.error(`  archive size: host ${size(plainHost)} MiB, ours ${size(plainOurs)} MiB; zstd: host ${size(zstHost)} MiB, ours ${size(zstOurs)} MiB`);
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
	console.log('| tree | operation | host tar (s) | tar.ts (s) | tar.ts / host |');
	console.log('|---|---|---:|---:|---:|');
	for (const r of rows) console.log(`| ${r.tree} | ${r.op} | ${r.host.toFixed(3)} | ${r.ours.toFixed(3)} | ${(r.ours / r.host).toFixed(2)}x |`);
	fs.rmSync(root, { recursive: true, force: true });
}

main().catch((e) => { console.error(e); process.exit(1); });
