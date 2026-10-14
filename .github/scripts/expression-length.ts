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
