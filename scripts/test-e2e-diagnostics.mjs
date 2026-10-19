import { open } from 'node:fs/promises';
import { join } from 'node:path';

const maximumBytes = 64 * 1024;

/** Preserve only the invocation's operational log before its profile is removed. */
export async function reportRuntimeDiagnostics(
  profileHome,
  report = console.error,
) {
  try {
    const file = await open(join(profileHome, 'logs', 'daemon.log'), 'r');
    try {
      const { size } = await file.stat();
      const buffer = Buffer.alloc(Math.min(size, maximumBytes));
      const { bytesRead } = await file.read(
        buffer,
        0,
        buffer.length,
        Math.max(0, size - buffer.length),
      );
      // A byte-bounded tail may start within a UTF-8 character.
      let start = 0;
      while (start < bytesRead && (buffer[start] & 0xc0) === 0x80) start++;
      const tail = buffer.toString('utf8', start, bytesRead);
      if (tail)
        report(`--- Runtime daemon diagnostics (last 64 KiB) ---\n${tail}`);
    } finally {
      await file.close();
    }
  } catch (error) {
    if (error.code !== 'ENOENT')
      report('Runtime daemon diagnostics unavailable:', error);
  }
}
