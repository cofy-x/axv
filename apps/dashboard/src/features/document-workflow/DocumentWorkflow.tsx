import { useCallback, useRef, useState } from 'react';
import { Button, Field, IconButton, Select, Tabs } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { useResource } from '../../shared/api/useResource';
import { RequestIdentity } from '../../shared/api/request-identity';
import { projectsAPI } from '../project-files/api';
import { projectLabel } from '../project-files/presentation';
import { openProjectFile } from '../project-files/navigation';
import { projectName, sessionTitle } from '../codex/api';
import { contextAPI, contextStatusLabel } from '../codex/context-api';
import { useDraftNamespace } from '../../shared/drafts/context';
import { loadDocumentActions } from './data';
import { contextRunKey, readyContextRun, selectedContextRun } from './state';
import { DocumentDetails } from './DocumentDetails';
import { SavedVersionPicker } from './SavedVersionPicker';
import { useSavedVersion, versionInfo } from './useSavedVersion';

export function DocumentWorkflow({ window, desktop }: AppWindowProps) {
  const entryId = window.params.entryId;
  const requestedTask = window.viewState.task || window.params.task;
  const task =
    requestedTask === 'attach' || requestedTask === 'export'
      ? requestedTask
      : 'details';
  const [revision, refresh] = useState(0);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [feedback, setFeedback] = useState('');
  const [resultTask, setResultTask] = useState(task);
  const [projectId, setProjectId] = useState('');
  const [exportPath, setExportPath] = useState('');
  const [runSelection, setRunSelection] = useState('');
  const version = useSavedVersion(entryId);
  const { databaseId } = useDraftNamespace();
  const exportRequest = useRef(
    new RequestIdentity(`${databaseId}:document-export:${entryId}`),
  );
  const attachRequest = useRef(
    new RequestIdentity(`${databaseId}:document-context:${entryId}`),
  );
  const load = useCallback(() => loadDocumentActions(entryId), [entryId]);
  const { data, error: loadError } = useResource(
    `${entryId}:${revision}`,
    load,
  );

  async function perform(label: string, work: () => Promise<void>) {
    if (busy) return;
    setResultTask(task);
    setBusy(label);
    setError('');
    setFeedback('');
    try {
      await work();
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy('');
    }
  }

  const selectedRun = selectedContextRun(data?.sessions || [], runSelection);
  const selectedProject = data?.projects.find(
    (project) => project.id === projectId,
  );
  const savedAvailable =
    !!version.data?.entry?.editable && !version.data.entry.trashedAt;
  return (
    <div className="document-workflow app-pad">
      <div className="document-workflow-heading">
        <strong>
          {version.data?.entry?.name ||
            (version.choice && versionInfo(version.choice).name) ||
            window.title}
        </strong>
        <IconButton
          icon="refresh"
          label="Refresh"
          disabled={!!busy}
          onClick={() => {
            refresh((value) => value + 1);
            version.reload();
          }}
        />
      </div>
      <div className="document-task-tabs">
        <Tabs
          label="Document actions"
          value={task}
          tabs={[
            { id: 'attach', label: 'Attach', disabled: !!busy },
            { id: 'export', label: 'Export', disabled: !!busy },
            { id: 'details', label: 'Details', disabled: !!busy },
          ]}
          onChange={(task) => {
            desktop.updateWindowState(window.id, { task });
            setError('');
            setFeedback('');
          }}
        >
          {loadError && <p role="alert">{loadError}</p>}
          {data?.warnings.map((warning) => (
            <p role="alert" key={warning}>
              {warning}
            </p>
          ))}
          <SavedVersionPicker version={version} disabled={!!busy} />
          <div
            className="document-task-pane"
            hidden={task !== 'attach'}
            inert={task !== 'attach'}
          >
            <Select
              label="Conversation"
              value={selectedRun ? runSelection : ''}
              disabled={!!busy}
              onChange={(event) => {
                setRunSelection(event.target.value);
                setError('');
                setFeedback('');
              }}
            >
              <option value="">Choose a conversation</option>
              {data?.sessions.filter(readyContextRun).map((session) => (
                <option
                  value={contextRunKey(session)}
                  key={contextRunKey(session)}
                >
                  {sessionTitle(session)} · run {session.runId.slice(0, 8)} ·{' '}
                  {projectName(session.projectPath)}
                </option>
              ))}
            </Select>
            {selectedRun && (
              <p className="document-target-summary">
                {selectedRun.projectPath} · run {selectedRun.runId.slice(0, 8)}
              </p>
            )}
            {runSelection && !selectedRun ? (
              <p role="status">Run unavailable. Choose a conversation.</p>
            ) : (
              data &&
              !data.sessions.some(readyContextRun) && (
                <p>Start or continue Codex, then refresh.</p>
              )
            )}
            {selectedRun && (
              <p className="document-hint">
                For your next message in this run.
              </p>
            )}
          </div>
          <div
            className="document-task-pane"
            hidden={task !== 'export'}
            inert={task !== 'export'}
          >
            <Select
              label="Destination project"
              value={selectedProject ? projectId : ''}
              disabled={!!busy}
              onChange={(event) => {
                setProjectId(event.target.value);
                setError('');
                setFeedback('');
              }}
            >
              <option value="">Choose a host project</option>
              {data?.projects.map((project) => (
                <option value={project.id} key={project.id}>
                  {projectLabel(project, data.projects)} · {project.rootPath}
                </option>
              ))}
            </Select>
            {selectedProject && (
              <p className="document-target-summary">
                Host · {selectedProject.rootPath}
              </p>
            )}
            {data?.projects.length === 0 && (
              <Button onClick={() => desktop.openApp('project-files')}>
                Open Project
              </Button>
            )}
            <Field
              label="New file path"
              placeholder="docs/task-brief.md"
              value={exportPath}
              disabled={!!busy}
              onChange={(event) => {
                setExportPath(event.target.value);
                setError('');
                setFeedback('');
              }}
            />
            <p className="document-hint">
              Creates a new copy; never overwrites.
            </p>
          </div>
          <div
            className="document-action-result"
            role="group"
            aria-label="Saved version actions"
          >
            {resultTask === task && error && <p role="alert">{error}</p>}
            {resultTask === task && feedback && <p role="status">{feedback}</p>}
            {task === 'attach' && (
              <Button
                primary
                disabled={
                  !!busy || version.loading || !version.choice || !selectedRun
                }
                onClick={() =>
                  void perform('Attaching…', async () => {
                    const session = selectedRun;
                    if (!session) throw new Error('Choose a conversation.');
                    const snapshot = await version.resolve();
                    const snapshotIds = [snapshot.id];
                    const requestId = attachRequest.current.for({
                      sessionId: session.id,
                      runId: session.runId,
                      snapshotIds,
                    });
                    const { batch } = await contextAPI.attach(
                      session.id,
                      session.runId,
                      requestId,
                      snapshotIds,
                    );
                    if (
                      batch.status !== 'unknown' &&
                      !attachRequest.current.complete(requestId)
                    )
                      setError(
                        'The attachment was queued, but its retry identity could not be cleared in this browser.',
                      );
                    setFeedback(contextStatusLabel[batch.status]);
                    desktop.openApp('conversation-context', {
                      sessionId: session.id,
                      runId: session.runId,
                    });
                  })
                }
              >
                {busy || 'Attach'}
              </Button>
            )}
            {task === 'export' && (
              <Button
                primary
                disabled={
                  !!busy ||
                  version.loading ||
                  !version.choice ||
                  !selectedProject ||
                  !exportPath.trim()
                }
                onClick={() =>
                  void perform('Exporting…', async () => {
                    const snapshot = await version.resolve();
                    const payload = {
                      snapshotId: snapshot.id,
                      path: exportPath.trim(),
                    };
                    const requestId = exportRequest.current.for({
                      projectId,
                      ...payload,
                    });
                    const result = await projectsAPI.export(projectId, {
                      ...payload,
                      requestId,
                    });
                    if (!exportRequest.current.complete(requestId))
                      setError(
                        'The export succeeded, but its retry identity could not be cleared in this browser.',
                      );
                    setFeedback(`Exported to ${result.path}.`);
                    openProjectFile(
                      desktop,
                      projectId,
                      result.path,
                      selectedProject
                        ? projectLabel(selectedProject, data?.projects || [])
                        : undefined,
                    );
                  })
                }
              >
                {busy || 'Export'}
              </Button>
            )}
            {task === 'details' && (
              <Button
                disabled={
                  !!busy ||
                  version.loading ||
                  !savedAvailable ||
                  !version.choice
                }
                onClick={() =>
                  void perform('Keeping…', async () => {
                    const snapshot = await version.resolve();
                    version.reload();
                    setFeedback(`Kept revision ${snapshot.revision}.`);
                  })
                }
              >
                {busy || 'Keep version'}
              </Button>
            )}
          </div>
          <div
            className="document-task-pane"
            hidden={task !== 'details'}
            inert={task !== 'details'}
          >
            <DocumentDetails
              entryId={entryId}
              data={data}
              savedAvailable={savedAvailable}
              desktop={desktop}
              onRefresh={() => refresh((value) => value + 1)}
            />
          </div>
        </Tabs>
      </div>
    </div>
  );
}
