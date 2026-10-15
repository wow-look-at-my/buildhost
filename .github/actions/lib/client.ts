// The authenticated buildhost client a publish sends every request through.

export interface Core { warning(m: string): void }

export interface Response { status: number; text: string }

export type Send = (
	method: string,
	urlPath: string,
	body?: string | Buffer | Blob | null,
	headers?: Record<string, string>,
) => Promise<Response>;

/** A token that can die mid-run, such as a minted OIDC token, which lives minutes while a large upload runs longer. */
export interface TokenSource {
	/** The token to present now. */
	bearer(): Promise<string>;
	/** Drops a token the server refused. True when the next bearer() mints a fresh one, so the request is worth retrying. */
	expire(): boolean;
}


export const maxAttempts = 5;

export const isTransient = (status: number, text: string): boolean =>
	status >= 500 || (status === 404 && text.trim() === '');

/**A sender that retries a transient failure with backoff, so a rolling buildhost deploy never fails the publish. */
export function client(server: string, token: string | TokenSource, core: Core, delay = (ms: number) => new Promise((r) => setTimeout(r, ms))): Send {
	const source: TokenSource = typeof token === 'string' ? { bearer: async () => token, expire: () => false } : token;
	return async (method, urlPath, body, extra) => {
		const headers: Record<string, string> = {};
		if (typeof body === 'string') headers['Content-Type'] = 'application/json';
		else if (body) headers['Content-Type'] = 'application/octet-stream';
		Object.assign(headers, extra ?? {});
		for (let attempt = 1; attempt <= maxAttempts; attempt++) {
			try {
				const resp = await fetch(`${server}${urlPath}`, { method, headers: { ...headers, Authorization: `Bearer ${await source.bearer()}` }, body });
				const text = await resp.text();
				if (resp.status === 401 && attempt < maxAttempts && source.expire()) {
					core.warning(`${method} ${urlPath} -> HTTP 401 (attempt ${attempt}/${maxAttempts}); minting a fresh OIDC token and retrying`);
					continue;
				}
				if (attempt === maxAttempts || !isTransient(resp.status, text)) return { status: resp.status, text };
				core.warning(`${method} ${urlPath} -> HTTP ${resp.status} (transient; attempt ${attempt}/${maxAttempts}); retrying`);
			} catch (e) {
				const msg = e instanceof Error ? e.message : String(e);
				if (attempt === maxAttempts) return { status: 0, text: `request failed after ${maxAttempts} attempts: ${msg}` };
				core.warning(`${method} ${urlPath} -> ${msg} (network error; attempt ${attempt}/${maxAttempts}); retrying`);
			}
			await delay(1000 * 2 ** (attempt - 1));
		}
		return { status: 0, text: 'unreachable' };
	};
}
