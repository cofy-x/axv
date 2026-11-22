import { Button, Field, TextArea } from '@cofy-x/axv-nextstep';
import type { ComparisonRuntime } from './api';

export function ComparisonRuntimes({
  runtimes,
  onChange,
  issue,
  errorId,
}: {
  runtimes: ComparisonRuntime[];
  onChange: (runtimes: ComparisonRuntime[]) => void;
  issue?: { runtimeId?: string; field?: string };
  errorId?: string;
}) {
  function update(id: string, patch: Partial<ComparisonRuntime>) {
    onChange(runtimes.map((r) => (r.id === id ? { ...r, ...patch } : r)));
  }

  function invalidField(runtimeId: string, field: string) {
    const invalid = issue?.runtimeId === runtimeId && issue.field === field;
    return {
      'aria-invalid': invalid || undefined,
      'aria-describedby': invalid ? errorId : undefined,
    };
  }

  return (
    <details
      className="github-review-comparison-runtimes"
      data-review-runtimes=""
    >
      <summary>Comparison runtimes</summary>
      <p className="github-review-detail">
        {runtimes.length === 0 &&
          'Candidate comparisons use the VM’s POSIX shell by default. '}
        Each launcher receives the assertion as its final argument.
      </p>
      {runtimes.map((runtime, index) => (
        <fieldset
          key={runtime.id}
          className="github-review-profile-editor"
          data-review-settings-item={runtime.id}
        >
          <legend>{runtime.name.trim() || `Runtime ${index + 1}`}</legend>
          <Field
            {...invalidField(runtime.id, 'name')}
            label="Runtime name"
            value={runtime.name}
            maxLength={80}
            onChange={(e) => update(runtime.id, { name: e.target.value })}
          />
          <TextArea
            {...invalidField(runtime.id, 'command')}
            label="Launcher (one argument per line)"
            rows={2}
            spellCheck={false}
            className="github-review-input-command"
            placeholder={'python3\n-c'}
            value={runtime.command.join('\n')}
            onChange={(e) =>
              update(runtime.id, { command: e.target.value.split('\n') })
            }
          />
          <div className="app-actions">
            <Button
              onClick={() =>
                onChange(runtimes.filter((r) => r.id !== runtime.id))
              }
            >
              Remove runtime
            </Button>
          </div>
        </fieldset>
      ))}
      <div className="app-actions">
        <Button
          disabled={runtimes.length >= 8}
          onClick={() =>
            onChange([
              ...runtimes,
              { id: crypto.randomUUID(), name: '', command: [] },
            ])
          }
        >
          Add runtime
        </Button>
      </div>
    </details>
  );
}
