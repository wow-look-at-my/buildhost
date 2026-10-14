// How a published file's name maps to the binary and the project it publishes to.

/** Matches `<binary>_<os>_<arch>` after a trailing `.exe` is removed. */
export const ArtifactRe = /^(.+)_([a-z]+)_([a-z0-9]+)$/;

/**
 * A binary named exactly after the repo publishes flat as `<repo>`. Every other binary
 * nests under `<repo>/<binary>`, without a redundant leading `<repo>-`.
 */
export function projectForBinary(project: string, binary: string): string {
	if (binary === project) return project;
	if (binary.startsWith(`${project}-`)) return `${project}/${binary.slice(project.length + 1)}`;
	return `${project}/${binary}`;
}

/**
 * The binary name of a portable manifest entry. It is the served filename when the
 * manifest gave one, else the file's own name without a trailing `_<os>_<arch>`.
 */
export function binaryForPortable(entry: { file: string; filename: string }, basename: (p: string) => string): string {
	const own = basename(entry.file);
	if (entry.filename !== own) return entry.filename;
	const base = own.replace(/\.exe$/, '');
	const m = base.match(ArtifactRe);
	return m ? m[1] : base;
}
