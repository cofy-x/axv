import { Button, Checkbox, Field } from '@cofy-x/axv-nextstep';
import type { Machine, Volume } from './api';

export type VolumeEditor = {
  kind: 'create' | 'copy' | 'rename' | 'resize' | 'retain';
  provider: string;
  hostId: string;
  volume?: Volume;
  machine?: Machine;
  name: string;
  size: string;
  retain: boolean;
};

export interface VolumeEditorFormProps {
  editor: VolumeEditor;
  locked: boolean;
  minimumSize: number;
  maxDiskGiB?: number;
  editorReason: string;
  editorChanged: boolean;
  latestEditorVolume?: Volume;
  latestEditorMachine?: Machine;
  hostId: string;
  revealRef: (element: HTMLElement | null) => void;
  onChange: (next: VolumeEditor) => void;
  onSubmit: () => void;
  onCancel: () => void;
}

export function VolumeEditorForm({
  editor,
  locked,
  minimumSize,
  maxDiskGiB,
  editorReason,
  editorChanged,
  latestEditorVolume,
  latestEditorMachine,
  hostId,
  revealRef,
  onChange,
  onSubmit,
  onCancel,
}: VolumeEditorFormProps) {
  return (
    <form
      ref={revealRef}
      className="app-pad machines-form machines-volume-editor"
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit();
      }}
    >
      <fieldset disabled={locked}>
        <legend>
          {editor.kind === 'create'
            ? 'New project volume'
            : `${editor.kind === 'copy' ? 'Copy' : editor.kind === 'rename' ? 'Rename' : editor.kind === 'resize' ? 'Grow' : 'Retention for'} “${editor.volume?.name || editor.volume?.id}”`}
        </legend>
        {['create', 'copy', 'rename'].includes(editor.kind) && (
          <Field
            label="Volume name"
            value={editor.name}
            required
            maxLength={80}
            onChange={(event) =>
              onChange({ ...editor, name: event.target.value })
            }
          />
        )}
        {['create', 'copy', 'resize'].includes(editor.kind) && (
          <>
            <Field
              label={editor.kind === 'resize' ? 'New size (GiB)' : 'Size (GiB)'}
              type="number"
              min={minimumSize}
              max={maxDiskGiB}
              value={editor.size}
              required
              onChange={(event) =>
                onChange({ ...editor, size: event.target.value })
              }
            />
            <p className="machines-form-help">
              {editor.kind === 'resize'
                ? 'Disk space shares the host budget. Disks cannot shrink; an attached VM must be stopped before growth. Its filesystem grows on the next boot.'
                : 'Disk space shares the host budget. Volumes cannot shrink.'}
            </p>
          </>
        )}
        {['create', 'copy', 'retain'].includes(editor.kind) && (
          <Checkbox
            label="Keep after VM deletion"
            checked={editor.retain}
            onChange={(event) =>
              onChange({ ...editor, retain: event.target.checked })
            }
          />
        )}
        {editorReason && <p className="machines-warning">{editorReason}</p>}
        {editorChanged && latestEditorVolume && editor.hostId === hostId && (
          <Button
            onClick={() =>
              onChange({
                ...editor,
                volume: structuredClone(latestEditorVolume),
                machine: latestEditorMachine
                  ? structuredClone(latestEditorMachine)
                  : undefined,
              })
            }
          >
            Use Latest
          </Button>
        )}
        <div className="machines-form-actions">
          <Button type="submit" disabled={!!editorReason}>
            {editor.kind === 'copy'
              ? 'Create Copy'
              : editor.kind === 'create'
                ? 'Create'
                : editor.kind === 'rename'
                  ? 'Rename'
                  : editor.kind === 'resize'
                    ? 'Grow'
                    : 'Apply'}
          </Button>
          <Button onClick={onCancel}>Cancel</Button>
        </div>
      </fieldset>
    </form>
  );
}
