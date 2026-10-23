import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { deferWindow } from '../../shared/desktop/defer-window';

const CodexManager = deferWindow(() =>
  import('./CodexManager').then((module) => module.CodexManager),
);
const CodexTerminal = deferWindow(() =>
  import('./CodexTerminal').then((module) => module.CodexTerminal),
);

export function CodexApp(props: AppWindowProps) {
  return props.window.params.sessionId && props.window.params.runId ? (
    <CodexTerminal {...props} />
  ) : (
    <CodexManager {...props} />
  );
}
