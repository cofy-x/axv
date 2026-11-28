import { Button, Field, Select } from '@cofy-x/axv-nextstep';
import type { VMProject } from '../project-files/vm-api';
import type { CloudflarePublishTarget } from './types';

export interface PublishDraft {
  target?: CloudflarePublishTarget;
  name: string;
  visibility: 'owner' | 'public';
  requestId: string;
}

export function targetLabel(target: CloudflarePublishTarget) {
  return `Project ${target.binding.projectId} · VM ${target.binding.machineId} · port ${target.port}`;
}

interface CloudflarePublicationFormProps {
  draft: PublishDraft;
  reviewing: boolean;
  reviewed?: {
    instanceId: string;
    domain: string;
    mode?: 'dedicated-site' | 'public-suffix';
  };
  disabled: boolean;
  busy: boolean;
  canSubmit: boolean;
  validName: boolean;
  normalizedName: string;
  reviewChanged: boolean;
  unconfirmedRequestId: string;
  projectsLoading: boolean;
  projectsError?: string;
  projects?: VMProject[];
  projectId: string;
  port: string;
  onChangeProjectId: (value: string) => void;
  onChangePort: (value: string) => void;
  onChooseProject: () => void;
  onRefreshProjects: () => void;
  onChangeDraft: (next: PublishDraft) => void;
  onBack: () => void;
  onReview: () => void;
  onPublish: () => void;
  onCancel: () => void;
}

export function CloudflarePublicationForm({
  draft,
  reviewing,
  reviewed,
  disabled,
  busy,
  canSubmit,
  validName,
  normalizedName,
  reviewChanged,
  unconfirmedRequestId,
  projectsLoading,
  projectsError,
  projects,
  projectId,
  port,
  onChangeProjectId,
  onChangePort,
  onChooseProject,
  onRefreshProjects,
  onChangeDraft,
  onBack,
  onReview,
  onPublish,
  onCancel,
}: CloudflarePublicationFormProps) {
  return (
    <fieldset className="cloudflare-card">
      <legend>{reviewing ? 'Review publication' : 'New publication'}</legend>
      {draft.target ? (
        <p className="cloudflare-target">{targetLabel(draft.target)}</p>
      ) : (
        <>
          {projectsError && (
            <p className="app-error" role="alert">
              {projectsError}
            </p>
          )}
          <div className="cloudflare-field">
            <Select
              label="VM project"
              disabled={disabled || projectsLoading}
              value={projectId}
              onChange={(event) => onChangeProjectId(event.target.value)}
            >
              <option value="">Select a project…</option>
              {projects?.map((project) => (
                <option key={project.id} value={project.id}>
                  {project.name || project.id}
                </option>
              ))}
            </Select>
          </div>
          <Field
            label="Service port"
            type="number"
            min={1024}
            max={65535}
            value={port}
            disabled={disabled}
            onChange={(event) => onChangePort(event.target.value)}
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
              onClick={onChooseProject}
            >
              Use project target
            </Button>
            <Button
              disabled={disabled || projectsLoading}
              onClick={onRefreshProjects}
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
              onChangeDraft({ ...draft, name: event.target.value })
            }
          />
          <p
            id="cloudflare-publication-name-hint"
            className={draft.name && !validName ? 'app-error' : 'fine-print'}
          >
            {draft.name && !validName
              ? 'Use 1–24 letters, numbers or internal hyphens; do not start with xn--.'
              : 'A unique URL is assigned when you publish.'}
          </p>
          <div className="cloudflare-field">
            <Select
              label="Access"
              value={draft.visibility}
              disabled={disabled}
              onChange={(event) =>
                onChangeDraft({
                  ...draft,
                  visibility: event.target.value as PublishDraft['visibility'],
                })
              }
            >
              <option value="owner">
                Owner only — require Cloudflare Access
              </option>
              <option value="public">Public — anyone on the internet</option>
            </Select>
          </div>
          <p className="fine-print">
            Owner access needs an existing Access application and its publishing
            audience (AUD).
          </p>
        </>
      ) : (
        <>
          <dl className="info-list">
            <dt>Name</dt>
            <dd>{normalizedName}</dd>
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
            className={draft.visibility === 'public' ? 'cloudflare-notice' : ''}
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
          Publishing settings changed. Go Back and review the allocation domain
          again. Your draft is kept.
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
            <Button disabled={disabled} onClick={onBack}>
              Back
            </Button>
            <Button
              primary
              disabled={disabled || !canSubmit || reviewChanged}
              onClick={onPublish}
            >
              {busy ? 'Publishing…' : 'Publish'}
            </Button>
          </>
        ) : (
          <Button
            primary
            disabled={disabled || !draft.target || !validName || !canSubmit}
            onClick={onReview}
          >
            Review publication
          </Button>
        )}
        <Button disabled={disabled} onClick={onCancel}>
          Cancel draft
        </Button>
      </div>
      <details className="cloudflare-disclosure is-nested">
        <summary>Details</summary>
        <div className="cloudflare-disclosure-body">
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
        </div>
      </details>
    </fieldset>
  );
}
