import { describe, expect, it } from 'vitest';
import { prioritizeUbuntuMirrors } from './ci-ubuntu-mirrors.mjs';

describe('CI Ubuntu mirror selection', () => {
  it('retains the runner mirror set and metadata with an HTTPS Azure fallback', () => {
    const result = prioritizeUbuntuMirrors(
      '# Runner mirrors\n' +
        'http://azure.archive.ubuntu.com/ubuntu/\tarch:amd64 priority:1\n' +
        'https://archive.ubuntu.com/ubuntu/\ttype:deb\n' +
        'https://security.ubuntu.com/ubuntu/\n',
    );
    expect(result).toBe(
      '# Runner mirrors\n' +
        'https://azure.archive.ubuntu.com/ubuntu/\tarch:amd64 priority:20\n' +
        'https://archive.ubuntu.com/ubuntu/\ttype:deb priority:10\n' +
        'https://security.ubuntu.com/ubuntu/\tpriority:10\n' +
        'https://mirrors.edge.kernel.org/ubuntu/\tpriority:30\n',
    );
    expect(prioritizeUbuntuMirrors(result)).toBe(result);
  });

  it('keeps a runner usable when Azure is its only supplied mirror', () => {
    expect(
      prioritizeUbuntuMirrors('https://azure.archive.ubuntu.com/ubuntu'),
    ).toBe(
      'https://azure.archive.ubuntu.com/ubuntu\tpriority:20\n' +
        'https://mirrors.edge.kernel.org/ubuntu/\tpriority:30\n',
    );
  });

  it('keeps an existing independent fallback and its metadata without duplicating it', () => {
    const input =
      'https://mirrors.edge.kernel.org/ubuntu/\tarch:amd64 priority:1\n';
    const result = prioritizeUbuntuMirrors(input);
    expect(result).toBe(
      'https://mirrors.edge.kernel.org/ubuntu/\tarch:amd64 priority:30\n',
    );
    expect(prioritizeUbuntuMirrors(result)).toBe(result);
  });

  it('rejects an empty list instead of removing every download source', () => {
    expect(() => prioritizeUbuntuMirrors('# No mirrors\n')).toThrow(
      'no package mirrors',
    );
  });

  it('rejects transports outside the CI HTTPS package-source contract', () => {
    expect(() => prioritizeUbuntuMirrors('file:/tmp/packages')).toThrow(
      'must use HTTP or HTTPS',
    );
  });
});
