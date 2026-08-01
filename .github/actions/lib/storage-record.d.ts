/** The injected `core` (only what this module calls). */
export interface RecordCore {
	info(message: string): void;
	setFailed(message: string): void;
}

/** The injected `octokit` (only what this module calls). */
export interface RecordOctokit {
	request(route: string, params: Record<string, unknown>): Promise<unknown>;
	rest: {
		users: {
			getByUsername(params: { username: string }): Promise<{ data: { type?: string } }>;
		};
	};
}

/** The injected `context` (only what this module reads). */
export interface RecordContext {
	repo: { owner: string; repo: string };
}

/** One published os/arch slot. `project`/`version` default to the call's. */
export interface PublishedArtifact {
	os?: string;
	arch?: string;
	/** Bare hex sha256 of the bytes as UPLOADED. Missing = a failed publish. */
	sha256?: string;
	project?: string;
	version?: string;
}

/**
 * Record release artifacts. Every field, URL and message is derived here --
 * pass only what was published.
 *
 * Returns false only after calling core.setFailed. True means the records were
 * posted, or deliberately skipped (unreachable registry, user-account owner).
 */
export declare function recordReleaseArtifacts(
	octokit: RecordOctokit,
	core: RecordCore,
	context: RecordContext,
	params: {
		/** Buildhost server URL; the download origin is derived from it. */
		server: string;
		/** Default project for artifacts that do not name their own. */
		project?: string;
		/** Default version for artifacts that do not name their own. */
		version?: string;
		artifacts: readonly PublishedArtifact[];
	},
): Promise<boolean>;

/** Record a published static site archive. Same contract as above. */
export declare function recordSite(
	octokit: RecordOctokit,
	core: RecordCore,
	context: RecordContext,
	params: {
		server: string;
		project: string;
		branch: string;
		/** Release version (the site's git commit). */
		version: string;
		/** Bare hex sha256 of the uploaded archive. */
		sha256: string;
	},
): Promise<boolean>;

/** Record a pushed image. Same contract as above. */
export declare function recordImage(
	octokit: RecordOctokit,
	core: RecordCore,
	context: RecordContext,
	params: {
		/** Buildhost-bound reference `<oci-host>/<repository>:<tag>`. */
		reference: string;
		/** Manifest digest, `sha256:`-prefixed as OCI writes it. */
		digest: string;
	},
): Promise<boolean>;
