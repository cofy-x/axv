import { useCallback, useState } from 'react';
import { IconButton, Select } from '@cofy-x/axv-nextstep';
import { cloudflareAPI } from './api';
import { cacheHitRate, formatTrafficBytes } from './traffic-format';
import { useResource } from './useResource';

const shortTime = new Intl.DateTimeFormat(undefined, {
  month: 'short',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23',
});
const fullTime = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'medium',
  timeStyle: 'long',
});

function TrafficTime({ value }: { value: string }) {
  const date = new Date(value);
  return (
    <time dateTime={value} title={fullTime.format(date)}>
      {shortTime.format(date)}
    </time>
  );
}

export function CloudflareTraffic({
  active,
  allowed,
}: {
  active: boolean;
  allowed: boolean;
}) {
  const [hours, setHours] = useState(24);
  const read = useCallback(
    (signal: AbortSignal) => cloudflareAPI.traffic(hours, signal),
    [hours],
  );
  const resource = useResource(read, active && allowed, 60000);
  if (!allowed)
    return (
      <div className="cloudflare-card">
        <p className="cloudflare-empty-line">
          Traffic is unavailable for this connection.
        </p>
      </div>
    );
  const data = resource.value;
  return (
    <div className="cloudflare-overview-stack">
      <section className="cloudflare-card">
        <div className="cloudflare-section-heading">
          <h2>Zone traffic</h2>
          <IconButton
            icon="refresh"
            label="Refresh traffic"
            disabled={!active || resource.loading}
            onClick={resource.refresh}
          />
        </div>
        <div className="cloudflare-traffic-controls">
          <div className="cloudflare-field">
            <Select
              label="Time range"
              value={String(hours)}
              disabled={!active}
              onChange={(event) => setHours(Number(event.target.value))}
            >
              <option value="1">Last hour</option>
              <option value="24">Last 24 hours</option>
              <option value="168">Last 7 days</option>
              <option value="720">Last 30 days</option>
            </Select>
          </div>
          <p className="fine-print">Includes all sites in the selected zone.</p>
        </div>
        {resource.error && (
          <p className="app-error" role="alert">
            {resource.error}
          </p>
        )}
        {resource.error && data && (
          <p className="cloudflare-stale-note">
            Showing previous traffic. Refresh to check.
          </p>
        )}
        {resource.loading && data && (
          <p className="fine-print" role="status">
            Updating… Previous results are shown.
          </p>
        )}
        {resource.loading && !data && <p>Loading traffic…</p>}
        {data && !data.supported && (
          <p className="cloudflare-notice">
            {data.reason ||
              'Traffic analytics are unavailable for this account.'}
          </p>
        )}
      </section>
      {data?.supported && (
        <section className="cloudflare-card">
          <p className="fine-print cloudflare-traffic-window">
            <TrafficTime value={data.since} /> –{' '}
            <TrafficTime value={data.until} />
          </p>
          <dl className="cloudflare-metric-grid">
            <div className="cloudflare-metric-card">
              <dt>Requests</dt>
              <dd>{data.requests.toLocaleString()}</dd>
            </div>
            <div className="cloudflare-metric-card">
              <dt>Bytes transferred</dt>
              <dd title={`${data.bytes.toLocaleString()} bytes`}>
                {formatTrafficBytes(data.bytes)}
              </dd>
            </div>
            <div className="cloudflare-metric-card">
              <dt>Cached requests</dt>
              <dd>
                {data.cachedRequests.toLocaleString()}
                <span className="cloudflare-metric-sub">
                  {cacheHitRate(data.requests, data.cachedRequests)} hit rate
                </span>
              </dd>
            </div>
          </dl>
          <div className="cloudflare-table-scroll">
            <table>
              <caption>Hourly traffic</caption>
              <thead>
                <tr>
                  <th scope="col">Time</th>
                  <th scope="col">Requests</th>
                  <th scope="col">Data</th>
                  <th scope="col">Cached</th>
                </tr>
              </thead>
              <tbody>
                {data.series.map((point) => (
                  <tr key={point.time}>
                    <th scope="row">
                      <TrafficTime value={point.time} />
                    </th>
                    <td>{point.requests.toLocaleString()}</td>
                    <td title={`${point.bytes.toLocaleString()} bytes`}>
                      {formatTrafficBytes(point.bytes)}
                    </td>
                    <td>{point.cachedRequests.toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {!data.series.length && (
            <p className="cloudflare-empty-line">No traffic in this range.</p>
          )}
        </section>
      )}
    </div>
  );
}
