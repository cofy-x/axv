import type { DesktopAPI } from '@cofy-x/axv-nextstep';

export async function reportError(desktop: DesktopAPI, error: unknown) {
  await desktop.dialog({
    title: 'Unable to complete the operation',
    message: error instanceof Error ? error.message : String(error),
    buttons: [{ label: 'OK', value: 'ok', default: true }],
  });
}

export async function runCommand(
  desktop: DesktopAPI,
  work: () => Promise<void>,
) {
  try {
    await work();
  } catch (error) {
    await reportError(desktop, error);
  }
}

export async function nameDialog(
  desktop: DesktopAPI,
  title: string,
  value: string,
) {
  const answer = await desktop.dialog({
    title,
    input: { label: 'Name', value },
    buttons: [
      { label: 'Cancel', value: 'cancel', cancel: true },
      { label: 'OK', value: 'ok', default: true },
    ],
  });
  return answer?.button === 'ok' ? answer.value.trim() : null;
}
