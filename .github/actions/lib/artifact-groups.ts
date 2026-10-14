// Which files of a publish directory upload, and to which project each goes.

import * as crypto from "node:crypto";
import * as fs from "node:fs";
import * as path from "node:path";

export const manifestName = "buildhost-artifacts.json";

// matches <binary>_<os>_<arch>[.exe]; binary is the cmd/ leaf name from go-toolchain and may contain hyphens (e.g. log-streamer-client)
export const artifactRe = /^(.+)_([a-z]+)_([a-z0-9]+)$/;

/** A file that runs on several platforms, declared in the manifest because a filename cannot say so. */
export type PortableEntry = { file: string; filename: string; kind: string; platforms: string[] };

export type Item = { file: string; os: string; arch: string };

export type Group = { slots: Item[]; portable: PortableEntry[] };

/** One slot the publish would upload, with the sha256 of its bytes. */
export interface Slot { os: string; arch: string; kind: string; sha256: string }

export const fileSHA256 = (p: string): Promise<string> => new Promise((resolve, reject) => {
	const hash = crypto.createHash("sha256");
	fs.createReadStream(p)
		.on("error", reject)
		.on("data", (chunk: Buffer) => hash.update(chunk))
		.on("end", () => resolve(hash.digest("hex")));
});

/** The portable entries of the manifest in sourceDir, or none when there is no manifest. A malformed manifest throws. */
export function readPortableManifest(sourceDir: string): PortableEntry[] {
	const manifestPath = path.join(sourceDir, manifestName);
	if (!fs.existsSync(manifestPath)) return [];
	let doc: any;
	try {
		doc = JSON.parse(fs.readFileSync(manifestPath, "utf-8"));
	} catch (e) {
		throw new Error(`${manifestName}: not valid JSON: ${e instanceof Error ? e.message : String(e)}`);
	}
	if (doc?.schema !== 1) throw new Error(`${manifestName}: unsupported schema ${JSON.stringify(doc?.schema)}, want 1`);
	if (!Array.isArray(doc.artifacts)) throw new Error(`${manifestName}: "artifacts" must be an array`);
	const out: PortableEntry[] = [];
	for (const raw of doc.artifacts) {
		const file = String(raw?.file ?? "");
		if (!file || file.includes("..") || path.isAbsolute(file)) throw new Error(`${manifestName}: bad "file" ${JSON.stringify(raw?.file)}`);
		if (!fs.existsSync(path.join(sourceDir, file))) throw new Error(`${manifestName}: "${file}" does not exist in ${sourceDir}`);
		const platforms = raw?.platforms;
		if (!Array.isArray(platforms) || platforms.length === 0 || platforms.some((p: any) => typeof p !== "string" || !p.includes("/"))) {
			throw new Error(`${manifestName}: "${file}" needs a non-empty "platforms" array of "os/arch" strings`);
		}
		out.push({ file, filename: String(raw?.filename ?? path.basename(file)), kind: String(raw?.kind ?? "binary"), platforms });
// Which files a publish uploads, and the project each one goes to.

export const ManifestName = 'buildhost-artifacts.json';

/** One file that runs on several platforms, declared in the manifest because a filename cannot say so. */
export interface PortableEntry { file: string; filename: string; kind: string; platforms: string[] }

/** One per-platform file. */
export interface Item { file: string; os: string; arch: string }

/** Everything one project receives. */
export interface Group { slots: Item[]; portable: PortableEntry[] }

/** What the caller reads from the directory. The caller owns the filesystem, so this module needs no node builtins. */
export interface Dir {
	/** The regular files in the directory, without symlinks or subdirectories. */
	files: string[];
	/** The manifest's text, or null when the directory has none. */
	manifest: string | null;
	/** Reports whether a path relative to the directory exists. */
	exists(file: string): boolean;
}

export type Found =
	| { ok: true; groups: Map<string, Group>; files: string[]; portable: PortableEntry[] }
	| { ok: false; message: string };

/** Matches <binary>_<os>_<arch>. The binary is the cmd/ leaf name from go-toolchain and may hold hyphens. */
export const artifactRe = /^(.+)_([a-z]+)_([a-z0-9]+)$/;

const baseName = (file: string): string => file.split(/[\\/]/).pop() ?? file;
const isAbsolute = (file: string): boolean => /^(?:[A-Za-z]:)?[\\/]/.test(file);

/** A binary named after the repo publishes flat. Every other binary nests under the repo, without a redundant leading "<repo>-". */
export function projectForBinary(project: string, binary: string): string {
	if (binary === project) return project;
	if (binary.startsWith(`${project}-`)) return `${project}/${binary.slice(project.length + 1)}`;
	return `${project}/${binary}`;
}

/** The served filename when the manifest gave one, else the file's own name without a trailing _<os>_<arch>. */
export function binaryForPortable(entry: PortableEntry): string {
	const base = baseName(entry.file).replace(/\.exe$/, '');
	if (entry.filename !== baseName(entry.file)) return entry.filename;
	const m = base.match(artifactRe);
	return m ? m[1] : base;
}

/** The manifest's entries, or the reason it is unusable. */
export function readManifest(dir: Dir): PortableEntry[] | string {
	if (dir.manifest === null) return [];
	let doc: any;
	try {
		doc = JSON.parse(dir.manifest);
	} catch (e) {
		return `${ManifestName}: not valid JSON: ${e instanceof Error ? e.message : String(e)}`;
	}
	if (doc?.schema !== 1) return `${ManifestName}: unsupported schema ${JSON.stringify(doc?.schema)}, want 1`;
	if (!Array.isArray(doc.artifacts)) return `${ManifestName}: "artifacts" must be an array`;
	const out: PortableEntry[] = [];
	for (const raw of doc.artifacts) {
		const file = String(raw?.file ?? '');
		if (!file || file.includes('..') || isAbsolute(file)) return `${ManifestName}: bad "file" ${JSON.stringify(raw?.file)}`;
		if (!dir.exists(file)) return `${ManifestName}: "${file}" does not exist in the publish directory`;
		const platforms = raw?.platforms;
		if (!Array.isArray(platforms) || platforms.length === 0 || platforms.some((p: any) => typeof p !== 'string' || !p.includes('/'))) {
			return `${ManifestName}: "${file}" needs a non-empty "platforms" array of "os/arch" strings`;
		}
		out.push({ file, filename: String(raw?.filename ?? baseName(file)), kind: String(raw?.kind ?? 'binary'), platforms });
	}
	return out;
}

/** A binary named exactly after the repo publishes flat as "<repo>"; every other binary nests under "<repo>/<binary>", stripping a redundant leading "<repo>-". */
export function projectForBinary(project: string, binary: string): string {
	if (binary === project) return project;
	if (binary.startsWith(`${project}-`)) return `${project}/${binary.slice(project.length + 1)}`;
	return `${project}/${binary}`;
}

// the binary name is the served filename when the manifest gave one, else the file's own name with a trailing _<os>_<arch> stripped
function binaryForPortable(entry: PortableEntry): string {
	const base = path.basename(entry.file).replace(/\.exe$/, "");
	if (entry.filename !== path.basename(entry.file)) return entry.filename;
	const m = base.match(artifactRe);
	return m ? m[1] : base;
}

/** The artifacts of sourceDir grouped by destination project, and how many of each kind it found. */
export function groupArtifacts(sourceDir: string, project: string): { groups: Map<string, Group>; files: number; portable: number } {
	const portable = readPortableManifest(sourceDir);
	const portableFiles = new Set(portable.map((p) => p.file));
	const files = fs.readdirSync(sourceDir).filter((f) => {
		const full = path.join(sourceDir, f);
		if (fs.lstatSync(full).isSymbolicLink()) return false;
		if (!fs.statSync(full).isFile()) return false;
		if (f === "checksums.txt" || f === manifestName || f.endsWith(".zip")) return false;
		if (portableFiles.has(f)) return false;
		return artifactRe.test(f.replace(/\.exe$/, ""));
	});
/** Groups the directory's artifacts by the project each one publishes to. */
export function findArtifacts(dir: Dir, project: string): Found {
	const portable = readManifest(dir);
	if (typeof portable === 'string') return { ok: false, message: portable };
	const portableFiles = new Set(portable.map((p) => p.file));
	const files = dir.files.filter((f) => {
		if (f === 'checksums.txt' || f === ManifestName || f.endsWith('.zip')) return false;
		if (portableFiles.has(f)) return false;
		return artifactRe.test(f.replace(/\.exe$/, ''));
	});
	if (files.length === 0 && portable.length === 0) {
		return { ok: false, message: `No matrix artifacts matching '<binary>_{os}_{arch}' found, and no ${ManifestName}. Files: ${dir.files.join(', ')}` };
	}
	const groups = new Map<string, Group>();
	const groupFor = (proj: string): Group => {
		const g = groups.get(proj) ?? { slots: [], portable: [] };
		groups.set(proj, g);
		return g;
	};
	for (const file of files) {
		const [, binary, fileOs, arch] = file.replace(/\.exe$/, "").match(artifactRe)!;
		groupFor(projectForBinary(project, binary)).slots.push({ file, os: fileOs, arch });
	}
	for (const entry of portable) groupFor(projectForBinary(project, binaryForPortable(entry))).portable.push(entry);
	return { groups, files: files.length, portable: portable.length };
}

/** Every slot the group would upload, with the sha256 of its bytes. */
export async function slotsOf(sourceDir: string, group: Group): Promise<Slot[]> {
	const slots: Slot[] = [];
	for (const item of group.slots) {
		slots.push({ os: item.os, arch: item.arch, kind: "binary", sha256: await fileSHA256(path.join(sourceDir, item.file)) });
	}
	for (const entry of group.portable) {
		const sum = await fileSHA256(path.join(sourceDir, entry.file));
		for (const platform of entry.platforms) {
			const [platformOS, platformArch] = platform.split("/");
			slots.push({ os: platformOS, arch: platformArch, kind: entry.kind, sha256: sum });
		}
	}
	return slots;
		const [, binary, fileOs, arch] = file.replace(/\.exe$/, '').match(artifactRe)!;
		groupFor(projectForBinary(project, binary)).slots.push({ file, os: fileOs, arch });
	}
	for (const entry of portable) {
		groupFor(projectForBinary(project, binaryForPortable(entry))).portable.push(entry);
	}
	return { ok: true, groups, files, portable };
}
