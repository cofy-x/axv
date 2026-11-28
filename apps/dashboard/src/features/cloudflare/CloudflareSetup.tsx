import { useEffect, useRef, useState } from 'react';
import { Button, ButtonGroup, IconButton } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { cloudflareAPI } from './api';
import { CloudflareAccountWizard } from './CloudflareAccountWizard';
import { CloudflareDesktopSetupCard } from './CloudflareDesktopSetupCard';
import { CloudflareTokenRotation } from './CloudflareTokenRotation';
import {
  configDraft,
  configWrite,
  sameDraft,
  savedDraft,
} from './config-draft';
import type { ConfigDraft } from './config-draft';
import { SetupGuide } from './SetupGuide';
import type {
  CloudflareConfig,
  CloudflareWizardResult,
  CloudflareWizardStep,
} from './types';
import { useResource } from './useResource';

const readConfig = (signal: AbortSignal) =>
  cloudflareAPI.config({ local: true }, signal);

export function CloudflareSetup({
  active,
  present,
  allowed,
  desktop,
  enabled,
  onSaved,
  onDirtyChange,
  section,
  onSectionChange,
}: {
  active: boolean;
  present: boolean;
  allowed: boolean;
  desktop: DesktopAPI;
  enabled: boolean;
  onSaved: () => void;
  onDirtyChange: (dirty: boolean) => void;
  section?: 'desktop' | 'account';
  onSectionChange: (section: 'desktop' | 'account') => void;
}) {
  const resource = useResource(readConfig, active && allowed);
  const [baseline, setBaseline] = useState<CloudflareConfig>();
  const [draft, setDraft] = useState<ConfigDraft>();
  const [wizard, setWizard] = useState<number>();
  const [result, setResult] = useState<CloudflareWizardResult>();
  const [choices, setChoices] = useState<CloudflareWizardResult>({
    ok: true,
    message: '',
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const [guide, setGuide] = useState(false);
  const [rotating, setRotating] = useState(false);
  const [replacementToken, setReplacementToken] = useState('');
  const operation = useRef<AbortController | null>(null);
  const confirming = useRef(false);
  const latest = useRef({ active, present, allowed, draft, baseline, enabled });
  latest.current = { active, present, allowed, draft, baseline, enabled };
  const heading = useRef<HTMLHeadingElement>(null);
  const wizardHeading = useRef<HTMLParagraphElement>(null);
  const visibleStep = useRef<number | undefined>(undefined);
  const selectedSection =
    section ||
    (baseline?.access.origin && !baseline.account.configured
      ? 'account'
      : 'desktop');
  const configDirty =
    !!draft && !!baseline && !sameDraft(draft, configDraft(baseline));
  const dirty = !!replacementToken || configDirty;

  useEffect(() => {
    onDirtyChange(dirty);
  }, [dirty, onDirtyChange]);
  useEffect(() => {
    if (resource.value && !baseline) {
      setBaseline(resource.value);
      setDraft(configDraft(resource.value));
    }
  }, [resource.value, baseline]);
  useEffect(() => () => operation.current?.abort(), []);
  useEffect(() => {
    if ((active || confirming.current) && present && allowed) return;
    operation.current?.abort();
    operation.current = null;
    setBusy(false);
  }, [active, present, allowed]);
  useEffect(() => {
    if (wizard === undefined) {
      visibleStep.current = undefined;
      return;
    }
    if (
      !active ||
      !present ||
      !allowed ||
      selectedSection !== 'account' ||
      visibleStep.current === wizard
    )
      return;
    visibleStep.current = wizard;
    wizardHeading.current?.scrollIntoView({ block: 'nearest' });
  }, [active, present, allowed, selectedSection, wizard]);

  function edit(next: ConfigDraft) {
    setDraft(next);
    setResult(undefined);
    setError('');
    setMessage('');
  }

  async function validate(step: CloudflareWizardStep, advance = false) {
    const current = latest.current;
    if (
      !current.active ||
      !current.allowed ||
      !current.draft ||
      operation.current
    )
      return;
    const submitted = current.draft;
    const controller = new AbortController();
    operation.current = controller;
    setBusy(true);
    setError('');
    setResult(undefined);
    const signal = AbortSignal.any([
      controller.signal,
      AbortSignal.timeout(30000),
    ]);
    try {
      const value = await cloudflareAPI.validate(
        { local: true },
        {
          step,
          ...(submitted.tokenAction === 'replace'
            ? { token: submitted.token }
            : {}),
          accountId: submitted.account.accountId,
          zoneId: submitted.account.zoneId,
          tunnelId: submitted.account.tunnelId,
          domain: submitted.account.domain,
          access: submitted.access,
        },
        signal,
      );
      signal.throwIfAborted();
      if (!latest.current.draft || !sameDraft(latest.current.draft, submitted))
        return;
      setResult(value);
      setChoices((previous) => ({ ...previous, ...value }));
      if (advance)
        setWizard((previous) =>
          previous === undefined ? undefined : Math.min(previous + 1, 5),
        );
    } catch (cause) {
      if (!controller.signal.aborted)
        setError(
          cause instanceof Error ? cause.message : 'Validation failed. Retry.',
        );
    } finally {
      confirming.current = false;
      if (operation.current === controller) {
        operation.current = null;
        setBusy(false);
      }
    }
  }

  async function save() {
    const initial = latest.current;
    if (
      !initial.active ||
      !initial.allowed ||
      !initial.draft ||
      !initial.baseline ||
      operation.current
    )
      return;
    if (initial.enabled) {
      setError('Disable desktop access in Overview before saving settings.');
      return;
    }
    const submitted = initial.draft;
    const captured = initial.baseline;
    const controller = new AbortController();
    operation.current = controller;
    setError('');
    setMessage('');
    try {
      confirming.current = true;
      const answer = await desktop.dialog({
        title: 'Save Cloudflare settings?',
        message: `Desktop: ${submitted.access.origin || 'not configured'}\nPublishing domain: ${submitted.account.domain || 'not configured'}\nAPI token: ${submitted.tokenAction === 'replace' ? 'replace saved token' : submitted.tokenAction === 'clear' ? 'remove saved token' : 'keep saved token'}\n\nThis saves the current AXV profile. It does not enable desktop access, start cloudflared, or create an Access policy.`,
        buttons: [
          { label: 'Cancel', value: 'cancel', cancel: true, default: true },
          { label: 'Save settings', value: 'save' },
        ],
      });
      confirming.current = false;
      if (
        answer?.button !== 'save' ||
        controller.signal.aborted ||
        !latest.current.present ||
        !latest.current.allowed
      )
        return;
      setBusy(true);
      const signal = AbortSignal.any([
        controller.signal,
        AbortSignal.timeout(30000),
      ]);
      const saved = await cloudflareAPI.saveConfig(
        { local: true },
        configWrite(captured, submitted),
        signal,
      );
      signal.throwIfAborted();
      setBaseline(saved);
      setDraft((current) =>
        current ? savedDraft(current, submitted, saved) : configDraft(saved),
      );
      setMessage(
        'Settings saved. Enable desktop access in Overview when your connector and Access policy are ready.',
      );
      setWizard(undefined);
      setResult(undefined);
      resource.refresh();
      onSaved();
    } catch (cause) {
      if (!controller.signal.aborted)
        setError(
          cause instanceof Error
            ? cause.message
            : 'Could not save settings. Your draft is kept.',
        );
    } finally {
      confirming.current = false;
      if (operation.current === controller) {
        operation.current = null;
        setBusy(false);
      }
    }
  }

  async function reload() {
    if (!active || !allowed || operation.current) return;
    const controller = new AbortController();
    operation.current = controller;
    setError('');
    try {
      if (dirty) {
        confirming.current = true;
        const answer = await desktop.dialog({
          title: 'Replace unsaved settings?',
          message:
            'Reloading fetches saved settings from AXV and replaces this draft.',
          buttons: [
            { label: 'Cancel', value: 'cancel', cancel: true, default: true },
            { label: 'Reload settings', value: 'reload' },
          ],
        });
        confirming.current = false;
        if (
          answer?.button !== 'reload' ||
          controller.signal.aborted ||
          !latest.current.present ||
          !latest.current.allowed
        )
          return;
      }
      setBusy(true);
      const signal = AbortSignal.any([
        controller.signal,
        AbortSignal.timeout(15000),
      ]);
      const saved = await cloudflareAPI.config({ local: true }, signal);
      signal.throwIfAborted();
      setBaseline(saved);
      setDraft(configDraft(saved));
      setWizard(undefined);
      setResult(undefined);
      setMessage('Settings reloaded.');
      setReplacementToken('');
      setRotating(false);
      resource.refresh();
    } catch (cause) {
      if (!controller.signal.aborted)
        setError(
          cause instanceof Error
            ? cause.message
            : 'Could not reload settings. Your draft is kept.',
        );
    } finally {
      confirming.current = false;
      if (operation.current === controller) {
        operation.current = null;
        setBusy(false);
      }
    }
  }

  async function rotateToken() {
    const current = latest.current;
    if (
      !current.active ||
      !current.allowed ||
      !current.baseline ||
      !replacementToken.trim() ||
      operation.current
    )
      return;
    const captured = current.baseline;
    const token = replacementToken;
    const controller = new AbortController();
    operation.current = controller;
    setError('');
    setMessage('');
    try {
      confirming.current = true;
      const answer = await desktop.dialog({
        title: 'Rotate Cloudflare API token?',
        message:
          'Replace the token stored on this AXV computer. Desktop access and existing publications keep their settings. Unsaved configuration edits are kept.',
        buttons: [
          { label: 'Cancel', value: 'cancel', cancel: true, default: true },
          { label: 'Rotate token', value: 'rotate' },
        ],
      });
      confirming.current = false;
      if (
        answer?.button !== 'rotate' ||
        controller.signal.aborted ||
        !latest.current.present ||
        !latest.current.allowed
      )
        return;
      setBusy(true);
      const validationSignal = AbortSignal.any([
        controller.signal,
        AbortSignal.timeout(30000),
      ]);
      await cloudflareAPI.validate(
        { local: true },
        { step: 'token', token },
        validationSignal,
      );
      validationSignal.throwIfAborted();
      const signal = AbortSignal.any([
        controller.signal,
        AbortSignal.timeout(30000),
      ]);
      const saved = await cloudflareAPI.rotateToken(
        { local: true },
        {
          instanceId: captured.instanceId,
          expectedRevision: captured.revision,
          token,
        },
        signal,
      );
      signal.throwIfAborted();
      setBaseline(saved);
      setDraft((value) =>
        value && !sameDraft(value, configDraft(captured))
          ? value
          : configDraft(saved),
      );
      setReplacementToken('');
      setRotating(false);
      setMessage('API token rotated. Other settings and drafts are kept.');
      resource.refresh();
      onSaved();
    } catch (cause) {
      if (!controller.signal.aborted)
        setError(
          cause instanceof Error
            ? cause.message
            : 'Could not confirm token rotation. Refresh settings before retrying.',
        );
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
          Configure Cloudflare on the computer running AXV. Publishing and
          traffic remain available to authorized remote owners.
        </p>
      </div>
    );
  if (!draft || !baseline)
    return (
      <div className="cloudflare-card">
        <p className="cloudflare-empty-line">
          {resource.loading
            ? 'Loading saved settings…'
            : 'Settings are unavailable.'}
        </p>
        {resource.error && (
          <p className="app-error" role="alert">
            {resource.error}
          </p>
        )}
        <div className="cloudflare-actions">
          <Button
            disabled={!active || resource.loading}
            onClick={resource.refresh}
          >
            Retry settings
          </Button>
        </div>
      </div>
    );

  const disabled = !active || busy;
  const wizardOpen = selectedSection === 'account' && wizard !== undefined;
  const saveBlocked = configDirty && enabled;
  const changedElsewhere =
    resource.value && resource.value.revision !== baseline.revision;

  return (
    <div className={`cloudflare-setup ${wizardOpen ? 'is-wizard-open' : ''}`}>
      <div className="cloudflare-setup-toolbar">
        <ButtonGroup label="Setup area" className="cloudflare-setup-areas">
          <Button
            aria-pressed={selectedSection === 'desktop'}
            disabled={disabled}
            onClick={() => onSectionChange('desktop')}
          >
            Desktop access
          </Button>
          <Button
            aria-pressed={selectedSection === 'account'}
            disabled={disabled}
            onClick={() => onSectionChange('account')}
          >
            App publishing
          </Button>
        </ButtonGroup>
        <IconButton
          icon="refresh"
          label="Reload saved settings"
          disabled={disabled}
          onClick={() => void reload()}
        />
      </div>
      {(resource.error || error) && (
        <p className="app-error" role="alert">
          {error || resource.error}
        </p>
      )}
      {changedElsewhere && (
        <p className="cloudflare-notice" role="status">
          Saved settings changed elsewhere. Your draft is kept. Reload saved
          settings to replace it explicitly.
        </p>
      )}
      {message && (
        <p className="cloudflare-status-banner" role="status">
          {message}
        </p>
      )}
      <CloudflareDesktopSetupCard
        visible={selectedSection === 'desktop'}
        active={active}
        disabled={disabled}
        draft={draft}
        onEdit={edit}
        onValidate={() => void validate('access')}
      />
      <fieldset
        className="cloudflare-card"
        hidden={selectedSection !== 'account'}
        inert={selectedSection !== 'account' || !active}
      >
        <legend>
          App publishing <span className="fine-print">Optional</span>
        </legend>
        <div
          hidden={wizard !== undefined}
          inert={!active || wizard !== undefined}
        >
          <p>Connect Cloudflare to publish VM services.</p>
          <dl className="info-list">
            <dt>API token</dt>
            <dd>
              {baseline.account.hasToken
                ? 'Saved on this computer'
                : 'Not saved'}
            </dd>
            <dt>Base domain</dt>
            <dd>{draft.account.domain || 'Not configured'}</dd>
          </dl>
          <div className="cloudflare-actions">
            <Button
              primary={!baseline.account.configured}
              disabled={disabled}
              onClick={() => {
                if (!baseline.account.hasToken && draft.tokenAction === 'keep')
                  edit({ ...draft, tokenAction: 'replace' });
                setWizard(0);
                setResult(undefined);
                setError('');
              }}
            >
              {baseline.account.configured
                ? 'Change connection'
                : 'Connect account'}
            </Button>
            {baseline.account.hasToken && !rotating && (
              <Button disabled={disabled} onClick={() => setRotating(true)}>
                Rotate token
              </Button>
            )}
          </div>
          {baseline.account.hasToken && rotating && (
            <CloudflareTokenRotation
              disabled={disabled}
              rotating={rotating}
              replacementToken={replacementToken}
              onStart={() => setRotating(true)}
              onChangeToken={setReplacementToken}
              onRotate={() => void rotateToken()}
              onCancel={() => {
                setRotating(false);
                setReplacementToken('');
              }}
            />
          )}
        </div>
        {wizard !== undefined && (
          <CloudflareAccountWizard
            wizard={wizard}
            wizardHeading={wizardHeading}
            baseline={baseline}
            draft={draft}
            choices={choices}
            disabled={disabled}
            busy={busy}
            configDirty={configDirty}
            enabled={enabled}
            saveBlocked={saveBlocked}
            onEdit={edit}
            onBack={() => {
              setWizard(wizard - 1);
              setResult(undefined);
            }}
            onValidate={(step) => void validate(step, true)}
            onSave={() => void save()}
            onClose={() => {
              setWizard(undefined);
              setResult(undefined);
            }}
          />
        )}
      </fieldset>
      {result && (
        <div className="cloudflare-status-banner" role="status">
          <p>{result.message}</p>
          {result.warnings?.map((warning) => (
            <p className="cloudflare-notice" key={warning}>
              {warning}
            </p>
          ))}
        </div>
      )}
      {!wizardOpen && (
        <>
          {saveBlocked && (
            <p
              id="cloudflare-settings-save-blocker"
              className="cloudflare-notice"
            >
              Disable desktop access in Overview to save these settings.
            </p>
          )}
          <div className="cloudflare-actions">
            <Button
              primary
              disabled={disabled || !configDirty || enabled}
              aria-describedby={
                saveBlocked ? 'cloudflare-settings-save-blocker' : undefined
              }
              onClick={() => void save()}
            >
              Save settings
            </Button>
            {configDirty && (
              <span className="fine-print">Unsaved settings</span>
            )}
          </div>
        </>
      )}
      <details
        className="cloudflare-disclosure"
        open={guide}
        hidden={wizardOpen}
        inert={wizardOpen || !active}
        onToggle={(event) => setGuide(event.currentTarget.open)}
      >
        <summary>Manual setup guide</summary>
        {guide && active && (
          <div className="cloudflare-disclosure-body">
            <SetupGuide
              headingRef={heading}
              interactive={active}
              onClose={() => setGuide(false)}
            />
          </div>
        )}
      </details>
    </div>
  );
}
