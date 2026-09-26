// Delivering an artifact body from a composite action.
//
// So a body larger than the server's advertised `max_direct_upload_bytes` is
// assembled through an upload session instead: POST /api/v1/uploads, PATCH
// .../{id}?offset=N per chunk, then the ORIGINAL endpoint with an empty body
// and ?upload_session=&upload_sha256=.
//
// The size decision is made BEFORE anything is sent, from server-info. This
// mirrors internal/uploadclient, which is the CLI's engine for the same
// protocol.
//
// Loaded by `require(".../upload.ts")` (node strips the types) typed as
// `typeof import(".../upload")`. See docs/uploads.md.

/* */
export const DefaultChunkSize = 64 << 20;

/* */
export const DefaultDirectLimit = 95 << 20;

/** How many times a chunk that commits no bytes is retried before giving up. */
const retryAttempts = 5;

export interface Core { info(m: string): void; warning(m: string): void }

export interface Response { status: number; text: string }

/** Send a single request. The caller owns authentication, retries and base
 * URL, so this module carries only the session protocol. */
export type Send = (
	method: string,
	urlPath: string,
	body?: Buffer | null,
	headers?: Record<string, string>,
) => Promise<Response>;

/** The bytes to send. The caller owns the filesystem, so this module needs
 * no node builtins of its own and stays a single file of protocol. */
export interface Body {
	size: number;
	/** Bare hex sha256 of the whole body, used as the finalize integrity check. */
	sha256: string;
	/** Returns exactly `length` bytes starting at `offset`. */
	read(offset: number, length: number): Buffer;
}

export interface PutFileOptions {
	/** The artifact endpoint, e.g. `/api/v1/projects/p/releases/v1/artifacts/linux/amd64?kind=binary`. */
	urlPath: string;
	body: Body;
	/** Extra request headers, e.g. `X-Artifact-Filename`. */
	headers?: Record<string, string>;
	/** Bytes per chunk. */
	chunkSize?: number;
	/** Largest direct body. */
	directLimit?: number;
}

/* */
export async function directLimit(send: Send, core: Core): Promise<number> {
	const resp = await send('GET', '/api/v1/server-info');
	if (resp.status !== 200) {
		core.warning(`server-info answered ${resp.status}; assuming a ${DefaultDirectLimit >> 20} MiB direct limit`);
		return DefaultDirectLimit;
	}
	try {
		const info = JSON.parse(resp.text) as { max_direct_upload_bytes?: number };
		if (typeof info.max_direct_upload_bytes === 'number' && info.max_direct_upload_bytes > 0) {
			return info.max_direct_upload_bytes;
		}
	} catch {
	}
	core.warning(`server-info carried no max_direct_upload_bytes; assuming ${DefaultDirectLimit >> 20} MiB`);
	return DefaultDirectLimit;
}

/** Sends the body to `urlPath`, directly when it fits and through an upload
 * session when it does not. */
export async function putFile(send: Send, core: Core, opts: PutFileOptions): Promise<Response> {
	const size = opts.body.size;
	const chunkSize = opts.chunkSize ?? DefaultChunkSize;
	const limit = opts.directLimit ?? await directLimit(send, core);
	if (chunkSize <= 0 || size <= limit) {
		return send('PUT', opts.urlPath, opts.body.read(0, size), opts.headers);
	}

	const id = await createSession(send);
	try {
		await sendChunks(send, core, id, opts.body, size, chunkSize);
		const sep = opts.urlPath.includes('?') ? '&' : '?';
		const finalize = `${opts.urlPath}${sep}upload_session=${encodeURIComponent(id)}&upload_sha256=${opts.body.sha256}`;
		const resp = await send('PUT', finalize, null, opts.headers);
		if (resp.status < 200 || resp.status >= 300) {
			await abortSession(send, id);
		}
		return resp;
	} catch (e) {
		await abortSession(send, id);
		throw e;
	}
}

async function createSession(send: Send): Promise<string> {
	const resp = await send('POST', '/api/v1/uploads');
	if (resp.status !== 201) {
		throw new Error(`create upload session failed (${resp.status}): ${resp.text}`);
	}
	const id = (JSON.parse(resp.text) as { id?: string }).id;
	if (!id) throw new Error('create upload session: response carried no id');
	return id;
}

// Every iteration trusts the size the SERVER reports, so a chunk that landed
// only partway resumes from where it stopped instead of restarting the file.
async function sendChunks(send: Send, core: Core, id: string, body: Body, size: number, chunkSize: number): Promise<void> {
	const total = Math.ceil(size / chunkSize);
	core.info(`uploading ${size >> 20} MiB in ${total} chunk(s) of ${chunkSize >> 20} MiB through session ${id}`);
	let offset = 0;
	let stalls = 0;
	while (offset < size) {
		const next = await appendChunk(send, id, offset, body.read(offset, Math.min(chunkSize, size - offset)));
		if (next <= offset) {
			stalls++;
			if (stalls >= retryAttempts) {
				throw new Error(`upload session ${id} made no progress at offset ${offset}`);
			}
			core.warning(`upload session ${id} committed nothing at offset ${offset}; retry ${stalls}/${retryAttempts}`);
			await new Promise((r) => setTimeout(r, 1000 * 2 ** (stalls - 1)));
		} else {
			stalls = 0;
			core.info(`  chunk ${Math.ceil(next / chunkSize)}/${total} uploaded (${next}/${size} bytes)`);
		}
		offset = next;
	}
}

// Anything else asks the status endpoint, because the bytes may have landed
// before the answer went missing.
async function appendChunk(send: Send, id: string, offset: number, chunk: Buffer): Promise<number> {
	const resp = await send('PATCH', `/api/v1/uploads/${encodeURIComponent(id)}?offset=${offset}`, chunk);
	if (resp.status === 200 || resp.status === 409) {
		const size = (JSON.parse(resp.text) as { size?: number }).size;
		if (typeof size !== 'number') {
			throw new Error(`upload chunk at ${offset}: response carried no size`);
		}
		return size;
	}
	if (resp.status >= 500 || resp.status === 0) {
		return sessionSize(send, id);
	}
	throw new Error(`upload chunk at ${offset} failed (${resp.status}): ${resp.text}`);
}

async function sessionSize(send: Send, id: string): Promise<number> {
	const resp = await send('GET', `/api/v1/uploads/${encodeURIComponent(id)}`);
	if (resp.status !== 200) {
		throw new Error(`read upload session ${id} failed (${resp.status}): ${resp.text}`);
	}
	const size = (JSON.parse(resp.text) as { size?: number }).size;
	if (typeof size !== 'number') throw new Error(`read upload session ${id}: response carried no size`);
	return size;
}

async function abortSession(send: Send, id: string): Promise<void> {
	try {
		await send('DELETE', `/api/v1/uploads/${encodeURIComponent(id)}`);
	} catch {
		// nothing to do
	}
}
