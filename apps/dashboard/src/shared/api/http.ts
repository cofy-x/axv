export class APIError extends Error {
  constructor(
    message: string,
    public status: number,
    public code: string,
  ) {
    super(message);
  }
}

export async function requestResponse(
  path: string,
  init: RequestInit = {},
): Promise<Response> {
  const headers = new Headers(init.headers);
  if (!headers.has('X-Requested-With'))
    // Access returns 401 for expired AJAX authorization rather than an HTML
    // sign-in redirect. Keep the current desktop and its drafts mounted.
    headers.set('X-Requested-With', 'XMLHttpRequest');
  if (
    init.body &&
    !(init.body instanceof FormData) &&
    !headers.has('Content-Type')
  )
    headers.set('Content-Type', 'application/json');
  const response = await fetch(`/api${path}`, {
    ...init,
    headers,
  });
  if (!response.ok) {
    const value: unknown = await response.json().catch(() => null);
    const body =
      value !== null && typeof value === 'object' && !Array.isArray(value)
        ? (value as Record<string, unknown>)
        : {};
    const code = typeof body.code === 'string' ? body.code : 'http_error';
    if (
      response.status === 401 &&
      (code === 'cloudflare_unauthorized' || code === 'http_error')
    )
      throw new APIError(
        'Authorization is required. Restore access, then retry.',
        response.status,
        'authentication_required',
      );
    throw new APIError(
      typeof body.error === 'string' && body.error
        ? body.error
        : 'The server could not complete this operation.',
      response.status,
      code,
    );
  }
  return response;
}

export async function request<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  return (await requestResponse(path, init)).json() as Promise<T>;
}
