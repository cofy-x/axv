import { useState, useSyncExternalStore } from 'react';
import { Button, Select } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { desktopDatabase } from '../../shared/api/desktop';
import { documentsAPI } from '../../filesystem/documents';
import type { DocumentLink, DocumentTarget } from '../../filesystem/documents';
import { computeStore } from '../machines/compute-store';
import { projectLabel } from '../project-files/presentation';
import { openProjectFile } from '../project-files/navigation';
import type { loadDocumentActions } from './data';
import { conversationLabel } from './state';

export function DocumentDetails({
  entryId,
  data,
  savedAvailable,
  desktop,
  onRefresh,
}: {
  entryId: string;
  data?: Awaited<ReturnType<typeof loadDocumentActions>>;
  savedAvailable: boolean;
  desktop: DesktopAPI;
  onRefresh: () => void;
}) {
  const [target, setTarget] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [feedback, setFeedback] = useState('');
  const machines = useSyncExternalStore(
    computeStore.subscribe,
    computeStore.getSnapshot,
  );
  const targets: { key: string; label: string; value: DocumentTarget }[] = [
    ...(data?.projects || []).map((project) => ({
      key: `project:${project.id}`,
      label: `Project · ${projectLabel(project, data?.projects || [])} · ${project.rootPath}`,
      value: { kind: 'project' as const, projectId: project.id },
    })),
    ...(data?.sessions || []).map((session) => ({
      key: `conversation:${session.id}`,
      label: `Conversation · ${conversationLabel(session, data?.sessions || [])}`,
      value: { kind: 'conversation' as const, conversationId: session.id },
    })),
    ...machines.machines
      .filter((machine) => machine.hostId === machines.info?.hostId)
      .map((machine) => ({
        key: `machine:${machine.hostId}:${machine.id}`,
        label: `VM · ${machine.spec.name}`,
        value: {
          kind: 'machine' as const,
          hostId: machine.hostId,
          machineId: machine.id,
        },
      })),
  ];

  function linkLabel(link: DocumentLink) {
    return (
      targets.find(
        (item) =>
          item.value.kind === link.kind &&
          (link.kind === 'project'
            ? item.value.projectId === link.projectId
            : link.kind === 'conversation'
              ? item.value.conversationId === link.conversationId
              : item.value.hostId === link.hostId &&
                item.value.machineId === link.machineId),
      )?.label ||
      `${link.kind} · ${link.projectId || link.conversationId || link.machineId} (unavailable)`
    );
  }

  async function perform(work: () => Promise<void>) {
    if (busy) return;
    setBusy(true);
    setError('');
    setFeedback('');
    try {
      await work();
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <details className="document-detail-group">
        <summary>Related work ({data?.links.length ?? '…'})</summary>
        <p className="document-hint">
          Links organize work. They don't send content.
        </p>
        <section aria-label="Document associations">
          <ul className="document-link-list">
            {data?.links.map((link) => (
              <li key={link.id}>
                <span>{linkLabel(link)}</span>
                <Button
                  disabled={busy}
                  onClick={() =>
                    void perform(async () => {
                      await documentsAPI.unlink(link.id);
                      desktopDatabase.assertUnchanged();
                      onRefresh();
                      setFeedback('Link removed.');
                    })
                  }
                >
                  Unlink
                </Button>
              </li>
            ))}
          </ul>
          <Select
            label="Link to"
            value={target}
            disabled={busy}
            onChange={(event) => {
              setTarget(event.target.value);
              setError('');
              setFeedback('');
            }}
          >
            <option value="">Choose a project, conversation or VM</option>
            {targets.map((item) => (
              <option value={item.key} key={item.key}>
                {item.label}
              </option>
            ))}
          </Select>
          <div className="document-action-result">
            {error && <p role="alert">{error}</p>}
            {feedback && <p role="status">{feedback}</p>}
            <Button
              disabled={busy || !target || !savedAvailable}
              onClick={() =>
                void perform(async () => {
                  const value = targets.find(
                    (item) => item.key === target,
                  )?.value;
                  if (!value) throw new Error('Choose an available target.');
                  await documentsAPI.link(entryId, value);
                  desktopDatabase.assertUnchanged();
                  onRefresh();
                  setFeedback('Link added.');
                })
              }
            >
              Add Link
            </Button>
          </div>
        </section>
      </details>
      {!!data?.origins.length && (
        <details className="document-detail-group">
          <summary>Imported from ({data.origins.length})</summary>
          {data.origins.map((origin) => (
            <div key={`${origin.projectId}:${origin.path}:${origin.createdAt}`}>
              <p>
                {data.projects.find((item) => item.id === origin.projectId)
                  ?.name || 'Original project'}{' '}
                · <code>{origin.path}</code>
              </p>
              <small className="document-digest">SHA-256 {origin.sha256}</small>
              <Button
                onClick={() =>
                  openProjectFile(desktop, origin.projectId, origin.path)
                }
              >
                Open Source
              </Button>
            </div>
          ))}
        </details>
      )}
    </>
  );
}
