import { useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { Button, PixelIcon } from '@cofy-x/axv-nextstep';
import { describeBuiltin, groupBuiltins } from './package-utils';
import type { BuiltinApplication } from './types';

interface BuiltinApplicationsSectionProps {
  builtins: BuiltinApplication[];
  onOpenBuiltin: (appId: string) => void;
  renderDesktopOption: (appId: string, title: string) => ReactNode;
}

function matchesQuery(app: BuiltinApplication, query: string): boolean {
  if (!query) return true;
  const haystack =
    `${app.title} ${app.id} ${describeBuiltin(app)}`.toLowerCase();
  return haystack.includes(query);
}

export function BuiltinApplicationsSection({
  builtins,
  onOpenBuiltin,
  renderDesktopOption,
}: BuiltinApplicationsSectionProps) {
  const [filter, setFilter] = useState('');
  const grouped = useMemo(() => groupBuiltins(builtins), [builtins]);
  const query = filter.trim().toLowerCase();

  const workstation = useMemo(
    () => grouped.workstation.filter((app) => matchesQuery(app, query)),
    [grouped.workstation, query],
  );
  const system = useMemo(
    () => grouped.system.filter((app) => matchesQuery(app, query)),
    [grouped.system, query],
  );
  const internal = useMemo(
    () => grouped.internal.filter((app) => matchesQuery(app, query)),
    [grouped.internal, query],
  );

  const launchableTotal = grouped.workstation.length + grouped.system.length;
  const visibleLaunchable = workstation.length + system.length;
  const hasMatches = visibleLaunchable > 0 || internal.length > 0;

  function renderLaunchableGroup(
    label: string,
    items: BuiltinApplication[],
    groupId: string,
  ) {
    if (!items.length) return null;
    return (
      <div className="applications-group" aria-labelledby={groupId}>
        <div className="applications-group-head">
          <h3 id={groupId}>{label}</h3>
          <span>{items.length}</span>
        </div>
        <ul className="applications-list">
          {items.map((app) => (
            <li className="applications-row applications-builtin" key={app.id}>
              <div className="applications-identity">
                <PixelIcon name={app.icon} size={28} />
                <div className="applications-identity-text">
                  <strong>{app.title}</strong>
                  <p className="applications-meta">{describeBuiltin(app)}</p>
                </div>
              </div>
              <div className="applications-builtin-controls">
                {renderDesktopOption(app.id, app.title)}
                <Button
                  aria-label={`Open ${app.title}`}
                  onClick={() => onOpenBuiltin(app.id)}
                >
                  Open
                </Button>
              </div>
            </li>
          ))}
        </ul>
      </div>
    );
  }

  return (
    <section
      className="applications-section applications-builtins"
      aria-labelledby="applications-builtins-heading"
    >
      <div className="applications-heading applications-builtins-head">
        <div>
          <h2 id="applications-builtins-heading">Built-in applications</h2>
          <p className="applications-meta">
            {query
              ? `${visibleLaunchable} of ${launchableTotal} launchable apps shown`
              : `${launchableTotal} launchable workstation and system apps`}
          </p>
        </div>
        <div className="applications-search">
          <input
            type="search"
            className="ns-field-input applications-search-input"
            aria-label="Filter built-in applications"
            placeholder="Filter apps…"
            value={filter}
            onChange={(event) => setFilter(event.currentTarget.value)}
          />
          {filter && <Button onClick={() => setFilter('')}>Clear</Button>}
        </div>
      </div>

      {!hasMatches ? (
        <div className="applications-empty">
          <div className="applications-empty-body">
            <p className="applications-empty-title">
              No built-in applications match &ldquo;{filter.trim()}&rdquo;
            </p>
            <p className="applications-meta">
              Try searching by app name or capability.
            </p>
          </div>
          <div className="applications-actions">
            <Button onClick={() => setFilter('')}>Clear filter</Button>
          </div>
        </div>
      ) : (
        <>
          {renderLaunchableGroup(
            'Workstation',
            workstation,
            'applications-group-workstation',
          )}
          {renderLaunchableGroup(
            'System & Remote',
            system,
            'applications-group-system',
          )}
          {internal.length > 0 && (
            <details
              className="applications-internal-panels"
              open={query.length > 0 ? true : undefined}
            >
              <summary>Internal panels ({internal.length})</summary>
              <p className="applications-meta applications-internal-note">
                Supporting windows opened automatically from their owning
                applications.
              </p>
              <ul className="applications-list">
                {internal.map((app) => (
                  <li
                    className="applications-row applications-builtin is-internal"
                    key={app.id}
                  >
                    <div className="applications-identity">
                      <PixelIcon name={app.icon} size={24} />
                      <div className="applications-identity-text">
                        <strong>{app.title}</strong>
                        <p className="applications-meta">
                          {app.kind === 'panel'
                            ? 'Utility panel'
                            : 'Application'}{' '}
                          · {describeBuiltin(app)}
                        </p>
                      </div>
                    </div>
                  </li>
                ))}
              </ul>
            </details>
          )}
        </>
      )}
    </section>
  );
}
