import * as fs from "node:fs";

// GitHub refuses a `with:` value whose expression text is longer than this, and fails the step before it runs.
const maxExpression = 21000;
// An installed action path is many characters. This bound covers a long owner and ref.
const actionPathAllowance = 200;

type Block = { file: string; line: number; length: number };

function scriptBlocks(file: string): Block[] {
	const lines = fs.readFileSync(file, "utf8").split("\n");
	const blocks: Block[] = [];
	let indent = -1;
	let start = 0;
	let body: string[] = [];

	const flush = (): void => {
		if (indent < 0) return;
		const bodyIndent = Math.min(...body.filter((line) => line.trim() !== "").map((line) => line.search(/\S/)));
		const text = body.map((line) => line.slice(bodyIndent)).join("\n");
		const paths = text.split("${{ github.action_path }}").length - 1;
		blocks.push({ file, line: start, length: text.length + paths * actionPathAllowance });
		indent = -1;
		body = [];
	};

	lines.forEach((line, idx) => {
		const key = /^(\s*)script: \|/.exec(line);
		if (key !== null) {
			flush();
			indent = key[1].length;
			start = idx + 1;
			return;
		}
		if (indent < 0) return;
		if (line.trim() !== "" && line.search(/\S/) <= indent) {
			flush();
			return;
		}
		body.push(line);
	});
	flush();
	return blocks;
}

const files = process.argv.slice(2);
if (files.length === 0) {
	console.error("usage: script-length.ts <yaml>...");
	process.exit(2);
}

const over = files.flatMap(scriptBlocks).filter((block) => block.length > maxExpression);
for (const block of over) {
	console.error(
		`::error file=${block.file},line=${block.line}::an inline script of about ${block.length} characters is over ` +
			`GitHub's ${maxExpression}-character expression limit. Move code into .github/actions/lib/.`,
	);
}
if (over.length > 0) process.exit(1);
console.log(`every inline script is within the expression limit in ${files.length} file(s)`);
