import { useImperativeHandle, useRef } from 'react';
import { TextArea } from '@cofy-x/axv-nextstep';
import type { CodeEditorProps } from './types';

export function PlainCodeEditor({
  ref,
  label,
  value,
  disabled,
  readOnly,
  onSave,
}: CodeEditorProps) {
  const input = useRef<HTMLTextAreaElement>(null);
  useImperativeHandle(
    ref,
    () => ({
      focus: () => input.current?.focus(),
      blur: () => input.current?.blur(),
      isComposing: () => false,
    }),
    [],
  );
  return (
    <div className="code-editor code-editor-plain">
      <p role="alert">
        Code editor unavailable. Text is read-only; reload AXV to retry.
      </p>
      <TextArea
        ref={input}
        aria-label={label}
        value={value}
        disabled={disabled}
        readOnly
        spellCheck={false}
        onKeyDown={(event) => {
          if (
            onSave &&
            (event.metaKey || event.ctrlKey) &&
            event.key.toLowerCase() === 's'
          ) {
            event.preventDefault();
            if (!disabled && !readOnly) onSave();
          }
        }}
      />
    </div>
  );
}
