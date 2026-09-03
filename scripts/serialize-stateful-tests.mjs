// Inserts t.Serial() as the first statement of every top-level Test function in
// the packages named on the command line.
//
// The gosmopolitan fork runs tests in parallel by default; these packages boot a
// server per test, and that rewires process-wide state (auth.Init and the
// handler singletons). t.Serial() is the fork's opt-out.
//
// Usage: node scripts/serialize-stateful-tests.mjs internal/server internal/sites

import { readFileSync, writeFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

const dirs = process.argv.slice(2);
if (dirs.length === 0) {
	console.error("usage: serialize-stateful-tests.mjs <dir>...");
	process.exit(2);
}

// Matches `func TestName(t *testing.T) {` on its own line. A subtest closure
// takes its own *testing.T from t.Run, so it never matches.
const testFunc = /^func (Test[A-Za-z0-9_]*)\(([a-zA-Z_][A-Za-z0-9_]*) \*testing\.T\) \{$/;

let changed = 0;
for (const dir of dirs) {
	for (const name of readdirSync(dir)) {
		if (!name.endsWith("_test.go")) continue;
		const path = join(dir, name);
		const lines = readFileSync(path, "utf8").split("\n");
		const out = [];
		let touched = false;
		for (let i = 0; i < lines.length; i++) {
			out.push(lines[i]);
			const m = testFunc.exec(lines[i]);
			if (!m) continue;
			const next = lines[i + 1] ?? "";
			if (next.trim() === `${m[2]}.Serial()`) continue;
			out.push(`\t${m[2]}.Serial()`);
			touched = true;
		}
		if (!touched) continue;
		writeFileSync(path, out.join("\n"));
		changed++;
		console.log(`serialized ${path}`);
	}
}
console.log(`${changed} files rewritten`);
