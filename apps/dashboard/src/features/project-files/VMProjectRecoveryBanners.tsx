import type { ReactNode } from 'react';
import { Button } from '@cofy-x/axv-nextstep';

export interface VMProjectRecoveryBannersProps {
  interactive: boolean;
  saving: boolean;
  reading: boolean;
  composing: boolean;
  hasEditor: boolean;
  fileErrors: (string | undefined)[];
  errorOperation?: 'read' | 'save' | 'create';
  errorNeedsReconnect: boolean;
  hasConnectionError: boolean;
  hasListingError: boolean;
  existingReconnect: boolean;
  connecting: boolean;
  actionFeedback?: string;
  blockedRecovery: boolean;
  pendingRecoveryPath?: string;
  visibleNotice: {
    kind: 'changed' | 'error' | 'binding';
    message: string;
  } | null;
  downloadAction: ReactNode;
  onRetryRead: () => void;
  onReloadFile: () => void;
  onRefreshListing: () => void;
  onReconnect: () => void;
  onDiscardBlockedRecovery: () => void;
  onRestoreDraft: () => void;
  onDiscardDraft: () => void;
  onRetryCheck: () => void;
}

export function VMProjectRecoveryBanners({
  interactive,
  saving,
  reading,
  composing,
  hasEditor,
  fileErrors,
  errorOperation,
  errorNeedsReconnect,
  hasConnectionError,
  hasListingError,
  existingReconnect,
  connecting,
  actionFeedback,
  blockedRecovery,
  pendingRecoveryPath,
  visibleNotice,
  downloadAction,
  onRetryRead,
  onReloadFile,
  onRefreshListing,
  onReconnect,
  onDiscardBlockedRecovery,
  onRestoreDraft,
  onDiscardDraft,
  onRetryCheck,
}: VMProjectRecoveryBannersProps) {
  return (
    <>
      {fileErrors.length > 0 && (
        <div className="project-error" role="alert">
          <div>
            {fileErrors.map((reason) => (
              <p key={reason}>{reason}</p>
            ))}
          </div>
          <div className="app-actions">
            {downloadAction}
            {errorOperation === 'read' && !errorNeedsReconnect && (
              <Button disabled={reading || !interactive} onClick={onRetryRead}>
                Retry
              </Button>
            )}
            {errorOperation === 'save' && !errorNeedsReconnect && hasEditor && (
              <Button
                disabled={saving || composing || !interactive}
                onClick={onReloadFile}
              >
                Reload
              </Button>
            )}
            {hasListingError && (
              <Button disabled={!interactive} onClick={onRefreshListing}>
                Refresh
              </Button>
            )}
            {(errorNeedsReconnect || hasConnectionError || hasListingError) && (
              <Button
                disabled={saving || composing || !interactive}
                onClick={onReconnect}
              >
                Reconnect
              </Button>
            )}
          </div>
        </div>
      )}
      {actionFeedback && (
        <p className="project-feedback" role="status">
          {actionFeedback}
        </p>
      )}
      {blockedRecovery && (
        <section
          className="project-recovery app-pad"
          aria-label="Unavailable VM project recovery"
        >
          <p>
            The stored recovery record could not be read. Editing is paused to
            preserve it. Copy or inspect that browser record before choosing to
            discard it.
          </p>
          <Button disabled={!interactive} onClick={onDiscardBlockedRecovery}>
            Discard Recovery
          </Button>
        </section>
      )}
      {pendingRecoveryPath && (
        <section
          className="project-recovery app-pad"
          aria-label="VM project draft recovery"
        >
          <strong>Unsaved draft: {pendingRecoveryPath}</strong>
          <p>
            This tab kept your text and its original VM binding. Restore keeps
            the original file version check.
          </p>
          <Button disabled={!interactive} onClick={onRestoreDraft}>
            Restore Draft
          </Button>
          <Button disabled={!interactive} onClick={onDiscardDraft}>
            Discard Draft
          </Button>
        </section>
      )}
      {connecting && (
        <p className="project-feedback" role="status">
          Connecting to VM project…
        </p>
      )}
      {visibleNotice && (
        <section
          className="project-file-observation"
          role="status"
          aria-label="VM file updates"
        >
          <p>{visibleNotice.message}</p>
          <div className="app-actions">
            {visibleNotice.kind !== 'changed' &&
              fileErrors.length === 0 &&
              downloadAction}
            {visibleNotice.kind === 'binding' && !existingReconnect ? (
              <Button
                disabled={saving || composing || !interactive}
                onClick={onReconnect}
              >
                Reconnect
              </Button>
            ) : visibleNotice.kind === 'error' ? (
              <Button disabled={!interactive} onClick={onRetryCheck}>
                Retry check
              </Button>
            ) : null}
          </div>
        </section>
      )}
    </>
  );
}
