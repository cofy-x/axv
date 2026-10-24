import { useEffect, useRef, useState } from 'react';
import { Button, ButtonGroup, Field } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { cloudflareAPI } from './api';
import {
  configDraft,
  configWrite,
  sameDraft,
  savedDraft,
} from './config-draft';
import type { ConfigDraft } from './config-draft';
import type {
  CloudflareConfig,
  CloudflareWizardResult,
  CloudflareWizardStep,
} from './types';
import { useResource } from './useResource';
import { SetupGuide } from './SetupGuide';

const readConfig = (signal: AbortSignal) =>
  cloudflareAPI.config({ local: true }, signal);
const steps = [
  'Token',
  'Account',
  'Zone',
  'Tunnel',
  'Domain',
  'Review',
] as const;

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
      setMessage('');
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
      <p>
        Configure Cloudflare on the computer running AXV. Publishing and traffic
        remain available to authorized remote owners.
      </p>
    );
  if (!draft || !baseline)
    return (
      <>
        <p>
          {resource.loading
            ? 'Loading saved settings…'
            : 'Settings are unavailable.'}
        </p>
        {resource.error && (
          <p className="app-error" role="alert">
            {resource.error}
          </p>
        )}
        <Button
          disabled={!active || resource.loading}
          onClick={resource.refresh}
        >
          Retry settings
        </Button>
      </>
    );
  const disabled = !active || busy;
  const wizardOpen = selectedSection === 'account' && wizard !== undefined;
  const saveBlocked = configDirty && enabled;
  const changedElsewhere =
    resource.value && resource.value.revision !== baseline.revision;
  const accessField = (
    key: keyof ConfigDraft['access'],
    label: string,
    placeholder?: string,
  ) => (
    <Field
      label={label}
      value={draft.access[key]}
      placeholder={placeholder}
      disabled={disabled}
      onChange={(event) =>
        edit({
          ...draft,
          access: { ...draft.access, [key]: event.target.value },
        })
      }
    />
  );
  const accountField = (
    key: keyof ConfigDraft['account'],
    label: string,
    placeholder?: string,
  ) => (
    <Field
      label={label}
      value={draft.account[key]}
      placeholder={placeholder}
      disabled={disabled}
      onChange={(event) =>
        edit({
          ...draft,
          account: { ...draft.account, [key]: event.target.value },
        })
      }
    />
  );
  const selector = (
    key: 'accountId' | 'zoneId' | 'tunnelId',
    label: string,
    options?: { id: string; name: string }[],
  ) => (
    <>
      {!!options?.length && (
        <label className="cloudflare-field">
          Choose {label.toLowerCase()}
          <select
            value={draft.account[key]}
            disabled={disabled}
            onChange={(event) =>
              edit({
                ...draft,
                account: { ...draft.account, [key]: event.target.value },
              })
            }
          >
            <option value="">Select…</option>
            {options.map((choice) => (
              <option key={choice.id} value={choice.id}>
                {choice.name} ({choice.id})
              </option>
            ))}
          </select>
        </label>
      )}
      {accountField(key, `${label} ID`)}
    </>
  );
  return (
    <div className="cloudflare-setup">
      <div className="cloudflare-section-heading">
        <h2>Setup</h2>
        <Button disabled={disabled} onClick={() => void reload()}>
          Reload saved settings
        </Button>
      </div>
      {(resource.error || error) && (
        <p className="app-error" role="alert">
          {error || resource.error}
        </p>
      )}
      {changedElsewhere && (
        <p role="status">
          Saved settings changed elsewhere. Your draft is kept. Reload saved
          settings to replace it explicitly.
        </p>
      )}
      {message && <p role="status">{message}</p>}
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
      <fieldset
        hidden={selectedSection !== 'desktop'}
        inert={selectedSection !== 'desktop' || !active}
      >
        <legend>Desktop access</legend>
        <p>
          Protect the desktop hostname with an owner-only Access policy and MFA.
        </p>
        <div className="cloudflare-form-grid">
          {accessField(
            'origin',
            'Desktop origin',
            'https://desktop.example.com',
          )}
          {accessField('listen', 'Desktop origin address', '127.0.0.1:3002')}
          {accessField(
            'issuer',
            'Access issuer',
            'https://team.cloudflareaccess.com',
          )}
          {accessField('audience', 'Desktop application AUD')}
          {accessField('ownerSubject', 'Owner subject (JWT sub)')}
        </div>
        <div className="cloudflare-actions">
          <Button disabled={disabled} onClick={() => void validate('access')}>
            Validate access
          </Button>
        </div>
      </fieldset>
      <fieldset
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
          {baseline.account.hasToken && (
            <div className="cloudflare-token-rotation">
              {!rotating ? (
                <Button disabled={disabled} onClick={() => setRotating(true)}>
                  Rotate token
                </Button>
              ) : (
                <>
                  <Field
                    label="Replacement API token"
                    type="password"
                    autoComplete="new-password"
                    value={replacementToken}
                    disabled={disabled}
                    onChange={(event) =>
                      setReplacementToken(event.target.value)
                    }
                  />
                  <p className="fine-print">
                    Replaces only the API token. Desktop access and publications
                    keep their settings.
                  </p>
                  <div className="cloudflare-actions">
                    <Button
                      disabled={disabled || !replacementToken.trim()}
                      onClick={() => void rotateToken()}
                    >
                      Validate and rotate token
                    </Button>
                    <Button
                      disabled={disabled}
                      onClick={() => {
                        setRotating(false);
                        setReplacementToken('');
                      }}
                    >
                      Cancel rotation
                    </Button>
                  </div>
                </>
              )}
            </div>
          )}
          <Button
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
        </div>
        {wizard !== undefined && (
          <div
            className="cloudflare-wizard"
            aria-label="Cloudflare account setup"
          >
            <p className="cloudflare-step" role="status" ref={wizardHeading}>
              Step {wizard + 1} of {steps.length}: {steps[wizard]}
            </p>
            {wizard === 0 && (
              <>
                <p>
                  Required: Tunnel Edit, Zone Read and DNS Edit. Analytics Read
                  is optional.
                </p>
                <label className="cloudflare-field">
                  API token action
                  <select
                    value={draft.tokenAction}
                    disabled={disabled}
                    onChange={(event) =>
                      edit({
                        ...draft,
                        tokenAction: event.target
                          .value as ConfigDraft['tokenAction'],
                        token: '',
                      })
                    }
                  >
                    <option value="keep">Keep saved token</option>
                    <option value="replace">Replace token</option>
                  </select>
                </label>
                {draft.tokenAction === 'replace' && (
                  <Field
                    label="Cloudflare API token"
                    type="password"
                    autoComplete="new-password"
                    value={draft.token || ''}
                    disabled={disabled}
                    onChange={(event) =>
                      edit({ ...draft, token: event.target.value })
                    }
                  />
                )}
                <p className="fine-print">
                  Stored on the AXV host. Saved tokens are never shown.
                </p>
                <details className="cloudflare-disclosure">
                  <summary>Token permissions</summary>
                  <p>
                    Scope the token to the selected account and zone. Validation
                    checks read access; publishing also requires Tunnel and DNS
                    edit permissions.
                  </p>
                </details>
                {!baseline.account.hasToken && draft.tokenAction === 'keep' && (
                  <p>
                    Choose Replace token to connect an account for the first
                    time.
                  </p>
                )}
              </>
            )}
            {wizard === 1 && selector('accountId', 'Account', choices.accounts)}
            {wizard === 2 && selector('zoneId', 'Zone', choices.zones)}
            {wizard === 3 && (
              <>
                {selector('tunnelId', 'Tunnel', choices.tunnels)}
                <p>
                  Use an existing, remotely managed tunnel. Run cloudflared
                  separately on the AXV host.
                </p>
              </>
            )}
            {wizard === 4 && (
              <>
                {accountField('domain', 'Base domain', 'example.net')}
                <p>
                  Use a registered domain separate from your desktop. Its first
                  publication permanently reserves it.
                </p>
                <details className="cloudflare-disclosure">
                  <summary>Domain isolation</summary>
                  <p>
                    A subdomain such as apps.example.com shares cookies with
                    example.com and is not an independent site. Use a separate
                    registered domain, or a Public Suffix List domain whose
                    direct children are independent sites. A public suffix can
                    support multiple publications; an ordinary registered domain
                    supports one and cannot be reused after removal.
                  </p>
                </details>
              </>
            )}
            {wizard === 5 && (
              <>
                <dl className="info-list">
                  <dt>Account</dt>
                  <dd>{draft.account.accountId}</dd>
                  <dt>Zone</dt>
                  <dd>{draft.account.zoneId}</dd>
                  <dt>Tunnel</dt>
                  <dd>{draft.account.tunnelId}</dd>
                  <dt>Base domain</dt>
                  <dd>{draft.account.domain}</dd>
                </dl>
                <div className="cloudflare-form-grid">
                  {accountField(
                    'listen',
                    'Publishing origin address',
                    '127.0.0.1:3003',
                  )}
                  {accountField(
                    'audience',
                    'Apps application AUD (owner access)',
                  )}
                </div>
                <p>
                  Owner access needs an existing Access application with your
                  owner-only policy and this AUD.
                </p>
                <details className="cloudflare-disclosure">
                  <summary>Owner access requirements</summary>
                  <p>
                    Cover your app hostnames using the desktop issuer and owner
                    identity. AXV does not create the Access policy. Public
                    publications skip Access authentication. Set the Access
                    application session duration to 15 minutes or less; longer
                    tokens are rejected.
                  </p>
                </details>
              </>
            )}
            {wizard === 5 && saveBlocked && (
              <p
                id="cloudflare-wizard-save-blocker"
                className="cloudflare-notice"
              >
                Disable desktop access in Overview to save these settings.
              </p>
            )}
            <div className="cloudflare-actions">
              <Button
                disabled={disabled || wizard === 0}
                onClick={() => {
                  setWizard(wizard - 1);
                  setResult(undefined);
                }}
              >
                Back
              </Button>
              {wizard < 5 && (
                <Button
                  primary
                  disabled={
                    disabled || (wizard === 0 && draft.tokenAction === 'clear')
                  }
                  onClick={() =>
                    void validate(
                      steps[wizard].toLowerCase() as CloudflareWizardStep,
                      true,
                    )
                  }
                >
                  {busy ? 'Validating…' : 'Validate and continue'}
                </Button>
              )}
              {wizard === 5 && (
                <Button
                  primary
                  disabled={disabled || !configDirty || enabled}
                  aria-describedby={
                    saveBlocked ? 'cloudflare-wizard-save-blocker' : undefined
                  }
                  onClick={() => void save()}
                >
                  Save settings
                </Button>
              )}
              <Button
                disabled={disabled}
                onClick={() => {
                  setWizard(undefined);
                  setResult(undefined);
                }}
              >
                Close wizard
              </Button>
            </div>
          </div>
        )}
      </fieldset>
      {result && (
        <div role="status">
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
            <span className="fine-print">
              {configDirty ? 'Unsaved settings' : 'Settings saved'}
            </span>
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
          <SetupGuide
            headingRef={heading}
            interactive={active}
            onClose={() => setGuide(false)}
          />
        )}
      </details>
    </div>
  );
}
