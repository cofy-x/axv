import { describe, expect, it } from 'vitest';
import {
  assessCapture,
  compareCaptures,
  normalizeIPTables,
  normalizeNetwork,
  parseNameservers,
} from './verify-tsnet-host.mjs';

function baseline() {
  const network = Object.fromEntries(
    [
      'interfaces',
      'routes4',
      'routes6',
      'rules4',
      'rules6',
      'iptables4',
      'iptables6',
      'nft',
    ].map((key) => [key, { available: true, fingerprint: key }]),
  );
  return {
    schema: 'axv-tsnet-host-v1',
    inputs: { dnsName: 'github.com' },
    service: {
      active: { available: true, state: 'inactive' },
      enabled: { available: true, state: 'disabled' },
    },
    network,
    dns: {
      configuration: { available: true, fingerprint: 'resolv-conf' },
      system: { ok: true },
      direct: [{ ok: true }],
    },
    git: { ok: true },
  };
}

describe('tsnet host evidence', () => {
  it('ignores traffic counters and lease time while retaining routing and firewall changes', () => {
    const first = {
      addr_info: [
        {
          local: '100.100.2.136',
          valid_life_time: 3500,
          preferred_life_time: 3500,
        },
      ],
      rule: { counter: { packets: 5, bytes: 180 }, verdict: 'accept' },
    };
    const next = structuredClone(first);
    next.addr_info[0].valid_life_time = 3400;
    next.rule.counter.packets = 10;
    expect(normalizeNetwork(first)).toEqual(normalizeNetwork(next));
    next.rule.verdict = 'drop';
    expect(normalizeNetwork(first)).not.toEqual(normalizeNetwork(next));
    next.rule.verdict = 'accept';
    next.addr_info[0].local = '100.100.2.138';
    expect(normalizeNetwork(first)).not.toEqual(normalizeNetwork(next));
  });

  it('drops iptables timestamps and counters, retaining rule ordering and verdicts', () => {
    const first =
      '# Generated at time A\n*filter\n:INPUT ACCEPT [1:20]\n-A INPUT -j DROP\nCOMMIT\n';
    const next = first.replace('time A', 'time B').replace('[1:20]', '[9:99]');
    expect(normalizeIPTables(first)).toEqual(normalizeIPTables(next));
    expect(normalizeIPTables(first)).not.toEqual(
      normalizeIPTables(next.replace('-j DROP', '-j ACCEPT')),
    );
    expect(
      normalizeNetwork([{ verdict: 'accept' }, { verdict: 'drop' }]),
    ).not.toEqual(
      normalizeNetwork([{ verdict: 'drop' }, { verdict: 'accept' }]),
    );
  });

  it('extracts only configured nameservers', () => {
    expect(
      parseNameservers(
        '# nameserver secret\nsearch private.example\nnameserver 100.100.2.136\nnameserver 100.100.2.138 # provider\nnameserver 100.100.2.136\n',
      ),
    ).toEqual(['100.100.2.136', '100.100.2.138']);
  });

  it('requires before/after availability and a stopped, disabled system daemon', () => {
    const before = baseline();
    const after = structuredClone(before);
    expect(compareCaptures(before, after).ok).toBe(true);
    after.dns.system.ok = false;
    expect(compareCaptures(before, after)).toMatchObject({
      ok: false,
      networkUnchanged: true,
      after: { dnsOK: false },
    });
    after.dns.system.ok = true;
    after.service.active.state = 'active';
    expect(compareCaptures(before, after)).toMatchObject({
      ok: false,
      changed: ['service'],
    });
  });

  it('does not call missing network/firewall evidence an unchanged success', () => {
    const before = baseline();
    before.network.nft = { available: false };
    expect(assessCapture(before).complete).toBe(true);
    before.network.iptables4 = { available: false };
    expect(assessCapture(before).complete).toBe(false);
    expect(compareCaptures(before, structuredClone(before))).toMatchObject({
      ok: false,
      unavailable: ['iptables4', 'nft'],
    });
  });

  it('detects route fingerprints and nameserver configuration changes', () => {
    const before = baseline();
    const after = structuredClone(before);
    after.network.routes4.fingerprint = 'new-route';
    after.dns.configuration.fingerprint = 'new-nameserver';
    expect(compareCaptures(before, after)).toMatchObject({
      ok: false,
      changed: ['routes4', 'dns-configuration'],
    });
  });
});
