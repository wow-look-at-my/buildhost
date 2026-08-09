// The typescript action refuses to run any INLINE `script:` carrying two or
// more consecutive comment-only `//` lines, with no opt-out. Most of these
// actions only execute on some triggers, so without this a violation ships and
// surfaces as a broken publish in whichever consumer repo runs it next.
//
// Scans the same thing the action does, and only that: a shell `run:` block and
// a checked-in `file:` script are both exempt.
//
// Usage: node .github/scripts/no-stacked-comments.ts <yaml>...
import * as fs from "node:fs";

type Run = { file: string; start: number; end: number };

function stackedRuns(file: string): Run[] {
	const lines = fs.readFileSync(file, "utf8").split("\n");
	const runs: Run[] = [];
	let scriptIndent = -1; // indentation of the `script: |` key, -1 when outside one
	let start = -1;

	const flush = (end: number): void => {
		if (start >= 0 && end - start >= 2) runs.push({ file, start: start + 1, end });
		start = -1;
	};

	lines.forEach((line, i) => {
		const key = /^(\s*)script: \|/.exec(line);
		if (key !== null) {
			flush(i);
			scriptIndent = key[1].length;
			return;
		}
		if (scriptIndent < 0) return;
		if (line.trim() !== "" && line.search(/\S/) <= scriptIndent) {
			flush(i);
			scriptIndent = -1;
			return;
		}
		if (/^\s*\/\//.test(line)) {
			if (start < 0) start = i;
		} else {
			flush(i);
		}
	});
	flush(lines.length);
	return runs;
}

const files = process.argv.slice(2);
if (files.length === 0) {
	console.error("usage: no-stacked-comments.ts <yaml>...");
	process.exit(2);
}

const found = files.flatMap(stackedRuns);
for (const run of found) {
	console.error(
		`::error file=${run.file},line=${run.start}::${run.end - run.start + 1} consecutive // comment lines ` +
			`(${run.start}-${run.end}) in an inline script. The typescript action refuses to run this: ` +
			`say it in one line, or move the prose to docs/.`,
	);
}
if (found.length > 0) {
	console.error(`${found.length} stacked comment block(s) across ${new Set(found.map((r) => r.file)).size} file(s)`);
	process.exit(1);
}
console.log(`no stacked inline-script comments in ${files.length} file(s)`);
