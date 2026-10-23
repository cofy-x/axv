import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  access,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { delimiter, join } from 'node:path';
import { promisify } from 'node:util';
import { describe, expect, it } from 'vitest';
import { root, spawnChild, stop } from './lib.mjs';

const execute = promisify(execFile);

describe.skipIf(process.platform === 'win32')(
  'dashboard measurement ownership',
  () => {
    it('rejects aliased output before writing outside the selected root or replacing a linked file', async () => {
      const directory = await mkdtemp(join(tmpdir(), 'measure-output-'));
      const selectedRoot = join(directory, 'test-root');
      const outside = join(directory, 'outside');
      const alias = join(selectedRoot, 'aliased-output');
      const validOutput = join(selectedRoot, 'valid-output');
      const saved = join(outside, 'saved.json');
      try {
        await mkdir(selectedRoot, { mode: 0o700 });
        await mkdir(outside, { mode: 0o700 });
        await mkdir(validOutput, { mode: 0o700 });
        await writeFile(saved, 'preserved previous evidence');
        await symlink(outside, alias);
        await symlink(saved, join(validOutput, 'measurements.json'));
        for (const output of [join(alias, 'new-report'), validOutput]) {
          await expect(
            execute(
              process.execPath,
              [
                join(root, 'scripts/measure-dashboard.mjs'),
                '--binary',
                join(directory, 'missing-binary'),
                '--output',
                output,
                '--samples',
                '1',
              ],
              {
                cwd: root,
                env: { ...process.env, AXV_TEST_TMPDIR: selectedRoot },
              },
            ),
          ).rejects.toMatchObject({
            code: 1,
            stderr: expect.stringContaining('symbolic links'),
          });
        }
        await expect(access(join(outside, 'new-report'))).rejects.toMatchObject(
          {
            code: 'ENOENT',
          },
        );
        expect(await readFile(saved, 'utf8')).toBe(
          'preserved previous evidence',
        );
      } finally {
        await rm(directory, { recursive: true, force: true });
      }
    });

    it('waits for interrupted acquisition, removes its late tmux fixture and preserves the frozen binary identity', async () => {
      const directory = await mkdtemp(join(tmpdir(), 'measure-startup-'));
      const bin = join(directory, 'bin');
      const gate = join(directory, 'gate');
      const release = join(directory, 'release');
      const socketsPath = join(directory, 'sockets');
      const source = join(directory, 'requested-axv');
      const launched = join(directory, 'unexpected-launch');
      const output = join(directory, 'report');
      const realTmux = (
        await execute('/bin/sh', ['-c', 'command -v tmux'])
      ).stdout.trim();
      const original =
        '#!/bin/sh\nprintf started > "$AXV_MEASURE_TEST_LAUNCHED"\nexit 97\n';
      let child;
      let stderr = '';
      let closed;
      const errors = [];
      try {
        await mkdir(bin);
        await writeFile(source, original, { mode: 0o700 });
        await writeFile(
          join(bin, 'tmux'),
          `#!/bin/sh
if [ "$1" = "-V" ]; then
  : > "$AXV_MEASURE_TEST_GATE"
  while [ ! -f "$AXV_MEASURE_TEST_RELEASE" ]; do sleep 0.01; done
fi
if [ "$1" = "-L" ]; then printf '%s\\n' "$2" >> "$AXV_MEASURE_TEST_SOCKETS"; fi
exec "$AXV_MEASURE_REAL_TMUX" "$@"
`,
          { mode: 0o700 },
        );
        child = spawnChild(
          process.execPath,
          [
            join(root, 'scripts/measure-dashboard.mjs'),
            '--binary',
            source,
            '--output',
            output,
            '--samples',
            '1',
          ],
          {
            cwd: root,
            ownProcessGroup: true,
            stdio: ['ignore', 'ignore', 'pipe'],
            env: {
              ...process.env,
              PATH: `${bin}${delimiter}${process.env.PATH}`,
              AXV_MEASURE_REAL_TMUX: realTmux,
              AXV_MEASURE_TEST_GATE: gate,
              AXV_MEASURE_TEST_RELEASE: release,
              AXV_MEASURE_TEST_SOCKETS: socketsPath,
              AXV_MEASURE_TEST_LAUNCHED: launched,
            },
          },
        );
        child.stderr.setEncoding('utf8');
        child.stderr.on('data', (chunk) => {
          stderr += chunk;
        });
        closed = new Promise((resolve, reject) => {
          child.once('error', reject);
          child.once('exit', (code, signal) => resolve({ code, signal }));
        });
        await expect
          .poll(async () =>
            access(gate).then(
              () => true,
              () => false,
            ),
          )
          .toBe(true);
        // Replace the source while setup owns the already captured executable.
        await writeFile(source, '#!/bin/sh\nexit 98\n');
        child.kill('SIGINT');
        await expect
          .poll(() => stderr.includes('Measurement interrupted (SIGINT)'))
          .toBe(true);
        await writeFile(release, 'continue');
        await expect(closed).resolves.toEqual({ code: 130, signal: null });
        const report = JSON.parse(
          await readFile(join(output, 'measurements.json'), 'utf8'),
        );
        expect(report.successful).toBe(false);
        expect(report.harnessSHA256).toBe(report.harnessSources[0].sha256);
        expect(report.harnessSources).toEqual(
          expect.arrayContaining([
            {
              path: 'scripts/lib.mjs',
              sha256: createHash('sha256')
                .update(await readFile(join(root, 'scripts/lib.mjs')))
                .digest('hex'),
            },
            {
              path: 'scripts/test-output.mjs',
              sha256: createHash('sha256')
                .update(await readFile(join(root, 'scripts/test-output.mjs')))
                .digest('hex'),
            },
          ]),
        );
        expect(report.harnessSourcesSHA256).toBe(
          createHash('sha256')
            .update(JSON.stringify(report.harnessSources))
            .digest('hex'),
        );
        expect(report.binary.sha256).toBe(
          createHash('sha256').update(original).digest('hex'),
        );
        expect(report.measurements).toEqual([]);
        await expect(access(launched)).rejects.toMatchObject({
          code: 'ENOENT',
        });
        const sockets = [
          ...new Set((await readFile(socketsPath, 'utf8')).trim().split('\n')),
        ];
        expect(sockets).toHaveLength(1);
        for (const socket of sockets) {
          expect(socket).toMatch(/^axv-test-dashboard-runtime-/);
          await expect(
            execute(realTmux, ['-L', socket, 'list-sessions']),
          ).rejects.toBeDefined();
          await expect(
            access(
              join(
                process.env.AXV_TEST_TMPDIR,
                socket.slice('axv-test-'.length),
              ),
            ),
          ).rejects.toMatchObject({ code: 'ENOENT' });
        }
        const evidence = await readFile(
          join(output, 'measurements.json'),
          'utf8',
        );
        await expect(
          execute(
            process.execPath,
            [
              join(root, 'scripts/measure-dashboard.mjs'),
              '--binary',
              source,
              '--output',
              output,
              '--samples',
              '1',
            ],
            { cwd: root },
          ),
        ).rejects.toMatchObject({ code: 1 });
        expect(await readFile(join(output, 'measurements.json'), 'utf8')).toBe(
          evidence,
        );
      } catch (error) {
        errors.push(error);
      } finally {
        await writeFile(release, 'continue');
        if (child) await stop(child);
        const recorded = await readFile(socketsPath, 'utf8').catch(() => '');
        for (const socket of new Set(recorded.trim().split('\n'))) {
          if (!socket.startsWith('axv-test-dashboard-runtime-')) continue;
          try {
            await execute(realTmux, ['-L', socket, 'kill-server']);
          } catch (error) {
            if (
              !/no server running|No such file|Connection refused/.test(
                error.stderr || '',
              )
            )
              errors.push(error);
          }
        }
        await rm(directory, { recursive: true, force: true });
      }
      if (errors.length)
        throw new AggregateError(
          errors,
          'Measurement ownership integration failed.',
        );
    }, 15_000);
  },
);
