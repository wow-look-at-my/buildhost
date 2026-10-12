// The release stamp: data the publish appends to a binary before it uploads
// it. see docs/release-stamp.md

import * as fs from "node:fs";
import * as path from "node:path";

export const Magic = Buffer.from("BUILDHOST-STAMP1", "ascii");

export interface Stamp { project: string; release: string; revision: string }

// The trailer is the JSON payload, its length as a big-endian uint32, then Magic. A reader finds it from the end of the file.
export function encode(s: Stamp): Buffer {
	const payload = Buffer.from(JSON.stringify({ project: s.project, release: s.release, revision: s.revision }), "utf8");
	const len = Buffer.alloc(4);
	len.writeUInt32BE(payload.length);
	return Buffer.concat([payload, len, Magic]);
}

// The stamp at the end of the bytes and where it starts, or null when there is none.
export function decode(buf: Buffer): { stamp: Stamp; start: number } | null {
	const tail = Magic.length + 4;
	if (buf.length < tail || !buf.subarray(buf.length - Magic.length).equals(Magic)) return null;
	const len = buf.readUInt32BE(buf.length - tail);
	const start = buf.length - tail - len;
	if (start < 0) throw new Error(`release stamp: the trailer claims ${len} payload bytes, more than the file holds`);
	const raw = JSON.parse(buf.subarray(start, start + len).toString("utf8"));
	for (const key of ["project", "release", "revision"] as const) {
		if (typeof raw[key] !== "string" || raw[key] === "") throw new Error(`release stamp: the payload has no ${key}`);
	}
	return { stamp: { project: raw.project, release: raw.release, revision: raw.revision }, start };
}

// Writes a stamped copy of src to out. An old stamp on src is replaced, not stacked. The source file is never changed.
export function stampCopy(src: string, out: string, s: Stamp): string {
	for (const key of ["project", "release", "revision"] as const) {
		if (s[key] === "") throw new Error(`release stamp for ${src}: ${key} is empty`);
	}
	const body = fs.readFileSync(src);
	const old = decode(body);
	fs.mkdirSync(path.dirname(out), { recursive: true });
	fs.writeFileSync(out, Buffer.concat([old ? body.subarray(0, old.start) : body, encode(s)]), { mode: fs.statSync(src).mode & 0o777 });
	return out;
}
