#!/usr/bin/env node
// Truncates the Go doc comments go-toolchain's commentspan analyzer reports.
//
// commentspan warns when a comment group is bigger than the declaration under
// it: more non-blank lines, or more non-whitespace characters than
// max(120, code chars). One prose line of at most 120 non-whitespace characters
// therefore clears both limits for any declaration at all, so that is what this
// keeps. It truncates; it never rewords.
//
// Usage: node scripts/truncate-long-comments.mjs internal/foo/bar.go:74 ...
// The argument is the file:line commentspan prints, which is where the comment
// group starts.

import { readFileSync, writeFileSync } from 'node:fs';

const CHAR_BUDGET = 120;

// isDirective matches the comment forms the compiler and the tools read. They
// carry no prose and are kept whole.
const isDirective = (text) => /^\/\/(go|line|nolint|export|sys|cgo|extern):/.test(text.trim());

// truncate cuts body down to CHAR_BUDGET counted the way commentspan counts:
// whitespace is free, everything else costs one.
function truncate(body) {
	let kept = '';
	let chars = 0;
	for (const ch of body) {
		if (!/\s/.test(ch)) {
			if (chars === CHAR_BUDGET) break;
			chars++;
		}
		kept += ch;
	}
	return kept.trimEnd();
}

let changed = 0;
for (const target of process.argv.slice(2)) {
	const at = target.lastIndexOf(':');
	if (at < 0) throw new Error(`expected file:line, got ${target}`);
	const file = target.slice(0, at);
	const start = Number(target.slice(at + 1));
	if (!Number.isInteger(start) || start < 1) throw new Error(`bad line in ${target}`);

	const lines = readFileSync(file, 'utf8').split('\n');
	const first = lines[start - 1];
	if (first === undefined || !first.trim().startsWith('//')) {
		throw new Error(`${target} does not start a // comment group: ${first}`);
	}

	let end = start - 1;
	while (end + 1 < lines.length && lines[end + 1].trim().startsWith('//')) end++;

	const group = lines.slice(start - 1, end + 1);
	const directives = group.filter(isDirective);
	const prose = group.filter((l) => !isDirective(l));
	if (prose.length === 0) continue;

	const indent = prose[0].slice(0, prose[0].indexOf('//'));
	const body = prose[0].trim().replace(/^\/\/\s?/, '');
	const replacement = [`${indent}// ${truncate(body)}`.trimEnd(), ...directives];
	if (replacement.length === group.length && replacement.every((l, i) => l === group[i])) continue;

	lines.splice(start - 1, group.length, ...replacement);
	writeFileSync(file, lines.join('\n'));
	changed++;
	console.log(`${file}: ${group.length} comment line(s) -> ${replacement.length}`);
}
console.log(`truncated ${changed} comment group(s)`);
