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
		const [, binary, fileOs, arch] = file.replace(/\.exe$/, '').match(artifactRe)!;
		groupFor(projectForBinary(project, binary)).slots.push({ file, os: fileOs, arch });
	}
	for (const entry of portable) {
		groupFor(projectForBinary(project, binaryForPortable(entry))).portable.push(entry);
	}
	return { ok: true, groups, files, portable };
}
