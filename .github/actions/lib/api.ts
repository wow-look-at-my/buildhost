// The authenticated request a publish sends to the buildhost API.

export interface Core { warning(m: string): void }

export interface Response { status: number; text: string }

export type Api = (method: string, urlPath: string, body?: string | Buffer | null, extra?: Record<string, string>) => Promise<Response>;

/** A server error, or a proxy's not-found with an empty body. The app's own not-found carries JSON and is final. */
export function isTransient(status: number, text: string): boolean {
	return status >= 500 || (status === 404 && text.trim() === '');
}

/**
 * Returns a sender for server that presents token. A transient failure is retried with backoff,
 * so a rolling buildhost deploy does not fail the publish.
 */
export function apiSender(server: string, token: string, core: Core, delayMs = 1000): Api {
	return async (method, urlPath, body, extra) => {
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
			await new Promise((resolve) => setTimeout(resolve, delayMs * 2 ** (attempt - 1)));
		}
		return { status: 0, text: 'unreachable' };
	};
}
