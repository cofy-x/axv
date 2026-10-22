import type { Ref } from 'react';

export interface CodeEditorHandle {
  focus: () => void;
  blur: () => void;
  isComposing: () => boolean;
}

export interface CodeEditorChange {
  documentId: string;
  source: 'input' | 'composition';
}

export interface CodeEditorProps {
  ref?: Ref<CodeEditorHandle>;
  documentId: string;
  filename: string;
  value: string;
  label: string;
  disabled?: boolean;
  readOnly?: boolean;
  onChange?: (value: string, change: CodeEditorChange) => void;
  onSave?: () => void;
  onCompositionChange?: (active: boolean) => void;
}
