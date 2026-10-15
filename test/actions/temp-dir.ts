// A temporary directory holding the given files, for the tests of the action libs.

import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

export function tempDir(prefix: string, files: Record<string, string>): string {
	const d = fs.mkdtempSync(path.join(os.tmpdir(), prefix));
	for (const [name, body] of Object.entries(files)) fs.writeFileSync(path.join(d, name), body);
	return d;
}
