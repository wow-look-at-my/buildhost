// Every in-app link on the PREVIEW dashboard must reach a page that renders.
// The LIVE layout, with a failing API, must show the error page and never a fixture.

const ROOT = "internal/admin/static";
const PREFIX = "/buildhost/@preview/";
const PORT = 18321;
const BASE = `http://127.0.0.1:${PORT}${PREFIX}`;
const LIVE = `http://127.0.0.1:${PORT}/`;

const http = require("node:http");
const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright-core");

// Progress lines, not assertions: what the crawl reached, in order.
const core = { info: (m: string) => console.log(m) };

const types: Record<string, string> = {
	".html": "text/html",
	".js": "text/javascript",
	".css": "text/css",
};

const root = path.resolve(ROOT);

const serveLive = (url: string, res: any) => {
	if (url.startsWith("/api/")) {
		res.writeHead(502);
		res.end("bad gateway");
		return;
	}
	const rel = url === "/" ? "/index.html" : url;
	const file = path.join(root, rel);
	const known = file.startsWith(root) && rel !== "/demo.json" && fs.existsSync(file);
	const served = known ? file : path.join(root, "index.html");
	res.writeHead(200, { "content-type": types[path.extname(served)] || "application/octet-stream" });
	res.end(fs.readFileSync(served));
};

const server = http.createServer((req: any, res: any) => {
	const url = String(req.url).split("?")[0];
	if (!url.startsWith(PREFIX)) {
		serveLive(url, res);
		return;
	}
	let rel = url.slice(PREFIX.length - 1) || "/";
	if (rel === "/") rel = "/index.html";
	const file = path.join(root, rel);
	if (!file.startsWith(root) || !fs.existsSync(file)) {
		res.writeHead(404);
		res.end("not found");
		return;
	}
	res.writeHead(200, { "content-type": types[path.extname(file)] || "application/octet-stream" });
	res.end(fs.readFileSync(file));
});

// This is the same sequence sites-cors.test.ts uses, in the same order. It
// tries an explicit binary outside a runner, then the runner's own Chrome,
// then a staged download. A browser is REQUIRED -- no browser fails this check rather than
// reducing it to something that cannot go red.
const launch = async () => {
	const attempts: Array<[string, Record<string, unknown>]> = [
		...(process.env.PLAYWRIGHT_CHROME_PATH
			? ([["executablePath", { executablePath: process.env.PLAYWRIGHT_CHROME_PATH, args: ["--no-sandbox"] }]] as Array<
					[string, Record<string, unknown>]
				>)
			: []),
		["channel:chrome", { channel: "chrome", args: ["--no-sandbox"] }],
		["channel:chromium", { channel: "chromium", args: ["--no-sandbox"] }],
		["bundled", { args: ["--no-sandbox"] }],
	];
	const errors: string[] = [];
	for (const [name, opts] of attempts) {
		try {
			const browser = await chromium.launch(opts);
			core.info(`browser: launched via ${name}`);
			return browser;
		} catch (e: any) {
			errors.push(`${name}: ${e?.message ?? e}`);
		}
	}
	throw new Error(`no browser could be launched (this check REQUIRES one):\n${errors.join("\n")}`);
};

const main = async () => {
	await new Promise<void>((r) => server.listen(PORT, r));
	const browser = await launch();
	const page = await browser.newPage();

	const errors: string[] = [];
	page.on("pageerror", (e: Error) => errors.push(`${page.url()}: ${e.message}`));
	page.on("console", (m: any) => {
		if (m.type() === "error") errors.push(`${page.url()}: console ${m.text()}`);
	});

	const seen = new Set<string>();
	const queue = ["#/"];
	let visited = 0;

	while (queue.length > 0) {
		const hash = queue.shift()!;
		if (seen.has(hash)) continue;
		seen.add(hash);

		await page.goto(BASE + hash);
		// The heading is what every page writes LAST, so its presence means the
		// renderer ran to the end rather than throwing halfway.
		await page.waitForSelector("#content h1", { timeout: 10000 }).catch(() => {
			throw new Error(`${hash} rendered no heading -- the page did not draw`);
		});
		const heading = (await page.textContent("#content h1"))!.trim();
		if (/^Error/.test(heading)) {
			const detail = (await page.textContent("#content pre")) || "";
			throw new Error(`${hash} rendered the error page: ${detail.trim()}`);
		}
		core.info(`${hash} -> ${heading}`);
		visited++;

		// A link whose href is a bare "#" carries no target.
		const placeholders: string[] = await page.$$eval("#content a[href='#']",
			(as: any[]) => as.map((a) => a.textContent.trim()));
		if (placeholders.length > 0) {
			throw new Error(`${hash} renders ${placeholders.length} link(s) with href="#": ${placeholders.join(", ")}`);
		}

		const links: string[] = await page.$$eval("#content a[href^='#/'], .sidebar a[href^='#/']",
			// This callback runs in the PAGE, where DOM types exist.
			(as: any[]) => as.map((a) => a.getAttribute("href")));
		for (const l of links) if (!seen.has(l)) queue.push(l);
	}

	// A fixture string on the live dashboard reads as real server state.
	const live = await browser.newPage();
	const fixtures = ["v0.0.0-demo", "myapp", "cli-tool", "builds.example.com"];
	const expectLiveError = async (where: string) => {
		await live.waitForSelector("#content h1", { timeout: 10000 }).catch(() => {
			throw new Error(`live ${where} rendered no heading`);
		});
		const heading = (await live.textContent("#content h1"))!.trim();
		const body = (await live.textContent("body")) || "";
		const leaked = fixtures.filter((f) => body.includes(f));
		if (leaked.length > 0) {
			throw new Error(`live ${where} with a failing API drew demo data (${leaked.join(", ")}) under "${heading}"`);
		}
		if (heading !== "Error") {
			throw new Error(`live ${where} with a failing API drew "${heading}" instead of the error page`);
		}
		core.info(`live ${where} -> ${heading}`);
	};
	await live.goto(LIVE + "#/");
	await expectLiveError("#/");
	// demo mode once stuck for the rest of the tab after one failed request.
	// These callbacks run in the PAGE; this file type-checks without DOM types.
	await live.evaluate(() => {
		const w = globalThis as any;
		w.document.getElementById("content").innerHTML = "";
		w.location.hash = "#/projects";
	});
	await live.waitForFunction(() => (globalThis as any).document.querySelector("#content h1") !== null);
	await expectLiveError("#/projects");
	// The admin server serves the SPA at every path, not only at "/".
	await live.goto(LIVE + "index.html#/");
	await expectLiveError("/index.html#/");
	core.info("OK: the live dashboard never drew demo data");

	await browser.close();
	server.close();
	// The browser's keep-alive sockets outlive close().
	server.closeAllConnections();

	if (errors.length > 0) {
		throw new Error(`the preview dashboard raised errors:\n${errors.join("\n")}`);
	}
	core.info(`OK: ${visited} preview pages, every in-app link rendered`);
};

// A hang here is a job that runs until the workflow's own limit, with no output to read.
const DEADLINE_MS = 5 * 60 * 1000;
let timer: any;
const deadline = new Promise((_resolve, reject) => {
	timer = setTimeout(() => reject(new Error(`the crawl did not finish within ${DEADLINE_MS / 1000}s`)), DEADLINE_MS);
});

Promise.race([main(), deadline])
	.finally(() => clearTimeout(timer))
	.catch((err: unknown) => {
		console.error(err);
		process.exit(1);
	});
