import { reviewEnvironment } from './api';
import type { ReviewSnapshot } from './api';
import type { ReviewSettingsDestination } from './ReviewSettings';
import './review-automation-status.css';

function ScanTime({ at }: { at: string }) {
  const date = new Date(at);
  return (
    <time dateTime={at} title={date.toLocaleString()}>
      {date.toLocaleString(undefined, {
        dateStyle: 'short',
        timeStyle: 'short',
      })}
    </time>
  );
}

function discoverySummary(snapshot: ReviewSnapshot) {
  const { config, github, codex, discovery } = snapshot;
  if (!config.enabled) return 'Paused in Settings';
  const githubUnavailable = !github.connected && !github.available;
  const codexUnavailable = !codex.connected && !codex.available;
  if (githubUnavailable && codexUnavailable)
    return 'GitHub and Codex unavailable';
  if (githubUnavailable) return 'GitHub unavailable';
  if (codexUnavailable) return 'Codex unavailable';
  if (!github.connected && !codex.connected) return 'Connect GitHub and Codex';
  if (!github.connected) return 'Connect GitHub';
  if (!codex.connected) return 'Connect Codex';
  if (!discovery) return `Every ${config.intervalMinutes} min`;
  if (discovery.scanning) return 'Scanning GitHub…';
  if (!discovery.nextScanAt) return 'Scan due';
  return (
    <>
      Next scan <ScanTime at={discovery.nextScanAt} />
    </>
  );
}

function verificationStatus(snapshot: ReviewSnapshot): {
  summary: string;
  destination?: ReviewSettingsDestination;
  missingVMs?: string[];
} {
  const enabled = Object.entries(
    snapshot.config.repositoryPolicies ?? {},
  ).filter(([, policy]) => policy.autoCandidateValidation);
  if (enabled.length === 0)
    return {
      summary: 'Off · No repositories enabled',
      destination: 'repositories',
    };
  if (!snapshot.github.connected)
    return {
      summary: snapshot.github.available
        ? 'Connect GitHub'
        : 'GitHub unavailable',
      destination: 'connections',
    };
  const approved = enabled.filter(
    ([, policy]) => policy.comparisonScenarios?.length,
  );
  if (approved.length === 0)
    return { summary: 'Approve a repository comparison' };
  const missingVMs = approved
    .map(([repository]) => repository)
    .filter(
      (repository) => !reviewEnvironment(snapshot, repository).configured,
    );
  const count = approved.length - missingVMs.length;
  return {
    summary: count
      ? `Enabled for ${count} ${count === 1 ? 'repository' : 'repositories'}`
      : 'Review VM needs setup',
    destination: missingVMs.length ? 'repositories' : undefined,
    missingVMs,
  };
}

const settingsActions = {
  connections: 'Open connections',
  schedule: 'Open review schedule',
  repositories: 'Open repository settings',
} satisfies Record<ReviewSettingsDestination, string>;

export function ReviewAutomationStatus({
  snapshot,
  onOpenSettings,
}: {
  snapshot: ReviewSnapshot;
  onOpenSettings: (section: ReviewSettingsDestination) => void;
}) {
  const { discovery, budget, config, github, codex } = snapshot;
  const discoveryDestination = !config.enabled
    ? 'schedule'
    : !github.connected || !codex.connected
      ? 'connections'
      : undefined;
  const verification = verificationStatus(snapshot);

  function settingsShortcut(section: ReviewSettingsDestination) {
    return (
      <button
        type="button"
        className="review-automation-shortcut"
        onClick={() => onOpenSettings(section)}
      >
        {settingsActions[section]}
      </button>
    );
  }

  return (
    <section
      className="review-automation-status"
      aria-label="Automation status"
    >
      <dl>
        <div>
          <dt>PR discovery</dt>
          <dd>
            {discoverySummary(snapshot)}
            {discovery?.lastSuccessAt && (
              <span className="review-automation-note">
                Last successful scan <ScanTime at={discovery.lastSuccessAt} />
              </span>
            )}
            {discovery?.error && (
              <details className="review-automation-error">
                <summary>Last scan failed</summary>
                <p>{discovery.error}</p>
              </details>
            )}
            {discoveryDestination && settingsShortcut(discoveryDestination)}
          </dd>
        </div>
        <div>
          <dt>Candidate verification</dt>
          <dd>
            {verification.summary}
            {!!verification.missingVMs?.length && (
              <details className="review-automation-note review-automation-missing-vms">
                <summary>
                  VM setup needed for {verification.missingVMs.length}
                </summary>
                <ul>
                  {verification.missingVMs.map((repository) => (
                    <li key={repository}>{repository}</li>
                  ))}
                </ul>
              </details>
            )}
            {verification.destination &&
              verification.destination !== discoveryDestination &&
              settingsShortcut(verification.destination)}
          </dd>
        </div>
      </dl>
      <p className="review-automation-note review-automation-budget">
        Codex reviews today · {budget.used} of {config.dailyLimit}.
        {budget.used >= config.dailyLimit && (
          <>
            {' '}
            Daily review limit reached. New Codex reviews wait until{' '}
            <ScanTime at={budget.resetsAt} />.
          </>
        )}
      </p>
    </section>
  );
}
