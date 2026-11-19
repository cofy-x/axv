import { useState } from 'react';
import { Button, Field, Select, TextArea } from '@cofy-x/axv-nextstep';
import type { TestProfile } from './api';

export interface TestProfileDraft {
  id: string;
  name: string;
  entryPath: string;
  appliesTo: string;
  command: string;
}

export function testProfileDraft(profile: TestProfile): TestProfileDraft {
  return {
    ...profile,
    appliesTo: (profile.appliesTo ?? []).join('\n'),
    command: profile.command.join('\n'),
  };
}

export function testProfileConfig(profile: TestProfileDraft): TestProfile {
  const appliesTo = profile.appliesTo
    .split('\n')
    .map((path) => path.trim())
    .filter(Boolean);
  return {
    id: profile.id,
    name: profile.name.trim(),
    entryPath: profile.entryPath.trim(),
    ...(appliesTo.length ? { appliesTo } : {}),
    command: profile.command.split('\n').filter(Boolean),
  };
}

export function QuickTestProfiles({
  profiles,
  singleCommand,
  onProfiles,
  onCommand,
}: {
  profiles: TestProfileDraft[];
  singleCommand: string;
  onProfiles: (profiles: TestProfileDraft[]) => void;
  onCommand: (command: string) => void;
}) {
  const [selectedId, setSelectedId] = useState('');
  const selected =
    profiles.find((profile) => profile.id === selectedId) ?? profiles[0];
  const position = selected ? profiles.indexOf(selected) : -1;

  function update(change: Partial<TestProfileDraft>) {
    onProfiles(
      profiles.map((item) =>
        item === selected ? { ...item, ...change } : item,
      ),
    );
  }

  function move(offset: number) {
    const reordered = [...profiles];
    reordered.splice(position, 1);
    reordered.splice(position + offset, 0, selected);
    setSelectedId(selected.id);
    onProfiles(reordered);
  }

  function add() {
    const id = crypto.randomUUID();
    onProfiles([
      ...profiles,
      {
        id,
        name: '',
        entryPath: '',
        appliesTo: '',
        command: profiles.length ? '' : singleCommand,
      },
    ]);
    setSelectedId(id);
  }

  return (
    <section
      className="github-review-profile-settings"
      aria-label="Quick tests"
    >
      <strong>Quick tests</strong>
      {selected ? (
        <>
          <p className="github-review-detail">
            AXV runs the first matching test whose entry file exists in the PR
            head.
          </p>
          <div className="github-review-test-profile-picker">
            <Select
              label="Test order"
              value={selected.id}
              onChange={(e) => setSelectedId(e.target.value)}
            >
              {profiles.map((item, index) => (
                <option key={item.id} value={item.id}>
                  {index + 1}. {item.name.trim() || 'Untitled test'}
                </option>
              ))}
            </Select>
            <div className="app-actions">
              <Button disabled={position === 0} onClick={() => move(-1)}>
                Move earlier
              </Button>
              <Button
                disabled={position === profiles.length - 1}
                onClick={() => move(1)}
              >
                Move later
              </Button>
            </div>
          </div>
          <div className="github-review-profile-editor" key={selected.id}>
            <Field
              label="Test name"
              value={selected.name}
              required
              maxLength={80}
              onChange={(e) => update({ name: e.target.value })}
            />
            <Field
              label="Required entry file"
              value={selected.entryPath}
              required
              spellCheck={false}
              placeholder="deploy/scripts/test_configure.py"
              onChange={(e) => update({ entryPath: e.target.value })}
            />
            <TextArea
              label="Changed files or directories (one per line)"
              value={selected.appliesTo}
              rows={2}
              spellCheck={false}
              placeholder="Any changed path"
              onChange={(e) => update({ appliesTo: e.target.value })}
            />
            <TextArea
              label="Test command (one argument per line)"
              value={selected.command}
              rows={3}
              required
              spellCheck={false}
              className="github-review-input-command"
              placeholder={'python3\ndeploy/scripts/test_configure.py'}
              onChange={(e) => update({ command: e.target.value })}
            />
          </div>
        </>
      ) : (
        <>
          <TextArea
            label="VM test command (one argument per line)"
            value={singleCommand}
            rows={3}
            spellCheck={false}
            className="github-review-input-command"
            placeholder={'go\ntest\n./...'}
            onChange={(e) => onCommand(e.target.value)}
          />
          <p className="github-review-detail">
            Use a test profile to select by changed path and check its entry
            file before running.
          </p>
        </>
      )}
      <div className="app-actions">
        <Button disabled={profiles.length >= 8} onClick={add}>
          Add test profile
        </Button>
        {selected && (
          <Button
            onClick={() =>
              onProfiles(profiles.filter((item) => item !== selected))
            }
          >
            Remove selected profile
          </Button>
        )}
      </div>
    </section>
  );
}
