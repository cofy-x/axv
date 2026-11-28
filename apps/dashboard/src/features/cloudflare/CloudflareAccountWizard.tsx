import type { RefObject } from 'react';
import { Button, Field, Select } from '@cofy-x/axv-nextstep';
import type { ConfigDraft } from './config-draft';
import type {
  CloudflareConfig,
  CloudflareWizardResult,
  CloudflareWizardStep,
} from './types';

export const wizardSteps = [
  'Token',
  'Account',
  'Zone',
  'Tunnel',
  'Domain',
  'Review',
] as const;

interface CloudflareAccountWizardProps {
  wizard: number;
  wizardHeading: RefObject<HTMLParagraphElement | null>;
  baseline: CloudflareConfig;
  draft: ConfigDraft;
  choices: CloudflareWizardResult;
  disabled: boolean;
  busy: boolean;
  configDirty: boolean;
  enabled: boolean;
  saveBlocked: boolean;
  onEdit: (next: ConfigDraft) => void;
  onBack: () => void;
  onValidate: (step: CloudflareWizardStep) => void;
  onSave: () => void;
  onClose: () => void;
}

export function CloudflareAccountWizard({
  wizard,
  wizardHeading,
  baseline,
  draft,
  choices,
  disabled,
  busy,
  configDirty,
  enabled,
  saveBlocked,
  onEdit,
  onBack,
  onValidate,
  onSave,
  onClose,
}: CloudflareAccountWizardProps) {
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
        onEdit({
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
        <div className="cloudflare-field">
          <Select
            label={`Choose ${label.toLowerCase()}`}
            value={draft.account[key]}
            disabled={disabled}
            onChange={(event) =>
              onEdit({
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
          </Select>
        </div>
      )}
      {accountField(key, `${label} ID`)}
    </>
  );

  return (
    <div className="cloudflare-wizard" aria-label="Cloudflare account setup">
      <ol className="cloudflare-wizard-steps" aria-hidden="true">
        {wizardSteps.map((label, index) => (
          <li
            key={label}
            className={`cloudflare-wizard-step${
              index < wizard ? ' is-done' : index === wizard ? ' is-active' : ''
            }`}
          >
            <span>{index < wizard ? '✓' : index + 1}</span>
            <span>{label}</span>
          </li>
        ))}
      </ol>
      <p className="cloudflare-step" role="status" ref={wizardHeading}>
        Step {wizard + 1} of {wizardSteps.length}: {wizardSteps[wizard]}
      </p>
      {wizard === 0 && (
        <>
          <p>
            Required: Tunnel Edit, Zone Read and DNS Edit. Analytics Read is
            optional.
          </p>
          <div className="cloudflare-field">
            <Select
              label="API token action"
              value={draft.tokenAction}
              disabled={disabled}
              onChange={(event) =>
                onEdit({
                  ...draft,
                  tokenAction: event.target.value as ConfigDraft['tokenAction'],
                  token: '',
                })
              }
            >
              <option value="keep">Keep saved token</option>
              <option value="replace">Replace token</option>
            </Select>
          </div>
          {draft.tokenAction === 'replace' && (
            <Field
              label="Cloudflare API token"
              type="password"
              autoComplete="new-password"
              value={draft.token || ''}
              disabled={disabled}
              onChange={(event) =>
                onEdit({ ...draft, token: event.target.value })
              }
            />
          )}
          <p className="fine-print">
            Stored on the AXV host. Saved tokens are never shown.
          </p>
          <details className="cloudflare-disclosure is-nested">
            <summary>Token permissions</summary>
            <div className="cloudflare-disclosure-body">
              <p>
                Scope the token to the selected account and zone. Validation
                checks read access; publishing also requires Tunnel and DNS edit
                permissions.
              </p>
            </div>
          </details>
          {!baseline.account.hasToken && draft.tokenAction === 'keep' && (
            <p>
              Choose Replace token to connect an account for the first time.
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
            Use an existing, remotely managed tunnel. Run cloudflared separately
            on the AXV host.
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
          <details className="cloudflare-disclosure is-nested">
            <summary>Domain isolation</summary>
            <div className="cloudflare-disclosure-body">
              <p>
                A subdomain such as apps.example.com shares cookies with
                example.com and is not an independent site. Use a separate
                registered domain, or a Public Suffix List domain whose direct
                children are independent sites. A public suffix can support
                multiple publications; an ordinary registered domain supports
                one and cannot be reused after removal.
              </p>
            </div>
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
            {accountField('audience', 'Apps application AUD (owner access)')}
          </div>
          <p>
            Owner access needs an existing Access application with your
            owner-only policy and this AUD.
          </p>
          <details className="cloudflare-disclosure is-nested">
            <summary>Owner access requirements</summary>
            <div className="cloudflare-disclosure-body">
              <p>
                Cover your app hostnames using the desktop issuer and owner
                identity. AXV does not create the Access policy. Public
                publications skip Access authentication. Set the Access
                application session duration to 15 minutes or less; longer
                tokens are rejected.
              </p>
            </div>
          </details>
        </>
      )}
      {wizard === 5 && saveBlocked && (
        <p id="cloudflare-wizard-save-blocker" className="cloudflare-notice">
          Disable desktop access in Overview to save these settings.
        </p>
      )}
      <div className="cloudflare-actions">
        <Button disabled={disabled || wizard === 0} onClick={onBack}>
          Back
        </Button>
        {wizard < 5 && (
          <Button
            primary
            disabled={
              disabled || (wizard === 0 && draft.tokenAction === 'clear')
            }
            onClick={() =>
              onValidate(
                wizardSteps[wizard].toLowerCase() as CloudflareWizardStep,
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
            onClick={onSave}
          >
            Save settings
          </Button>
        )}
        <Button disabled={disabled} onClick={onClose}>
          Close wizard
        </Button>
      </div>
    </div>
  );
}
