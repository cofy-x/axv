/** Download a text snapshot without saving or changing its recovery record. */
export function downloadDraft(name: string, text: string) {
  const basename = name.split(/[\\/]/).at(-1) || 'document';
  const url = URL.createObjectURL(
    new Blob([text], { type: 'text/plain;charset=utf-8' }),
  );
  const link = document.createElement('a');
  try {
    link.href = url;
    link.download = `${basename}.draft.txt`;
    link.hidden = true;
    document.body.append(link);
    link.click();
  } finally {
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
}
