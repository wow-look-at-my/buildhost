// Asserts that the SHIPPED IMAGE strips binaries on download.
//
// This check exists because that stopped being true and nobody noticed for
// weeks. Stripping used to shell out to strip(1)/objcopy(1); the production
// image is distroless and ships neither, so `strip.Available()` was false
// there, every download went out unstripped, and `fmt=symbols` could not work
// at all. Nothing failed, nothing logged, and the unit tests all passed --
// because the CI runner has binutils and the container does not. Only a check
// that runs against the real image can catch that class of regression, which
// is exactly what this is.
//
// Run against `docker compose -f docker-compose.ci.yml up` with port 8080
// published.

const BASE = "http://localhost:8080";
const STATIC = "http://static.localhost:8080";
const COMPOSE = ["compose", "-f", "docker-compose.ci.yml"];

function sh(cmd: string, args: string[]): string {
	const res = child_process.spawnSync(cmd, args, { encoding: "utf8" });
	if (res.status !== 0) {
		throw new Error(`${cmd} ${args.join(" ")} failed (${res.status}): ${res.stderr || res.stdout}`);
	}
	return res.stdout;
}

// -L matters: /file canonicalizes its query (sorted params) with a 301, so an
// unredirected request returns the redirect body, not the artifact.
function curl(args: string[]): string {
	return sh("curl", ["-fsSL", ...args]);
}

// The image has no shell, so exec the binary directly.
const token = sh("docker", [...COMPOSE, "exec", "-T", "buildhost", "buildhost", "bootstrap", "--name", "image-e2e"])
	.trim()
	.split("\n")
	.pop()!
	.trim();
if (!token) throw new Error("no token from bootstrap inside the container");
core.setSecret(token);

const auth = ["-H", `Authorization: Bearer ${token}`];
const project = "image-strip-e2e";

// This asserts ELF stripping, so the artifact has to BE an ELF carrying debug
// info. The shipped binary is an APE, a polyglot that is not an ELF, so the
// workflow compiles a small unstripped ELF and names it here.
const artifact = process.env.STRIP_FIXTURE;
if (!artifact) throw new Error("STRIP_FIXTURE must name an unstripped ELF to upload");
const uploadedSize = fs.statSync(artifact).size;
core.info(`uploading ${artifact} (${uploadedSize} bytes)`);

curl([...auth, "-H", "Content-Type: application/json", "-d",
	JSON.stringify({ name: project, versioning: "auto", is_private: false }), `${BASE}/api/v1/projects`]);
const relBody = curl([...auth, "-H", "Content-Type: application/json", "-d",
	JSON.stringify({ git_branch: "master" }), `${BASE}/api/v1/projects/${project}/releases`]);
const version = JSON.parse(relBody).version as string;
curl([...auth, "-X", "PUT", "--data-binary", `@${artifact}`,
	`${BASE}/api/v1/projects/${project}/releases/${version}/artifacts/linux/amd64?kind=binary`]);
curl([...auth, "-X", "POST", `${BASE}/api/v1/projects/${project}/releases/${version}/publish`]);

const query = `project=${project}&v=${version}&os=linux&arch=amd64`;

// 1. The download must be STRIPPED -- smaller than what was uploaded.
const out = `${process.env.RUNNER_TEMP ?? "/tmp"}/image-strip-download`;
curl(["-o", out, `${STATIC}/file?${query}&fmt=raw`]);
const downloadedSize = fs.statSync(out).size;
core.info(`downloaded ${downloadedSize} bytes (uploaded ${uploadedSize})`);
if (downloadedSize >= uploadedSize) {
	throw new Error(
		`the shipped image did not strip: uploaded ${uploadedSize} bytes, downloaded ${downloadedSize}. ` +
		`Stripping is supposed to happen at download time; if it silently no-ops here it no-ops in production.`,
	);
}

// 2. It must still be a usable ELF with its debug sections gone.
//
// Checked against the SECTION TABLE, not by searching for the string: the
// section-name string table keeps every name it ever held (the rewrite does not
// rebuild it), so ".debug_info" is still present as BYTES in a correctly
// stripped file. A substring check would fail on a good binary.
if (!fs.readFileSync(out).subarray(0, 4).equals(Buffer.from([0x7f, 0x45, 0x4c, 0x46]))) {
	throw new Error("the stripped download is not an ELF any more");
}

function sectionNames(file: string): string[] {
	const listing = sh("readelf", ["-SW", file]);
	return [...listing.matchAll(/^\s*\[\s*\d+\]\s+(\S+)/gm)].map((m) => m[1]);
}

const kept = sectionNames(out);
core.info(`sections after stripping: ${kept.join(" ")}`);
for (const gone of [".symtab", ".debug_info", ".debug_line"]) {
	if (kept.includes(gone)) throw new Error(`the stripped download still has a ${gone} section`);
}
for (const want of [".text", ".rodata"]) {
	if (!kept.includes(want)) throw new Error(`the stripped download lost ${want} -- it could not run`);
}

// 3. The header must advertise symbols for this artifact...
const headers = curl(["-D", "-", "-o", "/dev/null", `${STATIC}/file?${query}&fmt=raw`]);
if (!/x-debug-symbols:\s*available/i.test(headers)) {
	throw new Error(`expected X-Debug-Symbols: available, got:\n${headers}`);
}

// ... and fmt=symbols must actually serve them. This endpoint could never work
// in the distroless image before: it required objcopy.
const dbg = `${process.env.RUNNER_TEMP ?? "/tmp"}/image-strip-symbols`;
curl(["-o", dbg, `${STATIC}/file?${query}&fmt=symbols`]);
if (!sectionNames(dbg).includes(".debug_info")) {
	throw new Error("fmt=symbols did not return a file with a .debug_info section");
}
core.info(`symbols: ${fs.statSync(dbg).size} bytes`);

// 4. ?debug=1 opts out and returns exactly what was uploaded.
const full = `${process.env.RUNNER_TEMP ?? "/tmp"}/image-strip-full`;
curl(["-o", full, `${STATIC}/file?${query}&fmt=raw&debug=1`]);
if (fs.statSync(full).size !== uploadedSize) {
	throw new Error(`debug=1 must serve the artifact unstripped: got ${fs.statSync(full).size}, want ${uploadedSize}`);
}

core.info("OK: the shipped image strips on download, serves symbols, and honors debug=1");
