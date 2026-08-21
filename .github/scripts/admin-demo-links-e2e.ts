// Every in-app link on the PREVIEW dashboard must reach a page that renders.
//
// The preview publishes the admin SPA with no backend, so every page draws from
// the built-in demo dataset. That dataset linked to pages it had no data for:
// clicking a release on the project page threw inside the renderer, left the
// previous page on screen, and read as a link that does nothing.
//
// A unit test cannot see this. The bug is a link target with no fixture behind
// it, and only walking the links the app itself renders finds one.
//
// The preview also serves the app under a path prefix, which is what turns demo
// mode on, so this serves it the same way.

const ROOT = "internal/admin/static";
const PREFIX = "/buildhost/@preview/";
const PORT = 18321;
const BASE = `http://127.0.0.1:${PORT}${PREFIX}`;

// http is not one of the modules the action injects, unlike fs and path.
const http = require("node:http");
const { chromium } = require("playwright-core");

const types: Record<string, string> = {
	".html": "text/html",
	".js": "text/javascript",
	".css": "text/css",
};

const root = path.resolve(ROOT);
const server = http.createServer((req: any, res: any) => {
	let rel = String(req.url).split("?")[0].slice(PREFIX.length - 1) || "/";
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

// The same sequence sites-cors-e2e uses, in the same order: an explicit
// binary outside a runner, then the runner's own Chrome, then a staged
// download. A browser is REQUIRED -- no browser fails this check rather than
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

		const links: string[] = await page.$$eval("#content a[href^='#/'], .sidebar a[href^='#/']",
			// This callback runs in the PAGE, where DOM types exist. The action
			// compiles this file without the DOM lib, so it cannot name them.
			(as: any[]) => as.map((a) => a.getAttribute("href")));
		for (const l of links) if (!seen.has(l)) queue.push(l);
	}

	await browser.close();
	server.close();
	// The browser's keep-alive sockets outlive close(); without this the
	// process sits with a live event loop after the crawl is done.
	server.closeAllConnections();

	if (errors.length > 0) {
		throw new Error(`the preview dashboard raised errors:\n${errors.join("\n")}`);
	}
	core.info(`OK: ${visited} preview pages, every in-app link rendered`);
};

// A hang here is a job that runs until the workflow's own limit, with no
// output to read. Fail it while the log still says what it was doing.
const DEADLINE_MS = 5 * 60 * 1000;
let timer: any;
const deadline = new Promise((_resolve, reject) => {
	timer = setTimeout(() => reject(new Error(`the crawl did not finish within ${DEADLINE_MS / 1000}s`)), DEADLINE_MS);
});

try {
	await Promise.race([main(), deadline]);
} finally {
	clearTimeout(timer);
}
