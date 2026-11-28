import { Button, Field } from '@cofy-x/axv-nextstep';
import { checklistPackage } from './sample';
import { downloadJSON } from './package-utils';
import type { PackageReview } from './package-utils';
import type { ExtensionPackage, ExtensionRecord } from './types';

interface AddPackagePanelProps {
  panelId: string;
  open: boolean;
  disabled: boolean;
  busy: boolean;
  review?: PackageReview;
  reviewedApp?: ExtensionRecord;
  reviewStale: boolean;
  onClose: () => void;
  onSelectFile: (file: File) => void;
  onStagePackage: (pkg: ExtensionPackage) => void;
  onCancelReview: () => void;
  onInstall: () => void;
}

export function AddPackagePanel({
  panelId,
  open,
  disabled,
  busy,
  review,
  reviewedApp,
  reviewStale,
  onClose,
  onSelectFile,
  onStagePackage,
  onCancelReview,
  onInstall,
}: AddPackagePanelProps) {
  return (
    <section
      className="applications-add-panel"
      id={panelId}
      hidden={!open}
      aria-labelledby={`${panelId}-heading`}
    >
      <div className="applications-panel-heading">
        <h2 id={`${panelId}-heading`}>Add an extension</h2>
        <Button onClick={onClose}>Close</Button>
      </div>
      <p className="applications-meta">Install packages from the AXV host.</p>
      <Field
        label="Extension package"
        className="applications-package-input"
        type="file"
        accept=".json,application/json"
        disabled={disabled}
        onChange={(event) => {
          const file = event.currentTarget.files?.[0];
          event.currentTarget.value = '';
          if (file) onSelectFile(file);
        }}
      />
      <div className="applications-actions applications-example-actions">
        <Button
          disabled={disabled}
          onClick={() => onStagePackage(checklistPackage)}
        >
          Install Checklist
        </Button>
        <Button
          onClick={() =>
            downloadJSON('local.checklist-1.0.0.json', checklistPackage)
          }
        >
          Download example
        </Button>
      </div>
      {review && (
        <div className="applications-review">
          <h3>Review {review.package.manifest.title}</h3>
          <dl className="info-list">
            <dt>Application</dt>
            <dd>{review.package.manifest.id}</dd>
            <dt>Version</dt>
            <dd>
              {reviewedApp?.installed && `${reviewedApp.version} → `}
              {review.package.manifest.version}
            </dd>
            <dt>Access</dt>
            <dd>Private application storage</dd>
          </dl>
          <p>
            {reviewedApp?.installed
              ? 'This replaces the installed package and keeps its saved data.'
              : 'The application will be enabled after installation.'}
          </p>
          {reviewStale && (
            <p role="alert">
              The application changed since this review. Review again to use its
              current version.
            </p>
          )}
          <div className="applications-actions">
            <Button disabled={busy} onClick={onCancelReview}>
              Cancel
            </Button>
            {reviewStale ? (
              <Button
                disabled={disabled}
                onClick={() => onStagePackage(review.package)}
              >
                Review again
              </Button>
            ) : (
              <Button primary disabled={disabled} onClick={onInstall}>
                {reviewedApp?.installed ? 'Replace package' : 'Install'}
              </Button>
            )}
          </div>
        </div>
      )}
    </section>
  );
}
