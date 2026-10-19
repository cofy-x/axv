import { IconButton } from '@cofy-x/axv-nextstep';
import { useCodexAvailability } from './CodexContext';

export function CodexEnvironment() {
  const { info, loading, error, status, refresh } = useCodexAvailability();
  const details = info && (
    <dl className="info-list">
      <dt>Codex CLI</dt>
      <dd>
        {info.codexVersion || (info.installed ? 'Installed' : 'Not found')}
        {info.codexPath && <code>{info.codexPath}</code>}
      </dd>
      <dt>tmux</dt>
      <dd>
        {info.tmuxVersion || (info.tmuxPath ? 'Installed' : 'Not found')}
        {info.tmuxPath && <code>{info.tmuxPath}</code>}
      </dd>
      {info.configPath && (
        <>
          <dt>AXV config</dt>
          <dd>
            <code>{info.configPath}</code>
          </dd>
        </>
      )}
      {info.network && (
        <>
          <dt>Network</dt>
          <dd>
            {
              {
                environment: 'Environment proxy',
                configuration: 'AXV config proxy',
                system: 'System proxy',
                direct: 'Direct',
              }[info.network.proxySource]
            }
            {info.network.proxy && <code>{info.network.proxy}</code>}
          </dd>
        </>
      )}
    </dl>
  );
  return (
    <div className="codex-environment" data-testid="codex-environment">
      <div className="app-toolbar codex-environment-toolbar">
        <span className="toolbar-caption" role="status">
          {status}
        </span>
        <IconButton
          icon="refresh"
          label="Refresh"
          disabled={loading}
          onClick={() => void refresh()}
        />
      </div>
      <div className="app-pad codex-environment-content" aria-busy={loading}>
        {(error || info?.reason) && (
          <p className="app-error" role="alert">
            {error || info?.reason}
          </p>
        )}
        {error && info && (
          <p className="codex-note">Showing the last successful check.</p>
        )}
        {!error && info && !info.ready && info.guidance.length > 0 && (
          <ul className="codex-environment-guidance">
            {info.guidance.map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
        )}
        {details}
        {info && (
          <p className="codex-note">
            Checks local dependencies, not sign-in or model connectivity.
            Network settings apply to new sessions.
          </p>
        )}
      </div>
    </div>
  );
}
