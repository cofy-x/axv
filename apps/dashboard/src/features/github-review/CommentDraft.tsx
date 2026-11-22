import { useEffect, useRef, useState } from 'react';
import { Button, TextArea } from '@cofy-x/axv-nextstep';
import { MarkdownPreview } from '../../shared/markdown';
import type { CandidateCommentDraft, Finding } from './api';

export interface CommentDraftEdit {
  body: string;
  savedBody: string;
  revision: number;
}

const maxDraftBytes = 8 * 1024;

function DraftAnchor({
  path,
  line,
  side,
  anchorSha,
}: Pick<CandidateCommentDraft, 'path' | 'line' | 'side' | 'anchorSha'>) {
  return (
    <span className="github-review-draft-anchor">
      <code>
        {path}:{line}
      </code>
      <span>
        {side === 'base' ? 'Common ancestor' : 'PR head'}{' '}
        <code title={anchorSha}>{anchorSha.slice(0, 12)}</code>
      </span>
    </span>
  );
}

function DraftBody({ body, source }: { body: string; source: boolean }) {
  return source ? (
    <pre
      className="github-review-draft-source"
      aria-label="Markdown source"
      tabIndex={0}
    >
      {body}
    </pre>
  ) : (
    <MarkdownPreview
      content={body}
      emptyMessage="Enter a comment to preview it."
    />
  );
}

export function HistoricalCommentDraft({
  draft,
}: {
  draft: CandidateCommentDraft;
}) {
  return (
    <details className="github-review-draft">
      <summary>Local comment draft · Earlier revision</summary>
      <DraftAnchor {...draft} />
      <MarkdownPreview content={draft.body} />
    </details>
  );
}

export function CommentDraft({
  finding,
  anchorSha,
  saved,
  edit,
  disabled,
  stale,
  focusInitially,
  onEdit,
  onSave,
  onReviewed,
}: {
  finding: Finding;
  anchorSha: string;
  saved?: CandidateCommentDraft;
  edit?: CommentDraftEdit;
  disabled: boolean;
  stale: boolean;
  focusInitially: boolean;
  onEdit: (edit?: CommentDraftEdit) => void;
  onSave: (edit: CommentDraftEdit) => void;
  onReviewed: (revision: number, reviewed: boolean) => void;
}) {
  const [source, setSource] = useState(false);
  const [preview, setPreview] = useState(false);
  const [copyStatus, setCopyStatus] = useState('');
  const disclosure = useRef<HTMLDetailsElement>(null);
  const summary = useRef<HTMLElement>(null);
  const editor = useRef<HTMLDivElement>(null);
  const wasEditing = useRef(false);
  const editing = !!edit;
  const historical = stale || !!saved?.stale;
  const canEdit = !disabled && !historical;
  const conflict = !!edit && edit.revision !== (saved?.revision ?? 0);
  const length = edit ? new TextEncoder().encode(edit.body).byteLength : 0;
  const tooLong = length > maxDraftBytes;

  useEffect(() => {
    if (editing) editor.current?.querySelector('textarea')?.focus();
    else if (wasEditing.current) summary.current?.focus();
    wasEditing.current = editing;
  }, [editing]);
  useEffect(() => {
    if (!focusInitially || !disclosure.current) return;
    disclosure.current.open = true;
    summary.current?.focus({ preventScroll: true });
    summary.current?.scrollIntoView({ block: 'start' });
  }, [focusInitially]);

  function begin() {
    const body = saved?.body ?? '';
    setCopyStatus('');
    setPreview(false);
    onEdit({ body, savedBody: body, revision: saved?.revision ?? 0 });
  }

  async function copy(body: string) {
    try {
      await navigator.clipboard.writeText(body);
      setCopyStatus('Markdown copied.');
    } catch {
      setSource(true);
      setCopyStatus('Could not copy. Select the Markdown source to copy it.');
    }
  }

  return (
    <details
      ref={disclosure}
      className="github-review-draft"
      open={editing || undefined}
    >
      <summary ref={summary}>
        <strong>Local comment draft</strong>
        <span className="github-review-draft-state">
          {historical
            ? 'Earlier revision'
            : editing
              ? 'Editing'
              : saved
                ? saved.reviewedAt
                  ? 'Reviewed'
                  : 'Ready to review'
                : 'No draft'}
        </span>
      </summary>
      <div className="github-review-draft-content">
        <DraftAnchor
          {...(saved ?? {
            path: finding.path,
            line: finding.line,
            side: finding.side ?? 'head',
            anchorSha,
          })}
        />
        {historical && (
          <p className="github-review-detail">
            This draft belongs to an earlier PR revision.
          </p>
        )}
        {edit ? (
          <div ref={editor}>
            {conflict && (
              <div className="github-review-warning" role="alert">
                <p>The saved draft changed. Your edits are still here.</p>
                {saved && (
                  <details>
                    <summary>View saved version</summary>
                    <MarkdownPreview content={saved.body} />
                  </details>
                )}
                <Button
                  disabled={!canEdit}
                  onClick={() =>
                    onEdit({
                      ...edit,
                      revision: saved?.revision ?? 0,
                      savedBody: saved?.body ?? '',
                    })
                  }
                >
                  Keep my edits
                </Button>
              </div>
            )}
            <TextArea
              label="Comment Markdown"
              rows={7}
              value={edit.body}
              readOnly={disabled || historical}
              onChange={(event) =>
                onEdit({ ...edit, body: event.target.value })
              }
            />
            {tooLong && (
              <p className="github-review-problem" role="alert">
                The draft exceeds 8 KiB.
              </p>
            )}
            <div className="app-actions">
              <Button
                primary
                disabled={
                  !canEdit ||
                  conflict ||
                  tooLong ||
                  !edit.body.trim() ||
                  edit.body === edit.savedBody
                }
                onClick={() => onSave(edit)}
              >
                Save local draft
              </Button>
              <Button
                aria-pressed={preview}
                onClick={() => setPreview(!preview)}
              >
                Preview Markdown
              </Button>
              <Button
                disabled={disabled && !historical}
                onClick={() => onEdit()}
              >
                Cancel draft edit
              </Button>
            </div>
            {preview && <DraftBody body={edit.body} source={false} />}
          </div>
        ) : (
          <>
            <div className="app-actions">
              {saved && canEdit && (
                <Button
                  primary={!saved.reviewedAt}
                  onClick={() => onReviewed(saved.revision, !saved.reviewedAt)}
                >
                  {saved.reviewedAt ? 'Mark as unread' : 'Mark reviewed'}
                </Button>
              )}
              {canEdit && (
                <Button onClick={begin}>
                  {saved ? 'Edit draft' : 'Create draft'}
                </Button>
              )}
            </div>
            {saved && (
              <details className="github-review-markdown-tools">
                <summary>Markdown tools</summary>
                <div className="app-actions">
                  <Button onClick={() => void copy(saved.body)}>
                    Copy Markdown
                  </Button>
                  <Button
                    aria-pressed={source}
                    onClick={() => setSource(!source)}
                  >
                    Markdown source
                  </Button>
                </div>
              </details>
            )}
            {copyStatus && (
              <p className="github-review-detail" role="status">
                {copyStatus}
              </p>
            )}
            {saved && <DraftBody body={saved.body} source={source} />}
          </>
        )}
      </div>
    </details>
  );
}
