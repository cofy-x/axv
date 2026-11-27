import type { RefObject } from 'react';
import type { Machine, Operation, Volume } from './api';
import {
  humanize,
  operationLabel,
  operationPhase,
  operationTarget,
} from './presentation';

export interface MachinesActivityTableProps {
  operations: Operation[];
  machines: Machine[];
  volumes: Volume[];
  selectedActivity?: string;
  activityFound: boolean;
  loading: boolean;
  refreshing: boolean;
  error?: string;
  activityBodyRef: RefObject<HTMLTableSectionElement | null>;
}

export function MachinesActivityTable({
  operations,
  machines,
  volumes,
  selectedActivity,
  activityFound,
  loading,
  refreshing,
  error,
  activityBodyRef,
}: MachinesActivityTableProps) {
  return (
    <>
      {selectedActivity &&
        !activityFound &&
        !loading &&
        !refreshing &&
        !error && (
          <p className="machines-message">
            This operation is unavailable in recent Activity.
          </p>
        )}
      <table className="machines-table machines-inventory-table machines-activity-table">
        <colgroup>
          <col span={2} className="machines-activity-label-column" />
          <col />
        </colgroup>
        <thead>
          <tr>
            <th scope="col">Operation</th>
            <th scope="col">Target</th>
            <th scope="col">State</th>
          </tr>
        </thead>
        <tbody ref={activityBodyRef}>
          {operations.map((op) => {
            const createdAt = new Date(op.createdAt);
            const phase = operationPhase(op);
            return (
              <tr
                key={op.id}
                data-operation-id={op.id}
                data-selected={selectedActivity === op.id}
                aria-current={selectedActivity === op.id ? 'true' : undefined}
              >
                <td>
                  <strong>{operationLabel(op, volumes)}</strong>
                  <small>
                    <time
                      dateTime={op.createdAt}
                      title={createdAt.toLocaleString()}
                    >
                      {createdAt.toLocaleDateString(undefined, {
                        dateStyle: 'short',
                      })}{' '}
                      <span>
                        {createdAt.toLocaleTimeString(undefined, {
                          timeStyle: 'short',
                        })}
                      </span>
                    </time>
                  </small>
                </td>
                <td>{operationTarget(op, machines, volumes)}</td>
                <td>
                  <span
                    className="machines-status-dot"
                    data-status={
                      op.status === 'succeeded'
                        ? 'running'
                        : op.status === 'failed'
                          ? 'failed'
                          : 'transitioning'
                    }
                  >
                    {humanize(op.status)}
                  </span>
                  {phase && <small>{phase}</small>}
                  {op.error && (
                    <small className="machines-failure">{op.error}</small>
                  )}
                </td>
              </tr>
            );
          })}
          {!operations.length && (
            <tr>
              <td colSpan={3}>No activity yet.</td>
            </tr>
          )}
        </tbody>
      </table>
    </>
  );
}
