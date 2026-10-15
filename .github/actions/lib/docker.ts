// The typescript steps of buildhost-docker-push and buildhost-publish-docker.

import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { createRequire } from 'node:module';
import type { ActionGlobals } from './action-globals';

const { mintOidcToken } = createRequire(import.meta.url)('./oidc.ts') as typeof import('./oidc');

/**
 * The tags a docker publish pushes when its tags input is empty: the commit, the ref name and, on the default branch, latest.
 * The ref name is docker-sanitized, so "claude/foo" becomes "claude-foo", always a bare buildhost-bound tag.
 */
export function defaultTagList(refName: string, ref: string, sha: string, defaultBranch: string): string[] {
	const refTag = refName.toLowerCase().replace(/[^a-z0-9._-]+/g, "-").replace(/^[.-]+/, "");
	const tags = [sha, refTag].filter(Boolean);
	if (ref === `refs/heads/${defaultBranch}`) tags.push("latest");
	return tags;
}

/** buildhost-publish-docker's default-tags step: outputs tags, newline-separated. */
export function defaultTags(g: ActionGlobals): void {
	const { core, context } = g;
	// mirrors GITHUB_REF_NAME: the branch for refs/heads/*, the git tag for refs/tags/*, "<pr>/merge" for pulls
	const refName: string = process.env.GITHUB_REF_NAME ?? context.ref.replace(/^refs\/[^/]+\//, "");
	const tags = defaultTagList(refName, context.ref, context.sha, context.payload.repository?.default_branch ?? "master");
	core.setOutput("tags", tags.join("\n"));
	core.info(`tags input omitted; defaulting to: ${tags.join(", ")}`);
}

/** The manifest digest the layout's index.json names, which is what buildhost stored, or empty when it names none. */
export function layoutDigest(layout: string): string {
	const index = JSON.parse(readFileSync(join(layout, "index.json"), "utf8")) as { manifests?: { digest?: string }[] };
	return index.manifests?.[0]?.digest ?? "";
}

/** The token the push sends: the token input, else a minted OIDC token. Null after it reports why there is none. */
async function pushToken(g: ActionGlobals, registry: string): Promise<string | null> {
	const given = String(g.inputs.token);
	if (given) return given;
	const minted = await mintOidcToken(registry);
	if (minted === null) g.core.setFailed("No token input and OIDC is unavailable. Add 'permissions: { id-token: write }' to the job, or pass token.");
	else if (!minted) g.core.setFailed(`Failed to obtain OIDC token for ${registry}`);
	return minted || null;
}

/**
 * buildhost-docker-push: pushes the OCI layout with the buildhost CLI the earlier steps put in RUNNER_TEMP, and outputs digest.
 * buildhost auto-provisions the project on first push.
 */
export async function dockerPush(g: ActionGlobals): Promise<void> {
	const registry = String(g.inputs.server);
	const bearer = await pushToken(g, registry);
	if (bearer === null) return;
	g.core.setSecret(bearer);

	const pushRefs = String(g.inputs.refs).split("\n").map((s) => s.trim()).filter(Boolean);
	if (pushRefs.length === 0) {
		g.core.setFailed("refs is empty: nothing to push");
		return;
	}
	const layout = String(g.inputs.image);
	const cli = join(process.env.RUNNER_TEMP!, "buildhost-cli");
	await g.exec.exec(cli, ["docker-push", "--server", registry, "--image", layout, ...pushRefs], {
		env: { ...process.env as Record<string, string>, BUILDHOST_TOKEN: bearer },
	});

	const digest = layoutDigest(layout);
	if (digest === "") {
		g.core.setFailed(`Pushed image recorded no manifest digest in ${join(layout, "index.json")}; cannot record what was stored`);
		return;
	}
	g.core.setOutput("digest", digest);
}
