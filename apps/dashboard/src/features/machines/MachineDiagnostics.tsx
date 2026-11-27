import { Fragment, useEffect, useRef, useState } from 'react';
import { Button, TextArea } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { APIError } from '../../shared/api/http';
import { machinesApi } from './api';
import type { DoctorReport } from './api';
import { humanize } from './presentation';
import type { useCompute } from './useCompute';
import './machine-diagnostics.css';

type DiagnosticData = Pick<
  ReturnType<typeof useCompute>,
  'environment' | 'info' | 'machines' | 'preparation' | 'error'
>;

interface DiagnosticOptions {
  status: string;
  detail?: string;
  connection?: { label: string; value: string };
}

type DiagnosticRow = [label: string, value?: string, code?: boolean];
type CopyState = 'idle' | 'copying' | 'copied' | 'failed';

function diagnosticRows(
  { environment, info, machines, preparation, error }: DiagnosticData,
  { status, detail, connection }: DiagnosticOptions,
) {
  const check = preparation.check ?? environment?.preparation;
  const platform = check?.platform || environment?.platform;
  const arch = check?.arch || info?.hostArch;
  const runtime = environment?.runtime;
  const configurationSource =
    check?.configurationSource || environment?.configurationSource;
  const socket =
    check?.configured &&
    !runtime?.readOnly &&
    configurationSource !== 'environment'
      ? check.defaults.socket
      : undefined;
  const connectionRow: DiagnosticRow = connection
    ? [connection.label, connection.value, true]
    : ['Socket', socket, true];
  const hostService = environment?.hostService;
  const versionDriftDetail = hostService?.versionDrift
    ? `Linux VM service runs ${hostService.installedVersion || 'an older build'} while desktop runs ${hostService.currentVersion || 'current'}. Run sudo axv daemon upgrade.`
    : undefined;
  const issues: DiagnosticRow[] = [
    ['Details', detail],
    ['Version drift', versionDriftDetail],
    ['Service error', runtime?.error],
    ['Inventory error', error],
    ['Preparation error', preparation.connectionError || preparation.error],
    ['Configuration restriction', check?.lockedReason],
    ['Logging error', environment?.loggingError],
  ];
  const reported = new Set([status]);
  const distinctIssues = issues.filter(([, value]) => {
    if (!value || reported.has(value)) return false;
    reported.add(value);
    return true;
  });
  const summary: DiagnosticRow[] = [
    ['Status', status],
    connectionRow,
    ...distinctIssues,
    ['Desktop log', environment?.logPath, true],
    [
      'Service logs',
      platform === 'linux' || runtime?.mode === 'external'
        ? 'Host service supervisor'
        : undefined,
    ],
  ];
  const context: DiagnosticRow[] = [
    ['Profile', environment?.profileHome, true],
    ['Profile config', environment?.configPath, true],
    [
      'Configuration source',
      configurationSource && humanize(configurationSource),
    ],
    [
      'Platform',
      [platform === 'darwin' ? 'macOS' : platform && humanize(platform), arch]
        .filter(Boolean)
        .join(' · '),
    ],
    [
      'Runtime',
      runtime && runtime.state !== 'not_configured'
        ? [humanize(runtime.state), runtime.mode && humanize(runtime.mode)]
            .filter(Boolean)
            .join(' · ')
        : undefined,
    ],
    ['Host identity', runtime?.hostId || info?.hostId, true],
    [
      'VMs reserving CPU and memory',
      !error && info?.capacity
        ? String(machines.filter((machine) => machine.reserved).length)
        : undefined,
    ],
    ['Download cache', check?.cacheHome, true],
    [
      'Required Lima',
      platform === 'darwin'
        ? check?.requiredLimaVersion || environment?.requiredLimaVersion
        : undefined,
    ],
    ...(info?.providers?.map((provider): DiagnosticRow => [
      humanize(provider.provider),
      [
        provider.available ? 'Available' : 'Unavailable',
        provider.version,
        provider.reason,
      ]
        .filter(Boolean)
        .join(' · '),
    ]) ?? []),
  ];
  return {
    summary: summary.filter(([, value]) => value),
    context: context.filter(([, value]) => value),
  };
}

function statusTone(
  value?: string,
): 'running' | 'failed' | 'transitioning' | undefined {
  if (!value) return undefined;
  const lower = value.toLowerCase();
  if (
    lower.includes('connected') ||
    lower.includes('ready') ||
    lower.includes('verified')
  )
    return 'running';
  if (
    lower.includes('fail') ||
    lower.includes('error') ||
    lower.includes('attention') ||
    lower.includes('unavailable')
  )
    return 'failed';
  return 'transitioning';
}

function DiagnosticList({ rows }: { rows: DiagnosticRow[] }) {
  return (
    <dl className="info-list machines-details machine-diagnostics-table">
      {rows.map(([label, value, code]) => {
        const tone = label === 'Status' ? statusTone(value) : undefined;
        return (
          <Fragment key={label}>
            <dt>{label}</dt>
            <dd>
              {tone ? (
                <span className="machines-status-dot" data-status={tone}>
                  {value}
                </span>
              ) : code ? (
                <code>{value}</code>
              ) : (
                value
              )}
            </dd>
          </Fragment>
        );
      })}
    </dl>
  );
}

function MachineDiagnostics({
  sections,
}: {
  sections: ReturnType<typeof diagnosticRows>;
}) {
  const [copyState, setCopyState] = useState<CopyState>('idle');
  const [doctorOpen, setDoctorOpen] = useState(false);
  const [doctorLoading, setDoctorLoading] = useState(false);
  const [doctorReport, setDoctorReport] = useState<DoctorReport>();
  const [doctorError, setDoctorError] = useState('');
  const doctorRequest = useRef<AbortController | null>(null);

  useEffect(() => () => doctorRequest.current?.abort(), []);

  async function loadDoctor() {
    doctorRequest.current?.abort();
    const controller = new AbortController();
    doctorRequest.current = controller;
    setDoctorLoading(true);
    setDoctorError('');
    try {
      const report = await machinesApi.doctor(controller.signal);
      if (controller.signal.aborted) return;
      setDoctorReport(report);
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof APIError && err.status === 403)
        setDoctorError(
          'System health checks are available only when connected locally on the machine running AXV.',
        );
      else
        setDoctorError(
          err instanceof Error
            ? err.message
            : 'Could not load system health checks.',
        );
    } finally {
      if (!controller.signal.aborted) setDoctorLoading(false);
    }
  }

  const doctorLines =
    doctorReport?.checks.map(
      (item) =>
        `[${item.status.toUpperCase()}] ${item.label}: ${item.detail}${item.fix ? ` (Fix: ${item.fix})` : ''}`,
    ) ?? [];
  const text = [
    ...[...sections.summary, ...sections.context].map(
      ([label, value]) => `${label}: ${value}`,
    ),
    ...(doctorLines.length ? ['', 'System health:', ...doctorLines] : []),
  ].join('\n');

  async function copy() {
    if (copyState === 'copying') return;
    setCopyState('copying');
    try {
      await navigator.clipboard.writeText(text);
      setCopyState('copied');
    } catch {
      setCopyState('failed');
    }
  }

  return (
    <div className="machine-diagnostics">
      <DiagnosticList rows={sections.summary} />
      {!!sections.context.length && (
        <details className="machine-diagnostics-section">
          <summary
            className="ns-disclosure"
            tabIndex={0}
            onKeyDown={(event) => {
              if (event.key === 'Enter') event.stopPropagation();
            }}
          >
            Environment details
          </summary>
          <div className="machine-diagnostics-section-body">
            <DiagnosticList rows={sections.context} />
          </div>
        </details>
      )}
      <details
        className="machine-diagnostics-section"
        open={doctorOpen}
        onToggle={(event) => {
          const open = event.currentTarget.open;
          setDoctorOpen(open);
          if (open && !doctorReport && !doctorLoading) void loadDoctor();
        }}
      >
        <summary
          className="ns-disclosure"
          tabIndex={0}
          onKeyDown={(event) => {
            if (event.key === 'Enter') event.stopPropagation();
          }}
        >
          System health (axv doctor)
        </summary>
        <div className="machine-diagnostics-section-body machine-diagnostics-doctor">
          <div className="machine-diagnostics-doctor-bar">
            {doctorLoading ? (
              <p className="fine-print" role="status">
                Running host checks…
              </p>
            ) : doctorReport ? (
              <p className="machine-diagnostics-summary">
                <span data-status="ok">{doctorReport.ok} ok</span>
                <span data-status="info">{doctorReport.info} info</span>
                <span data-status="warn">{doctorReport.warn} warn</span>
                <span data-status="fail">{doctorReport.fail} fail</span>
              </p>
            ) : (
              <span className="fine-print">Host diagnostics</span>
            )}
            {!doctorLoading && (
              <Button onClick={() => void loadDoctor()}>Refresh checks</Button>
            )}
          </div>
          {doctorError && (
            <p className="machines-error" role="alert">
              {doctorError}
            </p>
          )}
          {doctorReport && (
            <ul className="machine-diagnostics-checks">
              {doctorReport.checks.map((item) => (
                <li key={item.id} data-status={item.status}>
                  <span
                    className="machine-diagnostics-badge"
                    data-status={item.status}
                  >
                    {item.status.toUpperCase()}
                  </span>
                  <div className="machine-diagnostics-check-body">
                    <strong>{item.label}</strong>
                    <p>{item.detail}</p>
                    {item.fix && (
                      <p className="machine-diagnostics-fix">
                        Fix: <code>{item.fix}</code>
                      </p>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
      </details>
      <div className="app-actions machine-diagnostics-footer">
        <Button
          aria-disabled={copyState === 'copying'}
          onClick={() => void copy()}
        >
          Copy diagnostics
        </Button>
        <span className="fine-print" role="status">
          {
            {
              idle: '',
              copying: 'Copying…',
              copied: 'Diagnostics copied.',
              failed:
                'Clipboard access failed. Select and copy the text below.',
            }[copyState]
          }
        </span>
      </div>
      {copyState === 'failed' && (
        <TextArea
          label="Diagnostics text"
          value={text}
          rows={4}
          readOnly
          spellCheck={false}
          onFocus={(event) => event.currentTarget.select()}
        />
      )}
    </div>
  );
}

export function openMachineDiagnostics(
  desktop: DesktopAPI,
  data: DiagnosticData,
  options: DiagnosticOptions,
) {
  return desktop.dialog({
    title: 'VM Diagnostics',
    showIcon: false,
    message: <MachineDiagnostics sections={diagnosticRows(data, options)} />,
    buttons: [{ label: 'Close', value: 'close', default: true, cancel: true }],
  });
}
