// Asserts that a hosted site stays loadable CROSS-ORIGIN -- through its
// redirects, in a real browser.
//
// This check exists because that stopped being true and every cross-origin
// consumer broke at once. The legacy /{project}/branch/{branch}/ URL -- the one
// every deployed client, README and published preview link still says -- became
// a 302 to the canonical @{branch} form, and the redirect went out WITHOUT the
// site CORS headers. A browser re-checks Access-Control-Allow-Origin on every
// hop, so the load died at the 302 and never reached the 200 that had it: a
// dashboard importing an ES module from sites.pazer.build spent every page view
// retrying "error loading dynamically imported module".
//
// Nothing caught it. Unit tests covered the handlers that serve BYTES, and
// `curl` -- which follows redirects without enforcing CORS at all, and does not
// care about MIME types or CSP either -- reported a clean 200 for the same URL
// that was failing in production. Only a real browser doing a real cross-origin
// import can see this class of defect, which is exactly what runs below.
//
// Two layers, deliberately:
//   1. every hop of every redirect shape a site URL can take must carry the
//      CORS header (fast, precise, names the exact broken hop), and
//   2. one real headless browser must actually import a module through the
//      redirect chain (honest end-to-end; also covers MIME type and CSP, which
//      layer 1 cannot see).

const BIN = process.env.BUILDHOST_BIN || "build/buildhost_linux_amd64";
const PORT = 18080; // the sites origin
const CONSUMER_PORT = 18081; // a DIFFERENT port == a different origin, which is all CORS needs
const HOST = `sites.localhost:${PORT}`;
const SITES = `http://${HOST}`;
const CONSUMER_ORIGIN = `http://localhost:${CONSUMER_PORT}`;
const PROJECT = "cors-e2e";
// The shape that actually broke: a PRIVATE project serving one public site
// branch (X-Public-Site), which is how every PR preview and published library
// site under a private repo is served. A public project would not exercise the
// public-read bypass at all, so a redirect that lost either the CORS header or
// the anonymous bypass would go unnoticed.
const PRIVATE_PROJECT = "cors-e2e-private";
const MARKER = "site-module-loaded";

// The router dispatches on the Host's first label, so the sites service has to
// be addressed by name. GitHub runners resolve *.localhost to loopback (the
// same assumption image-strips-e2e.ts makes for static.localhost); say so
// outright rather than letting an unrelated-looking connection error surface.
{
	const probe = child_process.spawnSync("getent", ["hosts", "sites.localhost"], { encoding: "utf8" });
	if (probe.status !== 0) {
		throw new Error(
			"sites.localhost does not resolve on this machine, so the sites service cannot be addressed by name.\n" +
				'Add it: echo "127.0.0.1 sites.localhost" >> /etc/hosts',
		);
	}
}

const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "buildhost-cors-e2e-"));
const binAbs = path.resolve(BIN);
const serverEnv = {
	...process.env,
	BUILDHOST_DATA_DIR: dataDir,
	BUILDHOST_DB_PATH: path.join(dataDir, "buildhost.db"),
	BUILDHOST_LISTEN_ADDR: `127.0.0.1:${PORT}`,
	BUILDHOST_ADMIN_LISTEN_ADDR: `127.0.0.1:19090`,
};

function bootstrapToken(): string {
	const res = child_process.spawnSync(binAbs, ["bootstrap", "--name", "cors-e2e"], {
		encoding: "utf8",
		env: serverEnv,
	});
	if (res.status !== 0) throw new Error(`bootstrap failed (${res.status}): ${res.stderr || res.stdout}`);
	const token = res.stdout.trim().split("\n").pop()!.trim();
	if (!token) throw new Error("no token from bootstrap");
	return token;
}

function tarGz(files: Record<string, string>): Buffer {
	const dir = fs.mkdtempSync(path.join(os.tmpdir(), "site-"));
	for (const [name, body] of Object.entries(files)) {
		const p = path.join(dir, name);
		fs.mkdirSync(path.dirname(p), { recursive: true });
		fs.writeFileSync(p, body);
	}
	const out = path.join(dir, "site.tar.gz");
	const res = child_process.spawnSync("tar", ["czf", out, "-C", dir, ...Object.keys(files)], { encoding: "utf8" });
	if (res.status !== 0) throw new Error(`tar failed: ${res.stderr}`);
	return fs.readFileSync(out);
}

// A manual, NON-following fetch so each hop can be inspected on its own. This is
// the whole point: the bug was invisible to anything that auto-followed.
async function hop(url: string): Promise<{ status: number; acao: string | null; location: string | null; type: string | null }> {
	// sites.localhost resolves to loopback on the runner, so the service label
	// the router dispatches on rides the URL -- no Host header override, which
	// undici may refuse to set.
	const res = await fetch(url, { redirect: "manual", headers: { Origin: CONSUMER_ORIGIN } });
	// Drain so the connection can be reused.
	await res.arrayBuffer().catch(() => undefined);
	return {
		status: res.status,
		acao: res.headers.get("access-control-allow-origin"),
		location: res.headers.get("location"),
		type: res.headers.get("content-type"),
	};
}

const failures: string[] = [];

// Walks a URL's whole redirect chain and requires the CORS header on EVERY hop,
// redirects included -- the invariant a browser actually enforces.
async function assertChainCORS(label: string, startPath: string, wantRedirect: boolean) {
	let url = SITES + startPath;
	let hops = 0;
	let sawRedirect = false;
	for (;;) {
		if (++hops > 6) {
			failures.push(`${label}: redirect loop (>6 hops) starting at ${startPath}`);
			return;
		}
		const r = await hop(url);
		if (r.acao !== "*") {
			failures.push(
				`${label}: ${r.status} ${url} has Access-Control-Allow-Origin=${JSON.stringify(r.acao)}, want "*"` +
					(r.location ? ` (this hop redirects to ${r.location}; a browser fails the whole load HERE)` : ""),
			);
			return;
		}
		if (r.status >= 300 && r.status < 400 && r.location) {
			sawRedirect = true;
			url = new URL(r.location, url).toString();
			continue;
		}
		if (r.status !== 200) {
			failures.push(`${label}: chain ended at ${r.status} ${url}, want 200`);
			return;
		}
		break;
	}
	if (wantRedirect && !sawRedirect) {
		// The shape being guarded no longer redirects, so this case silently
		// stopped covering anything. That is a finding, not a pass.
		failures.push(`${label}: expected a redirect in the chain from ${startPath}, got none -- this case no longer guards what it claims`);
	}
	core.info(`  ok  ${label} (${hops} hop(s), CORS on each)`);
}

// ---------------------------------------------------------------------------

const server = child_process.spawn(binAbs, ["serve"], { env: serverEnv, stdio: ["ignore", "pipe", "pipe"] });
let serverLog = "";
server.stdout.on("data", (d) => (serverLog += d));
server.stderr.on("data", (d) => (serverLog += d));
server.on("exit", (code) => {
	if (code !== 0 && code !== null) core.error(`buildhost serve exited ${code}:\n${serverLog}`);
});

let consumer: any;
let browser: any;
try {
	for (let i = 0; ; i++) {
		try {
			const r = await fetch(`http://127.0.0.1:${PORT}/healthz`);
			if (r.ok) break;
		} catch {
			/* not up yet */
		}
		if (i > 100) throw new Error(`buildhost never became healthy:\n${serverLog}`);
		await new Promise((r) => setTimeout(r, 100));
	}

	const token = bootstrapToken();
	core.setSecret(token);
	const auth = { Authorization: `Bearer ${token}` };

	// The create-project field is `is_private` (bool). An unknown field is
	// IGNORED, so a wrong name silently yields a PUBLIC project -- which is how
	// the private-project case below spent its first life asserting nothing. Every
	// creation here therefore reads the visibility back and fails on a mismatch.
	async function createProject(name: string, isPrivate: boolean) {
		const res = await fetch(`http://localhost:${PORT}/api/v1/projects`, {
			method: "POST",
			headers: { ...auth, "Content-Type": "application/json" },
			body: JSON.stringify({ name, is_private: isPrivate }),
		});
		if (!res.ok) throw new Error(`create project ${name}: ${res.status} ${await res.text()}`);
		const got = (await res.json()) as { is_private?: boolean };
		if (got.is_private !== isPrivate) {
			throw new Error(
				`project ${name} came back is_private=${got.is_private}, wanted ${isPrivate}. ` +
					`The visibility field was not applied, so this case would silently test the wrong thing.`,
			);
		}
	}

	await createProject(PROJECT, false);

	// Two branches so `library` is NOT the default -- the production shape,
	// where the legacy URL redirects to the @branch form rather than collapsing
	// straight to the bare project path.
	for (const [branch, files] of [
		["master", { "index.html": "<h1>default</h1>" }],
		["library", { "index.html": "<h1>library</h1>", "ui/mod.js": `export const MARKER = ${JSON.stringify(MARKER)};\n` }],
	] as const) {
		const up = await fetch(`${SITES}/${PROJECT}/branch/${branch}`, {
			method: "PUT",
			headers: auth,
			body: new Uint8Array(tarGz(files as Record<string, string>)),
		});
		if (up.status !== 201) throw new Error(`upload ${branch}: ${up.status} ${await up.text()}`);
	}

	// A private project whose `library` branch is published public -- the
	// production shape. Its `master` branch is published WITHOUT the flag, and
	// asserted below to be refused anonymously: without that, "the public-read
	// bypass survives the redirect" would be an untested claim, since a project
	// that was accidentally public serves every branch anyway.
	await createProject(PRIVATE_PROJECT, true);
	const upGated = await fetch(`${SITES}/${PRIVATE_PROJECT}/branch/master`, {
		method: "PUT",
		headers: auth,
		body: new Uint8Array(tarGz({ "index.html": "<h1>gated</h1>" })),
	});
	if (upGated.status !== 201) throw new Error(`upload gated branch: ${upGated.status} ${await upGated.text()}`);
	{
		const gated = await fetch(`${SITES}/${PRIVATE_PROJECT}/@master/index.html`, {
			redirect: "manual",
			headers: { Origin: CONSUMER_ORIGIN },
		});
		await gated.arrayBuffer().catch(() => undefined);
		if (gated.status !== 401 && gated.status !== 404) {
			failures.push(
				`a NON-public branch of a private project answered ${gated.status} anonymously (want 401/404). ` +
					`Either the project is not actually private or the gate is open -- in both cases the ` +
					`public-branch case below proves nothing.`,
			);
		}
	}
	const upPriv = await fetch(`${SITES}/${PRIVATE_PROJECT}/branch/library`, {
		method: "PUT",
		headers: { ...auth, "X-Public-Site": "true" },
		body: new Uint8Array(tarGz({ "ui/mod.js": `export const MARKER = ${JSON.stringify(MARKER)};\n` })),
	});
	if (upPriv.status !== 201) throw new Error(`upload private site: ${upPriv.status} ${await upPriv.text()}`);

	// --- Layer 1: every hop of every redirect shape -------------------------
	core.info("CORS headers on every redirect hop:");
	// The exact URL that broke production: legacy spelling of a NON-default
	// branch, redirecting to the canonical @branch form.
	await assertChainCORS("legacy /branch/ (non-default branch)", `/${PROJECT}/branch/library/ui/mod.js`, true);
	// Legacy spelling of the DEFAULT branch: collapses to the bare project path.
	await assertChainCORS("legacy /branch/ (default branch)", `/${PROJECT}/branch/master/index.html`, true);
	// @<default-branch> is redundant and collapses to the bare path.
	await assertChainCORS("@default collapse", `/${PROJECT}/@master/index.html`, true);
	// Branch root without a trailing slash.
	await assertChainCORS("branch root, no trailing slash", `/${PROJECT}/@library`, true);
	// Apex project root without a trailing slash.
	await assertChainCORS("apex root, no trailing slash", `/${PROJECT}`, true);
	// Plain serves, which were never broken -- so a regression here is caught too.
	await assertChainCORS("canonical @branch file", `/${PROJECT}/@library/ui/mod.js`, false);
	await assertChainCORS("bare apex file", `/${PROJECT}/index.html`, false);
	// The production shape: private project, public site branch, ANONYMOUS (no
	// token is ever sent above -- assertChainCORS sends only an Origin). This
	// also pins that the public-read bypass survives the redirect, since a 401
	// mid-chain would fail here just as loudly as a missing header.
	await assertChainCORS("private project, public branch (legacy /branch/)", `/${PRIVATE_PROJECT}/branch/library/ui/mod.js`, true);

	// --- Layer 2: a real browser, a real cross-origin import ----------------
	// Everything above is a header assertion. This is the actual user-visible
	// claim, and it covers what header assertions cannot: MIME type, CSP, and
	// module specifier resolution against the post-redirect URL.
	// The private-project/public-branch URL, because that is the production
	// shape -- and the browser sends no credentials, so it also proves the
	// anonymous public-read bypass holds across the redirect.
	const MODULE_URL = `${SITES}/${PRIVATE_PROJECT}/branch/library/ui/mod.js`;
	const page_html = `<!doctype html><meta charset=utf-8><title>consumer</title><script type="module">
  const done = (t) => { window.__result = t; };
  try { const m = await import(${JSON.stringify(MODULE_URL)}); done('OK:' + m.MARKER); }
  catch (e) { done('ERR:' + (e && e.message ? e.message : String(e))); }
</script>`;

	const http = require("node:http");
	consumer = http.createServer((_req: any, res: any) => {
		res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
		res.end(page_html);
	});
	await new Promise<void>((r) => consumer.listen(CONSUMER_PORT, "127.0.0.1", () => r()));

	// A browser is REQUIRED. Skipping when one is missing is how a check that
	// cannot fail ends up reporting green forever -- so an unavailable browser
	// fails this job rather than quietly reducing it to the header assertions.
	const { chromium } = require("playwright-core");
	const launchAttempts: Array<[string, Record<string, unknown>]> = [
		// An explicit binary wins when set (how this runs outside a GH runner).
		...(process.env.PLAYWRIGHT_CHROME_PATH
			? ([["executablePath", { executablePath: process.env.PLAYWRIGHT_CHROME_PATH, args: ["--no-sandbox"] }]] as Array<
					[string, Record<string, unknown>]
				>)
			: []),
		["channel:chrome", { channel: "chrome", args: ["--no-sandbox"] }],
		["channel:chromium", { channel: "chromium", args: ["--no-sandbox"] }],
		["bundled", { args: ["--no-sandbox"] }],
	];
	const launchErrors: string[] = [];
	for (const [name, opts] of launchAttempts) {
		try {
			browser = await chromium.launch(opts);
			core.info(`browser: launched via ${name}`);
			break;
		} catch (e: any) {
			launchErrors.push(`${name}: ${e?.message ?? e}`);
		}
	}
	if (!browser) throw new Error(`no browser could be launched (this job REQUIRES one):\n${launchErrors.join("\n")}`);

	const page = await (await browser.newContext()).newPage();
	const trace: string[] = [];
	page.on("console", (m: any) => trace.push(`[console.${m.type()}] ${m.text()}`));
	page.on("pageerror", (e: any) => trace.push(`[pageerror] ${e.message}`));
	page.on("requestfailed", (r: any) => trace.push(`[requestfailed] ${r.url()} :: ${r.failure()?.errorText}`));
	page.on("response", (r: any) => trace.push(`[response] ${r.status()} ${r.url()}`));

	await page.goto(`${CONSUMER_ORIGIN}/`, { waitUntil: "load" });
	await page.waitForFunction("typeof window.__result === 'string'", null, { timeout: 30000 }).catch(() => undefined);
	const result = await page.evaluate("window.__result");

	core.info("browser trace:\n" + trace.join("\n"));
	if (result !== `OK:${MARKER}`) {
		failures.push(
			`real browser could not import ${MODULE_URL} cross-origin from ${CONSUMER_ORIGIN}: ${result}\n` +
				trace.map((l) => "    " + l).join("\n"),
		);
	} else {
		core.info(`  ok  real browser imported the module cross-origin through the redirect`);
	}
} finally {
	if (browser) await browser.close().catch(() => undefined);
	if (consumer) consumer.close();
	server.kill("SIGTERM");
}

if (failures.length) {
	core.setFailed(
		"A hosted site is not loadable cross-origin. A browser enforces CORS on EVERY redirect hop, so\n" +
			"a redirect missing the header fails the whole load even when the final 200 carries it.\n" +
			"Site responses get their headers from sites.setSiteSecurityHeaders -- call it BEFORE writing\n" +
			"any redirect, not after.\n\n" +
			failures.map((f) => "  - " + f).join("\n"),
	);
} else {
	core.info("hosted sites are loadable cross-origin, redirects included");
}
