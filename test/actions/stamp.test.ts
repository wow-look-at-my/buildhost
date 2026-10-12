// Behavior tests for .github/actions/lib/stamp.ts.

const { strict: assert } = require('node:assert');
const { mkdtempSync, writeFileSync, readFileSync, statSync, existsSync, rmSync } = require('node:fs') as typeof import('node:fs');
const { tmpdir } = require('node:os') as typeof import('node:os');
const { join } = require('node:path') as typeof import('node:path');
const { spawnSync } = require('node:child_process') as typeof import('node:child_process');

// Untyped on purpose: the module's types are checked where it is CALLED, in the publish composite.
const { encode, decode, stampCopy, Magic } = require(`${process.env.GITHUB_WORKSPACE ?? process.cwd()}/.github/actions/lib/stamp.ts`);

const dir = mkdtempSync(join(tmpdir(), 'stamp-test-'));
const want = { project: 'webhooks/webhook-runner', release: '347', revision: 'abc123' };

// encode then decode gives the same stamp back, and the trailer ends with the magic
{
	const t = encode(want);
	assert.ok(t.subarray(t.length - Magic.length).equals(Magic), 'trailer must end with the magic');
	const got = decode(Buffer.concat([Buffer.from('body'), t]));
	assert.deepEqual(got.stamp, want);
	assert.equal(got.start, 4, 'the stamp starts where the body ends');
}

// bytes with no stamp decode to null
assert.equal(decode(Buffer.from('a plain binary')), null);

// stampCopy keeps the original bytes as a prefix, keeps the mode, and never changes the source
{
	const src = join(dir, 'tool');
	writeFileSync(src, Buffer.from('ORIGINAL'), { mode: 0o755 });
	const out = stampCopy(src, join(dir, 'out', 'sub', 'tool'), want);
	const body = readFileSync(out);
	assert.ok(body.subarray(0, 8).equals(Buffer.from('ORIGINAL')), 'the original bytes come first');
	assert.deepEqual(decode(body).stamp, want);
	assert.equal(statSync(out).mode & 0o777, 0o755, 'the copy keeps the mode');
	assert.ok(readFileSync(src).equals(Buffer.from('ORIGINAL')), 'the source is unchanged');

	// a stamped file stamped again carries ONE stamp, the new one
	const twice = readFileSync(stampCopy(out, join(dir, 'again'), { ...want, release: '348' }));
	assert.equal(twice.length, body.length, 'the old stamp is replaced, not stacked');
	assert.equal(decode(twice).stamp.release, '348');
	assert.ok(twice.subarray(0, 8).equals(Buffer.from('ORIGINAL')));
}

// an empty field is refused, and nothing is written
{
	const src = join(dir, 'empty');
	writeFileSync(src, 'x');
	assert.throws(() => stampCopy(src, join(dir, 'empty-out'), { ...want, revision: '' }), /revision is empty/);
	assert.ok(!existsSync(join(dir, 'empty-out')), 'nothing is written for a refused stamp');
}

// a trailer that claims more bytes than the file holds fails loud
assert.throws(() => decode(Buffer.concat([Buffer.from([0, 0, 0xff, 0xff]), Magic])), /more than the file holds/);

// a stamped ELF still starts and exits 0
{
	const r = spawnSync(stampCopy('/bin/true', join(dir, 'true'), want), []);
	assert.equal(r.status, 0, `the stamped /bin/true must still run: ${r.error ?? r.stderr}`);
}

rmSync(dir, { recursive: true, force: true });
console.log('stamp: all checks passed');
