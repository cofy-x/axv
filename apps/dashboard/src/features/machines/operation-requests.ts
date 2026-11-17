import { APIError } from '../../shared/api/http';

export function isDefiniteOperationRejection(error: unknown) {
  return (
    error instanceof APIError &&
    error.status >= 400 &&
    error.status < 500 &&
    !['binding_changed', 'compute_forbidden'].includes(error.code)
  );
}

export interface OperationAttempt<Input> {
  input: Input;
  requestId: string;
  label: string;
}

type OperationState<Input, Result> =
  | { phase: 'idle' }
  | {
      phase: 'sending';
      attempt: OperationAttempt<Input>;
      promise: Promise<Result>;
    }
  | { phase: 'uncertain'; attempt: OperationAttempt<Input> };

/** Keep the exact payload until an uncertain response has been resolved. */
export class OperationRequest<Input, Result> {
  private state: OperationState<Input, Result> = { phase: 'idle' };

  constructor(
    private readonly send: (input: Input, requestId: string) => Promise<Result>,
  ) {}

  get pending() {
    return this.state.phase === 'idle' ? null : this.state.attempt;
  }

  get phase() {
    return this.state.phase;
  }

  submit(input: Input, label: string): Promise<Result> {
    if (this.state.phase === 'sending') return this.state.promise;
    if (this.state.phase === 'uncertain')
      return Promise.reject(
        new Error(
          'Resolve the previous request before submitting another change.',
        ),
      );
    return this.sendAttempt({
      input: structuredClone(input),
      requestId: crypto.randomUUID(),
      label,
    });
  }

  retry(): Promise<Result> {
    if (this.state.phase === 'sending') return this.state.promise;
    if (this.state.phase === 'idle')
      return Promise.reject(
        new Error('There is no unresolved request to retry.'),
      );
    return this.sendAttempt(this.state.attempt);
  }

  private sendAttempt(attempt: OperationAttempt<Input>): Promise<Result> {
    const promise = Promise.resolve()
      .then(() => this.send(attempt.input, attempt.requestId))
      .then(
        (result) => {
          this.state = { phase: 'idle' };
          return result;
        },
        (error: unknown) => {
          // A validation/conflict response establishes that this request was not
          // accepted. Transport failures and server errors can follow a commit.
          this.state = isDefiniteOperationRejection(error)
            ? { phase: 'idle' }
            : { phase: 'uncertain', attempt };
          throw error;
        },
      );
    this.state = { phase: 'sending', attempt, promise };
    return promise;
  }
}

export function operationError(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
