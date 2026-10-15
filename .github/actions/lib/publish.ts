// The buildhost-publish step: finds a build's artifacts, creates a release in each project they go to, uploads them and publishes.

import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { createRequire } from 'node:module';
import type { ActionGlobals } from './action-globals';
import type { PublishedArtifact } from './storage-record';
import type { Verdict } from './existing-release';
import type { Deps } from './publish-group';
import type { Response } from './client';

// A relative import that names its .ts file is a tsc error, and one that does not fails in node. The sibling modules load through require.
const load = createRequire(import.meta.url);
const upload = load('./upload.ts') as typeof import('./upload');
const existing = load('./existing-release.ts') as typeof import('./existing-release');
const artifactGroups = load('./artifact-groups.ts') as typeof import('./artifact-groups');
const buildhost = load('./client.ts') as typeof import('./client');
const publishGroup = load('./publish-group.ts') as typeof import('./publish-group');
const { fileSHA256 } = load('./portable-manifest.ts') as typeof import('./portable-manifest');
const io = load('./file-io.ts') as typeof import('./file-io');
const { mintOidcToken } = load('./oidc.ts') as typeof import('./oidc');

/** The create_service, default_branch and apt_depends fields go only when set, so an empty input leaves the stored setting untouched. */
export function releaseBody(f: { gitBranch: string; gitCommit: string; version: string; createService: string; defaultBranch: string; aptDepends: string }): string {
	return JSON.stringify({
		git_branch: f.gitBranch, git_commit: f.gitCommit,
		...(f.version.length > 0 ? { version: f.version } : {}),
		...(f.createService.length > 0 ? { create_service: f.createService === 'true' } : {}),
		// an older server ignores this unknown field
		...(f.defaultBranch.length > 0 ? { default_branch: f.defaultBranch } : {}),
		...(f.aptDepends.length > 0 ? { apt_depends: f.aptDepends } : {}),
	});
}

/** Anything else is the failure message. */
export function releaseVersionFrom(proj: string, version: string, resp: Response): { rv: string; existed: boolean } | string {
	if (resp.status === 201) return { rv: JSON.parse(resp.text).version, existed: false };
	if (resp.status === 409) {
		if (version.length === 0) return `Release exists for ${proj}, no explicit version`;
		return { rv: version, existed: true };
	}
	return `Create release for ${proj} failed (${resp.status}): ${resp.text}`;
}

/** The deployment URL: the release page for one project, else the project page of the repo-level project, else of the first one. */
export function deploymentURL(server: string, project: string, published: { proj: string; rv: string }[]): string | null {
	if (published.length === 0) return null;
	if (published.length === 1) return `${server}/projects/${published[0].proj}/releases/${published[0].rv}`;
	const primary = published.find((p) => p.proj === project) ?? published[0];
	return `${server}/projects/${primary.proj}`;
}

/** Runs the publish: the source directory, an OIDC token, then a release per project. A failure is reported through core.setFailed. */
export async function publish(g: ActionGlobals): Promise<void> {
	const { core, github, octokit, exec, inputs } = g;
	const artifactName: string = String(inputs.artifact_name);
	const inputPath: string = String(inputs.path);
	const inputBrewService: string = String(inputs.create_service);
	if (inputBrewService !== '' && inputBrewService !== 'true' && inputBrewService !== 'false') {
		core.setFailed(`create_service must be 'true', 'false', or empty; got '${inputBrewService}'`);
		return;
	}
	const server: string = String(inputs.server);
	const project: string = String(inputs.project || github.repository.split("/").pop()!);
	const version: string = String(inputs.version);

	let sourceDir: string;
	if (inputPath.length > 0) {
		// path mode reads a local directory as-is -- no GHA artifact download, no GitHub API call, and GITHUB_TOKEN is never read.
		sourceDir = path.resolve(process.env.GITHUB_WORKSPACE || process.cwd(), inputPath);
		if (!fs.existsSync(sourceDir) || !fs.statSync(sourceDir).isDirectory()) {
			core.setFailed(`Binaries directory '${sourceDir}' (from the 'path' input) does not exist or is not a directory`);
			return;
		}
		if (fs.readdirSync(sourceDir).length === 0) {
			core.setFailed(`Binaries directory '${sourceDir}' (from the 'path' input) is empty`);
			return;
		}
		core.info(`Publishing from local directory ${sourceDir} (GHA artifact download skipped)`);
	} else {
		// the default: downloads the run's artifact_name artifact and unzips it
		sourceDir = path.join(os.tmpdir(), "buildhost-publish");
		fs.mkdirSync(sourceDir, { recursive: true });
		const oct = octokit(process.env.GITHUB_TOKEN!);
		const [owner, repo] = github.repository.split("/");
		// the github context is rebuilt from environment variables, so run_id arrives as a string
		const { data: { artifacts } } = await oct.rest.actions.listWorkflowRunArtifacts({ owner, repo, run_id: Number(github.run_id) });
		const artifact = artifacts.find((a) => a.name === artifactName);
		if (!artifact) { core.setFailed(`Artifact '${artifactName}' not found in run`); return; }
		const { data: zip } = await oct.rest.actions.downloadArtifact({ owner, repo, artifact_id: artifact.id, archive_format: "zip" });
		const zipPath = path.join(sourceDir, "artifact.zip");
		fs.writeFileSync(zipPath, Buffer.from(zip as ArrayBuffer));
		await exec.exec("unzip", ["-o", zipPath, "-d", sourceDir]);
	}

	const token = await mintOidcToken(server);
	if (token === null) {
		core.setFailed("OIDC not available. Add 'permissions: { id-token: write }'.");
		return;
	}
	if (!token) { core.setFailed("Failed to obtain OIDC token"); return; }

	// artifacts accumulate here as they're accepted, for the storage record
	const uploadedSlots: PublishedArtifact[] = [];

	const manifestPath = path.join(sourceDir, artifactGroups.ManifestName);
	const found = artifactGroups.findArtifacts({
		files: fs.readdirSync(sourceDir).filter((f) => {
			const full = path.join(sourceDir, f);
			return !fs.lstatSync(full).isSymbolicLink() && fs.statSync(full).isFile();
		}),
		manifest: fs.existsSync(manifestPath) ? fs.readFileSync(manifestPath, "utf-8") : null,
		exists: (f) => fs.existsSync(path.join(sourceDir, f)),
	}, project);
	if (!found.ok) { core.setFailed(found.message); return; }
	const { groups, files, portable } = found;
	core.info(`Found ${files.length} per-platform artifact(s) and ${portable.length} portable artifact(s) across ${groups.size} project(s): ${[...groups.keys()].join(", ")}`);

	// The client retries a transient failure with backoff, so a rolling buildhost deploy never fails the publish.
	const api = buildhost.client(server, token, core);

	let directLimit = -1;
	const putFile = async (urlPath: string, filePath: string, sum: string, extra?: Record<string, string>) => {
		if (directLimit < 0) directLimit = await upload.directLimit(api, core);
		const body = {
			size: fs.statSync(filePath).size,
			sha256: sum,
			read: (offset: number, length: number): Buffer => io.readChunk(fs, filePath, offset, length),
		};
		return upload.putFile(api, core, { urlPath, body, headers: extra, directLimit });
	};

	const body = releaseBody({
		gitBranch: String(inputs.git_branch || github.ref_name),
		gitCommit: String(inputs.git_commit || github.sha),
		version,
		createService: inputBrewService,
		defaultBranch: String(inputs.default_branch),
		aptDepends: String(inputs.apt_depends),
	});
	const sha256 = (f: string) => fileSHA256(path.join(sourceDir, f));
	const deps: Deps = {
		send: api, core, server, sha256,
		put: (url, f, sum, extra) => putFile(url, path.join(sourceDir, f), sum, extra),
		record: (slot) => { uploadedSlots.push(slot); },
	};

	// publish each <repo>/<binary> project independently
	const published: { proj: string; rv: string }[] = [];
	for (const [proj, group] of groups) {
		const created = releaseVersionFrom(proj, version, await api("POST", `/api/v1/projects/${proj}/releases`, body));
		if (typeof created === 'string') { core.setFailed(created); return; }
		const rv = created.rv;
		core.info(`Release: ${proj}/${rv}`);
		// a published release never changes, so the same bytes are a no-op and other bytes fail naming each slot
		const verdict: Verdict = created.existed
			? await existing.existingRelease(api, core, proj, rv, await artifactGroups.slotsOf(group, sha256))
			: { action: "publish" };
		if (verdict.action === "fail") { core.setFailed(verdict.message); return; }
		if (verdict.action === "publish" && !(await publishGroup.publishGroup(deps, proj, rv, group))) return;
		published.push({ proj, rv });
	}

	const url = deploymentURL(server, project, published);
	if (url !== null) core.setOutput('deployment_url', url);
}
