import { describe, expect, it, vi } from 'vitest';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { Machine } from './api';
import type { ComputeSnapshot } from './compute-store';
import {
  createMachineDesktopIcons,
  createMachineDesktopMenu,
  machineDesktopId,
} from './desktop-icons';

const machine: Machine = {
  id: 'vm-one',
  hostId: 'host-one',
  spec: { name: 'Work VM' } as Machine['spec'],
  revision: 1,
  desired: 'stopped',
  status: 'stopped',
  ready: false,
  guestState: 'unavailable',
  reserved: false,
  createdAt: '2026-10-16T00:00:00Z',
  updatedAt: '2026-10-16T00:00:00Z',
};
const snapshot: ComputeSnapshot = {
  info: { configured: true, hostId: 'host-one' },
  machines: [machine],
  volumes: [],
  operations: [],
  images: [],
  loading: false,
  refreshing: false,
};

describe('VM desktop projections', () => {
  it('keeps positions stable through renames and status updates but never reuses another host identity', () => {
    const original = createMachineDesktopIcons(snapshot, () => null)[0];
    const renamed = {
      ...machine,
      spec: { ...machine.spec, name: 'Renamed VM' },
      status: 'running',
      ready: true,
    };
    const changed = createMachineDesktopIcons(
      { ...snapshot, machines: [renamed] },
      () => null,
    )[0];
    expect(changed.id).toBe(original.id);
    expect(changed).toMatchObject({
      label: 'Renamed VM',
      status: { label: 'Ready', tone: 'success' },
    });
    expect(machineDesktopId({ ...machine, hostId: 'host-two' })).not.toBe(
      original.id,
    );
    expect(changed.trashable).not.toBe(true);
  });

  it('opens only the exact inspector target and never creates a terminal or starts a VM', () => {
    const openApp = vi.fn();
    const desktop = { openApp } as unknown as DesktopAPI;
    const icons = createMachineDesktopIcons(snapshot, () => desktop);
    icons[0].onOpen?.();
    expect(openApp).toHaveBeenCalledExactlyOnceWith(
      'machine-inspector',
      { hostId: 'host-one', machineId: 'vm-one' },
      { title: 'Work VM — Inspector' },
    );
    const menu = createMachineDesktopMenu(icons, [icons[0].id]);
    expect(menu?.map((item) => item.label)).toEqual(['Open', 'Inspect']);
    expect(
      createMachineDesktopMenu(icons, [icons[0].id, 'document-one']),
    ).toBeUndefined();
  });

  it('retains offline icons as unavailable and removes only confirmed deleted records', () => {
    const icons = createMachineDesktopIcons(
      { ...snapshot, error: 'Offline' },
      () => null,
    );
    expect(icons[0]).toMatchObject({
      id: machineDesktopId(machine),
      status: { label: 'Unavailable', tone: 'warning' },
    });
    expect(
      createMachineDesktopIcons(
        { ...snapshot, machines: [{ ...machine, status: 'deleted' }] },
        () => null,
      ),
    ).toEqual([]);
  });

  it('orders icons by creation identity rather than inventory update order', () => {
    const other = {
      ...machine,
      id: 'vm-two',
      createdAt: '2026-10-17T00:00:00Z',
    };
    const icons = createMachineDesktopIcons(
      { ...snapshot, machines: [other, machine] },
      () => null,
    );
    expect(icons.map((icon) => icon.id)).toEqual([
      machineDesktopId(machine),
      machineDesktopId(other),
    ]);
  });
});
