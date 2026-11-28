import { useEffect, useRef, useState } from 'react';
import { Button, IconButton } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { request } from '../../shared/api/http';
import type { VMProject, VMProjectBinding } from '../project-files/vm-api';
import { cloudflareAPI } from './api';
import { CloudflarePublicationCard } from './CloudflarePublicationCard';
import {
  CloudflarePublicationForm,
  targetLabel,
} from './CloudflarePublicationForm';
import type { PublishDraft } from './CloudflarePublicationForm';
import { publicationActionReview } from './publication-action';
import { unconfirmedPublicationRequest } from './publication-outcome';
import {
  allocatedPublicationHostname,
  checkPublicationActionReceipt,
  checkPublicationReceipt,
  publicationName,
} from './publication-receipt';
import type { RequestedPublication } from './publishing-context';
import type { CloudflarePublication } from './types';
import { useResource } from './useResource';

const newDraft = (): PublishDraft => ({
  name: '',
  visibility: 'owner',
  requestId: crypto.randomUUID(),
});
const readPublications = (signal: AbortSignal) =>
  cloudflareAPI.publications(signal);

async function readProjects(signal: AbortSignal) {
  const environment = await request<{
    runtime?: { state: string; hostId?: string; generation: number };
  }>('/compute/environment', { signal, cache: 'no-store' });
  const runtime = environment.runtime;
  if (!runtime?.hostId || runtime.state !== 'ready')
    throw new Error(
      'Start the compute host to choose a VM project, or open Publishing from its project preview.',
    );
  return request<{ projects: VMProject[] }>('/compute/projects', {
    signal,
    cache: 'no-store',
    headers: {
      'X-AXV-Host-ID': runtime.hostId,
      'X-AXV-Compute-Generation': String(runtime.generation),
    },
  });
}

export function publicationPhase(record: CloudflarePublication) {
  if (record.state === 'published' || record.state === 'removed')
    return undefined;
  const labels: Record<string, string> = {
    'verify-target': 'Checking the original service',
    dns: 'Creating DNS record',
    ingress: 'Updating tunnel ingress',
    'revoke-origin': 'Revoking AXV access',
    'remove-ingress': 'Removing tunnel ingress',
    'remove-dns': 'Removing DNS record',
    'retry-original': 'Retrying the original target',
    'restore-original': 'Restoring the original publication',
    'restart-required': 'Review before resuming the original service',
    persistence: 'Recording publication state',
  };
  return (
    labels[record.phase] ||
    (record.state === 'provisioning'
      ? 'Updating publication'
      : record.state === 'removing'
        ? 'Removing publication'
        : undefined)
  );
}

export function CloudflarePublishing({
  active,
  present,
  allowed,
  requested,
  desktop,
  onDirtyChange,
  onSetup,
}: {
  active: boolean;
  present: boolean;
  allowed: boolean;
  requested?: RequestedPublication;
  desktop: DesktopAPI;
  onDirtyChange: (dirty: boolean) => void;
  onSetup: () => void;
}) {
  const records = useResource(readPublications, active && allowed, 5000);
  const [draft, setDraft] = useState<PublishDraft>(newDraft);
  const [editing, setEditing] = useState(false);
  const [reviewing, setReviewing] = useState(false);
  const [reviewed, setReviewed] = useState<{
    instanceId: string;
    domain: string;
    mode?: 'dedicated-site' | 'public-suffix';
  }>();
  const [incoming, setIncoming] = useState<RequestedPublication>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const [unconfirmedRequestId, setUnconfirmedRequestId] = useState('');
  const [port, setPort] = useState('3000');
  const [projectId, setProjectId] = useState('');
  const seen = useRef('');
  const operation = useRef<AbortController | null>(null);
  const confirming = useRef(false);
  const latest = useRef({ active, present, allowed, draft });
  latest.current = { active, present, allowed, draft };
  const dirty =
    editing &&
    (!!draft.target ||
      !!draft.name ||
      !!projectId ||
      port !== '3000' ||
      draft.visibility !== 'owner');
  const projects = useResource(
    readProjects,
    active && allowed && editing && !draft.target,
  );

  useEffect(() => {
    onDirtyChange(dirty);
  }, [dirty, onDirtyChange]);
  useEffect(() => {
    if (!requested || seen.current === requested.id || !active || !allowed)
      return;
    seen.current = requested.id;
    if (dirty || busy) setIncoming(requested);
    else {
      setDraft({
        ...newDraft(),
        target: { binding: { ...requested.binding }, port: requested.port },
      });
      setEditing(true);
      setReviewing(false);
    }
  }, [requested, active, allowed, dirty, busy]);
  useEffect(() => () => operation.current?.abort(), []);
  useEffect(() => {
    if ((active || confirming.current) && present && allowed) return;
    operation.current?.abort();
    operation.current = null;
    setBusy(false);
  }, [active, present, allowed]);

  async function chooseProject() {
    const project = projects.value?.projects.find(
      (item) => item.id === projectId,
    );
    const number = Number(port);
    if (
      !active ||
      !allowed ||
      !project ||
      !Number.isInteger(number) ||
      number < 1024 ||
      number > 65535 ||
      operation.current
    )
      return;
    const controller = new AbortController();
    operation.current = controller;
    setBusy(true);
    setError('');
    const signal = AbortSignal.any([
      controller.signal,
      AbortSignal.timeout(30000),
    ]);
    try {
      const result = await request<{
        project: VMProject;
        binding: VMProjectBinding;
      }>(`/compute/projects/${encodeURIComponent(project.id)}/binding`, {
        signal,
        headers: { 'X-AXV-Host-ID': project.hostId },
      });
      signal.throwIfAborted();
      if (
        result.binding.hostId !== project.hostId ||
        result.binding.projectId !== project.id
      )
        throw new Error(
          'The project changed. Refresh projects and choose it again.',
        );
      setDraft((current) => ({
        ...current,
        target: { binding: result.binding, port: number },
      }));
    } catch (cause) {
      if (!controller.signal.aborted)
        setError(
          cause instanceof Error
            ? cause.message
            : 'Could not capture the project target.',
        );
    } finally {
      confirming.current = false;
      if (operation.current === controller) {
        operation.current = null;
        setBusy(false);
      }
    }
  }

  async function publish() {
    const current = latest.current;
    if (
      !current.active ||
      !current.allowed ||
      !current.draft.target ||
      !publicationName(current.draft.name.trim().toLowerCase()) ||
      !reviewing ||
      operation.current ||
      !records.value ||
      records.error ||
      !canSubmit ||
      !reviewed ||
      reviewed.instanceId !== records.value.instanceId ||
      reviewed.domain !== records.value.domain
    )
      return;
    const captured = current.draft;
    const previouslyUnconfirmedId = unconfirmedRequestId;
    const controller = new AbortController();
    operation.current = controller;
    setBusy(true);
    setError('');
    setMessage('');
    setUnconfirmedRequestId(captured.requestId);
    const signal = AbortSignal.any([
      controller.signal,
      AbortSignal.timeout(30000),
    ]);
    try {
      const submitted = {
        ...captured.target!,
        instanceId: records.value.instanceId,
        name: captured.name.trim().toLowerCase(),
        domain: reviewed.domain,
        visibility: captured.visibility,
        requestId: captured.requestId,
      };
      const receipt = await cloudflareAPI.publish(submitted, signal);
      signal.throwIfAborted();
      checkPublicationReceipt(receipt, submitted, reviewed.domain);
      setUnconfirmedRequestId('');
      setMessage(
        'Publication accepted. Check its status below; public reachability is unverified.',
      );
      setEditing(false);
      setReviewing(false);
      setDraft(newDraft());
      records.refresh();
    } catch (cause) {
      const unconfirmed = unconfirmedPublicationRequest(
        captured.requestId,
        previouslyUnconfirmedId,
        cause,
      );
      setUnconfirmedRequestId((currentId) =>
        currentId === captured.requestId ? unconfirmed : currentId,
      );
      if (!controller.signal.aborted) {
        setError(
          cause instanceof Error
            ? cause.message
            : 'Could not confirm publication. Refresh to check its status before retrying.',
        );
        records.refresh();
      }
    } finally {
      confirming.current = false;
      if (operation.current === controller) {
        operation.current = null;
        setBusy(false);
      }
    }
  }

  async function act(
    record: CloudflarePublication,
    action: 'unpublish' | 'retry',
  ) {
    if (!active || !present || !allowed || operation.current) return;
    const controller = new AbortController();
    // Reserve the action during review without disabling the modal's opener.
    operation.current = controller;
    setError('');
    try {
      const review = publicationActionReview(record, action);
      if (review) {
        confirming.current = true;
        const answer = await desktop.dialog(review.dialog);
        confirming.current = false;
        if (
          answer?.button !== review.acceptedButton ||
          controller.signal.aborted ||
          !latest.current.present ||
          !latest.current.allowed
        )
          return;
      }
      setBusy(true);
      const signal = AbortSignal.any([
        controller.signal,
        AbortSignal.timeout(30000),
      ]);
      const receipt = await cloudflareAPI[action](record, signal);
      signal.throwIfAborted();
      checkPublicationActionReceipt(receipt, record);
      records.refresh();
    } catch (cause) {
      if (!controller.signal.aborted) {
        setError(
          cause instanceof Error
            ? cause.message
            : 'Could not confirm the operation. Refresh to check.',
        );
        records.refresh();
      }
    } finally {
      confirming.current = false;
      if (operation.current === controller) {
        operation.current = null;
        setBusy(false);
      }
    }
  }

  if (!allowed)
    return (
      <div className="cloudflare-card">
        <p className="cloudflare-empty-line">
          Publishing is unavailable for this connection.
        </p>
      </div>
    );
  const disabled = !active || busy;
  const name = draft.name.trim().toLowerCase();
  const validName = publicationName(name);
  const reviewChanged =
    reviewing &&
    !!reviewed &&
    !!records.value &&
    (reviewed.instanceId !== records.value.instanceId ||
      reviewed.domain !== records.value.domain);
  const originalRetained =
    unconfirmedRequestId === draft.requestId &&
    records.value?.publications.some(
      (record) => record.requestId === draft.requestId,
    );
  const canSubmit = Boolean(
    !records.error &&
    records.value?.domain &&
    (records.value.supported || originalRetained),
  );

  return (
    <div className="cloudflare-overview-stack">
      <div className="cloudflare-card">
        <div className="cloudflare-section-heading">
          <h2>Publishing</h2>
          <IconButton
            icon="refresh"
            label="Refresh publications"
            disabled={disabled || records.loading}
            onClick={records.refresh}
          />
        </div>
        <p className="cloudflare-section-intro">
          Publish a VM project’s service through your Cloudflare tunnel.
        </p>
        {(error || records.error) && (
          <p className="app-error" role="alert">
            {error || records.error}
          </p>
        )}
        {records.error && records.value && (
          <p className="cloudflare-stale-note">
            Showing previous publications. Status is unknown; links are hidden.
          </p>
        )}
        {message && (
          <p className="cloudflare-status-banner" role="status">
            {message}
          </p>
        )}
        {records.value && !records.value.supported && (
          <div className="cloudflare-notice">
            <p>
              {records.value.reason ||
                'App publishing needs an account connection.'}
            </p>
            <div className="cloudflare-actions">
              <Button disabled={disabled} onClick={onSetup}>
                Open setup
              </Button>
            </div>
          </div>
        )}
        {incoming && (
          <div className="cloudflare-notice" role="status">
            <p>
              A project requested publishing: {targetLabel(incoming)}. Your
              current draft is kept.
            </p>
            <div className="cloudflare-actions">
              <Button
                disabled={disabled}
                onClick={() => {
                  setDraft({
                    ...newDraft(),
                    target: {
                      binding: { ...incoming.binding },
                      port: incoming.port,
                    },
                  });
                  setEditing(true);
                  setReviewing(false);
                  setIncoming(undefined);
                  setError('');
                }}
              >
                Use requested project
              </Button>
              <Button
                disabled={disabled}
                onClick={() => setIncoming(undefined)}
              >
                Keep current draft
              </Button>
            </div>
          </div>
        )}
        {!editing && (
          <div className="cloudflare-actions">
            <Button
              primary
              disabled={disabled || !records.value?.supported || !canSubmit}
              onClick={() => {
                setEditing(true);
                setDraft(newDraft());
                setError('');
                setMessage('');
              }}
            >
              New publication
            </Button>
          </div>
        )}
      </div>
      {editing && (
        <CloudflarePublicationForm
          draft={draft}
          reviewing={reviewing}
          reviewed={reviewed}
          disabled={disabled}
          busy={busy}
          canSubmit={canSubmit}
          validName={validName}
          normalizedName={name}
          reviewChanged={reviewChanged}
          unconfirmedRequestId={unconfirmedRequestId}
          projectsLoading={projects.loading}
          projectsError={projects.error}
          projects={projects.value?.projects}
          projectId={projectId}
          port={port}
          onChangeProjectId={setProjectId}
          onChangePort={setPort}
          onChooseProject={() => void chooseProject()}
          onRefreshProjects={projects.refresh}
          onChangeDraft={setDraft}
          onBack={() => setReviewing(false)}
          onReview={() => {
            if (!records.value?.domain) return;
            setReviewed({
              instanceId: records.value.instanceId,
              domain: records.value.domain,
              mode: records.value.isolation?.mode,
            });
            setReviewing(true);
          }}
          onPublish={() => void publish()}
          onCancel={() => {
            setEditing(false);
            setReviewing(false);
            setDraft(newDraft());
          }}
        />
      )}
      <div className="cloudflare-publications" aria-label="Publications">
        {!records.value && records.loading && (
          <div className="cloudflare-card">
            <p className="cloudflare-empty-line">Loading publications…</p>
          </div>
        )}
        {records.value && !records.value.publications.length && (
          <div className="cloudflare-card">
            <p className="cloudflare-empty-line">No published services.</p>
          </div>
        )}
        {records.value?.publications.map((record) => (
          <CloudflarePublicationCard
            key={record.id}
            record={record}
            listError={!!records.error}
            disabled={disabled}
            phase={publicationPhase(record)}
            url={safePublicationURL(record)}
            onAction={(item, action) => void act(item, action)}
          />
        ))}
      </div>
    </div>
  );
}

export function safePublicationURL(record: CloudflarePublication) {
  if (
    record.state !== 'published' ||
    !record.url ||
    !allocatedPublicationHostname(record)
  )
    return undefined;
  try {
    const url = new URL(record.url);
    return url.protocol === 'https:' &&
      url.hostname === record.hostname &&
      !url.username &&
      !url.password &&
      !url.port &&
      url.pathname === '/' &&
      !url.search &&
      !url.hash
      ? url.origin
      : undefined;
  } catch {
    return undefined;
  }
}
