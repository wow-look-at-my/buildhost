// GitHub refuses a step whose expression passes many characters, and only when the step runs. A script block that holds `${{` is one
// expression, so each is measured here.

const assert = require('node:assert');
const fs = require('node:fs') as typeof import('node:fs');
const path = require('node:path') as typeof import('node:path');

const maxExpression = 21000;
const root = process.env.GITHUB_WORKSPACE ?? process.cwd();

function yamlFiles(): string[] {
	const out: string[] = [];
	const actions = path.join(root, '.github/actions');
	for (const name of fs.readdirSync(actions)) {
		const file = path.join(actions, name, 'action.yml');
		if (fs.existsSync(file)) out.push(file);
	}
	const workflows = path.join(root, '.github/workflows');
	for (const name of fs.readdirSync(workflows)) {
		if (name.endsWith('.yml') || name.endsWith('.yaml')) out.push(path.join(workflows, name));
	}
	return out;
}

// Each block scalar of a key, as YAML hands it to the runner: the common indent removed.
function blocks(text: string): { line: number; body: string }[] {
	const lines = text.split('\n');
	const out: { line: number; body: string }[] = [];
	for (let i = 0; i < lines.length; i++) {
		const m = lines[i].match(/^(\s*)(?:- )?[A-Za-z_-]+: [|>][-+]?\s*$/);
		if (!m) continue;
		const keyIndent = m[1].length;
		const body: string[] = [];
		let indent = -1;
		let j = i + 1;
		for (; j < lines.length; j++) {
			const l = lines[j];
			if (l.trim() === '') { body.push(''); continue; }
			const lead = l.length - l.trimStart().length;
			if (lead <= keyIndent) break;
			if (indent < 0) indent = lead;
			body.push(l.slice(Math.min(indent, lead)));
		}
		out.push({ line: i + 1, body: body.join('\n').trimEnd() });
		i = j - 1;
	}
	return out;
}

// The runner turns a block that holds `${{` into format('...', args). The
// literal text doubles each brace and each quote, and each expression becomes
// a `{N}` placeholder plus an argument.
function formatLength(body: string): number {
	let length = "format('".length + "')".length;
	let n = 0;
	let rest = body;
	for (;;) {
		const open = rest.indexOf('${{');
		const literal = open < 0 ? rest : rest.slice(0, open);
		for (const c of literal) length += c === '{' || c === '}' || c === "'" ? 2 : 1;
		if (open < 0) break;
		const close = rest.indexOf('}}', open);
		const expr = rest.slice(open + 3, close).trim();
		length += `{${n}}`.length + `, ${expr}`.length;
		n++;
		rest = rest.slice(close + 2);
	}
	return length;
}

assert.strictEqual(formatLength("a{'${{ x }}"), "format('a{{''{0}', x)".length);

let measured = 0;
for (const file of yamlFiles()) {
	for (const b of blocks(fs.readFileSync(file, 'utf8'))) {
		if (!b.body.includes('${{')) continue;
		measured++;
		const length = formatLength(b.body);
		assert.ok(
			length <= maxExpression,
			`${path.relative(root, file)}:${b.line}: the block holds \${{ }}, so it is one expression of ${length} characters, over GitHub's ${maxExpression}. Move code into .github/actions/lib/.`,
		);
	}
}
assert.ok(measured > 0, 'the scan found no block that holds an expression');
console.log('expression-length: all checks passed');
