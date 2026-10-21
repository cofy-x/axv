import { describe, expect, it } from 'vitest';
import { codexTurnInput } from './lib-vm-codex-workflow.mjs';

describe('native Codex provider input capture', () => {
  it('parses complete wrapped arrays with sorted skill fields and bracket characters in text', () => {
    const input = [
      { type: 'text', text: 'Review [one] and "two".', text_elements: [] },
      { name: 'axv-vm-project', path: '/private/SKILL.md', type: 'skill' },
    ];
    const json = JSON.stringify(input);
    const split = json.indexOf('axv-vm-project') + 4;
    const output = `message review\r\nTURN_INPUT ${json.slice(0, split)}\r\n${String.fromCharCode(27)}[0m${json.slice(split)}\r\ncodex-test> `;
    expect(codexTurnInput(output)).toEqual(input);
  });

  it('waits for a complete newest input instead of accepting an earlier turn or a nested array', () => {
    expect(
      codexTurnInput('TURN_INPUT [{"type":"text","text_elements":[]'),
    ).toBeUndefined();
    const previous = 'TURN_INPUT [{"type":"text","text":"old"}]\r\n';
    expect(
      codexTurnInput(previous + 'TURN_INPUT [{"type":"text"'),
    ).toBeUndefined();
    const latest = [
      { name: 'axv-vm-project', type: 'skill', path: '/run/SKILL.md' },
    ];
    expect(
      codexTurnInput(previous + `TURN_INPUT ${JSON.stringify(latest)}\r\n`),
    ).toEqual(latest);
  });
});
