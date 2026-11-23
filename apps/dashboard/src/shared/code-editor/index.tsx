import { lazy, Suspense } from 'react';
import { PlainCodeEditor } from './PlainCodeEditor';
import type { CodeEditorProps } from './types';
import './code-editor.css';

let editorModule: Promise<{ default: typeof PlainCodeEditor }> | undefined;
const loadEditor = () =>
  (editorModule ??= import('./CodeEditor').catch(() => ({
    default: PlainCodeEditor,
  })));
const Editor = lazy(loadEditor);

export function preloadCodeEditor() {
  void loadEditor();
}

export function CodeEditor(props: CodeEditorProps) {
  return (
    <Suspense
      fallback={
        <div className="code-editor code-editor-loading" role="status">
          Loading editor…
        </div>
      }
    >
      <Editor {...props} />
    </Suspense>
  );
}

export type { CodeEditorChange, CodeEditorHandle } from './types';
