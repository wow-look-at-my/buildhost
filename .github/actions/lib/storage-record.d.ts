/** One row on the org's linked artifacts page. `org`, `github_repository`,
 *  `status` and `return_records` are supplied by postStorageRecords. */
export interface StorageRecord {
	name: string;
	version: string;
	/** Lowercase `sha256:<64 hex>` of the bytes as UPLOADED. */
	digest: string;
	/** Must be `https://` -- a loopback or plain-http registry is skipped. */
	registry_url: string;
	repository?: string;
	path?: string;
	/** Must serve bytes hashing to `digest`; capped at 152 chars by the API. */
	artifact_url?: string;
}

export interface RecordOptions {
	/** GitHub owner the endpoint is scoped to (context.repo.owner). */
	owner: string;
	/** Repo NAME only -- the API rejects `owner/repo`. */
	githubRepository: string;
	/** Human label for one record, used in every log and error message. */
	label(record: StorageRecord): string;
}

export interface RecordCore {
	info(message: string): void;
	setFailed(message: string): void;
}

export interface RecordOctokit {
	request(route: string, params: Record<string, unknown>): Promise<unknown>;
	rest: {
		users: {
			getByUsername(params: { username: string }): Promise<{ data: { type?: string } }>;
		};
	};
}

/**
 * Post one storage record per entry, in order. Returns true when the records
 * were posted or deliberately skipped (unreachable registry, user-account
 * owner), false when one failed -- in which case core.setFailed has already
 * been called and the caller should stop.
 */
export declare function postStorageRecords(
	octokit: RecordOctokit,
	core: RecordCore,
	records: readonly StorageRecord[],
	opts: RecordOptions,
): Promise<boolean>;
