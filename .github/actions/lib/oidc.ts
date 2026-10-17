// The GitHub Actions OIDC token every buildhost action authenticates with when it has no token input.

/** Mints an OIDC token audience-scoped to the buildhost server. It answers null when the job may not mint one, and empty when the issuer sent none. */
export async function mintOidcToken(server: string, wait = (ms: number) => new Promise((done) => setTimeout(done, ms))): Promise<string | null> {
	const url = process.env.ACTIONS_ID_TOKEN_REQUEST_URL;
	const bearer = process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN;
	if (!url || !bearer) return null;
	// The issuer sometimes answers with a proxy's error page. That is not a refusal, so the request goes again until it gets a token.
	for (;;) {
		let status = 0;
		let text = '';
		try {
			const resp = await fetch(`${url}&audience=${server}`, { headers: { Authorization: `Bearer ${bearer}` } });
			status = resp.status;
			text = await resp.text();
			if (resp.ok) return (JSON.parse(text) as { value?: string }).value || '';
		} catch (err) {
			text = String(err);
		}
		if (status >= 400 && status < 500 && status !== 408 && status !== 429) {
			throw new Error(`the OIDC issuer refused the token request (${status}): ${text.slice(0, 500)}`);
		}
		console.warn(`the OIDC token request failed (${status || 'no answer'}): ${text.slice(0, 200)}; it goes again in 2s`);
		await wait(2000);
	}
}

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
