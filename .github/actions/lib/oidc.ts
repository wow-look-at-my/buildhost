// Minting a GitHub Actions OIDC token for the buildhost server.
//
// Loaded by `require(".../oidc.ts")` (node strips the types) typed as
// `typeof import(".../oidc")`, the same as storage-record.ts.

export interface MintOptions {
	// ACTIONS_ID_TOKEN_REQUEST_URL and ACTIONS_ID_TOKEN_REQUEST_TOKEN.
	url: string;
	bearer: string;
	// The buildhost server URL. buildhost checks the JWT audience against it.
	audience: string;
	warning(m: string): void;
	intervalMs?: number;
	fetchImpl?: typeof fetch;
}

export const RETRY_INTERVAL_MS = 2000;

// GitHub's token endpoint sometimes answers with a proxy error page, such as
// "upstream connect error", in place of JSON. That is transient, so the mint
// retries it on a fixed interval.
export async function mintOidcToken(o: MintOptions): Promise<string> {
	const doFetch = o.fetchImpl ?? fetch;
	const interval = o.intervalMs ?? RETRY_INTERVAL_MS;
	for (let attempt = 1; ; attempt++) {
		let problem: string;
		try {
			const r = await doFetch(`${o.url}&audience=${o.audience}`, { headers: { Authorization: `Bearer ${o.bearer}` } });
			const text = await r.text();
			if (r.status >= 400 && r.status < 500 && r.status !== 429) {
				throw new OidcRefused(`the OIDC token request was refused: HTTP ${r.status}: ${text.slice(0, 300)}`);
			}
			if (r.ok) {
				let value: unknown;
				try { value = (JSON.parse(text) as { value?: unknown }).value; } catch { value = undefined; }
				if (typeof value === 'string' && value !== '') return value;
			}
			problem = `HTTP ${r.status}: ${text.slice(0, 300)}`;
		} catch (e) {
			if (e instanceof OidcRefused) throw e;
			problem = e instanceof Error ? e.message : String(e);
		}
		o.warning(`OIDC token request failed (attempt ${attempt}), retrying in ${interval} ms: ${problem}`);
		await new Promise((r) => setTimeout(r, interval));
	}
}

export class OidcRefused extends Error {}
