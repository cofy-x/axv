import { Button, Field } from '@cofy-x/axv-nextstep';
import type { ConfigDraft } from './config-draft';

interface CloudflareDesktopSetupCardProps {
  visible: boolean;
  active: boolean;
  disabled: boolean;
  draft: ConfigDraft;
  onEdit: (next: ConfigDraft) => void;
  onValidate: () => void;
}

export function CloudflareDesktopSetupCard({
  visible,
  active,
  disabled,
  draft,
  onEdit,
  onValidate,
}: CloudflareDesktopSetupCardProps) {
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
        onEdit({
          ...draft,
          access: { ...draft.access, [key]: event.target.value },
        })
      }
    />
  );

  return (
    <fieldset
      className="cloudflare-card"
      hidden={!visible}
      inert={!visible || !active}
    >
      <legend>Desktop access</legend>
      <p>
        Protect the desktop hostname with an owner-only Access policy and MFA.
      </p>
      <div className="cloudflare-form-grid">
        {accessField('origin', 'Desktop origin', 'https://desktop.example.com')}
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
        <Button disabled={disabled} onClick={onValidate}>
          Validate access
        </Button>
      </div>
    </fieldset>
  );
}
