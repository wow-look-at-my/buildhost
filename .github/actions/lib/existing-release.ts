// What a publish does with a release the create request found already there.

export interface Core { info(m: string): void }

export interface Response { status: number; text: string }

export type Send = (method: string, urlPath: string) => Promise<Response>;

/** One slot the publish would upload. */
export interface Slot { os: string; arch: string; kind: string; sha256: string }

export type Verdict =
	| { action: 'publish' }
	| { action: 'noop' }
	| { action: 'fail'; message: string };

interface StoredArtifact {
	os: string;
	arch: string;
	kind: string;
	sha256: string;
	platforms?: { os: string; arch: string }[] | null;
}

const slotName = (s: { os: string; arch: string; kind: string }) => `${s.os}/${s.arch} (${s.kind})`;

/**
 * Decides the fate of an existing release. An unpublished one takes the upload.
 * A published one takes nothing. The publish is a no-op when every slot already
 * holds the same bytes, and a failure that names each slot otherwise.
 */
export async function existingRelease(send: Send, core: Core, proj: string, version: string, slots: Slot[]): Promise<Verdict> {
	const resp = await send('GET', `/api/v1/projects/${proj}/releases/${version}`);
	if (resp.status !== 200) {
		return { action: 'fail', message: `Release ${proj}/${version} exists, and reading it failed (${resp.status}): ${resp.text}` };
	}
	let rel: { published?: boolean; artifacts?: StoredArtifact[] | null };
	try {
		rel = JSON.parse(resp.text);
	} catch (e) {
		return { action: 'fail', message: `Release ${proj}/${version} exists, and its record is not JSON: ${e instanceof Error ? e.message : String(e)}` };
	}
	if (rel.published !== true) return { action: 'publish' };

	const stored = new Map<string, string>();
	for (const a of rel.artifacts ?? []) {
		stored.set(slotName(a), a.sha256);
		for (const p of a.platforms ?? []) stored.set(slotName({ os: p.os, arch: p.arch, kind: a.kind }), a.sha256);
	}
	const mismatches: string[] = [];
	for (const s of slots) {
		const have = stored.get(slotName(s));
		if (have === undefined) mismatches.push(`${slotName(s)}: the published release has no such artifact`);
		else if (have !== s.sha256) mismatches.push(`${slotName(s)}: published sha256 ${have}, this run built ${s.sha256}`);
	}
	if (mismatches.length > 0) {
		return { action: 'fail', message: `Release ${proj}/${version} is already published with other bytes. A published release never changes.\n${mismatches.join('\n')}` };
	}
	core.info(`Release ${proj}/${version} is already published, and each of its ${slots.length} slot(s) holds the bytes this run built. Nothing to upload.`);
	return { action: 'noop' };
}
