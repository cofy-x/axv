import { createHash } from 'node:crypto';
import { readFile, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureCommand, dispose, root, scratch } from './lib.mjs';

const schema = 'axv-tsnet-host-v1';
const volatileKeys = new Set([
  'valid_life_time',
  'preferred_life_time',
  'expires',
  'cache',
  'stats',
  'stats64',
  'packets',
  'bytes',
  'used',
  'lastuse',
]);

export function normalizeNetwork(value) {
  if (Array.isArray(value)) {
    // Rule and expression ordering can change firewall behavior.
    return value.map(normalizeNetwork);
  }
  if (value && typeof value === 'object') {
    return Object.fromEntries(
      Object.keys(value)
        .filter((key) => !volatileKeys.has(key))
        .sort()
        .map((key) => [key, normalizeNetwork(value[key])]),
    );
  }
  return value;
}

export function normalizeIPTables(value) {
  return value
    .split('\n')
    .filter((line) => line.trim() && !line.startsWith('#'))
    .map((line) => line.replace(/\[\d+:\d+\]/g, '[counters]'))
    .join('\n');
}

function fingerprint(value) {
  return createHash('sha256').update(JSON.stringify(value)).digest('hex');
}

export function parseNameservers(value) {
  return [
    ...new Set(
      value
        .split('\n')
        .map((line) => /^\s*nameserver\s+([^\s#;]+)/.exec(line)?.[1])
        .filter(Boolean),
    ),
  ].sort();
}

function cgnat(address) {
  const octets = address.split('.').map(Number);
  return (
    octets.length === 4 &&
    octets[0] === 100 &&
    octets[1] >= 64 &&
    octets[1] <= 127
  );
}

function failureCode(error) {
  return error.code === 'ENOENT' || error.cause?.code === 'ENOENT'
    ? 'command-not-found'
    : error.timedOut
      ? 'timeout'
      : 'command-failed';
}

async function run(program, args, options = {}) {
  try {
    return await captureCommand(program, args, {
      allowFailure: true,
      timeout: 20_000,
      ...options,
    });
  } catch (error) {
    // Raw command errors may contain host addresses, usernames or remote URLs.
    return { code: null, stdout: '', error: failureCode(error) };
  }
}

async function networkCheck(program, args, json = true) {
  const result = await run(program, args);
  if (result.code !== 0)
    return {
      available: false,
      error: result.error || 'command-failed',
      exitCode: result.code,
    };
  try {
    const value = json
      ? normalizeNetwork(JSON.parse(result.stdout))
      : normalizeIPTables(result.stdout);
    return { available: true, fingerprint: fingerprint(value) };
  } catch {
    return { available: false, error: 'invalid-command-output' };
  }
}

async function serviceCheck(operation) {
  const result = await run('systemctl', [operation, 'tailscaled']);
  const state = result.stdout.trim();
  const valid =
    operation === 'is-active'
      ? [
          'active',
          'inactive',
          'failed',
          'activating',
          'deactivating',
          'unknown',
        ]
      : [
          'enabled',
          'disabled',
          'static',
          'masked',
          'indirect',
          'generated',
          'not-found',
          'enabled-runtime',
          'masked-runtime',
        ];
  return valid.includes(state)
    ? { available: true, state }
    : { available: false, error: result.error || 'unknown-service-state' };
}

async function dnsCheck(name) {
  let content;
  try {
    content = await readFile('/etc/resolv.conf', 'utf8');
  } catch {
    return {
      configuration: { available: false, error: 'resolv-conf-unreadable' },
      system: { ok: false },
    };
  }
  const nameservers = parseNameservers(content);
  const result = await run('getent', ['ahosts', name]);
  const addresses = [
    ...new Set(
      result.stdout
        .split('\n')
        .map((line) => line.trim().split(/\s+/)[0])
        .filter(Boolean),
    ),
  ];
  const direct = await Promise.all(
    nameservers.map(async (server) => {
      const answer = await run('dig', [
        '+timeout=3',
        '+tries=1',
        '+short',
        `@${server}`,
        name,
        'A',
      ]);
      return {
        serverFingerprint: fingerprint(server),
        cgnat: cgnat(server),
        available: !answer.error,
        ok:
          answer.code === 0 &&
          answer.stdout
            .split('\n')
            .some((line) => /^\d+\.\d+\.\d+\.\d+$/.test(line.trim())),
        ...(answer.error ? { error: answer.error } : {}),
      };
    }),
  );
  return {
    configuration: {
      available: true,
      fingerprint: fingerprint(content),
      nameserverCount: nameservers.length,
      cgnatNameserverCount: nameservers.filter(cgnat).length,
      nameserverFingerprints: nameservers.map(fingerprint),
    },
    system: {
      ok: result.code === 0 && addresses.length > 0,
      addressCount: addresses.length,
    },
    direct,
  };
}

async function gitCheck(directory) {
  const result = await run(
    'git',
    ['--no-optional-locks', '-C', directory, 'ls-remote', 'origin', 'HEAD'],
    {
      timeout: 30_000,
      env: {
        ...process.env,
        GIT_TERMINAL_PROMPT: '0',
        GIT_OPTIONAL_LOCKS: '0',
        GIT_SSH_COMMAND: `${process.env.GIT_SSH_COMMAND || 'ssh'} -oBatchMode=yes -oStrictHostKeyChecking=yes -oUpdateHostKeys=no -oConnectTimeout=10`,
      },
    },
  );
  return {
    ok: result.code === 0 && /^[a-f0-9]{40,64}\s+HEAD\s*$/m.test(result.stdout),
    ...(result.error ? { error: result.error } : {}),
    exitCode: result.code,
  };
}

export function assessCapture(capture) {
  const unavailable = Object.entries(capture.network)
    .filter(([, item]) => !item.available)
    .map(([name]) => name);
  const firewallAvailable =
    capture.network.nft.available ||
    (capture.network.iptables4.available &&
      capture.network.iptables6.available);
  const complete =
    ['interfaces', 'routes4', 'routes6', 'rules4', 'rules6'].every(
      (key) => capture.network[key].available,
    ) &&
    firewallAvailable &&
    capture.service.active.available &&
    capture.service.enabled.available &&
    capture.dns.configuration.available;
  return {
    complete,
    unavailable,
    systemTailscaleStoppedAndDisabled:
      capture.service.active.state === 'inactive' &&
      capture.service.enabled.state === 'disabled',
    dnsOK: capture.dns.system.ok && capture.dns.direct.every((item) => item.ok),
    gitOK: capture.git.ok,
  };
}

export async function captureHost({
  gitDirectory = root,
  dnsName = 'github.com',
} = {}) {
  const jobs = {
    interfaces: ['ip', ['-j', 'address', 'show']],
    routes4: ['ip', ['-j', '-4', 'route', 'show', 'table', 'all']],
    routes6: ['ip', ['-j', '-6', 'route', 'show', 'table', 'all']],
    rules4: ['ip', ['-j', '-4', 'rule', 'show']],
    rules6: ['ip', ['-j', '-6', 'rule', 'show']],
    iptables4: ['iptables-save', [], false],
    iptables6: ['ip6tables-save', [], false],
    nft: ['nft', ['--json', 'list', 'ruleset']],
  };
  const [network, active, enabled, dns, git] = await Promise.all([
    Promise.all(
      Object.entries(jobs).map(async ([key, args]) => [
        key,
        await networkCheck(...args),
      ]),
    ),
    serviceCheck('is-active'),
    serviceCheck('is-enabled'),
    dnsCheck(dnsName),
    gitCheck(gitDirectory),
  ]);
  const capture = {
    schema,
    capturedAt: new Date().toISOString(),
    platform: process.platform,
    inputs: {
      gitDirectoryFingerprint: fingerprint(resolve(gitDirectory)),
      dnsName,
    },
    service: { active, enabled },
    network: Object.fromEntries(network),
    dns,
    git,
  };
  capture.assessment = assessCapture(capture);
  return capture;
}

export function compareCaptures(before, after) {
  if (before.schema !== schema || after.schema !== schema)
    throw new Error('Unsupported host baseline schema.');
  const changed = [];
  const unavailable = [];
  for (const key of Object.keys(before.network)) {
    const a = before.network[key],
      b = after.network[key];
    if (!a?.available || !b?.available) unavailable.push(key);
    if (a?.available !== b?.available || a?.fingerprint !== b?.fingerprint)
      changed.push(key);
  }
  if (JSON.stringify(before.inputs) !== JSON.stringify(after.inputs))
    changed.push('inputs');
  if (JSON.stringify(before.service) !== JSON.stringify(after.service))
    changed.push('service');
  if (
    before.dns.configuration.fingerprint !== after.dns.configuration.fingerprint
  )
    changed.push('dns-configuration');
  const checks = [assessCapture(before), assessCapture(after)];
  return {
    schema: 'axv-tsnet-host-comparison-v1',
    comparedAt: new Date().toISOString(),
    ok:
      changed.length === 0 &&
      checks.every(
        (item) =>
          item.complete &&
          item.systemTailscaleStoppedAndDisabled &&
          item.dnsOK &&
          item.gitOK,
      ),
    networkUnchanged: changed.length === 0,
    changed,
    unavailable,
    before: checks[0],
    after: checks[1],
    note: 'DNS answer IPs and Git HEAD may rotate; availability must succeed before and after. Firewall counters, address lifetimes and route cache metadata are excluded.',
  };
}

async function save(path, value) {
  await writeFile(path, `${JSON.stringify(value, null, 2)}\n`, {
    flag: 'wx',
    mode: 0o600,
  });
}

async function main() {
  const args = process.argv.slice(2);
  const action = args.shift();
  const options = {};
  while (args.length) {
    const key = args.shift();
    if (
      !['--before', '--output', '--report', '--git-dir', '--dns-name'].includes(
        key,
      ) ||
      !args.length
    )
      throw new Error(
        'Usage: verify-tsnet-host.mjs capture|compare [--before FILE] [--output FILE] [--report FILE] [--git-dir DIR] [--dns-name NAME]',
      );
    options[key] = args.shift();
  }
  if (
    !['capture', 'compare'].includes(action) ||
    (action === 'compare' && !options['--before'])
  )
    throw new Error('Choose capture, or compare --before FILE.');
  if (process.platform !== 'linux')
    throw new Error('This host baseline currently supports Linux.');
  process.env.AXV_TEST_TMPDIR ||= join(root, '.data', 'test', 'tsnet');
  const directory = await scratch('host-');
  const output = resolve(
    options['--output'] || join(directory, `${action}.json`),
  );
  const before =
    action === 'compare'
      ? JSON.parse(await readFile(resolve(options['--before']), 'utf8'))
      : null;
  const capture = await captureHost({
    gitDirectory: options['--git-dir'] || root,
    dnsName: options['--dns-name'] || before?.inputs.dnsName || 'github.com',
  });
  await save(output, capture);
  if (before) {
    const report = compareCaptures(before, capture);
    const reportPath = resolve(
      options['--report'] || join(dirname(output), 'comparison.json'),
    );
    await save(reportPath, report);
    console.log(JSON.stringify({ output, reportPath, ...report }, null, 2));
    if (!report.ok) process.exitCode = 1;
  } else {
    console.log(JSON.stringify({ output, ...capture.assessment }, null, 2));
    const check = capture.assessment;
    if (
      !check.complete ||
      !check.systemTailscaleStoppedAndDisabled ||
      !check.dnsOK ||
      !check.gitOK
    )
      process.exitCode = 1;
  }
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  for (const [signal, code] of [
    ['SIGINT', 130],
    ['SIGTERM', 143],
  ]) {
    process.once(signal, () => {
      void dispose().then(
        () => process.exit(code),
        () => process.exit(1),
      );
    });
  }
  try {
    await main();
  } catch (error) {
    console.error(
      error.code === 'EEXIST'
        ? 'Refusing to overwrite existing verification evidence.'
        : 'Host baseline failed; command output has been withheld to avoid exposing host details.',
    );
    process.exitCode = 1;
  } finally {
    await dispose();
  }
}
