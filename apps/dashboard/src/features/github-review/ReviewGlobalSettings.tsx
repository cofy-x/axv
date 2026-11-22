import { useEffect, useId } from 'react';
import { Button, Checkbox, Field } from '@cofy-x/axv-nextstep';
import { reviewLoginURL } from './api';
import type { ReviewConnection, ReviewProvider, ReviewSnapshot } from './api';
import './review-global-settings.css';

export const reviewProviderNames = {
  github: 'GitHub',
  codex: 'Codex',
} satisfies Record<ReviewProvider, string>;

function ReviewConnectionRow({
  provider,
  connection,
  disabled,
  onConnect,
  onCancel,
}: {
  provider: ReviewProvider;
  connection: ReviewConnection;
  disabled: boolean;
  onConnect: () => void;
  onCancel: () => void;
}) {
  const label = reviewProviderNames[provider];
  const pending = connection.attempt?.status === 'pending';
  const url = pending ? reviewLoginURL(provider, connection.attempt?.url) : '';
  const problem = connection.attempt?.error || connection.error;
  const status = pending
    ? 'Waiting for authorization'
    : connection.connected
      ? 'Connected'
      : connection.available
        ? 'Not connected'
        : 'Unavailable';

  return (
    <section className="github-review-provider" aria-label={label}>
      <div className="github-review-provider-row">
        <strong>{label}</strong>
        <span className="github-review-global-meta">
          {status}
          {connection.connected && connection.account && (
            <>
              {' · '}
              <span>{connection.account}</span>
            </>
          )}
        </span>
        {pending ? (
          <Button
            aria-label={`Cancel ${label} connection`}
            disabled={disabled}
            onClick={onCancel}
          >
            Cancel
          </Button>
        ) : (
          !connection.connected &&
          connection.available && (
            <Button
              primary
              aria-label={`Connect ${label}`}
              disabled={disabled}
              onClick={onConnect}
            >
              Connect
            </Button>
          )
        )}
      </div>
      {pending && connection.attempt?.code && url && (
        <div className="github-review-auth" role="status">
          Open{' '}
          <a href={url} target="_blank" rel="noreferrer">
            {label} authorization
          </a>{' '}
          and enter <code>{connection.attempt.code}</code>.
        </div>
      )}
      {problem && (
        <p className="github-review-problem" role="alert">
          {problem}
        </p>
      )}
    </section>
  );
}

export function ReviewConnections({
  id,
  github,
  codex,
  disabled,
  onConnect,
  onDisconnect,
}: {
  id?: string;
  github: ReviewConnection;
  codex: ReviewConnection;
  disabled: boolean;
  onConnect: (provider: ReviewProvider) => void;
  onDisconnect: (provider: ReviewProvider) => void;
}) {
  const providers = ['github', 'codex'] as const;
  const connections = { github, codex };
  const connected = providers.filter(
    (provider) =>
      connections[provider].connected &&
      connections[provider].attempt?.status !== 'pending',
  );
  return (
    <fieldset id={id} className="github-review-global-block">
      <legend>Connections on this computer</legend>
      <div className="github-review-provider-list">
        {providers.map((provider) => (
          <ReviewConnectionRow
            key={provider}
            provider={provider}
            connection={connections[provider]}
            disabled={disabled}
            onConnect={() => onConnect(provider)}
            onCancel={() => onDisconnect(provider)}
          />
        ))}
      </div>
      {connected.length > 0 && (
        <details className="github-review-connection-management">
          <summary>Manage connections</summary>
          <div className="app-actions">
            {connected.map((provider) => (
              <Button
                key={provider}
                disabled={disabled}
                onClick={() => onDisconnect(provider)}
              >
                Disconnect {reviewProviderNames[provider]}
              </Button>
            ))}
          </div>
        </details>
      )}
    </fieldset>
  );
}

interface ScheduleDraft {
  watchOnly: boolean;
  enabled: boolean;
  intervalMinutes: string;
  dailyLimit: string;
}

export function ReviewScanSettings({
  id,
  enabled,
  watchOnly,
  intervalMinutes,
  dailyLimit,
  savedEnabled,
  savedDailyLimit,
  budget,
  connectionsReady = true,
  disabled,
  issue,
  onChange,
}: ScheduleDraft & {
  id?: string;
  savedEnabled: boolean;
  savedDailyLimit: number;
  budget: ReviewSnapshot['budget'];
  connectionsReady?: boolean;
  disabled: boolean;
  issue?: { field: 'intervalMinutes' | 'dailyLimit'; message: string };
  onChange: (patch: Partial<ScheduleDraft>) => void;
}) {
  const intervalId = useId();
  const limitId = useId();
  const errorId = useId();
  const invalidInterval = issue?.field === 'intervalMinutes';
  const invalidLimit = issue?.field === 'dailyLimit';
  const error = issue && (
    <p id={errorId} className="github-review-problem">
      {issue.message}
    </p>
  );
  useEffect(() => {
    if (issue)
      document
        .getElementById(
          issue.field === 'intervalMinutes' ? intervalId : limitId,
        )
        ?.focus();
  }, [issue, intervalId, limitId]);

  return (
    <fieldset
      id={id}
      className="github-review-global-block"
      disabled={disabled}
    >
      <legend>Review schedule</legend>
      <div className="github-review-scan-toggle">
        <Checkbox
          label="Scan GitHub automatically"
          checked={enabled}
          onChange={(event) => onChange({ enabled: event.target.checked })}
        />
        <span className="github-review-global-meta" aria-live="polite">
          {enabled !== savedEnabled
            ? enabled
              ? 'Enables after saving'
              : 'Pauses after saving'
            : savedEnabled
              ? connectionsReady
                ? 'Enabled'
                : 'Waiting for connections'
              : 'Paused'}
        </span>
      </div>
      <Checkbox
        label="Include direct review requests"
        checked={!watchOnly}
        onChange={(event) => onChange({ watchOnly: !event.target.checked })}
      />
      <div className="github-review-schedule-fields">
        <div>
          <Field
            id={intervalId}
            label="Poll interval in minutes"
            type="number"
            value={intervalMinutes}
            onChange={(event) =>
              onChange({ intervalMinutes: event.target.value })
            }
            min={1}
            max={1440}
            step={1}
            required
            aria-invalid={invalidInterval || undefined}
            aria-describedby={invalidInterval ? errorId : undefined}
          />
          {invalidInterval && error}
        </div>
        <div>
          <Field
            id={limitId}
            label="Daily Codex review limit"
            type="number"
            value={dailyLimit}
            onChange={(event) => onChange({ dailyLimit: event.target.value })}
            min={1}
            max={1000}
            step={1}
            required
            aria-invalid={invalidLimit || undefined}
            aria-describedby={invalidLimit ? errorId : undefined}
          />
          {invalidLimit && error}
        </div>
      </div>
      <p className="github-review-global-meta github-review-saved-budget">
        {budget.used} of {savedDailyLimit} review starts used today. Resets{' '}
        <time dateTime={budget.resetsAt}>
          {new Date(budget.resetsAt).toLocaleString(undefined, {
            dateStyle: 'short',
            timeStyle: 'short',
          })}
        </time>
        .
      </p>
    </fieldset>
  );
}
