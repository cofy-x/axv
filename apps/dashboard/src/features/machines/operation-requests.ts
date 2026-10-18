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

/** Keep the exact payload until an uncertain response has been resolved. */
export class OperationRequest<Input, Result> {
  private attempt: OperationAttempt<Input> | null = null;
  private inFlight: Promise<Result> | null = null;

  constructor(
    private readonly send: (input: Input, requestId: string) => Promise<Result>,
  ) {}

  get pending() {
    return this.attempt;
  }

  submit(input: Input, label: string): Promise<Result> {
    if (this.inFlight) return this.inFlight;
    if (this.attempt)
      return Promise.reject(
        new Error(
          'Resolve the previous request before submitting another change.',
        ),
      );
    this.attempt = {
      input: structuredClone(input),
      requestId: crypto.randomUUID(),
      label,
    };
    return this.sendAttempt();
  }

  retry(): Promise<Result> {
    if (this.inFlight) return this.inFlight;
    if (!this.attempt)
      return Promise.reject(
        new Error('There is no unresolved request to retry.'),
      );
    return this.sendAttempt();
  }

  private sendAttempt(): Promise<Result> {
    const attempt = this.attempt!;
    this.inFlight = Promise.resolve()
      .then(() => this.send(attempt.input, attempt.requestId))
      .then((result) => {
        this.attempt = null;
        return result;
      })
      .catch((error: unknown) => {
        // A validation/conflict response establishes that this request was not
        // accepted. Transport failures and server errors can follow a commit.
        if (isDefiniteOperationRejection(error)) this.attempt = null;
        throw error;
      })
      .finally(() => {
        this.inFlight = null;
      });
    return this.inFlight;
  }
}

export function operationError(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
