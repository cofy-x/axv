import type { ReactNode } from 'react';
import { Button, Field, PixelIcon, Select } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { Machine, Operation, Volume } from './api';
import type { VMAction } from './lifecycle';
import { activeVMOperation } from './lifecycle';
import { MachineActions, MachineExpiry } from './MachineActions';
import { VMOperationNotice } from './OperationFeedback';
import { openMachineInspector } from './desktop-icons';
import { canOpenVMProjectVolume } from '../project-files/vm-project-volume';
import {
  humanize,
  linuxVMSetupGuide,
  machineGuestStatus,
  machineTransition,
} from './presentation';

export interface MachinesGridViewProps {
  desktop: DesktopAPI;
  windowId: string;
  machines: Machine[];
  visible: Machine[];
  volumes: Volume[];
  operations: Operation[];
  selected?: Machine;
  search: string;
  stateFilter: string;
  loading: boolean;
  error?: string;
  transition?: string;
  notice?: Operation;
  operationId?: string;
  feedbackInSelection: boolean;
  actionFeedback: ReactNode;
  guidance?: string;
  limaShellNotice?: { key: string; copied: boolean; command: string };
  selectedKey: string;
  emptyInventory: boolean;
  firecrackerImageRequired: boolean;
  availableProvidersCount: number;
  hasReadyImage: boolean;
  canCreate: boolean;
  canImportImages: boolean;
  actionReason: (action: VMAction) => string | undefined;
  terminalReason?: string;
  terminalPending: boolean;
  canCopyLimaShell: boolean;
  copyingLimaShell: boolean;
  onSearchChange: (value: string) => void;
  onStateFilterChange: (value: string) => void;
  onLifecycle: (action: VMAction) => void;
  onTerminal: () => void;
  onCopyLimaShell: () => void;
  onInspect: () => void;
  onShowActivity: (id: string) => void;
  onOpenImages: () => void;
  onShowHost: () => void;
}

export function MachinesGridView({
  desktop,
  windowId,
  machines,
  visible,
  volumes,
  operations,
  selected,
  search,
  stateFilter,
  loading,
  error,
  transition,
  notice,
  operationId,
  feedbackInSelection,
  actionFeedback,
  guidance,
  limaShellNotice,
  selectedKey,
  emptyInventory,
  firecrackerImageRequired,
  availableProvidersCount,
  hasReadyImage,
  canCreate,
  canImportImages,
  actionReason,
  terminalReason,
  terminalPending,
  canCopyLimaShell,
  copyingLimaShell,
  onSearchChange,
  onStateFilterChange,
  onLifecycle,
  onTerminal,
  onCopyLimaShell,
  onInspect,
  onShowActivity,
  onOpenImages,
  onShowHost,
}: MachinesGridViewProps) {
  return (
    <>
      {machines.length > 0 && (
        <div className="machines-filter">
          <Field
            label="Search VMs"
            placeholder="Filter by name…"
            value={search}
            onChange={(event) => onSearchChange(event.target.value)}
          />
          <Select
            label="State"
            value={stateFilter}
            onChange={(event) => onStateFilterChange(event.target.value)}
          >
            <option value="all">All states</option>
            {[
              ...new Set([
                ...machines.map((machine) => machine.status),
                ...(stateFilter === 'all' ? [] : [stateFilter]),
              ]),
            ]
              .sort()
              .map((state) => (
                <option key={state} value={state}>
                  {humanize(state)}
                </option>
              ))}
          </Select>
        </div>
      )}
      {selected && (
        <div
          className="machines-selection"
          role="group"
          aria-label="Selected VM actions"
        >
          <div className="machines-selection-heading">
            <div
              className="machines-selection-identity"
              data-status={transition ? 'transitioning' : selected.status}
            >
              <strong title={selected.spec.name}>{selected.spec.name}</strong>
              <MachineExpiry expiresAt={selected.expiresAt} />
            </div>
            <MachineActions
              desktop={desktop}
              windowId={windowId}
              machine={selected}
              reason={actionReason}
              transition={transition}
              terminalReason={terminalReason}
              terminalPending={terminalPending}
              onAction={onLifecycle}
              onTerminal={onTerminal}
              onCopyLimaShell={canCopyLimaShell ? onCopyLimaShell : undefined}
              limaShellPending={copyingLimaShell}
              onInspect={onInspect}
              onProjectFiles={(() => {
                if (!selected || error || transition) return;
                const selectedVolume = volumes.find(
                  (item) => item.id === selected.spec.volumeId,
                );
                if (
                  !selectedVolume ||
                  !canOpenVMProjectVolume(selected, selectedVolume)
                )
                  return;
                return () =>
                  desktop.openApp(
                    'project-files',
                    {
                      hostId: selected.hostId,
                      machineId: selected.id,
                      vmProjectVolumeId: selectedVolume.id,
                    },
                    {
                      title: `${selected.spec.name} — Project Files`,
                    },
                  );
              })()}
            />
          </div>
          {!transition &&
            selected.status === 'running' &&
            machineGuestStatus(selected) && (
              <p className="machines-form-help">
                {humanize(selected.status)} · {machineGuestStatus(selected)}
              </p>
            )}
          {transition && (
            <p className="machines-vm-transition" role="status">
              {transition}
            </p>
          )}
          {notice && feedbackInSelection && notice.id === operationId ? (
            actionFeedback
          ) : notice ? (
            <VMOperationNotice
              operation={notice}
              onShowActivity={onShowActivity}
            />
          ) : null}
          {guidance && <p className="machines-form-help">{guidance}</p>}
          {limaShellNotice?.key === selectedKey && (
            <p className="machines-form-help" role="status">
              {limaShellNotice.copied
                ? 'Copied local Lima shell command: '
                : 'Run on this Mac: '}
              <code>{limaShellNotice.command}</code>
            </p>
          )}
        </div>
      )}
      {visible.length ? (
        <div className="machines-grid" aria-label="Virtual machines">
          {visible.map((machine) => {
            const tileTransition =
              (selected?.id === machine.id && transition) ||
              machineTransition(
                machine,
                activeVMOperation(machine, operations),
              );
            const tileStatus = tileTransition
              ? 'transitioning'
              : machine.status;
            return (
              <button
                key={`${machine.hostId}:${machine.id}`}
                className="machines-tile"
                aria-label={`${machine.spec.name} ${machine.spec.provider}`}
                aria-pressed={
                  selected?.id === machine.id &&
                  selected.hostId === machine.hostId
                }
                onClick={() =>
                  desktop.updateWindowState(windowId, {
                    machineId: machine.id,
                    machineHostId: machine.hostId,
                  })
                }
                onDoubleClick={() => openMachineInspector(desktop, machine)}
              >
                <PixelIcon name="virtual-machine" size={38} />
                <div className="machines-tile-meta">
                  <strong>{machine.spec.name}</strong>
                  <span
                    className="machines-tile-status"
                    data-status={tileStatus}
                  >
                    {tileTransition || humanize(machine.status)}
                  </span>
                  <span className="machines-tile-specs">
                    {machine.spec.resources.cpus} CPU ·{' '}
                    {machine.spec.resources.memoryMiB} MiB ·{' '}
                    {machine.spec.projectGiB} GiB ·{' '}
                    {humanize(machine.spec.provider)}
                  </span>
                </div>
              </button>
            );
          })}
        </div>
      ) : error && !machines.length ? null : (
        <div
          className={
            emptyInventory ? 'machines-empty-status' : 'machines-empty'
          }
        >
          <h2>
            {machines.length
              ? 'No VMs match your filters'
              : loading
                ? 'Loading virtual machines…'
                : firecrackerImageRequired
                  ? 'Firecracker guest image required'
                  : !availableProvidersCount
                    ? 'VM provider unavailable'
                    : !hasReadyImage
                      ? 'VM image required'
                      : 'No VMs yet'}
          </h2>
          {firecrackerImageRequired && (
            <p>
              Ask the deployment host administrator to register a compatible
              Firecracker guest image. See the{' '}
              <a href={linuxVMSetupGuide} target="_blank" rel="noreferrer">
                Linux VM setup guide
              </a>
              .
            </p>
          )}
          {machines.length ? (
            <Button
              onClick={() => {
                onSearchChange('');
                onStateFilterChange('all');
              }}
            >
              Clear filters
            </Button>
          ) : (
            !canCreate &&
            !loading &&
            (!error && canImportImages ? (
              <Button onClick={onOpenImages}>Open Images</Button>
            ) : (
              <Button onClick={onShowHost}>View environment</Button>
            ))
          )}
        </div>
      )}
    </>
  );
}
