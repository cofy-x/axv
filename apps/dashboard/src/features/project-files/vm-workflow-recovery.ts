/** Serialize browser recovery writes across tabs before sending an operation. */
export async function replaceVMRecoveryRecord(
  key: string,
  expected: string | null,
  next: string | null,
) {
  if (!navigator.locks)
    throw new Error(
      'Browser recovery requires Web Locks. Use a supported browser before submitting this operation.',
    );
  await navigator.locks.request(key, () => {
    if (localStorage.getItem(key) !== expected)
      throw new Error(
        'The original recovery record changed. Reopen this window before submitting, recovering, or dismissing work.',
      );
    if (next === null) localStorage.removeItem(key);
    else localStorage.setItem(key, next);
  });
}
