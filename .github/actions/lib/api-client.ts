// The authenticated buildhost API client a publish step sends every request through.

export interface Core { warning(m: string): void }

export interface Response { status: number; text: string }

export type Api = (
	method: string,
	urlPath: string,
	body?: string | Buffer | null,
	extra?: Record<string, string>,
) => Promise<Response>;

/** How many times one request is sent before its last answer is returned. */
export const MaxAttempts = 5;

export const isTransient = (status: number, text: string): boolean => status >= 500 || (status === 404 && text.trim() === '');

/**
 * Returns a client that retries a transient failure, so a rolling buildhost deploy
 * never fails the publish. A network error after the last attempt returns status 0.
 */
export function makeApi(
	server: string,
	token: string,
	core: Core,
	sleep: (ms: number) => Promise<void> = (ms) => new Promise((r) => setTimeout(r, ms)),
): Api {
	return async (method, urlPath, body, extra) => {
		const headers: Record<string, string> = { Authorization: `Bearer ${token}`, ...(extra ?? {}) };
		if (typeof body === 'string') headers['Content-Type'] = 'application/json';
		else if (body) headers['Content-Type'] = 'application/octet-stream';
		for (let attempt = 1; attempt <= MaxAttempts; attempt++) {
			try {
				const resp = await fetch(`${server}${urlPath}`, { method, headers, body });
				const text = await resp.text();
				if (attempt === MaxAttempts || !isTransient(resp.status, text)) return { status: resp.status, text };
				core.warning(`${method} ${urlPath} -> HTTP ${resp.status} (transient; attempt ${attempt}/${MaxAttempts}); retrying`);
			} catch (e) {
				const msg = e instanceof Error ? e.message : String(e);
				if (attempt === MaxAttempts) return { status: 0, text: `request failed after ${MaxAttempts} attempts: ${msg}` };
				core.warning(`${method} ${urlPath} -> ${msg} (network error; attempt ${attempt}/${MaxAttempts}); retrying`);
			}
			await sleep(1000 * 2 ** (attempt - 1));
		}
		return { status: 0, text: 'unreachable' };
	};
}
