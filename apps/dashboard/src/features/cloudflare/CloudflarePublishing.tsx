import { useEffect, useRef, useState } from 'react';
import { Button, Field, IconButton } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { request } from '../../shared/api/http';
import type { VMProject, VMProjectBinding } from '../project-files/vm-api';
import { cloudflareAPI } from './api';
import type { CloudflarePublication, CloudflarePublishTarget } from './types';
import { useResource } from './useResource';
import {
  allocatedPublicationHostname,
  checkPublicationActionReceipt,
  checkPublicationReceipt,
  publicationName,
} from './publication-receipt';
import { unconfirmedPublicationRequest } from './publication-outcome';
import { publicationActionReview } from './publication-action';
import type { RequestedPublication } from './publishing-context';

interface PublishDraft {
  target?: CloudflarePublishTarget;
  name: string;
  visibility: 'owner' | 'public';
  requestId: string;
}

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

const stateLabels: Record<CloudflarePublication['state'], string> = {
  provisioning: 'Publishing…',
  published: 'Published',
  unavailable: 'Target unavailable',
  removing: 'Removing…',
  removed: 'Removed',
  error: 'Needs attention',
};

function targetLabel(target: CloudflarePublishTarget) {
  return `Project ${target.binding.projectId} · VM ${target.binding.machineId} · port ${target.port}`;
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

  if (!allowed) return <p>Publishing is unavailable for this connection.</p>;
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
  const canSubmit =
    !records.error &&
    !!records.value?.domain &&
    (records.value.supported || originalRetained);
  return (
    <div>
      <div className="cloudflare-section-heading">
        <h2>Publishing</h2>
        <IconButton
          icon="refresh"
          label="Refresh publications"
          disabled={disabled || records.loading}
          onClick={records.refresh}
        />
      </div>
      <p>Publish a VM project’s service through your Cloudflare tunnel.</p>
      {(error || records.error) && (
        <p className="app-error" role="alert">
          {error || records.error}
        </p>
      )}
      {records.error && records.value && (
        <p>
          Showing previous publications. Status is unknown; links are hidden.
        </p>
      )}
      {message && <p role="status">{message}</p>}
      {records.value && !records.value.supported && (
        <div className="cloudflare-notice">
          <p>
            {records.value.reason ||
              'App publishing needs an account connection.'}
          </p>
          <Button disabled={disabled} onClick={onSetup}>
            Open setup
          </Button>
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
            <Button disabled={disabled} onClick={() => setIncoming(undefined)}>
              Keep current draft
            </Button>
          </div>
        </div>
      )}
      {!editing ? (
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
      ) : (
        <fieldset>
          <legend>
            {reviewing ? 'Review publication' : 'New publication'}
          </legend>
          {draft.target ? (
            <p className="cloudflare-target">{targetLabel(draft.target)}</p>
          ) : (
            <>
              {projects.error && (
                <p className="app-error" role="alert">
                  {projects.error}
                </p>
              )}
              <label className="cloudflare-field">
                VM project
                <select
                  disabled={disabled || projects.loading}
                  value={projectId}
                  onChange={(event) => setProjectId(event.target.value)}
                >
                  <option value="">Select a project…</option>
                  {projects.value?.projects.map((project) => (
                    <option key={project.id} value={project.id}>
                      {project.name || project.id}
                    </option>
                  ))}
                </select>
              </label>
              <Field
                label="Service port"
                type="number"
                min={1024}
                max={65535}
                value={port}
                disabled={disabled}
                onChange={(event) => setPort(event.target.value)}
              />
              <div className="cloudflare-actions">
                <Button
                  disabled={
                    disabled ||
                    !projectId ||
                    !Number.isInteger(Number(port)) ||
                    Number(port) < 1024 ||
                    Number(port) > 65535
                  }
                  onClick={() => void chooseProject()}
                >
                  Use project target
                </Button>
                <Button
                  disabled={disabled || projects.loading}
                  onClick={projects.refresh}
                >
                  Refresh projects
                </Button>
              </div>
            </>
          )}
          {!reviewing ? (
            <>
              <Field
                label="Name"
                placeholder="editor"
                maxLength={24}
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
                aria-invalid={!!draft.name && !validName}
                aria-describedby="cloudflare-publication-name-hint"
                value={draft.name}
                disabled={disabled}
                onChange={(event) =>
                  setDraft({ ...draft, name: event.target.value })
                }
              />
              <p
                id="cloudflare-publication-name-hint"
                className={
                  draft.name && !validName ? 'app-error' : 'fine-print'
                }
              >
                {draft.name && !validName
                  ? 'Use 1–24 letters, numbers or internal hyphens; do not start with xn--.'
                  : 'A unique URL is assigned when you publish.'}
              </p>
              <label className="cloudflare-field">
                Access
                <select
                  value={draft.visibility}
                  disabled={disabled}
                  onChange={(event) =>
                    setDraft({
                      ...draft,
                      visibility: event.target
                        .value as PublishDraft['visibility'],
                    })
                  }
                >
                  <option value="owner">
                    Owner only — require Cloudflare Access
                  </option>
                  <option value="public">
                    Public — anyone on the internet
                  </option>
                </select>
              </label>
              <p className="fine-print">
                Owner access needs an existing Access application and its
                publishing audience (AUD).
              </p>
            </>
          ) : (
            <>
              <dl className="info-list">
                <dt>Name</dt>
                <dd>{name}</dd>
                <dt>Allocation domain</dt>
                <dd>{reviewed?.domain}</dd>
                <dt>Access</dt>
                <dd>
                  {draft.visibility === 'public'
                    ? 'Public — anyone on the internet'
                    : 'Owner only — Cloudflare Access required'}
                </dd>
              </dl>
              <p>A unique URL is assigned when you publish.</p>
              {reviewed?.mode === 'dedicated-site' && (
                <p className="fine-print">
                  This registered domain is permanently reserved for this
                  publication, even after removal.
                </p>
              )}
              <p
                className={
                  draft.visibility === 'public' ? 'cloudflare-notice' : ''
                }
              >
                {draft.visibility === 'public'
                  ? 'Anyone on the internet can reach this service. Keep its own authentication and security enabled. Publish only if intended.'
                  : 'Your Access application must cover the assigned hostname; AXV requires your configured owner identity.'}
              </p>
              <p className="fine-print">
                Published status does not verify public reachability or WAF
                protection.
              </p>
            </>
          )}
          {reviewChanged && (
            <p className="cloudflare-notice" role="status">
              Publishing settings changed. Go Back and review the allocation
              domain again. Your draft is kept.
            </p>
          )}
          {unconfirmedRequestId === draft.requestId && (
            <p className="cloudflare-notice" role="status">
              {busy
                ? 'Waiting for a publication receipt.'
                : 'Publication is unconfirmed. Refresh and inspect the original publication before retrying or starting another request.'}{' '}
              Request ID: {draft.requestId}
            </p>
          )}
          <div className="cloudflare-actions">
            {reviewing ? (
              <>
                <Button disabled={disabled} onClick={() => setReviewing(false)}>
                  Back
                </Button>
                <Button
                  primary
                  disabled={disabled || !canSubmit || reviewChanged}
                  onClick={() => void publish()}
                >
                  {busy ? 'Publishing…' : 'Publish'}
                </Button>
              </>
            ) : (
              <Button
                primary
                disabled={disabled || !draft.target || !validName || !canSubmit}
                onClick={() => {
                  if (!records.value?.domain) return;
                  setReviewed({
                    instanceId: records.value.instanceId,
                    domain: records.value.domain,
                    mode: records.value.isolation?.mode,
                  });
                  setReviewing(true);
                }}
              >
                Review publication
              </Button>
            )}
            <Button
              disabled={disabled}
              onClick={() => {
                setEditing(false);
                setReviewing(false);
                setDraft(newDraft());
              }}
            >
              Cancel draft
            </Button>
          </div>
          <details className="cloudflare-disclosure">
            <summary>Details</summary>
            <dl className="info-list">
              {unconfirmedRequestId !== draft.requestId && (
                <>
                  <dt>Request ID</dt>
                  <dd>{draft.requestId}</dd>
                </>
              )}
              {draft.target && (
                <>
                  <dt>Host</dt>
                  <dd>{draft.target.binding.hostId}</dd>
                  <dt>Boot</dt>
                  <dd>{draft.target.binding.bootId}</dd>
                </>
              )}
            </dl>
            <p className="fine-print">
              Recovery reuses this request and its original target. Inspect the
              publication before starting another request if its outcome is
              uncertain.
            </p>
          </details>
        </fieldset>
      )}
      <div className="cloudflare-publications" aria-label="Publications">
        {!records.value && records.loading && <p>Loading publications…</p>}
        {records.value && !records.value.publications.length && (
          <p>No published services.</p>
        )}
        {records.value?.publications.map((record) => (
          <article className="cloudflare-publication" key={record.id}>
            <div className="cloudflare-section-heading">
              <h3>{record.hostname}</h3>
              <strong>
                {records.error ? 'Unknown' : stateLabels[record.state]}
              </strong>
            </div>
            <p>
              {targetLabel(record)} ·{' '}
              {record.visibility === 'public' ? 'Public' : 'Owner only'}
            </p>
            {publicationPhase(record) && (
              <p className="fine-print">{publicationPhase(record)}</p>
            )}
            {record.error && <p className="app-error">{record.error}</p>}
            {record.cleanupPending && (
              <p>
                Cloudflare cleanup is pending. Retry cleanup removes only this
                publication’s cloud resources; AXV access stays closed.
              </p>
            )}
            {!records.error &&
              record.state === 'published' &&
              safePublicationURL(record) && (
                <p>
                  <a
                    href={safePublicationURL(record)}
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    Open {record.hostname}
                  </a>
                </p>
              )}
            <div className="cloudflare-actions">
              {record.state !== 'removed' && record.state !== 'removing' && (
                <Button
                  disabled={disabled}
                  onClick={() => void act(record, 'unpublish')}
                >
                  Unpublish
                </Button>
              )}
              {(record.state === 'error' ||
                record.state === 'unavailable' ||
                record.cleanupPending) && (
                <Button
                  disabled={disabled}
                  onClick={() => void act(record, 'retry')}
                >
                  {record.cleanupPending
                    ? 'Retry cleanup'
                    : record.phase === 'restart-required'
                      ? 'Resume'
                      : 'Retry'}
                </Button>
              )}
            </div>
            <details className="cloudflare-disclosure">
              <summary>Details</summary>
              <dl className="info-list">
                <dt>Name</dt>
                <dd>{record.name}</dd>
                <dt>Allocation domain</dt>
                <dd>{record.domain}</dd>
                <dt>Request ID</dt>
                <dd>{record.requestId}</dd>
                <dt>Host</dt>
                <dd>{record.binding.hostId}</dd>
                <dt>Boot</dt>
                <dd>{record.binding.bootId}</dd>
              </dl>
            </details>
          </article>
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
