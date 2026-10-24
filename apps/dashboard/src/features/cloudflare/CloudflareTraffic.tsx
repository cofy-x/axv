import { useCallback, useState } from 'react';
import { IconButton } from '@cofy-x/axv-nextstep';
import { cloudflareAPI } from './api';
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
  if (!allowed) return <p>Traffic is unavailable for this connection.</p>;
  const data = resource.value;
  return (
    <div>
      <div className="cloudflare-section-heading">
        <h2>Zone traffic</h2>
        <IconButton
          icon="refresh"
          label="Refresh traffic"
          disabled={!active || resource.loading}
          onClick={resource.refresh}
        />
      </div>
      <label className="cloudflare-field">
        Time range
        <select
          value={hours}
          disabled={!active}
          onChange={(event) => setHours(Number(event.target.value))}
        >
          <option value={1}>Last hour</option>
          <option value={24}>Last 24 hours</option>
          <option value={168}>Last 7 days</option>
          <option value={720}>Last 30 days</option>
        </select>
      </label>
      <p>Includes all sites in the selected zone.</p>
      {resource.error && (
        <p className="app-error" role="alert">
          {resource.error}
        </p>
      )}
      {resource.error && data && (
        <p>Showing previous traffic. Refresh to check.</p>
      )}
      {resource.loading && data && (
        <p role="status">Updating… Previous results are shown.</p>
      )}
      {resource.loading && !data && <p>Loading traffic…</p>}
      {data && !data.supported && (
        <p className="cloudflare-notice">
          {data.reason || 'Traffic analytics are unavailable for this account.'}
        </p>
      )}
      {data?.supported && (
        <>
          <p className="fine-print">
            <TrafficTime value={data.since} /> –{' '}
            <TrafficTime value={data.until} />
          </p>
          <dl className="info-list">
            <dt>Requests</dt>
            <dd>{data.requests.toLocaleString()}</dd>
            <dt>Bytes transferred</dt>
            <dd>{data.bytes.toLocaleString()}</dd>
            <dt>Cached requests</dt>
            <dd>{data.cachedRequests.toLocaleString()}</dd>
          </dl>
          <div className="cloudflare-table-scroll">
            <table>
              <caption>Hourly traffic</caption>
              <thead>
                <tr>
                  <th scope="col">Time</th>
                  <th scope="col">Requests</th>
                  <th scope="col">Bytes</th>
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
                    <td>{point.bytes.toLocaleString()}</td>
                    <td>{point.cachedRequests.toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {!data.series.length && <p>No traffic in this range.</p>}
        </>
      )}
    </div>
  );
}
