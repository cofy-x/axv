import { mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import http from 'node:http';
import { afterEach, describe, expect, it } from 'vitest';
import {
  acceptanceOptions,
  hostJSON,
  machineJSONArguments,
  ownedProtectedProjectVolumes,
  runAcceptanceCommand,
  sha256,
  verifiedBundle,
} from './verify-vm-firecracker.mjs';

const directories = [];
const explicitArguments = [
  '--yes',
  '--image-bundle',
  '/images/manifest.json',
  '--kernel',
  '/images/vmlinux',
  '--kernel-sha256',
  'a'.repeat(64),
  '--firecracker',
  '/tools/firecracker',
  '--jailer',
  '/tools/jailer',
  '--version',
  '1.17.0',
  '--subnet',
  '172.30.0.0/22',
];

afterEach(async () => {
  await Promise.all(
    directories
      .splice(0)
      .map((path) => rm(path, { recursive: true, force: true })),
  );
});

describe('native acceptance interruption and exact host binding', () => {
  it('keeps machine-readable CLI flags outside the guest command argv', () => {
    expect(
      machineJSONArguments(['project', 'task', 'list', 'project-1']),
    ).toEqual(['project', 'task', 'list', 'project-1', '--json']);
    expect(
      machineJSONArguments([
        'project',
        'task',
        'submit',
        'project-1',
        '--',
        'python3',
        '--json',
        'value',
      ]),
    ).toEqual([
      'project',
      'task',
      'submit',
      'project-1',
      '--json',
      '--',
      'python3',
      '--json',
      'value',
    ]);
  });

  it('waits for interrupted command cleanup before allowing the caller to remove its fixture', async () => {
    const directory = await mkdtemp(join(tmpdir(), 'axv-fc-abort-'));
    directories.push(directory);
    const stopped = join(directory, 'stopped');
    const controller = new AbortController();
    let announce;
    const ready = new Promise((resolve) => {
      announce = resolve;
    });
    let nextPhase = false;
    const command = runAcceptanceCommand(
      process.execPath,
      [
        '-e',
        'const fs=require("node:fs"); process.on("SIGTERM",()=>{ fs.writeFileSync(process.argv[1],"stopped"); process.exit(0); }); process.stderr.write("READY\\n"); setInterval(()=>{},1000);',
        stopped,
      ],
      {
        cwd: directory,
        signal: controller.signal,
        onStderr: (text) => {
          if (text.includes('READY')) announce();
        },
      },
    ).then(() => {
      nextPhase = true;
    });
    await ready;
    controller.abort(new Error('Explicit test interruption'));
    await expect(command).rejects.toThrow(/Explicit test interruption/);
    expect(await readFile(stopped, 'utf8')).toBe('stopped');
    expect(nextPhase).toBe(false);
  });

  async function hostFixture({ closeRuntime = false, wrongHost = false } = {}) {
    // The native Unix boundary needs its own short socket path; other fixtures
    // remain under the verification launcher's test root.
    const directory = await mkdtemp('/tmp/axv-host-test-');
    directories.push(directory);
    const socket = join(directory, 'c.sock');
    const hostId = `host-${'a'.repeat(32)}`;
    const runtime = {
      ready: true,
      protocolVersion: 3,
      configFingerprint: 'original-config',
      hostId: wrongHost ? `host-${'b'.repeat(32)}` : hostId,
    };
    const requests = [];
    const server = http.createServer((request, response) => {
      requests.push({
        path: request.url,
        connection: request.socket,
        hostId: request.headers['x-axv-host-id'],
      });
      if (closeRuntime && request.url === '/v1/runtime')
        response.setHeader('Connection', 'close');
      response.setHeader('Content-Type', 'application/json');
      response.end(
        JSON.stringify(
          request.url === '/v1/runtime' ? runtime : { accepted: true },
        ),
      );
    });
    await new Promise((resolve, reject) => {
      server.once('error', reject);
      server.listen(socket, resolve);
    });
    return {
      socket,
      hostId,
      requests,
      close: () =>
        new Promise((resolve) => {
          server.closeAllConnections();
          server.close(resolve);
        }),
    };
  }

  it('checks runtime and sends a mutation on the same verified Unix connection', async () => {
    const host = await hostFixture();
    try {
      await expect(
        hostJSON(
          {
            socket: host.socket,
            hostId: host.hostId,
            configFingerprint: 'original-config',
          },
          '/machines',
          'POST',
          { requestId: 'original-request' },
        ),
      ).resolves.toEqual({ accepted: true });
      expect(host.requests.map((request) => request.path)).toEqual([
        '/v1/runtime',
        '/v1/machines',
      ]);
      expect(host.requests[0].connection).toBe(host.requests[1].connection);
      expect(host.requests[1].hostId).toBe(host.hostId);
    } finally {
      await host.close();
    }
  });

  it('does not send a mutation after host replacement or a runtime connection closure', async () => {
    for (const options of [{ wrongHost: true }, { closeRuntime: true }]) {
      const host = await hostFixture(options);
      try {
        await expect(
          hostJSON(
            { socket: host.socket, hostId: host.hostId },
            '/machines',
            'POST',
            { requestId: 'must-not-submit' },
          ),
        ).rejects.toThrow();
        expect(host.requests.map((request) => request.path)).toEqual([
          '/v1/runtime',
        ]);
      } finally {
        await host.close();
      }
    }
  });
});

async function fixture(change = {}) {
  const directory = await mkdtemp(join(tmpdir(), 'axv-fc-input-'));
  directories.push(directory);
  const rootfs = join(directory, 'root.ext4');
  await writeFile(rootfs, 'Immutable native acceptance fixture');
  const manifest = {
    version: 1,
    id: 'axv-native-test',
    name: 'AXV native test',
    provider: 'firecracker',
    arch: 'amd64',
    guest: {
      imageVersion: 1,
      protocolVersion: 3,
      capabilities: [
        'exec',
        'terminals',
        'ssh',
        'network',
        'project-files',
        'project-tasks',
      ],
    },
    rootfs: { file: 'root.ext4', sha256: await sha256(rootfs) },
    ...change,
  };
  const path = join(directory, 'manifest.json');
  await writeFile(path, JSON.stringify(manifest));
  return { directory, path, rootfs, manifest };
}

describe('native Firecracker acceptance authorization and inputs', () => {
  it('requires explicit authorization before using any path or native resource', () => {
    expect(() => acceptanceOptions(explicitArguments.slice(1))).toThrow(
      /--yes/,
    );
    expect(acceptanceOptions(['--help']).help).toBe(true);
    expect(() =>
      acceptanceOptions([...explicitArguments, '--download']),
    ).toThrow();
    const options = acceptanceOptions(explicitArguments);
    expect(options.uid).toBe(60000);
    expect(options.gid).toBe(60000);
    expect(options['real-codex']).toBe(false);
    expect(
      acceptanceOptions([...explicitArguments, '--real-codex'])['real-codex'],
    ).toBe(true);
    expect(() =>
      acceptanceOptions([...explicitArguments.slice(1), '--real-codex']),
    ).toThrow(/--yes/);
  });

  it('rejects relative inputs, unchecked kernels and root jailer identities', () => {
    const relative = [...explicitArguments];
    relative[2] = 'relative/manifest.json';
    expect(() => acceptanceOptions(relative)).toThrow(/absolute/);
    const unchecked = [...explicitArguments];
    unchecked[6] = 'not-a-checksum';
    expect(() => acceptanceOptions(unchecked)).toThrow(/kernel-sha256/);
    expect(() =>
      acceptanceOptions([...explicitArguments, '--uid', '0']),
    ).toThrow(/jailer range/);
  });
});

describe('native acceptance artifact integrity', () => {
  it('verifies the rootfs without changing its contents or the manifest', async () => {
    const input = await fixture();
    const before = await readFile(input.path, 'utf8');
    const bundle = await verifiedBundle(input.path, 'amd64');
    expect(bundle.rootfsPath).toBe(input.rootfs);
    expect(await sha256(input.rootfs)).toBe(input.manifest.rootfs.sha256);
    expect(await readFile(input.path, 'utf8')).toBe(before);
    await writeFile(input.rootfs, 'Damaged image');
    await expect(verifiedBundle(input.path, 'amd64')).rejects.toThrow(
      /checksum/,
    );
  });

  it('rejects escaped or symlinked rootfs artifacts and mismatched architecture', async () => {
    const escaped = await fixture({
      rootfs: { file: '../root.ext4', sha256: 'a'.repeat(64) },
    });
    await expect(verifiedBundle(escaped.path, 'amd64')).rejects.toThrow(
      /filename beside/,
    );
    const linked = await fixture();
    await rm(linked.rootfs);
    const outside = join(linked.directory, 'different.ext4');
    await writeFile(outside, 'Not the declared regular file');
    await symlink(outside, linked.rootfs);
    await expect(verifiedBundle(linked.path, 'amd64')).rejects.toThrow(
      /regular file/,
    );
    const wrongArch = await fixture({ arch: 'arm64' });
    await expect(verifiedBundle(wrongArch.path, 'amd64')).rejects.toThrow();
  });

  it('requires the enhanced guest contract that the native assertions exercise', async () => {
    const basic = await fixture({ guest: undefined });
    await expect(verifiedBundle(basic.path, 'amd64')).rejects.toThrow();
    const older = await fixture({
      guest: {
        imageVersion: 1,
        protocolVersion: 2,
        capabilities: ['exec', 'terminals', 'ssh', 'network'],
      },
    });
    await expect(verifiedBundle(older.path, 'amd64')).rejects.toThrow();
    const limited = await fixture({
      guest: { imageVersion: 1, protocolVersion: 3, capabilities: ['exec'] },
    });
    await expect(verifiedBundle(limited.path, 'amd64')).rejects.toThrow(
      /requires terminals/,
    );
    const withoutProjectFiles = await fixture({
      guest: {
        imageVersion: 1,
        protocolVersion: 3,
        capabilities: ['exec', 'terminals', 'ssh', 'network'],
      },
    });
    await expect(
      verifiedBundle(withoutProjectFiles.path, 'amd64'),
    ).rejects.toThrow(/requires project-files/);
    const withoutProjectTasks = await fixture({
      guest: {
        imageVersion: 1,
        protocolVersion: 3,
        capabilities: ['exec', 'terminals', 'ssh', 'network', 'project-files'],
      },
    });
    await expect(
      verifiedBundle(withoutProjectTasks.path, 'amd64'),
    ).rejects.toThrow(/requires project-tasks/);
  });
});

describe('registered project volume native cleanup boundary', () => {
  const owned = {
    hostId: 'host-owned',
    projects: [
      {
        id: 'project-owned',
        hostId: 'host-owned',
        volumeId: 'volume-owned',
        root: 'workspace',
      },
    ],
    volumes: [{ id: 'volume-owned', retain: true, status: 'ready' }],
    machines: [],
    ownedMachines: [{ id: 'vm-owned', volumeId: 'volume-owned' }],
    registrations: [
      { id: 'project-owned', volumeId: 'volume-owned', root: 'workspace' },
    ],
  };

  it('leaves only its detached registered volume for final stopped-host directory cleanup', () => {
    expect([...ownedProtectedProjectVolumes(owned)]).toEqual(['volume-owned']);
    // A registration response may have been lost after acceptance. The original
    // saved volume/root still identifies this invocation's request.
    expect([
      ...ownedProtectedProjectVolumes({
        ...owned,
        registrations: [{ volumeId: 'volume-owned', root: 'workspace' }],
      }),
    ]).toEqual(['volume-owned']);
  });

  it('retains state when VM liveness, attachment or project ownership is uncertain', () => {
    for (const change of [
      { machines: [{ id: 'vm-owned', status: 'unknown' }] },
      { volumes: [{ ...owned.volumes[0], machineId: 'vm-owned' }] },
      { volumes: [{ ...owned.volumes[0], pendingMachineId: 'vm-other' }] },
      { volumes: [{ ...owned.volumes[0], pendingAction: 'detach' }] },
      { volumes: [{ ...owned.volumes[0], pendingSizeGiB: 2 }] },
      { volumes: [{ ...owned.volumes[0], status: 'unknown' }] },
      { projects: [{ ...owned.projects[0], hostId: 'host-other' }] },
      { registrations: [] },
      { ownedMachines: [] },
    ]) {
      expect(() =>
        ownedProtectedProjectVolumes({ ...owned, ...change }),
      ).toThrow();
    }
  });
});
