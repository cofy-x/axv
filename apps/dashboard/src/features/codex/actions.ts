import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { codexAPI, sessionTitle } from './api';
import type { CodexSessionRecord } from './api';
import { APIError } from '../../shared/api/http';
import { publishSessionMetadata } from './metadata';

export async function confirmEndSession(
  desktop: Pick<DesktopAPI, 'dialog'>,
  session: CodexSessionRecord,
) {
  const answer = await desktop.dialog({
    title: 'End Codex session?',
    message: session.vmProject
      ? `Stop this host Codex run for VM project ${session.vmProject.project.name}? Accepted VM tasks keep running and can be stopped separately in Tasks. Closing a window only detaches; saved history can be continued separately.`
      : `Stop this Codex run and its running work in ${session.projectPath}? Closing a window only detaches. Saved conversation history can be continued separately.`,
    buttons: [
      { label: 'Cancel', value: 'cancel', default: true, cancel: true },
      { label: 'End Session', value: 'end' },
    ],
  });
  return answer?.button === 'end'
    ? codexAPI.end(session.id, session.runId)
    : null;
}

function metadataConflict(error: unknown) {
  return (
    error instanceof APIError &&
    error.status === 409 &&
    error.code === 'metadata_conflict'
  );
}

export async function renameSession(
  desktop: Pick<DesktopAPI, 'dialog'>,
  session: CodexSessionRecord,
) {
  let current = session;
  let draft = session.title;
  let feedback = '';
  while (true) {
    const answer = await desktop.dialog({
      title: 'Rename Codex Session',
      message: `${feedback ? `${feedback}\n\n` : ''}Up to 120 characters. Leave empty to use the project name.`,
      input: { label: 'Session name', value: draft },
      buttons: [
        { label: 'Cancel', value: 'cancel', cancel: true },
        { label: 'Rename', value: 'rename', default: true },
      ],
    });
    if (answer?.button !== 'rename') return null;
    draft = answer.value;
    try {
      const updated = await codexAPI.update(current.id, {
        revision: current.metadataRevision,
        title: draft,
      });
      publishSessionMetadata(updated);
      return updated;
    } catch (error) {
      feedback =
        error instanceof Error
          ? error.message
          : 'Could not rename the session.';
      if (metadataConflict(error)) {
        try {
          current = await codexAPI.get(current.id);
          publishSessionMetadata(current);
          feedback = `This session changed elsewhere. Its current name is “${sessionTitle(current)}”. Review your name and choose Rename again to apply it.`;
        } catch (refreshError) {
          feedback = `The session changed elsewhere, and its latest details could not be loaded. ${refreshError instanceof Error ? refreshError.message : 'Try again.'}`;
        }
      }
    }
  }
}

export async function setSessionArchived(session: CodexSessionRecord) {
  try {
    const updated = await codexAPI.update(session.id, {
      revision: session.metadataRevision,
      archived: !session.archivedAt,
    });
    publishSessionMetadata(updated);
    return updated;
  } catch (error) {
    if (!metadataConflict(error)) throw error;
    const current = await codexAPI.get(session.id);
    publishSessionMetadata(current);
    throw new Error(
      'This session changed elsewhere. Review its latest details, then choose Archive or Unarchive again.',
      { cause: error },
    );
  }
}
