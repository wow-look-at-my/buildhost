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
	/** The server-URL form: an issuer that answers no token is "none", not a transient failure to retry. */
	emptyIsNone?: boolean;
	/** The delay between attempts. The server-URL form takes one, so a caller can drive the retry without waiting. */
	wait?(ms: number): Promise<unknown>;
}

export const RETRY_INTERVAL_MS = 2000;

// GitHub's token endpoint sometimes answers with a proxy error page, such as
// "upstream connect error", in place of JSON. That is transient, so the mint
// retries it on a fixed interval.
export async function mintOidcToken(o: MintOptions): Promise<string>;
/** Mints an OIDC token audience-scoped to the buildhost server. It answers null when the job may not mint one, and empty when the issuer sent none; a refusal throws OidcRefused. */
export async function mintOidcToken(server: string, wait?: (ms: number) => Promise<unknown>): Promise<string | null>;
export async function mintOidcToken(arg: MintOptions | string, wait?: (ms: number) => Promise<unknown>): Promise<string | null> {
	if (typeof arg === 'string') {
		const url = process.env.ACTIONS_ID_TOKEN_REQUEST_URL;
		const bearer = process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN;
		if (!url || !bearer) return null;
		return await mintOidcToken({ url, bearer, audience: arg, warning: () => {}, emptyIsNone: true, wait });
	}
	const o = arg;
	const doFetch = o.fetchImpl ?? fetch;
	const interval = o.intervalMs ?? RETRY_INTERVAL_MS;
	for (let attempt = 1; ; attempt++) {
		let problem: string;
		try {
			const r = await doFetch(`${o.url}&audience=${o.audience}`, { headers: { Authorization: `Bearer ${o.bearer}` } });
			const text = await r.text();
			if (r.status >= 400 && r.status < 500 && r.status !== 408 && r.status !== 429) {
				throw new OidcRefused(`the OIDC issuer refused the token request (${r.status}): HTTP ${r.status}: ${text.slice(0, 300)}`);
			}
			if (r.ok) {
				let parsed: { value?: unknown } | undefined;
				try { parsed = JSON.parse(text) as { value?: unknown }; } catch { parsed = undefined; }
				const value = parsed?.value;
				if (typeof value === 'string' && value !== '') return value;
				// A JSON answer with no token is the issuer saying "none"; a body that is not JSON at all is a proxy's error page, which is transient.
				if (o.emptyIsNone && parsed !== undefined) return '';
			}
			problem = `HTTP ${r.status}: ${text.slice(0, 300)}`;
		} catch (e) {
			if (e instanceof OidcRefused) throw e;
			problem = e instanceof Error ? e.message : String(e);
		}
		o.warning(`OIDC token request failed (attempt ${attempt}), retrying in ${interval} ms: ${problem}`);
		await (o.wait ? o.wait(interval) : new Promise((r) => setTimeout(r, interval)));
	}
}

export class OidcRefused extends Error {}
export function jwtExpiry(jwt: string): number {
	const parts = jwt.split('.');
	if (parts.length !== 3) return 0;
	try {
		const claims = JSON.parse(Buffer.from(parts[1], 'base64url').toString()) as { exp?: number };
		return typeof claims.exp === 'number' ? claims.exp : 0;
	} catch { return 0; }
}

/**
 * The given token, else OIDC tokens minted as needed: each request takes one more than 60s from its exp, which is the leeway buildhost allows.
 * A job that may not mint and passed no token presents an empty token.
 */
export function tokenSource(server: string, given: string, now = () => Date.now() / 1000): import('./client').TokenSource {
	const canMint = !given && !!process.env.ACTIONS_ID_TOKEN_REQUEST_URL && !!process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN;
	let token = given;
	let expiresAt = 0;
	return {
		bearer: async () => {
			if (!canMint) return token;
			if (!token || expiresAt === 0 || now() > expiresAt - 60) {
				token = (await mintOidcToken(server)) ?? '';
				expiresAt = jwtExpiry(token);
			}
			return token;
		},
		expire: () => {
			if (canMint) expiresAt = 0;
			return canMint;
		},
	};
}
