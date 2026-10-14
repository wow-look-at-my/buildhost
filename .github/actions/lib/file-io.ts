// Reads of a file the publish uploads. The caller passes node's fs and crypto modules.

export interface Fs {
	createReadStream(p: string): NodeJS.ReadableStream;
	openSync(p: string, flags: string): number;
	readSync(fd: number, buf: Buffer, offset: number, length: number, position: number): number;
	closeSync(fd: number): void;
}

export interface Crypto { createHash(alg: string): { update(b: Buffer): unknown; digest(enc: 'hex'): string } }

/** The bare hex sha256 of a file, from one streaming read. */
export function fileSHA256(fs: Fs, crypto: Crypto, p: string): Promise<string> {
	return new Promise((resolve, reject) => {
		const hash = crypto.createHash('sha256');
		fs.createReadStream(p)
			.on('error', reject)
			.on('data', (chunk: Buffer) => hash.update(chunk))
			.on('end', () => resolve(hash.digest('hex')));
	});
}

/**
 * Returns exactly `length` bytes of the file at `offset`. A short read would leave the
 * tail of an allocUnsafe buffer with stale memory, so it reads until the chunk is whole.
 */
export function readChunk(fs: Fs, filePath: string, offset: number, length: number): Buffer {
	const buf = Buffer.allocUnsafe(length);
	const fd = fs.openSync(filePath, 'r');
	try {
		let got = 0;
		while (got < length) {
			const n = fs.readSync(fd, buf, got, length - got, offset + got);
			if (n <= 0) throw new Error(`${filePath}: wanted ${length} bytes at ${offset}, the file ended after ${got}`);
			got += n;
		}
	} finally {
		fs.closeSync(fd);
	}
	return buf;
}
