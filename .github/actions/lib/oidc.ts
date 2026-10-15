// The GitHub Actions OIDC token every buildhost action authenticates with when it has no token input.

/** Mints an OIDC token audience-scoped to the buildhost server. It answers null when the job may not mint one, and empty when the issuer sent none. */
export async function mintOidcToken(server: string): Promise<string | null> {
	const url = process.env.ACTIONS_ID_TOKEN_REQUEST_URL;
	const bearer = process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN;
	if (!url || !bearer) return null;
	const resp = await fetch(`${url}&audience=${server}`, { headers: { Authorization: `Bearer ${bearer}` } });
	return ((await resp.json()) as { value?: string }).value || '';
}
