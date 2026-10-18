import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { CodexManager } from './CodexManager';
import { CodexTerminal } from './CodexTerminal';

export function CodexApp(props: AppWindowProps) {
  return props.window.params.sessionId && props.window.params.runId ? (
    <CodexTerminal {...props} />
  ) : (
    <CodexManager {...props} />
  );
}
