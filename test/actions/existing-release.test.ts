// Behavior tests for .github/actions/lib/existing-release.ts.

const assert = require('node:assert');
const lib = `${process.env.GITHUB_WORKSPACE ?? process.cwd()}/.github/actions/lib/existing-release`;
// Untyped on purpose: the module's types are checked where it is CALLED, in the publish composite.
const { existingRelease } = require(`${lib}.ts`);

const A = 'a'.repeat(64);
const B = 'b'.repeat(64);

type Artifact = { os: string; arch: string; kind: string; sha256: string; platforms?: { os: string; arch: string }[] };

function server(status: number, body: unknown) {
	const asked: string[] = [];
	const send = async (method: string, urlPath: string) => {
		asked.push(`${method} ${urlPath}`);
		return { status, text: typeof body === 'string' ? body : JSON.stringify(body) };
	};
	return { send, asked };
}

function logger() {
	const lines: string[] = [];
	return { core: { info: (m: string) => lines.push(m) }, lines };
}

const release = (published: boolean, artifacts: Artifact[]) => ({ version: '7', published, artifacts });
const ape: Artifact = {
	os: 'linux', arch: 'amd64', kind: 'binary', sha256: A,
	platforms: [{ os: 'linux', arch: 'amd64' }, { os: 'darwin', arch: 'arm64' }, { os: 'windows', arch: 'amd64' }],
};
const apeSlots = (sum: string) => ['linux/amd64', 'darwin/arm64', 'windows/amd64'].map((p) => {
	const [os, arch] = p.split('/');
	return { os, arch, kind: 'binary', sha256: sum };
});

async function main() {
	// An unpublished release, the one the build created, takes the upload.
	{
		const s = server(200, release(false, []));
		const { core } = logger();
		assert.deepStrictEqual(await existingRelease(s.send, core, 'hooks/runner', '7', apeSlots(A)), { action: 'publish' });
		assert.deepStrictEqual(s.asked, ['GET /api/v1/projects/hooks/runner/releases/7']);
	}

	// A published release with the same bytes in every slot is a no-op that says so.
	{
		const s = server(200, release(true, [ape]));
		const { core, lines } = logger();
		assert.deepStrictEqual(await existingRelease(s.send, core, 'hooks/runner', '7', apeSlots(A)), { action: 'noop' });
		assert.match(lines.join('\n'), /hooks\/runner\/7 is already published, and each of its 3 slot\(s\) holds the bytes this run built/);
	}

	// Per-platform rows match slot by slot.
	{
		const s = server(200, release(true, [
			{ os: 'linux', arch: 'amd64', kind: 'binary', sha256: A },
			{ os: 'darwin', arch: 'arm64', kind: 'binary', sha256: B },
		]));
		const { core } = logger();
		const slots = [{ os: 'linux', arch: 'amd64', kind: 'binary', sha256: A }, { os: 'darwin', arch: 'arm64', kind: 'binary', sha256: B }];
		assert.deepStrictEqual(await existingRelease(s.send, core, 'p', '7', slots), { action: 'noop' });
	}

	// Other bytes under a published number fail, naming every slot that differs.
	{
		const s = server(200, release(true, [ape]));
		const { core } = logger();
		const v = await existingRelease(s.send, core, 'hooks/runner', '7', apeSlots(B));
		assert.strictEqual(v.action, 'fail');
		assert.match(v.message, /Release hooks\/runner\/7 is already published with other bytes/);
		for (const slot of ['linux/amd64', 'darwin/arm64', 'windows/amd64']) {
			assert.ok(v.message.includes(`${slot} (binary): published sha256 ${A}, this run built ${B}`), `${slot} named in: ${v.message}`);
		}
	}

	// A slot the published release lacks fails too.
	{
		const s = server(200, release(true, [{ os: 'linux', arch: 'amd64', kind: 'binary', sha256: A }]));
		const { core } = logger();
		const v = await existingRelease(s.send, core, 'p', '7', [
			{ os: 'linux', arch: 'amd64', kind: 'binary', sha256: A },
			{ os: 'linux', arch: 'arm64', kind: 'binary', sha256: A },
		]);
		assert.strictEqual(v.action, 'fail');
		assert.match(v.message, /linux\/arm64 \(binary\): the published release has no such artifact/);
		assert.ok(!v.message.includes('linux/amd64'), v.message);
	}

	// The kind is part of the slot.
	{
		const s = server(200, release(true, [{ os: 'linux', arch: 'amd64', kind: 'binary', sha256: A }]));
		const { core } = logger();
		const v = await existingRelease(s.send, core, 'p', '7', [{ os: 'linux', arch: 'amd64', kind: 'library', sha256: A }]);
		assert.strictEqual(v.action, 'fail');
	}

	// A release that cannot be read fails rather than guessing.
	{
		const s = server(500, '{"error":"boom"}');
		const { core } = logger();
		const v = await existingRelease(s.send, core, 'p', '7', apeSlots(A));
		assert.strictEqual(v.action, 'fail');
		assert.match(v.message, /reading it failed \(500\)/);
	}
	{
		const s = server(200, 'not json');
		const { core } = logger();
		const v = await existingRelease(s.send, core, 'p', '7', apeSlots(A));
		assert.strictEqual(v.action, 'fail');
		assert.match(v.message, /not JSON/);
	}

	console.log('existing-release: all checks passed');
}

// Node runs this file directly, so the rejection has to become an exit code
// here.
main().catch((err: unknown) => {
	console.error(err);
	process.exit(1);
});
