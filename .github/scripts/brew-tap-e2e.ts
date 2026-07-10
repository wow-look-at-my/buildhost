// Homebrew e2e legs for ci.yml's homebrew-tap-e2e job, run via
// wow-look-at-my/actions@typescript#latest with LEG=public|private|anon-leak.
//
// The point of this script is that CI executes the Homebrew flows EXACTLY as
// the docs describe them: the brew commands are extracted from README.md's
// fenced blocks and executed with only mechanical substitutions (https->http,
// brew.pazer.build -> the local test instance). The same blocks are
// cross-checked against the /llms.txt the local server serves, so the two
// documents cannot drift from each other either. A change that "fixes CI"
// without fixing the docs -- an extra brew command, a trust/sandbox opt-out, a
// different URL shape -- goes red here instead of silently diverging. That is
// the regression this repo already lived through once: Homebrew 6.0 broke the
// documented flow and the fix (brew trust) went into CI only, leaving the
// docs promising a flow that failed for real users.

const SERVER = "http://localhost:18080";
const DOC_HOST = "brew.pazer.build";
const LOCAL_HOST = "brew.localhost:18080";

// --- doc extraction ---------------------------------------------------------

// sliceSection returns the markdown from `heading` up to the next heading of
// the same or higher level.
function sliceSection(md: string, heading: string, what: string): string {
	const anchor = "\n" + heading + "\n";
	const start = md.indexOf(anchor);
	if (start < 0) throw new Error(`${what}: heading ${JSON.stringify(heading)} not found`);
	const level = (heading.match(/^#+/) ?? [""])[0].length;
	const rest = md.slice(start + anchor.length);
	const stop = rest.search(new RegExp(`^#{1,${level}} `, "m"));
	return stop < 0 ? rest : rest.slice(0, stop);
}

function fencedBlocks(md: string, lang: string): string[] {
	const out: string[] = [];
	const re = new RegExp("^```" + lang + "\\n([\\s\\S]*?)^```", "gm");
	for (let m = re.exec(md); m !== null; m = re.exec(md)) out.push(m[1].trimEnd());
	return out;
}

// localize applies the ONLY rewrites allowed between the docs and what CI
// runs: the documented public host becomes the local test instance. Nothing
// else about the commands may change.
function localize(block: string): string {
	return block.replaceAll("https://", "http://").replaceAll(DOC_HOST, LOCAL_HOST);
}

function extractDocFlows(): { publicCmds: string; privateCmds: string } {
	const readme = fs.readFileSync("README.md", "utf8");
	const brewSection = sliceSection(readme, "## Homebrew", "README.md");
	const publicBlocks = fencedBlocks(brewSection, "bash");
	if (publicBlocks.length < 1) throw new Error("README.md: no fenced bash block in the Homebrew section");
	const privateSection = sliceSection(brewSection, "### Private projects", "README.md Homebrew section");
	const privateBlocks = fencedBlocks(privateSection, "bash");
	if (privateBlocks.length < 1) throw new Error("README.md: no fenced bash block under Homebrew > Private projects");
	return { publicCmds: localize(publicBlocks[0]), privateCmds: localize(privateBlocks[0]) };
}

// assertLlmsAgrees fetches the /llms.txt the local server renders (the same
// template the live instance serves users) and requires its brew command
// blocks to be identical to the README-derived ones. One flow, two documents,
// zero drift.
function assertLlmsAgrees(publicCmds: string, privateCmds: string): void {
	const llms = child_process.execFileSync("curl", ["-fsS", `${SERVER}/llms.txt`], { encoding: "utf8" });
	const brewBlocks = fencedBlocks(llms, "").filter((b) => b.startsWith("brew tap "));
	if (brewBlocks.length !== 2) {
		throw new Error(`llms.txt: expected exactly 2 brew flow blocks (public, private), found ${brewBlocks.length}`);
	}
	const pairs: Array<[string, string, string]> = [
		["public", publicCmds, brewBlocks[0]],
		["private", privateCmds, brewBlocks[1]],
	];
	for (const [name, fromReadme, fromLlms] of pairs) {
		if (fromReadme.trim() !== fromLlms.trim()) {
			core.error(`README.md ${name} flow (host-substituted):\n${fromReadme}`);
			core.error(`llms.txt ${name} flow (as served):\n${fromLlms}`);
			throw new Error(`the documented ${name} Homebrew flow differs between README.md and llms.txt`);
		}
	}
	core.info("llms.txt brew flows match README.md exactly");
}

// assertFlowShape is the tripwire against smuggling CI-only crutches into the
// executed commands: every line must be a plain brew command or a token
// export, and the tap/trust/install skeleton must be present.
function assertFlowShape(name: string, cmds: string): void {
	const lines = cmds
		.split("\n")
		.map((l) => l.trim())
		.filter((l) => l && !l.startsWith("#"));
	for (const l of lines) {
		if (!/^(brew|export) /.test(l)) {
			throw new Error(`${name} doc block contains a non-brew/export command line: ${JSON.stringify(l)}`);
		}
	}
	for (const want of ["brew tap ", "brew trust ", "brew install "]) {
		if (!lines.some((l) => l.startsWith(want))) {
			throw new Error(`${name} doc block lost its ${JSON.stringify(want.trim())} command`);
		}
	}
}

// --- execution ---------------------------------------------------------------

function runFlow(name: string, cmds: string): void {
	assertFlowShape(name, cmds);
	core.startGroup(`Documented ${name} Homebrew flow, executed verbatim (host-substituted)`);
	core.info(cmds);
	core.endGroup();
	const script = path.join(os.tmpdir(), `brew-doc-flow-${name}.sh`);
	fs.writeFileSync(script, cmds + "\n");
	const res = child_process.spawnSync("bash", ["-euo", "pipefail", script], {
		stdio: "inherit",
		// $TOKEN in the documented commands is the reader's buildhost token;
		// here it is the e2e instance's bootstrap token.
		env: { ...process.env, TOKEN: process.env.BUILDHOST_TOKEN ?? "" },
	});
	if (res.status !== 0) throw new Error(`documented ${name} flow failed with exit status ${res.status}`);
}

function legPublic(): void {
	const { publicCmds, privateCmds } = extractDocFlows();
	assertLlmsAgrees(publicCmds, privateCmds);
	runFlow("public", publicCmds);
}

function legPrivate(): void {
	const { privateCmds } = extractDocFlows();
	runFlow("private", privateCmds);

	// `brew update` refreshes taps by fetching each tap's git remote; for the
	// authenticated tap the credentials live in the stored remote URL. Prove
	// that refetch path works instead of running a full (slow, network-bound)
	// brew update.
	const tapDir = child_process.execFileSync("brew", ["--repository", "pazer/build"], { encoding: "utf8" }).trim();
	const remote = child_process
		.execFileSync("git", ["-C", tapDir, "remote", "get-url", "origin"], { encoding: "utf8" })
		.trim();
	if (!remote.includes("/private/tap.git")) {
		throw new Error(`authenticated tap remote is not the private tap URL: ${remote.replaceAll(/:[^:@/]+@/g, ":***@")}`);
	}
	const fetchRes = child_process.spawnSync("git", ["-C", tapDir, "fetch", "origin"], { stdio: "inherit" });
	if (fetchRes.status !== 0) {
		throw new Error("git fetch of the authenticated tap (the brew update mechanism) failed");
	}
	core.info("authenticated tap refetch (brew update mechanism) OK");
}

function legAnonLeak(): void {
	// The anonymous public tap must not know the private project exists --
	// not as a formula file, not as a name anywhere in any object.
	const dir = fs.mkdtempSync(path.join(os.tmpdir(), "anon-tap-"));
	const clone = child_process.spawnSync("git", ["clone", `http://${LOCAL_HOST}/tap.git`, dir], { stdio: "inherit" });
	if (clone.status !== 0) throw new Error("anonymous tap clone failed");

	const formulaDir = path.join(dir, "Formula");
	const formulas = fs.readdirSync(formulaDir);
	if (!formulas.includes("go-toolchain.rb")) {
		throw new Error(`anonymous tap is missing the public formula (found: ${formulas.join(", ")})`);
	}
	if (formulas.includes("myapp.rb")) {
		throw new Error("anonymous tap LEAKS the private formula file myapp.rb");
	}
	const leaks: string[] = [];
	const walk = (p: string): void => {
		for (const entry of fs.readdirSync(p, { withFileTypes: true })) {
			if (entry.name === ".git") continue;
			const full = path.join(p, entry.name);
			if (entry.isDirectory()) {
				walk(full);
			} else if (fs.readFileSync(full, "utf8").includes("myapp")) {
				leaks.push(path.relative(dir, full));
			}
		}
	};
	walk(dir);
	if (leaks.length > 0) {
		throw new Error(`anonymous tap leaks the private project name in: ${leaks.join(", ")}`);
	}
	core.info(`anonymous tap contains ${formulas.length} formula(s), none referencing the private project`);
}

const leg = process.env.LEG ?? "";
switch (leg) {
	case "public":
		legPublic();
		break;
	case "private":
		legPrivate();
		break;
	case "anon-leak":
		legAnonLeak();
		break;
	default:
		throw new Error(`unknown LEG ${JSON.stringify(leg)}: want public, private, or anon-leak`);
}
