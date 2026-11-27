import { useEffect, useMemo, useRef, useState } from 'react';
import { PixelIcon } from '@cofy-x/axv-nextstep';
import type { AppDefinition, DesktopAPI } from '@cofy-x/axv-nextstep';
import {
  codexAPI,
  isVMSession,
  sessionKey,
  sessionWindowTitle,
} from '../codex/api';
import type { CodexSessionRecord } from '../codex/api';
import { openCodexSession } from '../codex/definition';
import { vmCodexAPI } from '../codex/vm-api';
import { computeStore } from '../machines/compute-store';
import { openMachineInspector } from '../machines/desktop-icons';
import { humanize } from '../machines/presentation';
import { projectsAPI } from '../project-files/api';
import type { Project } from '../project-files/api';
import { projectLabel } from '../project-files/presentation';
import { vmProjectsAPI } from '../project-files/vm-api';
import type { VMProject } from '../project-files/vm-api';
import { filterQuickSwitcherItems } from './items';
import type { QuickSwitcherItem } from './items';
import './quick-switcher.css';

interface QuickSwitcherProps {
  apps: AppDefinition[];
  desktop: DesktopAPI;
  onClose: () => void;
}

export function QuickSwitcher({ apps, desktop, onClose }: QuickSwitcherProps) {
  const [query, setQuery] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [sessions, setSessions] = useState<CodexSessionRecord[]>([]);
  const [hostProjects, setHostProjects] = useState<Project[]>([]);
  const [vmProjects, setVMProjects] = useState<VMProject[]>([]);
  const inputRef = useRef<HTMLInputElement>(null);

  const compute = computeStore.getDesktopSnapshot();
  const hostId = compute.info?.configured ? compute.info.hostId || '' : '';

  useEffect(() => {
    inputRef.current?.focus();
    const controller = new AbortController();
    const codexVMs = compute.machines.filter(
      (machine) =>
        machine.status === 'running' &&
        machine.ready &&
        machine.guestCapabilities?.includes('codex'),
    );

    void Promise.allSettled([
      codexAPI.list(controller.signal),
      projectsAPI.list(),
      hostId
        ? vmProjectsAPI.list(hostId, controller.signal)
        : Promise.resolve({ projects: [] as VMProject[] }),
      Promise.all(
        codexVMs.map((machine) =>
          vmCodexAPI
            .list(
              {
                hostId: machine.hostId,
                machineId: machine.id,
                machineName: machine.spec.name || machine.id,
              },
              controller.signal,
            )
            .then((res) => res.sessions)
            .catch(() => [] as CodexSessionRecord[]),
        ),
      ),
    ]).then(([hostCodex, hostProj, vmProj, vmCodex]) => {
      if (controller.signal.aborted) return;
      const hostSessions =
        hostCodex.status === 'fulfilled' &&
        Array.isArray(hostCodex.value?.sessions)
          ? hostCodex.value.sessions
          : [];
      const vmSessions =
        vmCodex.status === 'fulfilled' ? vmCodex.value.flat() : [];
      setSessions([...hostSessions, ...vmSessions]);
      if (
        hostProj.status === 'fulfilled' &&
        Array.isArray(hostProj.value?.projects)
      )
        setHostProjects(hostProj.value.projects);
      if (
        vmProj.status === 'fulfilled' &&
        Array.isArray(vmProj.value?.projects)
      )
        setVMProjects(vmProj.value.projects);
    });

    return () => controller.abort();
  }, [compute.machines, hostId]);

  const allItems = useMemo<QuickSwitcherItem[]>(() => {
    const items: QuickSwitcherItem[] = [];

    for (const session of sessions) {
      if (session.archivedAt) continue;
      const needsAttention = !!session.activity?.needsAttention;
      if (
        !needsAttention &&
        session.status !== 'running' &&
        session.status !== 'starting'
      )
        continue;
      const vm = isVMSession(session);
      items.push({
        id: `codex:${sessionKey(session)}`,
        group: needsAttention ? 'Action needed' : 'Codex',
        title: sessionWindowTitle(session),
        subtitle: `${vm ? `VM · ${session.machineName || session.machineId}` : 'Host'} · ${session.projectPath}`,
        badge: needsAttention ? 'Action needed' : vm ? 'VM Codex' : 'Codex',
        keywords: `${session.projectPath} ${session.machineName || ''}`,
        icon: 'companion',
        onSelect: () => {
          openCodexSession(desktop, session);
          onClose();
        },
      });
    }

    for (const machine of compute.machines) {
      items.push({
        id: `vm:${machine.hostId}:${machine.id}`,
        group: 'Virtual Machines',
        title: machine.spec.name || machine.id,
        subtitle: `${humanize(machine.status)} · ${machine.spec.resources.cpus} CPU · ${machine.spec.resources.memoryMiB} MiB`,
        badge: 'VM',
        keywords: `${machine.id} ${machine.status} virtual machine`,
        icon: 'virtual-machine',
        onSelect: () => {
          openMachineInspector(desktop, machine);
          onClose();
        },
      });
    }

    for (const project of vmProjects) {
      items.push({
        id: `vm-project:${project.id}`,
        group: 'Projects',
        title: project.name,
        subtitle: `VM · /projects/${project.root}`,
        badge: 'VM Project',
        keywords: `${project.root} vm project`,
        icon: 'project',
        onSelect: () => {
          desktop.openApp(
            'project-files',
            { vmProjectId: project.id, hostId: project.hostId },
            { title: `VM · ${project.name}` },
          );
          onClose();
        },
      });
    }

    for (const project of hostProjects) {
      const label = projectLabel(project, hostProjects);
      items.push({
        id: `host-project:${project.id}`,
        group: 'Projects',
        title: label,
        subtitle: `Host · ${project.rootPath}`,
        badge: 'Host Project',
        keywords: `${project.rootPath} host project`,
        icon: 'folder',
        onSelect: () => {
          desktop.openApp(
            'project-files',
            { projectId: project.id },
            { title: label },
          );
          onClose();
        },
      });
    }

    for (const app of apps) {
      if (app.launchable === false || app.unavailable) continue;
      items.push({
        id: `app:${app.id}`,
        group: 'Applications',
        title: app.title,
        subtitle: 'Open application',
        badge: 'App',
        keywords: app.id,
        icon: app.icon,
        onSelect: () => {
          if (app.launch) void app.launch(desktop);
          else desktop.openApp(app.id);
          onClose();
        },
      });
    }

    items.push(
      {
        id: 'action:new-vm',
        group: 'Actions',
        title: 'New Virtual Machine',
        subtitle: 'Open Create VM wizard',
        badge: 'Action',
        keywords: 'create vm machine firecracker lima',
        icon: 'new-virtual-machine',
        onSelect: () => {
          desktop.openApp('machines', { section: 'create' });
          onClose();
        },
      },
      {
        id: 'action:new-terminal',
        group: 'Actions',
        title: 'New Host Terminal',
        subtitle: 'Launch a fresh host shell',
        badge: 'Action',
        keywords: 'shell bash terminal',
        icon: 'terminal',
        onSelect: () => {
          desktop.launchApp('terminal');
          onClose();
        },
      },
    );

    return items;
  }, [
    apps,
    compute.machines,
    desktop,
    hostProjects,
    onClose,
    sessions,
    vmProjects,
  ]);

  const filtered = useMemo(
    () => filterQuickSwitcherItems(allItems, query),
    [allItems, query],
  );
  const clampedIndex = Math.min(
    selectedIndex,
    Math.max(0, filtered.length - 1),
  );

  return (
    <div
      className="quick-switcher-backdrop"
      data-testid="quick-switcher"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div
        className="quick-switcher-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Quick Switcher"
        onKeyDown={(event) => {
          if (event.key === 'Escape') {
            event.preventDefault();
            event.stopPropagation();
            onClose();
          } else if (event.key === 'ArrowDown') {
            event.preventDefault();
            setSelectedIndex((index) =>
              filtered.length ? (index + 1) % filtered.length : 0,
            );
          } else if (event.key === 'ArrowUp') {
            event.preventDefault();
            setSelectedIndex((index) =>
              filtered.length
                ? (index - 1 + filtered.length) % filtered.length
                : 0,
            );
          } else if (event.key === 'Enter' && filtered[clampedIndex]) {
            event.preventDefault();
            filtered[clampedIndex].onSelect();
          }
        }}
      >
        <div className="quick-switcher-header">
          <input
            ref={inputRef}
            type="search"
            className="quick-switcher-input"
            placeholder="Jump to VM, project, Codex session, or app…"
            aria-label="Search workspace"
            value={query}
            onChange={(event) => {
              setQuery(event.target.value);
              setSelectedIndex(0);
            }}
          />
        </div>
        <ul
          className="quick-switcher-list"
          role="listbox"
          aria-label="Quick Switcher results"
        >
          {filtered.length === 0 ? (
            <li className="quick-switcher-empty">No matching items.</li>
          ) : (
            filtered.map((item, index) => {
              const selected = index === clampedIndex;
              return (
                <li key={item.id} role="option" aria-selected={selected}>
                  <button
                    type="button"
                    className={`quick-switcher-item${selected ? ' is-selected' : ''}`}
                    onMouseEnter={() => setSelectedIndex(index)}
                    onClick={item.onSelect}
                  >
                    <PixelIcon name={item.icon} size={18} />
                    <span className="quick-switcher-item-body">
                      <span className="quick-switcher-item-title">
                        {item.title}
                      </span>
                      {item.subtitle && (
                        <span className="quick-switcher-item-subtitle">
                          {item.subtitle}
                        </span>
                      )}
                    </span>
                    {item.badge && (
                      <span className="quick-switcher-badge">{item.badge}</span>
                    )}
                  </button>
                </li>
              );
            })
          )}
        </ul>
        <div className="quick-switcher-footer">
          <span>↑↓ Navigate · Enter Open · Esc Close</span>
          <span>⌘K / Ctrl+Shift+K</span>
        </div>
      </div>
    </div>
  );
}
