import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Button,
  Field,
  IconButton,
  PixelIcon,
  StatusBar,
} from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import {
  codexAPI,
  projectName,
  sessionTitle,
  sessionIsLive,
  sessionCanEnd,
  sessionCanContinue,
} from './api';
import { APIError } from '../../shared/api/http';
import type { CodexProject, CodexSessionRecord, HostDirectories } from './api';
import { useCodexAvailability } from './CodexContext';
import { openCodexEnvironment, openCodexSession } from './definition';
import {
  confirmEndSession,
  renameSession,
  setSessionArchived,
} from './actions';
import { showSessionDetails } from './details';
import {
  mergeSessionMetadata,
  publishSessionMetadata,
  subscribeSessionMetadata,
} from './metadata';
import { openTerminalWindow } from '../terminal/definition';
import { CodexResumeRequests, continueConversation } from './continuation';

export function CodexManager({ desktop, window }: AppWindowProps) {
  const { info, loading, error, status } = useCodexAvailability();
  const canStart = info?.ready === true && !loading && !error;
  const environmentIssue =
    error ||
    (info && !info.ready
      ? info.reason || 'Set up the host environment before starting a session.'
      : '');
  const setTitle = desktop.setTitle;
  const [sessions, setSessions] = useState<CodexSessionRecord[]>([]);
  const [projects, setProjects] = useState<CodexProject[]>([]);
  const [projectError, setProjectError] = useState('');
  const [showArchived, setShowArchived] = useState(false);
  const [changingMetadata, setChangingMetadata] = useState<string | null>(null);
  const [continuing, setContinuing] = useState<string | null>(null);
  const [sessionError, setSessionError] = useState('');
  const [directories, setDirectories] = useState<HostDirectories | null>(null);
  const [path, setPath] = useState('');
  const [choosingFolder, setChoosingFolder] = useState(false);
  const [directoryError, setDirectoryError] = useState('');
  const [browsing, setBrowsing] = useState(false);
  const [creating, setCreating] = useState<'default' | 'folder' | null>(null);
  const [ending, setEnding] = useState<string | null>(null);
  const [actionError, setActionError] = useState('');
  const mounted = useRef(false);
  const listing = useRef<AbortController | null>(null);
  const listingProjects = useRef<AbortController | null>(null);
  const browsingRequest = useRef<AbortController | null>(null);
  const creatingRequest = useRef(false);
  const requestKey = useRef<{ path?: string; id: string } | null>(null);
  const resumeRequests = useRef(new CodexResumeRequests());
  const continuingRequest = useRef(false);

  useEffect(() => setTitle(window.id, 'Codex'), [setTitle, window.id]);

  const refresh = useCallback(async () => {
    if (listing.current) return;
    const controller = new AbortController();
    listing.current = controller;
    try {
      const result = await codexAPI.list(controller.signal);
      if (!controller.signal.aborted && mounted.current) {
        setSessions((current) =>
          result.sessions.map((session) => {
            const previous = current.find((item) => item.id === session.id);
            return previous ? mergeSessionMetadata(session, previous) : session;
          }),
        );
        for (const session of result.sessions) publishSessionMetadata(session);
        setSessionError('');
      }
    } catch (error) {
      if (!controller.signal.aborted && mounted.current)
        setSessionError(
          error instanceof Error
            ? error.message
            : 'Could not load Codex sessions.',
        );
    } finally {
      if (listing.current === controller) listing.current = null;
    }
  }, []);

  const refreshProjects = useCallback(async () => {
    listingProjects.current?.abort();
    const controller = new AbortController();
    listingProjects.current = controller;
    try {
      const result = await codexAPI.projects(controller.signal);
      if (!controller.signal.aborted && mounted.current) {
        setProjects(result.projects);
        setProjectError('');
      }
    } catch (error) {
      if (!controller.signal.aborted && mounted.current)
        setProjectError(
          error instanceof Error
            ? error.message
            : 'Could not load recent projects.',
        );
    } finally {
      if (listingProjects.current === controller)
        listingProjects.current = null;
    }
  }, []);

  const browse = useCallback(async (next?: string) => {
    browsingRequest.current?.abort();
    const controller = new AbortController();
    browsingRequest.current = controller;
    setBrowsing(true);
    setDirectoryError('');
    try {
      const result = await codexAPI.directories(next, controller.signal);
      if (!controller.signal.aborted && mounted.current) {
        setDirectories(result);
        setPath(result.path);
      }
    } catch (error) {
      if (!controller.signal.aborted && mounted.current)
        setDirectoryError(
          error instanceof Error
            ? error.message
            : 'Could not read this directory.',
        );
    } finally {
      if (!controller.signal.aborted && mounted.current) setBrowsing(false);
    }
  }, []);

  useEffect(() => {
    mounted.current = true;
    const unsubscribe = subscribeSessionMetadata((metadata) => {
      setSessions((current) =>
        current.map((session) => mergeSessionMetadata(session, metadata)),
      );
    });
    void refresh();
    void refreshProjects();
    const timer = setInterval(() => {
      if (document.visibilityState === 'visible') {
        void refresh();
        if (!listingProjects.current) void refreshProjects();
      }
    }, 4000);
    const focus = () => {
      void refresh();
      void refreshProjects();
    };
    globalThis.addEventListener('focus', focus);
    return () => {
      mounted.current = false;
      unsubscribe();
      clearInterval(timer);
      globalThis.removeEventListener('focus', focus);
      listing.current?.abort();
      listing.current = null;
      listingProjects.current?.abort();
      listingProjects.current = null;
      browsingRequest.current?.abort();
    };
  }, [refresh, refreshProjects]);

  const closeFolderPicker = () => {
    browsingRequest.current?.abort();
    setBrowsing(false);
    setChoosingFolder(false);
    setDirectoryError('');
  };

  const create = async (project?: string) => {
    if (!canStart || creatingRequest.current) return;
    creatingRequest.current = true;
    setCreating(project ? 'folder' : 'default');
    setActionError('');
    if (!requestKey.current || requestKey.current.path !== project)
      requestKey.current = { path: project, id: crypto.randomUUID() };
    try {
      const session = await codexAPI.create(requestKey.current.id, project);
      requestKey.current = null;
      if (mounted.current) {
        if (sessionIsLive(session)) {
          closeFolderPicker();
          setShowArchived(false);
          openCodexSession(desktop, session);
        } else
          setActionError(
            session.reason ||
              `Codex could not start (${session.status}). Choose New Session to try again.`,
          );
        void refresh();
        void refreshProjects();
      }
    } catch (error) {
      if (error instanceof APIError && error.status < 500)
        requestKey.current = null;
      if (mounted.current)
        setActionError(
          error instanceof Error ? error.message : 'Could not start Codex.',
        );
    } finally {
      creatingRequest.current = false;
      if (mounted.current) setCreating(null);
    }
  };

  const end = async (session: CodexSessionRecord) => {
    if (ending) return;
    setEnding(session.id);
    setActionError('');
    try {
      const result = await confirmEndSession(desktop, session);
      if (result && mounted.current) {
        setSessions((current) =>
          current.map((item) =>
            item.id === result.id && item.runId === result.runId
              ? mergeSessionMetadata(result, item)
              : mergeSessionMetadata(item, result),
          ),
        );
        void refresh();
      }
    } catch (error) {
      if (mounted.current)
        setActionError(
          error instanceof Error
            ? error.message
            : 'Could not end this session.',
        );
    } finally {
      if (mounted.current) setEnding(null);
    }
  };

  const changeMetadata = async (
    session: CodexSessionRecord,
    action: 'rename' | 'archive',
  ) => {
    if (changingMetadata) return;
    setChangingMetadata(session.id);
    setActionError('');
    try {
      if (action === 'rename') await renameSession(desktop, session);
      else await setSessionArchived(session);
    } catch (error) {
      if (mounted.current)
        setActionError(
          error instanceof Error
            ? error.message
            : 'Could not update the session.',
        );
    } finally {
      if (mounted.current) setChangingMetadata(null);
    }
  };
  const continueSession = async (session: CodexSessionRecord) => {
    if (continuingRequest.current) return;
    continuingRequest.current = true;
    setContinuing(session.id);
    setActionError('');
    try {
      const result = await continueConversation(
        desktop,
        session,
        resumeRequests.current,
      );
      if (result && mounted.current) openCodexSession(desktop, result);
    } catch (error) {
      if (mounted.current) {
        const reason =
          error instanceof Error
            ? error.message
            : 'Could not continue the conversation.';
        setActionError(
          resumeRequests.current.hasPending(session.id)
            ? `The continuation result is unknown. Choose Continue again to check the same request, or open the running session if it appears. ${reason}`
            : reason,
        );
      }
    } finally {
      continuingRequest.current = false;
      if (mounted.current) {
        setContinuing(null);
        void refresh();
        void refreshProjects();
      }
    }
  };
  const visibleSessions = sessions.filter(
    (session) => !!session.archivedAt === showArchived,
  );

  return (
    <div className="codex-manager" data-testid="codex-manager">
      <div className="app-toolbar codex-manager-toolbar">
        <PixelIcon name="companion" size={16} />
        <Button
          primary
          disabled={!canStart || creating !== null}
          onClick={() => void create()}
        >
          {creating === 'default' ? 'Starting…' : 'New Session'}
        </Button>
        <Button
          disabled={creating !== null || (!choosingFolder && !canStart)}
          aria-expanded={choosingFolder}
          aria-controls="codex-folder-picker"
          onClick={() => {
            if (choosingFolder) closeFolderPicker();
            else {
              setChoosingFolder(true);
              void browse(path || undefined);
            }
          }}
        >
          New in Folder
        </Button>
        <div className="codex-manager-tools">
          <IconButton
            icon="refresh"
            label="Refresh"
            onClick={() => {
              void refresh();
              void refreshProjects();
            }}
          />
          <IconButton
            icon="more"
            label="More Codex manager actions"
            aria-haspopup="menu"
            onClick={(event) => {
              const rect = event.currentTarget.getBoundingClientRect();
              desktop.contextMenu(
                [
                  {
                    label: 'Environment',
                    action: () => openCodexEnvironment(desktop),
                  },
                ],
                { x: rect.left, y: rect.bottom },
              );
            }}
          />
        </div>
      </div>
      <div className="codex-manager-content">
        <p className="codex-default-project">
          <span>New sessions start in</span>{' '}
          <code data-testid="codex-default-project">
            {info?.defaultProjectPath || '~/.axv/workspace'}
          </code>
        </p>
        {environmentIssue && (
          <div className="app-error codex-environment-notice" role="alert">
            <p>{environmentIssue}</p>
            <Button onClick={() => openCodexEnvironment(desktop)}>
              Environment
            </Button>
          </div>
        )}
        {(projects.length > 0 || projectError) && (
          <section
            className="codex-section codex-recent"
            aria-label="Recent projects"
          >
            <h2>Recent Projects</h2>
            {projectError && (
              <p className="app-error" role="alert">
                {projectError}
              </p>
            )}
            <ul className="codex-recent-list">
              {projects.map((project) => (
                <li key={project.path}>
                  <span>
                    <strong>{projectName(project.path)}</strong>
                    <code title={project.path}>{project.path}</code>
                  </span>
                  <Button
                    disabled={!canStart || creating !== null}
                    aria-label={`New session in ${project.path}`}
                    onClick={() => void create(project.path)}
                  >
                    New Session
                  </Button>
                </li>
              ))}
            </ul>
          </section>
        )}
        {choosingFolder && (
          <section
            id="codex-folder-picker"
            className="codex-section codex-folder-picker"
            aria-label="New Codex session"
          >
            <h2>New session in a host folder</h2>
            <form
              className="codex-path-form"
              onSubmit={(event) => {
                event.preventDefault();
                void browse(path);
              }}
            >
              <Field
                label="Project directory"
                value={path}
                disabled={browsing || creating !== null}
                onChange={(event) => setPath(event.target.value)}
              />
              <Button type="submit" disabled={browsing || creating !== null}>
                Go
              </Button>
              <Button
                disabled={browsing || creating !== null}
                onClick={() => void browse()}
              >
                Home
              </Button>
              <Button
                disabled={
                  browsing || creating !== null || !directories?.parentPath
                }
                onClick={() =>
                  void browse(directories?.parentPath || undefined)
                }
              >
                Up
              </Button>
            </form>
            {directoryError && (
              <p className="app-error" role="alert">
                {directoryError}
              </p>
            )}
            <div
              className="codex-directory-list"
              aria-label="Host folders"
              aria-busy={browsing}
            >
              {browsing ? (
                <p role="status">Reading host directories…</p>
              ) : (
                directories?.directories.map((directory) => (
                  <button
                    type="button"
                    key={directory.path}
                    disabled={creating !== null}
                    aria-label={`Open folder ${directory.name}`}
                    onClick={() => void browse(directory.path)}
                  >
                    <PixelIcon name="folder" size={16} />
                    <span>{directory.name}</span>
                  </button>
                ))
              )}
              {!browsing && directories?.directories.length === 0 && (
                <p>No subfolders. You can use this directory.</p>
              )}
            </div>
            {directories?.truncated && (
              <p className="codex-note">
                Only part of this folder is listed. Enter a full directory path
                to navigate directly.
              </p>
            )}
            <div className="codex-project-choice">
              <code title={directories?.path}>
                {directories?.path || 'Choose an accessible directory.'}
              </code>
              <Button disabled={creating !== null} onClick={closeFolderPicker}>
                Cancel
              </Button>
              <Button
                primary
                disabled={
                  !canStart ||
                  !directories ||
                  browsing ||
                  !!directoryError ||
                  creating !== null ||
                  path !== directories.path
                }
                onClick={() => {
                  if (directories) void create(directories.path);
                }}
              >
                {creating === 'folder' ? 'Starting…' : 'Start Here'}
              </Button>
            </div>
          </section>
        )}
        {actionError && (
          <p className="app-error" role="alert">
            {actionError}
          </p>
        )}
        <section className="codex-section" aria-label="Codex sessions">
          <div className="codex-session-heading">
            <h2>Sessions</h2>
            <div className="codex-session-filter" aria-label="Session list">
              <Button
                aria-pressed={!showArchived}
                onClick={() => setShowArchived(false)}
              >
                Unarchived
              </Button>
              <Button
                aria-pressed={showArchived}
                onClick={() => setShowArchived(true)}
              >
                Archived
              </Button>
            </div>
          </div>
          <p className="codex-note">
            Archiving does not stop runs or free capacity.
          </p>
          {sessionError && (
            <p className="app-error" role="alert">
              {sessionError}
            </p>
          )}
          {!visibleSessions.length && !sessionError && (
            <p className="codex-empty">
              {showArchived
                ? 'No archived sessions.'
                : 'No unarchived sessions.'}
            </p>
          )}
          <ul className="codex-session-list">
            {visibleSessions.map((session) => (
              <li
                key={session.id}
                data-session-id={session.id}
                data-run-id={session.runId}
              >
                <div className="codex-session-summary">
                  <strong>{sessionTitle(session)}</strong>
                  <code title={session.projectPath}>{session.projectPath}</code>
                  <code data-testid="codex-tmux-session">
                    {session.tmuxSession}
                  </code>
                  <span>
                    {ending === session.id ? 'Ending…' : session.status} ·{' '}
                    {new Date(session.createdAt).toLocaleString()}
                  </span>
                  {session.reason && <span>{session.reason}</span>}
                  {!sessionCanEnd(session) &&
                    session.continuation.state === 'unavailable' && (
                      <span>
                        {session.continuation.reason ||
                          'Conversation history is unavailable.'}
                      </span>
                    )}
                </div>
                <div className="codex-session-actions">
                  <Button
                    disabled={
                      continuing === session.id ||
                      (sessionCanContinue(session) && !canStart)
                    }
                    onClick={() => {
                      if (sessionCanContinue(session))
                        void continueSession(session);
                      else openCodexSession(desktop, session);
                    }}
                  >
                    {continuing === session.id
                      ? 'Continuing…'
                      : sessionIsLive(session)
                        ? 'Open'
                        : sessionCanContinue(session)
                          ? 'Continue'
                          : 'View'}
                  </Button>
                  <Button
                    onClick={() => void showSessionDetails(desktop, session)}
                  >
                    Details
                  </Button>
                  <IconButton
                    icon="more"
                    label={`Session actions for ${sessionTitle(session)}`}
                    aria-haspopup="menu"
                    disabled={
                      changingMetadata === session.id || ending === session.id
                    }
                    onClick={(event) => {
                      const rect = event.currentTarget.getBoundingClientRect();
                      desktop.contextMenu(
                        [
                          {
                            label: 'Continue',
                            disabled:
                              !sessionCanContinue(session) ||
                              !canStart ||
                              continuing !== null,
                            action: () => void continueSession(session),
                          },
                          { separator: true },
                          {
                            label: 'Rename',
                            disabled: changingMetadata !== null,
                            action: () =>
                              void changeMetadata(session, 'rename'),
                          },
                          {
                            label: session.archivedAt ? 'Unarchive' : 'Archive',
                            disabled: changingMetadata !== null,
                            action: () =>
                              void changeMetadata(session, 'archive'),
                          },
                          {
                            label: 'Host Terminal',
                            action: () => {
                              openTerminalWindow(desktop, session.projectPath);
                            },
                          },
                          { separator: true },
                          {
                            label: 'End Session',
                            disabled:
                              !sessionCanEnd(session) || ending !== null,
                            action: () => void end(session),
                          },
                        ],
                        { x: rect.left, y: rect.bottom },
                      );
                    }}
                  />
                </div>
              </li>
            ))}
          </ul>
        </section>
      </div>
      <StatusBar>
        <span role="status" data-testid="codex-environment-status">
          Host · {status}
        </span>
        <span>{sessions.filter(sessionCanEnd).length} active / 4 maximum</span>
      </StatusBar>
    </div>
  );
}
