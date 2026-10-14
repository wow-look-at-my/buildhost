// The requests the publish composite sends to buildhost.

export interface Core { info(m: string): void; warning(m: string): void }

export interface Response { status: number; text: string }

/**
 * A transient 5xx, or a proxy 404 with an empty body, is retried. The app's
 * own 404 carries JSON, and any 4xx with a body returns at once.
 */
export const isTransient = (status: number, text: string): boolean =>
	status >= 500 || (status === 404 && text.trim() === '');

export interface Client {
	api(method: string, urlPath: string, body?: string | Buffer | null, extra?: Record<string, string>): Promise<Response>;
	putFile(urlPath: string, filePath: string, sum: string, extra?: Record<string, string>): Promise<Response>;
}

export interface ClientOptions {
	server: string;
	token: string;
	core: Core;
	upload: typeof import('./upload');
	fs: typeof import('node:fs');
}

export function newClient(opts: ClientOptions): Client {
	const { server, token, core, upload, fs } = opts;
	const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

	// Retries a transient failure with backoff, so a rolling buildhost deploy never hard-fails the publish.
	const api = async (method: string, urlPath: string, body?: string | Buffer | null, extra?: Record<string, string>): Promise<Response> => {
		const headers: Record<string, string> = { Authorization: `Bearer ${token}`, ...(extra ?? {}) };
		if (typeof body === 'string') headers['Content-Type'] = 'application/json';
		else if (body) headers['Content-Type'] = 'application/octet-stream';
		const maxAttempts = 5;
		for (let attempt = 1; attempt <= maxAttempts; attempt++) {
			try {
				const resp = await fetch(`${server}${urlPath}`, { method, headers, body });
				const text = await resp.text();
				if (attempt === maxAttempts || !isTransient(resp.status, text)) {
					return { status: resp.status, text };
				}
				core.warning(`${method} ${urlPath} -> HTTP ${resp.status} (transient; attempt ${attempt}/${maxAttempts}); retrying`);
			} catch (e) {
				const msg = e instanceof Error ? e.message : String(e);
				if (attempt === maxAttempts) {
					return { status: 0, text: `request failed after ${maxAttempts} attempts: ${msg}` };
				}
				core.warning(`${method} ${urlPath} -> ${msg} (network error; attempt ${attempt}/${maxAttempts}); retrying`);
			}
			await sleep(1000 * 2 ** (attempt - 1));
		}
		return { status: 0, text: 'unreachable' };
	};

	// Cloudflare answers 413 to a body over 100 MB, so a body past the advertised direct limit goes through an upload session.
	let directLimit = -1;
	const putFile = async (urlPath: string, filePath: string, sum: string, extra?: Record<string, string>): Promise<Response> => {
		if (directLimit < 0) directLimit = await upload.directLimit(api, core);
		const body = {
			size: fs.statSync(filePath).size,
			sha256: sum,
			// A short read leaves stale memory in the tail of an allocUnsafe buffer, so the read loops until the chunk is whole.
			read: (offset: number, length: number): Buffer => {
				const buf = Buffer.allocUnsafe(length);
				const fd = fs.openSync(filePath, 'r');
				try {
					let got = 0;
					while (got < length) {
						const n = fs.readSync(fd, buf, got, length - got, offset + got);
						if (n <= 0) throw new Error(`${filePath}: wanted ${length} bytes at ${offset}, the file ended after ${got}`);
						got += n;
					}
				} finally { fs.closeSync(fd); }
				return buf;
			},
		};
		return upload.putFile(api, core, { urlPath, body, headers: extra, directLimit });
	};

	return { api, putFile };
}
