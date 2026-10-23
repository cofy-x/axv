import type { CodexActivity as Activity } from './api';
import { activityLabel, hasActivityAction } from './activity';

export function CodexActivity({
  activity,
  stale = false,
}: {
  activity?: Activity;
  stale?: boolean;
}) {
  const warning = hasActivityAction(activity);
  return (
    <span
      className={`codex-activity${warning ? ' needs-attention is-warning' : ''}${stale ? ' is-stale' : ''}`}
      data-testid="codex-activity"
      data-activity-state={activity?.state || 'unknown'}
      data-needs-attention={warning}
      data-attention-kind={warning ? 'warning' : 'none'}
      title={
        activity?.updatedAt
          ? `Last activity: ${new Date(activity.updatedAt).toLocaleString()}`
          : undefined
      }
    >
      {warning && (
        <>
          <span
            className="codex-activity-marker"
            aria-hidden="true"
            title="Action needed"
          >
            !
          </span>
          <span className="visually-hidden">Action needed: </span>
        </>
      )}
      {activityLabel(activity)}
      {stale && <span>· Outdated</span>}
    </span>
  );
}
