import { Button } from '@cofy-x/axv-nextstep';
import type { Operation } from './api';
import { humanize, operationPhase } from './presentation';

export function RequestDetails({
  requestId,
  hostId,
}: {
  requestId: string;
  hostId?: string;
}) {
  return (
    <details className="machines-technical">
      <summary>Request details</summary>
      <dl className="info-list">
        <dt>Request</dt>
        <dd>
          <code>{requestId}</code>
        </dd>
        {hostId && (
          <>
            <dt>Host</dt>
            <dd>
              <code>{hostId}</code>
            </dd>
          </>
        )}
      </dl>
    </details>
  );
}

export function OperationFeedback({
  accepted,
  operations,
  onShowActivity,
}: {
  accepted?: { operation: Operation; label: string };
  operations: Operation[];
  onShowActivity?: (id: string) => void;
}) {
  if (!accepted) return null;
  const operation =
    operations.find((item) => item.id === accepted.operation.id) ||
    accepted.operation;
  const phase = operationPhase(operation);
  return (
    <div className="machines-operation-feedback" role="status">
      <p>
        {accepted.label} · {humanize(operation.status)}
        {phase && ` · ${phase}`}
      </p>
      {operation.error && <p className="machines-error">{operation.error}</p>}
      {onShowActivity && (
        <Button onClick={() => onShowActivity(operation.id)}>
          View Activity
        </Button>
      )}
    </div>
  );
}

export function VMOperationNotice({
  className,
  operation,
  onShowActivity,
}: {
  className?: string;
  operation: Operation;
  onShowActivity?: (id: string) => void;
}) {
  const failed = operation.status === 'failed';
  const working =
    operation.status === 'queued' || operation.status === 'running';
  const label = working
    ? operationPhase(operation)
    : failed
      ? `${humanize(operation.action)} failed${operation.error ? ` · ${operation.error}` : ''}`
      : `${humanize(operation.action)} · ${humanize(operation.status)}`;
  if (!label && !onShowActivity) return null;
  return (
    <div
      className={['machines-operation-feedback', className]
        .filter(Boolean)
        .join(' ')}
      role={failed ? 'alert' : 'status'}
    >
      {label && (
        <span className={failed ? 'machines-failure' : undefined}>{label}</span>
      )}
      {onShowActivity && (
        <Button onClick={() => onShowActivity(operation.id)}>
          View Activity
        </Button>
      )}
    </div>
  );
}
