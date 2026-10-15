// The buildhost-artifacts.json manifest a build writes to declare a file that runs on several platforms.

import * as crypto from 'node:crypto';
import * as nodeFs from 'node:fs';
import * as nodePath from 'node:path';

export interface Core { setFailed(m: string): void }

/** A filename cannot say that one file runs on several platforms, so the build declares the set here. */
export interface PortableEntry { file: string; filename: string; kind: string; platforms: string[] }

/** The part of node's `path` module the parser uses. */
export interface PathLike { basename(p: string): string; isAbsolute(p: string): boolean }

export const manifestName = 'buildhost-artifacts.json';

/**
 * Parses the manifest text. `exists` reports whether a named file is in the
 * source directory. Returns the entries, or a message that names the first defect.
 */
export function parsePortableManifest(text: string, exists: (file: string) => boolean, path: PathLike): PortableEntry[] | string {
	let doc: any;
	try {
		doc = JSON.parse(text);
	} catch (e) {
		return `${manifestName}: not valid JSON: ${e instanceof Error ? e.message : String(e)}`;
	}
	if (doc?.schema !== 1) return `${manifestName}: unsupported schema ${JSON.stringify(doc?.schema)}, want 1`;
	if (!Array.isArray(doc.artifacts)) return `${manifestName}: "artifacts" must be an array`;
	const out: PortableEntry[] = [];
	for (const raw of doc.artifacts) {
		const file = String(raw?.file ?? '');
		if (!file || file.includes('..') || path.isAbsolute(file)) return `${manifestName}: bad "file" ${JSON.stringify(raw?.file)}`;
		if (!exists(file)) return `${manifestName}: "${file}" does not exist in the source directory`;
		const platforms = raw?.platforms;
		if (!Array.isArray(platforms) || platforms.length === 0 || platforms.some((p: any) => typeof p !== 'string' || !p.includes('/'))) {
			return `${manifestName}: "${file}" needs a non-empty "platforms" array of "os/arch" strings`;
		}
		out.push({ file, filename: String(raw?.filename ?? path.basename(file)), kind: String(raw?.kind ?? 'binary'), platforms });
	}
	return out;
}

/**
 * Reads the manifest in sourceDir. No manifest is an empty list. A manifest it
 * cannot use fails the step with the source directory named, and answers null.
 */
export function readPortableManifest(
	sourceDir: string,
	core: Core,
	fs: typeof import('node:fs') = nodeFs,
	path: typeof import('node:path') = nodePath,
): PortableEntry[] | null {
	const manifestPath = path.join(sourceDir, manifestName);
	if (!fs.existsSync(manifestPath)) return [];
	const parsed = parsePortableManifest(fs.readFileSync(manifestPath, 'utf-8'), (f) => fs.existsSync(path.join(sourceDir, f)), path);
	if (typeof parsed === 'string') {
		core.setFailed(`${parsed} (source directory ${sourceDir})`);
		return null;
	}
	return parsed;
}

/** The hex sha256 of a file's bytes, from one streaming read. */
export function fileSHA256(file: string): Promise<string> {
	return new Promise((resolve, reject) => {
		const hash = crypto.createHash('sha256');
		nodeFs.createReadStream(file)
			.on('error', reject)
			.on('data', (chunk) => hash.update(chunk))
			.on('end', () => resolve(hash.digest('hex')));
	});
}
