// Fetches one file for a composite action. The limit is on silence, not on total time, so a large file on a slow link still succeeds.

/** How long a transfer may go without a byte, before the headers or during the body. */
export const DefaultStallMs = 30_000;

/**
 * Fetch url and return the whole body. It rejects on a non-2xx status, and
 * when no byte arrives for stallMs.
 */
export async function fetchBody(url: string, headers: Record<string, string>, stallMs = DefaultStallMs): Promise<Buffer> {
	const controller = new AbortController();
	const stalled = () => controller.abort(new Error(`no bytes for ${stallMs / 1000}s`));
	let timer = setTimeout(stalled, stallMs);
	try {
		const response = await fetch(url, { headers, redirect: 'follow', signal: controller.signal });
		if (!response.ok) throw new Error(`HTTP ${response.status}`);
		if (!response.body) throw new Error('response has no body');
		const chunks: Buffer[] = [];
		const reader = response.body.getReader();
		for (;;) {
			clearTimeout(timer);
			timer = setTimeout(stalled, stallMs);
			const { done, value } = await reader.read();
			if (done) break;
			chunks.push(Buffer.from(value));
		}
		return Buffer.concat(chunks);
	} finally {
		clearTimeout(timer);
	}
}
