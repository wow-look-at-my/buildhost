// CI gate: this repo's instruction files must stay inside the size budget an
// agent session pays on EVERY request.
//
// Why this is a CI check and not only a hook. The rule used to live here as a
// wrap check and was deleted in 76110b4, on the reasoning that it belongs in the
// web config where it covers every repo -- enforced by the claude-md-budget hook
// at edit time and at end of turn. The reasoning was right about WHERE the rule
// belongs and wrong about what enforcement survives the trip: a hook only fires
// if the session that happens to be running has a current config installed. A
// session whose environment was provisioned from an older build got the hook
// registered on SessionStart alone -- no PostToolUse re-measure, no Stop gate --
// and its one advisory line ended with "do NOT go reorganize these files right
// now unless that IS the task". So CLAUDE.md sat at 120,754 characters, 3.02x
// budget, through a dozen edits, and nothing anywhere went red.
//
// A hook advises the model. CI is the thing that cannot be skipped, and the two
// are layers, not duplicates. This is the layer that fails the build.
//
// Run: node .github/scripts/claude-md-budget.ts
// Erasable-syntax-only TypeScript, executed directly by node >= 22.18.

import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import process from "node:process";

// The CLI's own floor for the same measurement, and the value the web config's
// hook pins. Characters, not bytes -- the CLI counts characters.
const BUDGET = 40000;

// At or above this the file has effectively no room left: the next ordinary
// edit blows the budget, and whoever makes it inherits the extraction. Must
// match NEAR_FRACTION in the web config's setup/claude-md-budget.ts.
const NEAR_FRACTION = 0.975;

// An unwrapped file makes every edit a one-line diff no reviewer can read, and a
// paragraph that runs for thousands of columns is the SHAPE of an item that
// should have been a pointer to docs/.
const WIDTH_LIMIT = 150;

function instructionFiles(): string[] {
  const out = ["CLAUDE.md"];
  // A CLAUDE.md in any first-level subdirectory counts too: the CLI loads each
  // one it walks into, and each is measured on its own.
  for (const entry of readdirSync(".", { withFileTypes: true })) {
    if (!entry.isDirectory() || entry.name.startsWith(".")) continue;
    const candidate = join(entry.name, "CLAUDE.md");
    try {
      if (statSync(candidate).isFile()) out.push(candidate);
    } catch {
      // No CLAUDE.md in that directory; nothing to measure.
    }
  }
  return out;
}

// Lines that could have been wrapped and were not. Code fences, tables,
// indented blocks and headings cannot be rewrapped without changing what they
// render as, and a line whose first WIDTH_LIMIT columns hold no space is a
// single unbreakable token (a URL).
function wideLines(text: string): number[] {
  const out: number[] = [];
  let fenced = false;
  const lines = text.split("\n");
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (/^\s*(```|~~~)/.test(line)) {
      fenced = !fenced;
      continue;
    }
    if (fenced || line.length <= WIDTH_LIMIT) continue;
    if (line.startsWith("    ") || /^\s*[|>#]/.test(line)) continue;
    if (!line.slice(0, WIDTH_LIMIT).includes(" ")) continue;
    out.push(i + 1);
  }
  return out;
}

const failures: string[] = [];
const wall = Math.ceil(BUDGET * NEAR_FRACTION);

for (const path of instructionFiles()) {
  const text = readFileSync(path, "utf8");
  const chars = text.length;
  const wide = wideLines(text);

  if (chars > BUDGET) {
    failures.push(
      `${path}: ${chars} characters, ${(chars / BUDGET).toFixed(2)}x the ${BUDGET} budget ` +
        `(${chars - BUDGET} over). Move a section into docs/<topic>.md VERBATIM and leave a ` +
        `one-line pointer. Extraction, never summarization.`,
    );
  } else if (chars >= wall) {
    failures.push(
      `${path}: ${chars} characters, ${Math.round((chars / BUDGET) * 100)}% of the ${BUDGET} ` +
        `budget -- only ${BUDGET - chars} characters of room left. Landing just under the limit ` +
        `is not a fix: the next edit of any size breaks it. Extract a section into docs/.`,
    );
  }

  if (wide.length > 0) {
    const shown = wide.slice(0, 5).join(", ") + (wide.length > 5 ? ", ..." : "");
    failures.push(`${path}: ${wide.length} line(s) over ${WIDTH_LIMIT} columns (${shown}). Hard-wrap them.`);
  }

  if (chars <= BUDGET && chars < wall && wide.length === 0) {
    console.log(`ok  ${path}: ${chars} characters (${Math.round((chars / BUDGET) * 100)}% of budget), wrapped`);
  }
}

if (failures.length > 0) {
  for (const f of failures) console.error(`FAIL  ${f}`);
  process.exit(1);
}
