import type { DialogOptions } from '@cofy-x/axv-nextstep';
import type { CloudflarePublication } from './types';

export function publicationActionReview(
  record: CloudflarePublication,
  action: 'unpublish' | 'retry',
): { dialog: DialogOptions; acceptedButton: string } | undefined {
  // Removal intent is permanent for this receipt, so cleanup cannot reopen it.
  if (action === 'retry' && record.cleanupPending) return undefined;

  // A retained phase can predate another client's retry and a server restart.
  // Every retry that can reopen access therefore requires the same review.
  const resume = action === 'retry';
  const acceptedButton = resume ? 'resume' : 'unpublish';
  return {
    acceptedButton,
    dialog: {
      title: resume ? 'Resume publication?' : 'Unpublish hostname?',
      message: resume ? (
        <>
          <p>{record.hostname}</p>
          <p>
            Project: {record.binding.projectId}
            <br />
            Host: {record.binding.hostId}
            <br />
            VM: {record.binding.machineId}
            <br />
            Port: {record.port}
            <br />
            Boot: {record.binding.bootId}
            <br />
            Access:{' '}
            {record.visibility === 'public'
              ? 'Public — anyone on the internet'
              : 'Owner only — Cloudflare Access required'}
          </p>
          <p>
            {record.visibility === 'public'
              ? 'Anyone on the internet can reach this service after it resumes. Keep its own authentication enabled.'
              : 'Your Access application must still cover this hostname.'}
          </p>
          <p>
            Resume only this original publication and its captured target. The
            hostname and generation stay unchanged.
          </p>
        </>
      ) : (
        `${record.hostname}\n\nThis immediately revokes its AXV HTTP and WebSocket access, then removes Cloudflare resources owned by this publication. The VM and its service keep running.`
      ),
      buttons: [
        { label: 'Cancel', value: 'cancel', cancel: true, default: true },
        { label: resume ? 'Resume' : 'Unpublish', value: acceptedButton },
      ],
    },
  };
}
