// The authenticated buildhost client a publish sends every request through.

export interface Core { warning(m: string): void }

export interface Response { status: number; text: string }

export type Send = (
	method: string,
	urlPath: string,
	body?: string | Buffer | null,
	headers?: Record<string, string>,
) => Promise<Response>;

const maxAttempts = 5;

/** A 5xx, or a proxy 404 with an empty body, is transient. The app's own 404 carries JSON, so a 404 with a body returns at once. */
export const isTransient = (status: number, text: string): boolean =>
	status >= 500 || (status === 404 && text.trim() === '');

/** A sender that retries a transient failure with backoff, so a rolling buildhost deploy never fails the publish. */
export function client(server: string, token: string, core: Core, delay = (ms: number) => new Promise((r) => setTimeout(r, ms))): Send {
	return async (method, urlPath, body, extra) => {
		const headers: Record<string, string> = { Authorization: `Bearer ${token}`, ...(extra ?? {}) };
		if (typeof body === 'string') headers['Content-Type'] = 'application/json';
		else if (body) headers['Content-Type'] = 'application/octet-stream';
		for (let attempt = 1; attempt <= maxAttempts; attempt++) {
			try {
				const resp = await fetch(`${server}${urlPath}`, { method, headers, body });
				const text = await resp.text();
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
