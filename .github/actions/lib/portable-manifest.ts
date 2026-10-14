// The manifest a build writes to declare ONE file that runs on several platforms.

export const ManifestName = 'buildhost-artifacts.json';

export interface PortableEntry { file: string; filename: string; kind: string; platforms: string[] }

/** The part of node's `path` module this file uses. */
export interface PathLike { basename(p: string): string; isAbsolute(p: string): boolean }

/**
 * Parses the manifest text. The caller owns the filesystem: `exists` reports whether a
 * named file is in the source directory. Returns the entries, or a message that names
 * the first defect.
 */
export function parsePortableManifest(text: string, exists: (file: string) => boolean, path: PathLike): PortableEntry[] | string {
	let doc: any;
	try {
		doc = JSON.parse(text);
	} catch (e) {
		return `${ManifestName}: not valid JSON: ${e instanceof Error ? e.message : String(e)}`;
	}
	if (doc?.schema !== 1) return `${ManifestName}: unsupported schema ${JSON.stringify(doc?.schema)}, want 1`;
	if (!Array.isArray(doc.artifacts)) return `${ManifestName}: "artifacts" must be an array`;
	const out: PortableEntry[] = [];
	for (const raw of doc.artifacts) {
		const file = String(raw?.file ?? '');
		if (!file || file.includes('..') || path.isAbsolute(file)) return `${ManifestName}: bad "file" ${JSON.stringify(raw?.file)}`;
		if (!exists(file)) return `${ManifestName}: "${file}" does not exist in the source directory`;
		const platforms = raw?.platforms;
		if (!Array.isArray(platforms) || platforms.length === 0 || platforms.some((p: any) => typeof p !== 'string' || !p.includes('/'))) {
			return `${ManifestName}: "${file}" needs a non-empty "platforms" array of "os/arch" strings`;
		}
		out.push({ file, filename: String(raw?.filename ?? path.basename(file)), kind: String(raw?.kind ?? 'binary'), platforms });
	}
	return out;
}
