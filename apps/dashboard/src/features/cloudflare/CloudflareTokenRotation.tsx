import { Button, Field } from '@cofy-x/axv-nextstep';

interface CloudflareTokenRotationProps {
  disabled: boolean;
  rotating: boolean;
  replacementToken: string;
  onStart: () => void;
  onChangeToken: (value: string) => void;
  onRotate: () => void;
  onCancel: () => void;
}

export function CloudflareTokenRotation({
  disabled,
  rotating,
  replacementToken,
  onStart,
  onChangeToken,
  onRotate,
  onCancel,
}: CloudflareTokenRotationProps) {
  return (
    <div className="cloudflare-token-rotation">
      {!rotating ? (
        <Button disabled={disabled} onClick={onStart}>
          Rotate token
        </Button>
      ) : (
        <div className="cloudflare-subcard">
          <Field
            label="Replacement API token"
            type="password"
            autoComplete="new-password"
            value={replacementToken}
            disabled={disabled}
            onChange={(event) => onChangeToken(event.target.value)}
          />
          <p className="fine-print">
            Replaces only the API token. Desktop access and publications keep
            their settings.
          </p>
          <div className="cloudflare-actions">
            <Button
              disabled={disabled || !replacementToken.trim()}
              onClick={onRotate}
            >
              Validate and rotate token
            </Button>
            <Button disabled={disabled} onClick={onCancel}>
              Cancel rotation
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
