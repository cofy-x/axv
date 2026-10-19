import { randomUUID } from 'node:crypto';
import type { ConsoleMessage, Page } from '@playwright/test';

export interface RuntimeError {
  kind: 'error' | 'unhandledrejection' | 'monitor';
  message: string;
  stack: string;
  url: string;
  documentId: string;
}

export interface PageErrorDiagnostic {
  name: string;
  message: string;
  stack?: string;
}

/** Runs in each browser document, including non-secure cross-origin frames. */
export function installRuntimeErrorObservers(marker: string) {
  // Keep the native transport even if application code later replaces console.
  // A single console string introduces no Promise that can reject on unload.
  const send = console.debug.bind(console);
  let documentId = '';
  const describe = (reason: unknown) => {
    try {
      return reason instanceof Error
        ? { message: reason.message, stack: reason.stack || '' }
        : { message: String(reason), stack: '' };
    } catch {
      return { message: 'Unprintable rejection reason', stack: '' };
    }
  };
  const report = (
    kind: 'error' | 'unhandledrejection' | 'monitor' | 'ready',
    detail: { message: string; stack: string },
  ) =>
    send(
      marker +
        JSON.stringify({ kind, ...detail, url: location.href, documentId }),
    );
  try {
    // This is diagnostic identity, not the host's unpredictable report nonce.
    // crypto.randomUUID is unavailable in ordinary non-secure HTTP frames.
    documentId = `${performance.timeOrigin}:${Math.random()}`;
    globalThis.addEventListener(
      'error',
      (event) => {
        // Resource loading errors are plain Events, separate from uncaught JS.
        if (!(event instanceof ErrorEvent)) return;
        report('error', {
          message: event.message,
          stack: describe(event.error).stack,
        });
      },
      true,
    );
    globalThis.addEventListener('unhandledrejection', (event) =>
      report('unhandledrejection', describe(event.reason)),
    );
    report('ready', { message: '', stack: '' });
  } catch (error) {
    report('monitor', describe(error));
  }
}

/** Observe native uncaught exceptions without interpreting browser console text. */
export async function monitorRuntimeErrors(page: Page) {
  const prefix = `axv-runtime-${randomUUID()}:`;
  const errors: RuntimeError[] = [];
  const pageErrors: PageErrorDiagnostic[] = [];
  const documents = new Map<string, string>();
  const onPageError = (error: Error) =>
    pageErrors.push({
      name: error.name,
      message: error.message,
      stack: error.stack,
    });
  const onConsole = (message: ConsoleMessage) => {
    const text = message.text();
    if (message.type() !== 'debug' || !text.startsWith(prefix)) return;
    try {
      const value: unknown = JSON.parse(text.slice(prefix.length));
      if (
        !value ||
        typeof value !== 'object' ||
        !('kind' in value) ||
        (value.kind !== 'error' &&
          value.kind !== 'unhandledrejection' &&
          value.kind !== 'monitor' &&
          value.kind !== 'ready') ||
        !('message' in value) ||
        typeof value.message !== 'string' ||
        !('stack' in value) ||
        typeof value.stack !== 'string' ||
        !('url' in value) ||
        typeof value.url !== 'string' ||
        !('documentId' in value) ||
        typeof value.documentId !== 'string'
      )
        throw new Error('Invalid native runtime error report.');
      if (value.kind === 'ready') documents.set(value.documentId, value.url);
      else errors.push(value as RuntimeError);
    } catch (error) {
      errors.push({
        kind: 'monitor',
        message: error instanceof Error ? error.message : String(error),
        stack: '',
        url: page.url(),
        documentId: '',
      });
    }
  };
  page.on('console', onConsole);
  page.on('pageerror', onPageError);
  await page.addInitScript(installRuntimeErrorObservers, prefix);
  return {
    errors,
    pageErrors,
    documents,
    marker: prefix,
    dispose() {
      page.off('console', onConsole);
      page.off('pageerror', onPageError);
    },
  };
}
