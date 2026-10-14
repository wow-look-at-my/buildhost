// Uploads one project's artifacts into a release, then publishes the release.

import type { Group, Item } from './artifact-groups';
import type { Response, Send } from './client';

export interface Core { info(m: string): void; warning(m: string): void; setFailed(m: string): void }

/** One slot the upload filled, for the storage record. */
export interface Uploaded { project: string; version: string; os: string; arch: string; sha256: string }

/** What the publish step supplies. The caller owns the filesystem, so this module needs no node builtins. */
export interface Deps {
	send: Send;
	/** Sends the bytes of a file in the publish directory, the way upload.ts sizes a body. */
	put(urlPath: string, file: string, sha256: string, headers?: Record<string, string>): Promise<Response>;
	/** The sha256 of a file in the publish directory. */
	sha256(file: string): Promise<string>;
	record(slot: Uploaded): void;
	core: Core;
	server: string;
}

/**
 * Identical bytes under several slot names is the normal case: a cosmo build
 * emits one APE named for every platform. So slots group by content, and each
 * group transfers once. Answers false after it reports a failure.
 */
export async function publishGroup(d: Deps, proj: string, releaseVersion: string, group: Group): Promise<boolean> {
	const { core } = d;
	const slotURL = (os: string, arch: string) =>
		`/api/v1/projects/${proj}/releases/${releaseVersion}/artifacts/${os}/${arch}?kind=binary`;

	// One file that runs on several platforms is ONE artifact that carries the whole set, so consumers see one download link.
	for (const entry of group.portable) {
		const sum = await d.sha256(entry.file);
		const url = `/api/v1/projects/${proj}/releases/${releaseVersion}/artifacts/ape?platforms=${entry.platforms.join(',')}&kind=${entry.kind}`;
		const r = await d.put(url, entry.file, sum, { 'X-Artifact-Filename': entry.filename });
		if (r.status !== 201) { core.setFailed(`Upload ${proj} ${entry.file} [${entry.platforms.join(', ')}] failed (${r.status}): ${r.text}`); return false; }
		for (const platform of entry.platforms) {
			const [platformOS, platformArch] = platform.split('/');
			d.record({ project: proj, version: releaseVersion, os: platformOS, arch: platformArch, sha256: sum });
		}
		core.info(`Uploaded ${proj} ${entry.file} as one artifact covering ${entry.platforms.join(', ')}`);
	}

	// The storage record needs the hash of every slot, so each file is hashed whether it shares its bytes.
	const byContent = new Map<string, Item[]>();
	for (const item of group.slots) {
		const sum = await d.sha256(item.file);
		byContent.set(sum, [...(byContent.get(sum) ?? []), item]);
	}

	for (const [sum, same] of byContent) {
		const record = () => {
			for (const { os, arch } of same) d.record({ project: proj, version: releaseVersion, os, arch, sha256: sum });
		};
		const oses = [...new Set(same.map((i) => i.os))];
		const arches = [...new Set(same.map((i) => i.arch))];

		// When the slots are exactly the product of their platforms, buildhost makes every row from one streamed body, in one transaction.
		if (oses.length * arches.length === same.length) {
			const r = await d.put(slotURL(oses.join(','), arches.join(',')), same[0].file, sum);
			if (r.status !== 201) { core.setFailed(`Upload ${proj} ${oses.join(',')}/${arches.join(',')} failed (${r.status}): ${r.text}`); return false; }
			record();
			core.info(`Uploaded ${proj} ${oses.join(',')}/${arches.join(',')} (${same.length} slot(s), one transfer)`);
			continue;
		}

		// A ragged slot set sends the bytes once, then registers each other slot against the stored blob by hash.
		const [first, ...rest] = same;
		const r = await d.put(slotURL(first.os, first.arch), first.file, sum);
		if (r.status !== 201) { core.setFailed(`Upload ${proj} ${first.os}/${first.arch} failed (${r.status}): ${r.text}`); return false; }
		core.info(`Uploaded ${proj} ${first.os}/${first.arch}`);
		for (const { os, arch } of rest) {
			const ref = await d.send('PUT', `${slotURL(os, arch)}&upload_sha256=${sum}`);
			if (ref.status === 201) { core.info(`Registered ${proj} ${os}/${arch} by hash`); continue; }
			if (ref.status === 409) { core.setFailed(`Upload ${proj} ${os}/${arch} failed (${ref.status}): ${ref.text}`); return false; }
			// The server evicted the blob between both requests, and the bytes are still here.
			core.warning(`Hash-reference upload ${proj} ${os}/${arch} returned ${ref.status}; sending the bytes`);
			const full = await d.put(slotURL(os, arch), first.file, sum);
			if (full.status !== 201) { core.setFailed(`Upload ${proj} ${os}/${arch} failed (${full.status}): ${full.text}`); return false; }
		}
		record();
	}
	const pub = await d.send('POST', `/api/v1/projects/${proj}/releases/${releaseVersion}/publish`);
	if (pub.status !== 200) { core.setFailed(`Publish ${proj} failed (${pub.status}): ${pub.text}`); return false; }
	core.info(`Published ${proj}/${releaseVersion} (${group.slots.length + group.portable.length} artifacts) to ${d.server}`);
	return true;
}
