import * as fs from "node:fs";

// GitHub refuses the whole workflow when a string that holds an expression is longer than this.
export const maxExpressionLength = 21000;

type Block = { file: string; line: number; length: number };

// The length of the format() call GitHub makes of a string that holds an expression. The literal text doubles each brace and quote.
export function formatLength(value: string): number {
	const parts = value.split(/\$\{\{(.*?)\}\}/s);
	let length = "format('')".length;
	for (let k = 0; k < parts.length; k++) {
		if (k % 2 === 1) {
			length += `{${(k - 1) / 2}}`.length + `, ${parts[k].trim()}`.length;
			continue;
		}
		length += parts[k].length + (parts[k].match(/[{}']/g) ?? []).length;
	}
	return length;
}

// Every block scalar that holds an expression, with the length of the expression GitHub makes of it.
export function expressionBlocks(file: string, text: string): Block[] {
	const lines = text.split("\n");
	const out: Block[] = [];
	for (let i = 0; i < lines.length; i++) {
		const key = /^(\s*)(?:- )?[\w.-]+: [|>][-+]?\s*$/.exec(lines[i]);
		if (key === null) continue;
		const keyIndent = key[1].length;
		const body: string[] = [];
		let j = i + 1;
		for (; j < lines.length; j++) {
			if (lines[j].trim() !== "" && lines[j].search(/\S/) <= keyIndent) break;
			body.push(lines[j]);
		}
		const indent = Math.min(...body.filter((l) => l.trim() !== "").map((l) => l.search(/\S/)));
		const value = body.map((l) => l.slice(indent)).join("\n");
		if (value.includes("${{")) out.push({ file, line: i + 1, length: formatLength(value) });
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

if (process.argv[1] && process.argv[1].endsWith("expression-length.ts")) {
	const files = process.argv.slice(2);
	if (files.length === 0) {
		console.error("usage: expression-length.ts <yaml>...");
		process.exit(2);
	}
	const over = files.flatMap((f) => expressionBlocks(f, fs.readFileSync(f, "utf8"))).filter((b) => b.length > maxExpressionLength);
	for (const b of over) {
		console.error(
			`::error file=${b.file},line=${b.line}::this string holds an expression and is ${b.length} characters. ` +
				`GitHub refuses a string over ${maxExpressionLength} characters that holds one, and fails every workflow that uses the action. ` +
				`Move code into .github/actions/lib/.`,
		);
	}
	if (over.length > 0) process.exit(1);
	console.log(`every expression string is at most ${maxExpressionLength} characters in ${files.length} file(s)`);
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
