#!/usr/bin/env node
// Truncate every Go comment at the first line go-toolchain's commentnumbers
// analyzer reports. The rules here mirror that analyzer: a token is a run of
// name characters, a digit run counts unless a letter touches it, and a word
// counts when the whole word names a number. The exemptions it grants -- a
// URL, a qualified name, an HTTP status code, a section sign, an amount of
// money -- are granted here too, so this cuts what the build reports and
// nothing else.
//
// The offending line and the rest of its comment group go; a comment trailing
// a code line is stripped from that line. Nothing is reworded.
//
//   node scripts/truncate-number-comments.mjs [dir] [--dry]
import fs from "node:fs";
import path from "node:path";

const root = process.argv[2] && !process.argv[2].startsWith("--") ? process.argv[2] : ".";
const dry = process.argv.includes("--dry");

const NUMBER_WORDS = new Set([
	"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine",
	"ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen",
	"seventeen", "eighteen", "nineteen", "twenty", "thirty", "forty", "fifty",
	"sixty", "seventy", "eighty", "ninety", "hundred", "thousand", "million",
	"billion", "trillion", "dozen",
	"once", "twice", "thrice",
	"first", "second", "third", "fourth", "fifth", "sixth", "seventh", "eighth",
	"ninth", "tenth", "eleventh", "twelfth", "thirteenth", "fourteenth",
	"fifteenth", "sixteenth", "seventeenth", "eighteenth", "nineteenth",
	"twentieth", "thirtieth", "fortieth", "fiftieth", "sixtieth", "seventieth",
	"eightieth", "ninetieth", "hundredth", "thousandth",
]);

const NAME_MARKERS = "._/:";
const ORDINAL_SUFFIXES = new Set(["st", "nd", "rd", "th"]);

const isLetter = (c) => /\p{L}/u.test(c);
const isDigit = (c) => /\p{Nd}/u.test(c);
const isWordRune = (c) => isLetter(c) || isDigit(c);
const isNameRune = (c) => isWordRune(c) || (NAME_MARKERS + "-").includes(c);

// tokenize splits a comment into runs of name characters, so a URL, an import
// path and a version each stay whole.
function tokenize(text) {
	const chars = [...text];
	const toks = [];
	let start = -1;
	for (let i = 0; i < chars.length; i++) {
		if (isNameRune(chars[i])) {
			if (start < 0) start = i;
			continue;
		}
		if (start >= 0) {
			toks.push({ at: start, text: chars.slice(start, i).join("") });
			start = -1;
		}
	}
	if (start >= 0) toks.push({ at: start, text: chars.slice(start).join("") });
	return toks;
}

const trimMarkers = (s) => s.replace(new RegExp(`^[${NAME_MARKERS}]+|[${NAME_MARKERS}]+$`, "g"), "");

// isHTTPStatus reports whether the token names a protocol answer rather than a count.
function isHTTPStatus(toks, i) {
	if (i === 0) return false;
	const prefix = trimMarkers(toks[i - 1].text);
	const digits = trimMarkers(toks[i].text);
	return prefix.toUpperCase() === "HTTP" && digits.length === 3 && [...digits].every(isDigit);
}

// isSectionRef reports whether a section sign introduces the token, which cites
// a section instead of counting anything.
function isSectionRef(chars, tok) {
	const before = chars.slice(0, tok.at).join("").replace(/[ \t]+$/, "");
	return before.endsWith("§");
}

// isMoney reports whether a currency sign sits against the digits, which states
// an amount rather than a count.
function isMoney(chars, tok) {
	return tok.at > 0 && chars[tok.at - 1] === "$" && isDigit(tok.text[0]);
}

// isQualifiedName reports whether a marker sits between name characters, which
// is what separates an identifier from a sentence ending on a word.
function isQualifiedName(text) {
	const c = [...text];
	for (let i = 1; i < c.length - 1; i++) {
		if (NAME_MARKERS.includes(c[i]) && isWordRune(c[i - 1]) && isWordRune(c[i + 1])) return true;
	}
	return false;
}

function hasOrdinalSuffix(c, end) {
	const stop = end + 2;
	if (stop > c.length) return false;
	if (stop < c.length && isLetter(c[stop])) return false;
	return ORDINAL_SUFFIXES.has(c.slice(end, stop).join("").toLowerCase());
}

// tokenNumber reports whether a token carries a number.
function tokenNumber(tok) {
	if (tok.text.includes("://")) return false;
	const c = [...tok.text];
	for (let i = 0; i < c.length; i++) {
		if (!isDigit(c[i])) continue;
		let end = i;
		while (end < c.length && isDigit(c[end])) end++;
		const touches = (i > 0 && isLetter(c[i - 1])) || (end < c.length && isLetter(c[end]));
		if (!touches || hasOrdinalSuffix(c, end)) return true;
		i = end;
	}
	if (isQualifiedName(tok.text)) return false;
	for (let i = 0; i < c.length; i++) {
		if (!isLetter(c[i])) continue;
		let end = i;
		while (end < c.length && isLetter(c[end])) end++;
		if (NUMBER_WORDS.has(c.slice(i, end).join("").toLowerCase())) return true;
		i = end;
	}
	return false;
}

// A directive is code, not prose. It is never reported, and a truncation
// keeps it: dropping a go:embed or a go:generate line breaks the build.
const isDirective = (text) =>
	/^\/\/[a-z0-9]+:[^\s]/i.test(text.trim()) || /^\/\/\s*(\+build|export |line |nolint)/.test(text.trim());

function offends(commentText) {
	if (isDirective(commentText)) return false;
	const chars = [...commentText];
	const toks = tokenize(commentText);
	return toks.some((tok, i) =>
		!isHTTPStatus(toks, i) && !isSectionRef(chars, tok) && !isMoney(chars, tok) && tokenNumber(tok));
}

// splitCode returns the code and the trailing comment of a line, ignoring a
// slash pair inside a string or a rune literal.
function splitCode(line) {
	let quote = null;
	for (let i = 0; i < line.length; i++) {
		const ch = line[i];
		if (quote) {
			if (ch === "\\" && quote !== "`") i++;
			else if (ch === quote) quote = null;
			continue;
		}
		if (ch === '"' || ch === "'" || ch === "`") quote = ch;
		else if (ch === "/" && line[i + 1] === "/") return [line.slice(0, i), line.slice(i)];
	}
	return [line, null];
}

// measure counts non-blank lines and non-whitespace chars, the way the
// commentspan analyzer measures both a comment and the code under it.
function measure(text) {
	let lines = 0;
	let chars = 0;
	for (const line of text.split("\n")) {
		let content = false;
		for (const ch of line) {
			if (/\s/.test(ch)) continue;
			content = true;
			chars++;
		}
		if (content) lines++;
	}
	return { lines, chars };
}

const CHAR_FLOOR = 120;
const indentOf = (line) => line.match(/^[ \t]*/)[0];

// nodeSpan finds the code a comment group documents: the declaration or the
// statement under it, to the line that closes it at the same indent.
function nodeSpan(lines, start) {
	let k = start;
	while (k < lines.length && lines[k].trim() === "") k++;
	if (k >= lines.length) return null;
	const head = lines[k].trimEnd();
	if (!/[{(]$/.test(splitCode(head)[0].trimEnd())) return { from: k, to: k };
	const indent = indentOf(lines[k]);
	for (let j = k + 1; j < lines.length; j++) {
		if (indentOf(lines[j]) === indent && /^[})]/.test(lines[j].trim())) return { from: k, to: j };
	}
	return { from: k, to: k };
}

const GENERATED = /^\/\/ Code generated .* DO NOT EDIT\.$/m;
const SKIP_DIRS = new Set(["node_modules", "vendor", ".git", "build", "data", "dist"]);

let cutLines = 0;
let changedFiles = 0;

function walk(dir) {
	for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
		const p = path.join(dir, entry.name);
		if (entry.isDirectory()) {
			if (!SKIP_DIRS.has(entry.name)) walk(p);
		} else if (entry.name.endsWith(".go")) {
			truncateFile(p);
		}
	}
}

function truncateFile(file) {
	const src = fs.readFileSync(file, "utf8");
	if (GENERATED.test(src)) return;
	const lines = src.split("\n");
	const drop = new Set();
	let cut = 0;

	for (let i = 0; i < lines.length; ) {
		const [code, comment] = splitCode(lines[i]);
		if (comment === null) { i++; continue; }

		if (code.trim() !== "") {
			if (offends(comment)) {
				lines[i] = code.replace(/\s+$/, "");
				cut++;
			}
			i++;
			continue;
		}

		let end = i;
		while (end + 1 < lines.length) {
			const [nextCode, nextComment] = splitCode(lines[end + 1]);
			if (nextComment === null || nextCode.trim() !== "") break;
			end++;
		}
		for (let j = i; j <= end; j++) {
			if (!offends(splitCode(lines[j])[1])) continue;
			for (let k = j; k <= end; k++) {
				if (isDirective(splitCode(lines[k])[1])) continue;
				drop.add(k);
				cut++;
			}
			break;
		}
		i = end + 1;
	}

	const kept = lines.filter((_, n) => !drop.has(n));
	cut += fitSpans(kept);

	if (cut === 0) return;
	changedFiles++;
	cutLines += cut;
	if (!dry) fs.writeFileSync(file, kept.join("\n"));
}

// fitSpans truncates each comment group until it is no longer bigger than the
// code it documents, which is what commentspan reports. The package doc is
// exempt there, so it is exempt here.
function fitSpans(lines) {
	let cut = 0;
	for (let i = 0; i < lines.length; ) {
		const [code, comment] = splitCode(lines[i]);
		if (comment === null) { i++; continue; }

		if (code.trim() !== "") {
			const span = nodeSpan(lines, i);
			const limit = span ? measure(lines.slice(span.from, span.to + 1).join("\n")).chars : 0;
			if (measure(comment).chars > Math.max(CHAR_FLOOR, limit)) {
				lines[i] = code.replace(/\s+$/, "");
				cut++;
			}
			i++;
			continue;
		}

		let end = i;
		while (end + 1 < lines.length) {
			const [nextCode, nextComment] = splitCode(lines[end + 1]);
			if (nextComment === null || nextCode.trim() !== "") break;
			end++;
		}
		const span = nodeSpan(lines, end + 1);
		if (!span || /^package\s/.test(lines[span.from].trim())) { i = end + 1; continue; }

		const node = measure(lines.slice(span.from, span.to + 1).join("\n"));
		const charLimit = Math.max(CHAR_FLOOR, node.chars);
		const body = [];
		for (let j = i; j <= end; j++) {
			if (!isDirective(splitCode(lines[j])[1])) body.push(j);
		}
		while (body.length > 0) {
			const text = body.map((n) => lines[n].trim()).join("\n");
			const c = measure(text);
			if (c.lines <= node.lines && c.chars <= charLimit) break;
			lines[body.pop()] = null;
			cut++;
		}
		i = end + 1;
	}
	if (cut > 0) {
		for (let n = lines.length - 1; n >= 0; n--) {
			if (lines[n] === null) lines.splice(n, 1);
		}
	}
	return cut;
}

walk(root);
console.log(`${dry ? "would cut" : "cut"} ${cutLines} comment lines in ${changedFiles} files`);
