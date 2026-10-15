// The GitHub Actions OIDC token every buildhost action authenticates with when it has no token input.

/** Mints an OIDC token audience-scoped to the buildhost server. It answers null when the job may not mint one, and empty when the issuer sent none. */
export async function mintOidcToken(server: string): Promise<string | null> {
	const url = process.env.ACTIONS_ID_TOKEN_REQUEST_URL;
	const bearer = process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN;
	if (!url || !bearer) return null;
	const resp = await fetch(`${url}&audience=${server}`, { headers: { Authorization: `Bearer ${bearer}` } });
	return ((await resp.json()) as { value?: string }).value || '';
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
