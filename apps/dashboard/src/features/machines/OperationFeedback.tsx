import { Button } from '@cofy-x/axv-nextstep';
import type { Operation } from './api';
import { humanize } from './presentation';

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
  return (
    <div className="machines-operation-feedback" role="status">
      <p>
        {accepted.label} · {humanize(operation.status)}
        {operation.phase ? ` · ${humanize(operation.phase)}` : ''}
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
