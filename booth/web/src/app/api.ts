// Thin fetch wrapper for the Booth JSON API. The HMAC session cookie is sent
// automatically (credentials: "include"); a 401 from any call means the session
// expired/absent — callers surface the A3 session-expired state (handled centrally
// by the AuthProvider, which treats 401 as logged-out).

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

// Central 401 handler (W1). Any API call that comes back 401 means the Booth
// session is gone — the plan requires "401 from any API call → session-expired
// screen + redirect" (§3.1). The AuthProvider registers a handler here so a 401
// from a deep data call tears the whole app down to the session-expired surface,
// not just the component that happened to make the call. The initial whoami probe
// and the login POST also pass through here, but the handler no-ops unless a live
// session was already established, so those stay plain anonymous/invalid flows.
type UnauthorizedHandler = () => void;
let unauthorizedHandler: UnauthorizedHandler | null = null;

export function setUnauthorizedHandler(fn: UnauthorizedHandler | null): void {
  unauthorizedHandler = fn;
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: "include",
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });

  if (!res.ok) {
    if (res.status === 401) unauthorizedHandler?.();
    let msg = res.statusText;
    try {
      const data = await res.json();
      if (data && typeof data.error === "string") msg = data.error;
    } catch {
      /* non-JSON error body */
    }
    throw new ApiError(res.status, msg);
  }

  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, body),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, body),
  del: <T>(path: string) => request<T>("DELETE", path),
};
