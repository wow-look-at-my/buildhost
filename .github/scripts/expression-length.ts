import * as fs from "node:fs";

// GitHub rejects a whole action or workflow at load when one value's expression passes this.
const Limit = 21000;

type Found = { file: string; line: number; length: number };

// A value that holds `${{ }}` is evaluated as format('<text>', <expr>...). The format string
// doubles each quote and brace, and holds `{n}` where each `${{ expr }}` was.
export function expressionLength(text: string): number {
	const exprs: string[] = [];
	let fmt = "";
	let rest = text;
	for (;;) {
		const at = rest.indexOf("${{");
		if (at < 0) break;
		const end = rest.indexOf("}}", at);
		if (end < 0) break;
		fmt += rest.slice(0, at).replace(/['{}]/g, "$&$&") + `{${exprs.length}}`;
		exprs.push(rest.slice(at + 3, end).trim());
		rest = rest.slice(end + 2);
	}
	if (exprs.length === 0) return 0;
	fmt += rest.replace(/['{}]/g, "$&$&");
	return `format('${fmt}'${exprs.map((e) => `, ${e}`).join("")})`.length;
}

function scriptBlocks(file: string): Found[] {
	const lines = fs.readFileSync(file, "utf8").split("\n");
	const out: Found[] = [];
	for (let i = 0; i < lines.length; i++) {
		const key = /^(\s*)[A-Za-z_-]+: \|[-+]?\s*$/.exec(lines[i]);
		if (key === null) continue;
		const indent = key[1].length;
		const body: string[] = [];
		let j = i + 1;
		let strip = -1;
		for (; j < lines.length; j++) {
			const line = lines[j];
			if (line.trim() !== "" && line.search(/\S/) <= indent) break;
			if (strip < 0 && line.trim() !== "") strip = line.search(/\S/);
			body.push(line.slice(Math.max(strip, 0)));
		}
		const length = expressionLength(body.join("\n"));
		if (length > 0) out.push({ file, line: i + 1, length });
		i = j - 1;
	}
	return out;
}

const files = process.argv.slice(2);
if (files.length === 0) {
	console.error("usage: expression-length.ts <yaml>...");
	process.exit(2);
}

const all = files.flatMap(scriptBlocks);
const over = all.filter((f) => f.length > Limit);
for (const f of over) {
	console.error(
		`::error file=${f.file},line=${f.line}::this block's expression is ${f.length} characters, over GitHub's ${Limit} limit. ` +
			`GitHub refuses the whole action at load. Move code into a module under .github/actions/lib/.`,
	);
}
if (over.length > 0) process.exit(1);
const top = all.reduce((m, f) => (f.length > m.length ? f : m), { file: "", line: 0, length: 0 });
console.log(`every expression is within GitHub's ${Limit} limit (largest: ${top.length} at ${top.file}:${top.line})`);
