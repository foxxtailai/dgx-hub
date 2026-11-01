import type { Session } from './types';
import { t } from './strings';

let session: Session | undefined;
let loadingSession: Promise<Session> | undefined;

export async function getSession(force = false, signal?: AbortSignal): Promise<Session> {
  if (force) { session = undefined; loadingSession = undefined; }
  if (session) return session;
  const load = () => fetch('/api/session', { credentials: 'same-origin', cache: 'no-store', signal })
    .then(async response => {
      if (!response.ok) throw new Error(t.sessionFailed);
      const result = await response.json() as Session;
      signal?.throwIfAborted();
      session = result;
      return session;
    });
  // Dashboard reads own their cancellation; do not share their abortable request
  // with a form mutation that needs the same process-local CSRF token.
  if (signal) return load();
  if (!loadingSession) loadingSession = load().finally(() => { loadingSession = undefined; });
  return loadingSession;
}

export async function api<T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (method !== 'GET') headers['X-CSRF-Token'] = (await getSession()).csrf_token;
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  let response: Response;
  try {
    response = await fetch(path, { method, headers, credentials: 'same-origin', cache: 'no-store', body: body === undefined ? undefined : JSON.stringify(body), signal });
  } catch (error) { if (signal?.aborted) throw error; throw new Error(t.connectionLost); }
  const payload = response.status === 204 ? null : await response.json().catch(() => null);
  if (!response.ok) throw new Error(payload?.error || t.requestFailed);
  return payload as T;
}
