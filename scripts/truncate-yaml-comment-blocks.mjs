#!/usr/bin/env node
// Truncates every over-long `#` comment block in this repo's workflow and
// action files to its first line, which is what
// wow-look-at-my/actions@yaml-comment-block allows.
//
// The block rule is copied from that action's scan.ts: a run of `#` lines ends
// at the first line that is neither a comment nor blank, so a blank line
// between two comment lines does NOT split them into two blocks.
//
// Run it from the repo root: node scripts/truncate-yaml-comment-blocks.mjs

import {readFileSync, writeFileSync, readdirSync} from 'node:fs';
import {join} from 'node:path';

const SKIP_DIRS = new Set(['node_modules', '.git', 'build']);
const MAX_COMMENT_LINES = 1;

function isComment(line) {
	return line.trimStart().startsWith('#');
}

function isBlank(line) {
	return line.trim() === '';
}

// Every scanned file, workspace-relative, the way the guard walks for them.
function walk(root, relative, found) {
	for (const entry of readdirSync(join(root, relative), {withFileTypes: true})) {
		const child = relative === '' ? entry.name : `${relative}/${entry.name}`;
		if (entry.isDirectory()) {
			if (!SKIP_DIRS.has(entry.name)) {
				walk(root, child, found);
			}
			continue;
		}
		if (!entry.isFile()) {
			continue;
		}
		if (/^\.github\/workflows\/[^/]+\.ya?ml$/.test(child) || /(^|\/)action\.ya?ml$/.test(child)) {
			found.push(child);
		}
	}
	return found;
}

// The blocks running past the limit, as [startIndex, endIndex] pairs.
function overLongBlocks(lines) {
	const blocks = [];
	let start = -1;
	let end = -1;
	let count = 0;
	const flush = () => {
		if (count > MAX_COMMENT_LINES) {
			blocks.push([start, end]);
		}
		start = -1;
		end = -1;
		count = 0;
	};
	lines.forEach((line, index) => {
		if (isComment(line)) {
			if (count === 0) {
				start = index;
			}
			count++;
			end = index;
			return;
		}
		if (isBlank(line)) {
			return;
		}
		if (count > 0) {
			flush();
		}
	});
	if (count > 0) {
		flush();
	}
	return blocks;
}

// Keeps the block's first line and drops the comment and blank lines behind it,
// up to the block's last comment line.
function truncate(content) {
	const lines = content.split('\n');
	const drop = new Set();
	for (const [start, end] of overLongBlocks(lines)) {
		for (let index = start + 1; index <= end; index++) {
			drop.add(index);
		}
	}
	if (drop.size === 0) {
		return null;
	}
	return {text: lines.filter((_, index) => !drop.has(index)).join('\n'), dropped: drop.size};
}

const root = process.cwd();
let changed = 0;
let dropped = 0;
for (const file of walk(root, '', []).sort()) {
	const result = truncate(readFileSync(join(root, file), 'utf8'));
	if (result === null) {
		continue;
	}
	writeFileSync(join(root, file), result.text);
	changed++;
	dropped += result.dropped;
	console.log(`${file}: dropped ${result.dropped} line(s)`);
}
console.log(`${changed} file(s) rewritten, ${dropped} line(s) dropped`);
