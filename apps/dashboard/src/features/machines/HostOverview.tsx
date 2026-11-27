import { Fragment } from 'react';
import type {
  ComputeEnvironment,
  HostInfo,
  ImageInfo,
  Machine,
  PreparationRequirement,
  Volume,
} from './api';
import { humanize } from './presentation';

export function EnvironmentChecks({
  checks,
}: {
  checks: PreparationRequirement[];
}) {
  function list(items: PreparationRequirement[]) {
    return (
      <ul
        className="machines-environment-checks"
        aria-label="Environment checks"
      >
        {items.map((check) => (
          <li key={check.id} data-state={check.status}>
            <span className="machines-check-state">
              {check.status === 'ready'
                ? 'Ready'
                : check.status === 'needed'
                  ? 'Needed'
                  : 'Needs attention'}
            </span>
            <div>
              <strong>{check.label}</strong>
              <p>{check.detail}</p>
            </div>
          </li>
        ))}
      </ul>
    );
  }

  const blocked = checks.filter((check) => check.status === 'blocked');
  const other = checks.filter((check) => check.status !== 'blocked');

  return (
    <>
      {!!blocked.length && list(blocked)}
      {!!other.length && (
        <details className="machines-technical">
          <summary>Environment checks ({other.length})</summary>
          {list(other)}
        </details>
      )}
    </>
  );
}

export function HostOverview({
  info,
  platform,
  arch,
  runtime,
  machines,
  volumes,
  images,
}: {
  info?: HostInfo;
  platform?: string;
  arch?: string;
  runtime?: ComputeEnvironment['runtime'];
  machines: Machine[];
  volumes: Volume[];
  images: ImageInfo[];
}) {
  const capacity = info?.capacity;
  if (!capacity) return null;

  const hostPlatformLabel = [
    platform === 'darwin' ? 'macOS' : platform && humanize(platform),
    arch || info?.hostArch,
  ]
    .filter(Boolean)
    .join(' · ');
  const hostRuntimeLabel =
    runtime && runtime.state !== 'not_configured'
      ? [humanize(runtime.state), runtime.mode && humanize(runtime.mode)]
          .filter(Boolean)
          .join(' · ')
      : '';
  const runningVMs = machines.filter((m) => m.status === 'running').length;
  const readyImages = images.filter(
    (img) =>
      !img.status || img.status === 'ready' || img.status === 'configured',
  ).length;

  return (
    <>
      <div className="machines-host-limits">
        <h3>Capacity limits</h3>
        <dl className="machines-host-capacity">
          <div>
            <dt>CPUs</dt>
            <dd>{capacity.cpus}</dd>
          </div>
          <div>
            <dt>Memory</dt>
            <dd>
              {capacity.memoryMiB % 1024 === 0
                ? `${capacity.memoryMiB / 1024} GiB`
                : `${capacity.memoryMiB} MiB`}
            </dd>
          </div>
          <div>
            <dt>Storage</dt>
            <dd>{capacity.diskGiB} GiB</dd>
          </div>
          <div>
            <dt>VM slots</dt>
            <dd>
              {machines.length} of {capacity.machines} used
            </dd>
          </div>
        </dl>
      </div>
      <div className="machines-host-details">
        <h3>Host &amp; providers</h3>
        <dl className="info-list machines-details machines-host-overview-list">
          {hostPlatformLabel && (
            <>
              <dt>Platform</dt>
              <dd>{hostPlatformLabel}</dd>
            </>
          )}
          {hostRuntimeLabel && (
            <>
              <dt>Runtime</dt>
              <dd>{hostRuntimeLabel}</dd>
            </>
          )}
          <dt>Active inventory</dt>
          <dd>
            {runningVMs} running · {volumes.length} volume
            {volumes.length === 1 ? '' : 's'} · {readyImages} image
            {readyImages === 1 ? '' : 's'} ready
          </dd>
          {info?.providers?.map((provider) => (
            <Fragment key={provider.provider}>
              <dt>{humanize(provider.provider)}</dt>
              <dd>
                <span
                  className="machines-status-dot"
                  data-status={provider.available ? 'running' : 'failed'}
                >
                  {[
                    provider.available ? 'Available' : 'Unavailable',
                    provider.version,
                  ]
                    .filter(Boolean)
                    .join(' · ')}
                </span>
              </dd>
            </Fragment>
          ))}
        </dl>
      </div>
    </>
  );
}
