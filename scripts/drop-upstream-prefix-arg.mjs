import { readFileSync, writeFileSync } from "node:fs";

const files = process.argv.slice(2);
for (const f of files) {
	const before = readFileSync(f, "utf8");
	const after = before.replace(/(newUpstreamSource\([^\n]*?), \[\]string\{privateOrg\}\)/g, "$1)");
	if (after === before) continue;
	writeFileSync(f, after);
	console.log("rewrote", f);
}
