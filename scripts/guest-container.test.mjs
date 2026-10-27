import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { delimiter, join } from 'node:path';
import { afterEach, describe, expect, test } from 'vitest';
import { captureCommand, root } from './lib.mjs';

const directories = [];
const recipe = await readFile(
  join(root, 'images/guest/build-rootfs.sh'),
  'utf8',
);
// Execute the recipe's Docker commands without building Go, an image or ext4.
const start = recipe.indexOf('\ndocker build ');
const end = recipe.indexOf('\ndocker export ', start);
if (start < 0 || end < 0)
  throw new Error('Missing guest container build commands.');
const commands = recipe.slice(start + 1, end);

afterEach(async () => {
  await Promise.all(
    directories
      .splice(0)
      .map((directory) => rm(directory, { recursive: true, force: true })),
  );
});

describe.skipIf(process.platform === 'win32')(
  'guest container platform',
  () => {
    test.each([
      ['amd64', 'linux/amd64', true],
      ['arm64', 'linux/arm64', true],
      ['amd64', 'linux/arm64', false],
      ['amd64', 'windows/amd64', false],
    ])(
      'checks %s against the built %s platform',
      async (arch, platform, valid) => {
        const directory = await mkdtemp(join(tmpdir(), 'axv-guest-container-'));
        directories.push(directory);
        const observation = join(directory, 'calls');
        await writeFile(
          join(directory, 'docker'),
          `#!/bin/sh
set -eu
case "$1" in
  build|create)
    operation=$1
    shift
    if [ "$operation" = build ]; then
      test "$1" = --no-cache
      shift
    fi
    test "$1" = --platform
    test "$2" = "linux/$guest_arch"
    printf '%s\\n' "$operation" >> "$AXV_DOCKER_CALLS"
    if [ "$operation" = create ]; then printf 'fixture-container\\n'; fi
    ;;
  image)
    test "$2" = inspect
    printf 'inspect\\n' >> "$AXV_DOCKER_CALLS"
    printf '%s\\n' "$AXV_DOCKER_PLATFORM"
    ;;
  *) exit 99 ;;
esac
`,
          { mode: 0o700 },
        );
        const result = await captureCommand(
          'bash',
          ['-euo', 'pipefail', '-c', commands],
          {
            allowFailure: true,
            env: {
              ...process.env,
              PATH: `${directory}${delimiter}${process.env.PATH}`,
              DOCKER_DEFAULT_PLATFORM:
                arch === 'amd64' ? 'linux/arm64' : 'linux/amd64',
              AXV_DOCKER_CALLS: observation,
              AXV_DOCKER_PLATFORM: platform,
              guest_arch: arch,
              image_tag: 'axv-guest-build:fixture',
              BASE_IMAGE: 'ubuntu:24.04@sha256:fixture',
              package_source: 'preserve',
              build_dir: join(directory, 'build scratch'),
            },
          },
        );
        expect(result.code, result.stderr).toBe(valid ? 0 : 1);
        expect(await readFile(observation, 'utf8')).toBe(
          valid ? 'build\ninspect\ncreate\n' : 'build\ninspect\n',
        );
        if (!valid)
          expect(result.stderr).toContain(
            `Built image platform ${platform} does not match linux/${arch}.`,
          );
      },
    );
  },
);
