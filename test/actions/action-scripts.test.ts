// Checks each action and workflow block as GitHub hands it to a step: its expression length, and for buildhost-publish the typescript action's type-check.

const assert = require('node:assert');
const fs = require('node:fs') as typeof import('node:fs');
const os = require('node:os') as typeof import('node:os');
const path = require('node:path') as typeof import('node:path');
const { spawnSync } = require('node:child_process') as typeof import('node:child_process');

const maxExpression = 21000;
const root = process.env.GITHUB_WORKSPACE ?? process.cwd();

// Each block scalar of a key, as YAML hands it to the runner: the common indent removed.
function blocks(text: string): { key: string; line: number; body: string }[] {
	const lines = text.split('\n');
	const out: { key: string; line: number; body: string }[] = [];
	for (let i = 0; i < lines.length; i++) {
		const m = lines[i].match(/^(\s*)(?:- )?([A-Za-z_-]+): [|>][-+]?\s*$/);
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
		out.push({ key: m[2], line: i + 1, body: body.join('\n').trimEnd() });
		i = j - 1;
	}
	return out;
}

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

// The runner keeps each action a job used under _actions, a couple of levels above the workspace. buildhost-download runs typescript#latest earlier in this job.
const typescriptAction = path.join(path.dirname(path.dirname(root)), '_actions/wow-look-at-my/actions/typescript#latest/dist/index.js');
assert.ok(fs.existsSync(typescriptAction), `the typescript action is not at ${typescriptAction}; a step of this job must use wow-look-at-my/actions@typescript#latest first`);

const emptyDir = fs.mkdtempSync(path.join(os.tmpdir(), 'publish-typecheck-'));
const sampleInputs: Record<string, string> = {
	server: 'http://localhost:1',
	project: '',
	artifact_name: 'go-build',
	path: emptyDir,
	version: '',
	git_branch: '',
	git_commit: '',
	create_service: '',
	apt_depends: 'bubblewrap | docker.io',
	default_branch: '',
	deployment_environment: '',
	deployment_ref: '',
};
const sampleSteps = {
	publish: { outcome: 'failure', conclusion: 'failure', outputs: { deployment_url: '' } },
	deployment: { outcome: 'success', conclusion: 'success', outputs: { deployment_id: '1' } },
};
const sampleContext: Record<string, unknown> = {
	inputs: sampleInputs,
	steps: sampleSteps,
};

// The value of one expression the scripts use: a context path, or format() over them.
function sample(expr: string): unknown {
	const name = expr.trim();
	const fmt = /^format\('([^']*)',\s*(.*)\)$/.exec(name);
	if (fmt !== null) {
		const args = fmt[2].split(',').map((a) => String(sample(a)));
		return fmt[1].replace(/\{(\d+)\}/g, (_m, n: string) => args[Number(n)]);
	}
	const v = name.startsWith('inputs.') ? sampleInputs[name.slice('inputs.'.length)] : sampleContext[name];
	assert.ok(v !== undefined, `no sample value for ${name}`);
	return v;
}

// GitHub evaluates each ${{ }} into the text before the action reads it, and toJSON(x) becomes a JSON literal.
function render(text: string): string {
	return text.replace(/\$\{\{([\s\S]*?)\}\}/g, (_m, expr: string) => {
		const json = /^\s*toJSON\((.*)\)\s*$/.exec(expr);
		return json === null ? String(sample(expr)) : JSON.stringify(sample(json[1]));
	});
}

const typeChecked = ['buildhost-publish', 'buildhost-create-oidc-policy', 'buildhost-create-project', 'buildhost-create-release', 'buildhost-create-token', 'buildhost-get', 'buildhost-publish-release', 'buildhost-download', 'buildhost-docker-push', 'buildhost-publish-docker', 'buildhost-publish-site', 'buildhost-upload-artifact'];
const scripts = typeChecked.flatMap((action) => {
	const actionDir = path.join(root, '.github/actions', action);
	return blocks(fs.readFileSync(path.join(actionDir, 'action.yml'), 'utf8')).filter((b) => b.key === 'script').map((b) => ({ ...b, action, actionDir }));
});
for (const script of scripts) {
	sampleContext['github.action_path'] = script.actionDir;
	const env: Record<string, string | undefined> = {
		...process.env,
		GITHUB_WORKSPACE: root,
		GITHUB_REPOSITORY: 'o/r',
		GITHUB_REF_NAME: 'main',
		GITHUB_SHA: '0'.repeat(40),
		// The script runs after the type-check, so the GitHub API points at a port nothing listens on.
		GITHUB_API_URL: 'http://127.0.0.1:1',
		INPUT_SCRIPT: render(script.body),
		INPUT_INPUTS: JSON.stringify(sampleInputs),
		INPUT_STEPS: JSON.stringify(sampleSteps),
		'INPUT_GITHUB-TOKEN': 'token',
	};
	const run = spawnSync(process.execPath, [typescriptAction], { env, encoding: 'utf8' });
	const out = `${run.stdout}${run.stderr}`;
	assert.ok(out.includes('Type-check passed.'), `${script.action}/action.yml:${script.line}: the script fails the typescript action's type-check:\n${out}`);
}
fs.rmSync(emptyDir, { recursive: true });

// A step's type-check stops at its own text, so every lib module and every test of them is checked here, strictly. Those stops
// are against the action's own declarations: its globals.d.ts and the @types and @actions packages it ships.
const tsDist = path.dirname(typescriptAction);
const libDir = path.join(root, '.github/actions/lib');
const testDir = path.join(root, 'test/actions');
const tsFiles = (dir: string) => fs.readdirSync(dir).filter((f) => f.endsWith('.ts')).map((f) => path.join(dir, f));
const project = fs.mkdtempSync(path.join(os.tmpdir(), 'lib-tsc-'));
fs.writeFileSync(path.join(project, 'tsconfig.json'), JSON.stringify({
	compilerOptions: {
		strict: true, noEmit: true, target: 'ES2022', module: 'ES2022', moduleResolution: 'node10', moduleDetection: 'force',
		esModuleInterop: true, skipLibCheck: true, lib: ['ES2022'], types: ['node'],
		typeRoots: [path.join(tsDist, 'types/node_modules/@types')], baseUrl: path.join(tsDist, 'types/node_modules'),
	},
	files: [path.join(tsDist, 'globals.d.ts'), ...tsFiles(libDir), ...tsFiles(testDir)],
}));
const tsc = spawnSync('npx', ['--yes', '--package', 'typescript@5.9.3', 'tsc', '--project', path.join(project, 'tsconfig.json')], { encoding: 'utf8' });
fs.rmSync(project, { recursive: true });
assert.strictEqual(tsc.status, 0, `tsc --noEmit over .github/actions/lib and test/actions failed:\n${tsc.stdout}${tsc.stderr}`);
console.log('action-scripts: all checks passed');
