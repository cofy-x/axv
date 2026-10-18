import assert from 'node:assert/strict';
import { lstat, readFile, realpath } from 'node:fs/promises';
import { join } from 'node:path';

export async function verifyProfileLog(
  profileHome,
  { logPath, excluded = [], minimumDesktopStarts = 1 } = {},
) {
  const directory = join(await realpath(profileHome), 'logs');
  const path = join(directory, 'daemon.log');
  if (process.platform === 'win32') {
    assert.equal(logPath, undefined);
    await assert.rejects(lstat(path), { code: 'ENOENT' });
    return [];
  }
  if (logPath !== undefined) assert.equal(logPath, path);
  const directoryInfo = await lstat(directory);
  const fileInfo = await lstat(path);
  assert.equal(directoryInfo.isDirectory(), true);
  assert.equal(directoryInfo.isSymbolicLink(), false);
  assert.equal(fileInfo.isFile(), true);
  assert.equal(fileInfo.isSymbolicLink(), false);
  if (process.platform !== 'win32') {
    assert.equal(directoryInfo.mode & 0o777, 0o700);
    assert.equal(fileInfo.mode & 0o777, 0o600);
  }
  const contents = await readFile(path, 'utf8');
  assert.ok(contents.endsWith('\n'), 'Log records must be complete lines.');
  const records = contents.trimEnd().split('\n').map(JSON.parse);
  assert.ok(records.length > 0, 'The profile must contain operational logs.');
  for (const record of records) {
    assert.ok(Number.isFinite(Date.parse(record.time)));
    assert.equal(typeof record.level, 'string');
    assert.equal(typeof record.event, 'string');
    assert.equal(typeof record.role, 'string');
  }
  assert.ok(
    records.filter(
      (record) => record.role === 'desktop' && record.event === 'service.start',
    ).length >= minimumDesktopStarts,
    'Every new desktop instance must append to the same profile log.',
  );
  for (const value of excluded)
    assert.equal(
      contents.includes(value),
      false,
      'Session or secret leaked to logs.',
    );
  return records;
}
