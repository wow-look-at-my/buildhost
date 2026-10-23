// The tar the hand-off and the cache pack with.

import * as fs from 'node:fs';
import * as path from 'node:path';

const block = 512;

/** One entry of a tree, as pack reads it. */
type Entry = { name: string; abs: string; st: fs.BigIntStats };

function walk(root: string, member: string, out: Entry[]): void {
	const abs = member === '.' ? root : path.join(root, member);
	const st = fs.lstatSync(abs, { bigint: true });
	out.push({ name: member, abs, st });
	if (!st.isDirectory()) return;
	for (const child of fs.readdirSync(abs).sort()) walk(root, member === '.' ? child : `${member}/${child}`, out);
}

function octal(n: bigint | number, width: number): string {
	return n.toString(8).padStart(width - 1, '0') + '\0';
}

/** A pax record is "<len> <key>=<value>\n", where len counts its own digits too. */
function paxRecord(key: string, value: string): Buffer {
	const body = ` ${key}=${value}\n`;
	const base = Buffer.byteLength(body);
	let len = base + String(base).length;
	if (String(len).length !== String(base).length) len = base + String(len).length;
	return Buffer.from(`${len}${body}`);
}

function header(name: string, type: string, size: bigint, mode: number, mtimeSec: bigint, linkname = ''): Buffer {
	const h = Buffer.alloc(block);
	const ascii = (s: string, max: number) => s.replace(/[^\x20-\x7e]/g, '_').slice(0, max);
	const max = 0o77777777777n;
	h.write(ascii(name, 100), 0, 'latin1');
	h.write(octal(mode & 0o7777, 8), 100, 'latin1');
	h.write(octal(0, 8), 108, 'latin1');
	h.write(octal(0, 8), 116, 'latin1');
	h.write(octal(size <= max ? size : 0n, 12), 124, 'latin1');
	h.write(octal(mtimeSec >= 0n && mtimeSec <= max ? mtimeSec : 0n, 12), 136, 'latin1');
	h.write(type, 156, 'latin1');
	h.write(ascii(linkname, 100), 157, 'latin1');
	h.write('ustar\0', 257, 'latin1');
	h.write('00', 263, 'latin1');
	h.fill(0x20, 148, 156);
	let sum = 0;
	for (const b of h) sum += b;
	h.write(octal(sum, 7) + ' ', 148, 'latin1');
	return h;
}

function pad(n: bigint): Buffer {
	const rest = Number(n % BigInt(block));
	return Buffer.alloc(rest === 0 ? 0 : block - rest);
}

/**
 * Yields a pax tar of members of root, by default all of it. Each entry carries its full path, link target, size and
 * nanosecond mtime in a pax header, so no field is cut to fit ustar. A second name of one inode is a hardlink entry.
 */
export async function* tarOf(root: string, members = ['.']): AsyncGenerator<Buffer> {
	const entries: Entry[] = [];
	for (const m of members) walk(root, m, entries);
	const inodes = new Map<string, string>();
	for (const e of entries) {
		const name = e.name === '.' ? './' : e.st.isDirectory() ? `${e.name}/` : e.name;
		const sec = e.st.mtimeNs / 1_000_000_000n;
		const frac = (e.st.mtimeNs % 1_000_000_000n).toString().padStart(9, '0');
		let type: string;
		let size = 0n;
		let link = '';
		if (e.st.isSymbolicLink()) {
			type = '2';
			link = fs.readlinkSync(e.abs).split(path.sep).join('/');
		} else if (e.st.isDirectory()) {
			type = '5';
		} else if (e.st.isFile()) {
			const key = `${e.st.dev}:${e.st.ino}`;
			if (e.st.nlink > 1n && e.st.ino !== 0n && inodes.has(key)) {
				type = '1';
				link = inodes.get(key)!;
			} else {
				if (e.st.nlink > 1n) inodes.set(key, e.name);
				type = '0';
				size = e.st.size;
			}
		} else {
			console.warn(`tar: ${e.abs} is not a file, directory or symlink, so it is not packed`);
			continue;
		}
		const pax = [paxRecord('path', name), paxRecord('mtime', `${sec}.${frac}`), paxRecord('size', size.toString())];
		if (link) pax.push(paxRecord('linkpath', link));
		const paxBody = Buffer.concat(pax);
		yield header('PaxHeader', 'x', BigInt(paxBody.length), 0o644, sec);
		yield paxBody;
		yield pad(BigInt(paxBody.length));
		yield header(name, type, size, Number(e.st.mode), sec, link);
		if (type !== '0') continue;
		let sent = 0n;
		for await (const chunk of fs.createReadStream(e.abs, { highWaterMark: 1 << 20 })) {
			const b = chunk as Buffer;
			sent += BigInt(b.length);
			if (sent > size) throw new Error(`tar: ${e.abs} grew while it was packed`);
			yield b;
		}
		if (sent !== size) throw new Error(`tar: ${e.abs} shrank while it was packed (${sent} of ${size} bytes)`);
		yield pad(size);
	}
	yield Buffer.alloc(block * 2);
}

function field(h: Buffer, off: number, len: number): string {
	const s = h.subarray(off, off + len);
	const end = s.indexOf(0);
	return (end < 0 ? s : s.subarray(0, end)).toString('utf8');
}

/** Reads a numeric header field: octal text, or GNU base-256 when the high bit of the first byte is set. */
function number(h: Buffer, off: number, len: number): bigint {
	if (h[off] & 0x80) {
		let n = BigInt(h[off] & 0x7f);
		for (let i = off + 1; i < off + len; i++) n = (n << 8n) | BigInt(h[i]);
		return n;
	}
	const s = field(h, off, len).trim();
	return s ? BigInt(`0o${s}`) : 0n;
}

function parsePax(body: Buffer): Map<string, string> {
	const out = new Map<string, string>();
	let i = 0;
	while (i < body.length) {
		const sp = body.indexOf(0x20, i);
		if (sp < 0) break;
		const len = Number(body.subarray(i, sp).toString('latin1'));
		if (!Number.isInteger(len) || len <= 0) throw new Error('tar: a malformed pax record');
		const rec = body.subarray(sp + 1, i + len - 1).toString('utf8');
		const eq = rec.indexOf('=');
		out.set(rec.slice(0, eq), rec.slice(eq + 1));
		i += len;
	}
	return out;
}

/** Resolves an archive name under dir, and refuses one that leaves it. */
function inside(dir: string, name: string): string {
	const clean = name.replace(/^(\.\/)+/, '').replace(/\/+$/, '');
	if (clean === '' || clean === '.') return dir;
	if (clean.startsWith('/') || /^[A-Za-z]:/.test(clean) || clean.split('/').includes('..')) {
		throw new Error(`tar: entry ${name} leaves the directory it extracts into`);
	}
	return path.join(dir, ...clean.split('/'));
}

/** Removes what stands at p, a read-only file included, so an entry can take its place. */
function clear(p: string): void {
	const st = fs.lstatSync(p, { throwIfNoEntry: false });
	if (!st) return;
	if (st.isFile()) fs.chmodSync(p, 0o666);
	fs.rmSync(p, { recursive: true, force: true });
}

/**
 * Extracts a tar stream into dir: this module's pax archives, and the ustar and GNU archives an older action version wrote.
 * Extraction stops at the end-of-archive marker. Symlinks are made last, so no entry writes through one, and directories get
 * their modes and mtimes after their contents.
 */
export async function untar(source: AsyncIterable<Buffer | string>, dir: string): Promise<void> {
	fs.mkdirSync(dir, { recursive: true });
	const it = source[Symbol.asyncIterator]();
	let buf = Buffer.alloc(0);
	const need = async (n: number): Promise<boolean> => {
		while (buf.length < n) {
			const r = await it.next();
			if (r.done) return false;
			buf = Buffer.concat([buf, Buffer.from(r.value)]);
		}
		return true;
	};
	const take = (n: number): Buffer => {
		const b = buf.subarray(0, n);
		buf = buf.subarray(n);
		return b;
	};
	const dirs: { p: string; mode: number; mtime: number }[] = [];
	const links: { p: string; target: string }[] = [];
	let pax = new Map<string, string>();
	let longName = '';
	let longLink = '';
	for (;;) {
		if (!(await need(block))) throw new Error('tar: the archive ends before its end-of-archive marker');
		const h = take(block);
		if (h.every((b) => b === 0)) break;
		const type = String.fromCharCode(h[156] || 0x30);
		const size = pax.has('size') ? BigInt(pax.get('size')!) : number(h, 124, 12);
		const padded = Number(size) + ((block - (Number(size) % block)) % block);
		const body = async (): Promise<Buffer> => {
			if (!(await need(padded))) throw new Error('tar: the archive ends inside an entry');
			return take(padded).subarray(0, Number(size));
		};
		if (type === 'x') {
			pax = parsePax(await body());
			continue;
		}
		if (type === 'g') {
			await body();
			continue;
		}
		if (type === 'L' || type === 'K') {
			const v = (await body()).toString('utf8').replace(/\0+$/, '');
			if (type === 'L') longName = v;
			else longLink = v;
			continue;
		}
		const prefix = field(h, 345, 155);
		const name = pax.get('path') ?? (longName || (prefix ? `${prefix}/${field(h, 0, 100)}` : field(h, 0, 100)));
		const link = pax.get('linkpath') ?? (longLink || field(h, 157, 100));
		const mode = Number(number(h, 100, 8)) & 0o7777;
		const mtime = pax.has('mtime') ? Number(pax.get('mtime')) : Number(number(h, 136, 12));
		pax = new Map();
		longName = '';
		longLink = '';
		const p = inside(dir, name);
		if (type === '5') {
			if (!fs.lstatSync(p, { throwIfNoEntry: false })?.isDirectory()) {
				clear(p);
				fs.mkdirSync(p, { recursive: true });
			}
			dirs.push({ p, mode, mtime });
		} else if (type === '0' || type === '7') {
			fs.mkdirSync(path.dirname(p), { recursive: true });
			clear(p);
			const fd = fs.openSync(p, 'w');
			try {
				let left = size;
				while (left > 0n) {
					if (!(await need(1))) throw new Error(`tar: the archive ends inside ${name}`);
					const n = left < BigInt(buf.length) ? Number(left) : buf.length;
					fs.writeSync(fd, take(n));
					left -= BigInt(n);
				}
			} finally {
				fs.closeSync(fd);
			}
			if (!(await need(padded - Number(size)))) throw new Error(`tar: the archive ends inside ${name}`);
			take(padded - Number(size));
			fs.utimesSync(p, mtime, mtime);
			fs.chmodSync(p, mode);
		} else if (type === '1') {
			fs.mkdirSync(path.dirname(p), { recursive: true });
			clear(p);
			fs.linkSync(inside(dir, link), p);
		} else if (type === '2') {
			links.push({ p, target: link });
		} else {
			throw new Error(`tar: entry ${name} has type '${type}', which this tar does not extract`);
		}
	}
	// Bytes after the marker are padding, so nothing reads them.
	await it.return?.();
	for (const l of links) {
		fs.mkdirSync(path.dirname(l.p), { recursive: true });
		clear(l.p);
		const target = l.target.split('/').join(path.sep);
		const isDir = fs.statSync(path.resolve(path.dirname(l.p), target), { throwIfNoEntry: false })?.isDirectory() ?? false;
		fs.symlinkSync(target, l.p, isDir ? 'dir' : 'file');
	}
	dirs.sort((a, b) => b.p.length - a.p.length);
	for (const d of dirs) {
		fs.chmodSync(d.p, d.mode);
		fs.utimesSync(d.p, d.mtime, d.mtime);
	}
}
