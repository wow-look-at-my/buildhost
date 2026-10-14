// GitHub refuses a whole workflow when one value holding an expression is
// longer than many characters ("Exceeded max expression length 21000").

import { strict as assert } from "node:assert";
import * as fs from "node:fs";
import * as path from "node:path";

const limit = 21000;
const root = path.join(import.meta.dirname, "..", "..");
const actionsDir = path.join(root, ".github", "actions");

// Each block scalar ("key: |" and the deeper-indented lines after it) as GitHub reads it: dedented, lines joined with newlines.
function blockScalars(text: string): { key: string; line: number; value: string }[] {
	const lines = text.split("\n");
	const out: { key: string; line: number; value: string }[] = [];
	for (let i = 0; i < lines.length; i++) {
		const head = lines[i].match(/^(\s*)(?:- )?([\w-]+): [|>][-+]?\s*$/);
		if (!head) continue;
		const keyIndent = head[1].length;
		const body: string[] = [];
		let j = i + 1;
		for (; j < lines.length; j++) {
			const l = lines[j];
			if (l.trim() !== "" && l.length - l.trimStart().length <= keyIndent) break;
			body.push(l);
		}
		const indent = Math.min(...body.filter((l) => l.trim() !== "").map((l) => l.length - l.trimStart().length));
		out.push({ key: head[2], line: i + 1, value: body.map((l) => l.slice(indent)).join("\n") });
		i = j - 1;
	}
	return out;
}

// GitHub turns a value with embedded expressions into one format() call, which is what the limit counts. Each expression becomes {N} and an argument, and every other brace and single quote is doubled.
export function formatLength(value: string): number {
	const args: string[] = [];
	const literal = value.split(/(\$\{\{[\s\S]*?\}\})/).map((part, i) => {
		if (i % 2 === 1) {
			args.push(part.slice(3, -2).trim());
			return `{${args.length - 1}}`;
		}
		return part.replace(/[{}']/g, (c) => c + c);
	}).join("");
	return `format('${literal}'${args.map((a) => `, ${a}`).join("")})`.length;
}

let checked = 0;
for (const dir of fs.readdirSync(actionsDir)) {
	const file = path.join(actionsDir, dir, "action.yml");
	if (!fs.existsSync(file)) continue;
	for (const b of blockScalars(fs.readFileSync(file, "utf8"))) {
		if (!b.value.includes("${{")) continue;
		checked++;
		const n = formatLength(b.value);
		assert.ok(n < limit, `${path.relative(root, file)}:${b.line} ${b.key}: its expression is ${n} characters, past GitHub's limit of ${limit}; move code into .github/actions/lib`);
	}
}
assert.ok(checked > 0, "found no block scalar holding an expression, so this test checked nothing");
console.log(`expression-length: all checks passed (${checked} blocks)`);
