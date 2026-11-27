import type { MouseEvent } from 'react';
import { Button, IconButton } from '@cofy-x/axv-nextstep';

export interface VMProjectFileHeaderProps {
  displayPath: string;
  hasEditor: boolean;
  dirty: boolean;
  editing: boolean;
  saving: boolean;
  reading: boolean;
  composing: boolean;
  unavailable: boolean;
  interactive: boolean;
  onBack: (event: MouseEvent<HTMLButtonElement>) => void;
  onReload: () => void;
  onSave: () => void;
  onFinishEditing: () => void;
  onStartEditing: () => void;
}

export function VMProjectFileHeader({
  displayPath,
  hasEditor,
  dirty,
  editing,
  saving,
  reading,
  composing,
  unavailable,
  interactive,
  onBack,
  onReload,
  onSave,
  onFinishEditing,
  onStartEditing,
}: VMProjectFileHeaderProps) {
  return (
    <div className="project-preview-heading vm-project-file-toolbar">
      <div className="vm-project-file-name">
        <code title={displayPath}>{displayPath}</code>
        {dirty && <strong>Unsaved</strong>}
      </div>
      <div
        className="vm-project-file-controls"
        role="group"
        aria-label="File actions"
      >
        <IconButton
          className="project-back"
          icon="back"
          label="Back"
          title="Back to files"
          disabled={!interactive || composing}
          onClick={onBack}
        />
        {hasEditor && (
          <>
            <IconButton
              icon="refresh"
              label="Reload file"
              disabled={
                saving || reading || composing || unavailable || !interactive
              }
              onClick={onReload}
            />
            {editing ? (
              <>
                <Button
                  primary
                  disabled={
                    !dirty || saving || composing || unavailable || !interactive
                  }
                  onClick={onSave}
                >
                  {saving ? 'Saving…' : 'Save'}
                </Button>
                <Button
                  disabled={saving || composing || !interactive}
                  onClick={onFinishEditing}
                >
                  {dirty ? 'Cancel' : 'Done'}
                </Button>
              </>
            ) : (
              <Button
                disabled={
                  reading || saving || composing || unavailable || !interactive
                }
                onClick={onStartEditing}
              >
                Edit
              </Button>
            )}
          </>
        )}
      </div>
    </div>
  );
}
