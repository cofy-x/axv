import { expect } from '@playwright/test';
import type { Locator } from '@playwright/test';

export function codeEditor(window: Locator, name = 'VM project file text') {
  return window.getByRole('textbox', {
    name,
    exact: true,
    includeHidden: true,
  });
}

export async function editVMFile(window: Locator) {
  await window.getByRole('button', { name: 'Edit', exact: true }).click();
  const editor = codeEditor(window);
  await expect(editor).toBeEditable();
  return editor;
}

/** Small fixtures must be fully rendered; retain empty final lines verbatim. */
export async function expectEditorText(editor: Locator, expected: string) {
  await expect
    .poll(() =>
      editor.evaluate((element) => {
        if (element.querySelector('.cm-gap'))
          throw new Error(
            'This fixture is virtualized; verify its complete text through copying or the saved payload.',
          );
        const lines = Array.from(element.children);
        if (
          !lines.length ||
          lines.some((line) => !line.classList.contains('cm-line'))
        )
          throw new Error(
            'Expected the code editor text surface and its rendered lines.',
          );
        return lines.map((line) => line.textContent).join('\n');
      }),
    )
    .toBe(expected);
}

/** Exercise the editor's normal copy handling without reading private state. */
export async function copyEditorText(editor: Locator) {
  await editor.focus();
  await editor.press('ControlOrMeta+a');
  return editor.evaluate((element) => {
    const clipboardData = new DataTransfer();
    const event = new ClipboardEvent('copy', {
      clipboardData,
      bubbles: true,
      cancelable: true,
    });
    element.dispatchEvent(event);
    if (!event.defaultPrevented)
      throw new Error(
        'The code editor did not handle copying the selected text.',
      );
    if (!event.clipboardData)
      throw new Error('The browser did not provide copy event clipboard data.');
    return event.clipboardData.getData('text/plain');
  });
}
