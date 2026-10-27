import type { Session } from './types';
import { t } from './strings';

let session: Session | undefined;
let loadingSession: Promise<Session> | undefined;

export async function getSession(force = false): Promise<Session> {
  if (force) { session = undefined; loadingSession = undefined; }
  if (session) return session;
  if (!loadingSession) loadingSession = fetch('/api/session', { credentials: 'same-origin', cache: 'no-store' })
    .then(async response => {
      if (!response.ok) throw new Error(t.sessionFailed);
      session = await response.json() as Session;
      return session;
    }).catch(error => { loadingSession = undefined; throw error; });
  return loadingSession;
}

export async function api<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (method !== 'GET') headers['X-CSRF-Token'] = (await getSession()).csrf_token;
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  let response: Response;
  try {
    response = await fetch(path, { method, headers, credentials: 'same-origin', cache: 'no-store', body: body === undefined ? undefined : JSON.stringify(body) });
  } catch { throw new Error(t.connectionLost); }
  const payload = response.status === 204 ? null : await response.json().catch(() => null);
  if (!response.ok) throw new Error(payload?.error || t.requestFailed);
  return payload as T;
}
