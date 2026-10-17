// buildhost-download runs secret-server only in a job that can mint an OIDC token, so a job without id-token: write still reads a public project.

import assert from 'node:assert';
import { mkdtempSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';

const action = readFileSync('.github/actions/buildhost-download/action.yml', 'utf8');

const probe = action.match(/- id: oidc\n\s+shell: bash\n\s+run: \|\n((?:\s{8}.*\n)+)/);
assert.ok(probe, 'the action carries the oidc probe step');
const script = probe[1].replace(/^ {8}/gm, '');

function available(env: Record<string, string>): string {
	const out = join(mkdtempSync(join(tmpdir(), 'oidc-gate-')), 'out');
	const run = spawnSync('bash', ['-c', script], { env: { PATH: process.env.PATH!, GITHUB_OUTPUT: out, ...env }, encoding: 'utf8' });
	assert.strictEqual(run.status, 0, run.stderr);
	return readFileSync(out, 'utf8').trim();
}

assert.strictEqual(available({}), 'available=false', 'a job without id-token: write cannot mint a token');
assert.strictEqual(available({ ACTIONS_ID_TOKEN_REQUEST_URL: 'https://issuer.example/token' }), 'available=false', 'the URL without its bearer mints nothing');
assert.strictEqual(available({ ACTIONS_ID_TOKEN_REQUEST_URL: 'https://issuer.example/token', ACTIONS_ID_TOKEN_REQUEST_TOKEN: 'b' }), 'available=true');

const secretServer = action.match(/- if: (.*)\n\s+uses: wow-look-at-my\/actions@secret-server#latest/);
assert.ok(secretServer, 'the secret-server step carries a condition');
assert.match(secretServer[1], /steps\.oidc\.outputs\.available == 'true'/, 'secret-server runs only when the job can mint a token');

console.log('download-oidc-gate: all checks passed');
