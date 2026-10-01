// Runs buildhost-download's own lib-path lines: a container job hands the action the HOST path.
const assert = require('node:assert');
const fs = require('node:fs') as typeof import('node:fs');
const path = require('node:path') as typeof import('node:path');
const { stripTypeScriptTypes } = require('node:module');

const root = process.env.GITHUB_WORKSPACE ?? process.cwd();
const lines = fs.readFileSync(`${root}/.github/actions/buildhost-download/action.yml`, 'utf8').split('\n');
const start = lines.findIndex((l) => l.includes('const hostActionPath'));
const end = lines.findIndex((l) => l.includes('const { fetchBody }'));
assert.ok(start >= 0 && end > start, 'the action resolves its lib through hostActionPath');
const snippet: string = lines.slice(start, end).join('\n');

function resolve(actionPath: string, workspace: string, impl: typeof path): string {
	const code = stripTypeScriptTypes(snippet.replace('${{ toJSON(github.action_path) }}', JSON.stringify(actionPath)));
	const run = new Function('path', 'process', `${code}\nreturn actionPath;`);
	return run(impl, { env: { GITHUB_WORKSPACE: workspace } });
}

assert.strictEqual(
	resolve('/home/runner/_work/_actions/o/buildhost/master/.github/actions/buildhost-download', '/__w/vega/vega', path.posix),
	'/__w/_actions/o/buildhost/master/.github/actions/buildhost-download',
	'a container job reads the action from the work root mounted in the container',
);
assert.strictEqual(
	resolve('/home/runner/work/_actions/o/buildhost/master/.github/actions/buildhost-download', '/home/runner/work/r/r', path.posix),
	'/home/runner/work/_actions/o/buildhost/master/.github/actions/buildhost-download',
	'a host job keeps the path it was given',
);
assert.strictEqual(
	resolve('D:\\a\\_actions\\o\\buildhost\\master\\.github\\actions\\buildhost-download', 'D:\\a\\r\\r', path.win32),
	'D:\\a\\_actions\\o\\buildhost\\master\\.github\\actions\\buildhost-download',
	'a Windows job keeps the path it was given',
);
assert.strictEqual(
	resolve('/work/repo/.github/actions/buildhost-download', '/work/repo', path.posix),
	'/work/repo/.github/actions/buildhost-download',
	'a local action outside _actions keeps its path',
);
console.log('download-action-path: all checks passed');
