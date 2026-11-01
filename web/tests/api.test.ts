import { beforeEach, expect, it, vi } from 'vitest';

beforeEach(() => { vi.resetModules(); });

it('passes cancellation to fetch and preserves an abort error', async () => {
  const fetch = vi.fn().mockImplementation((_path, options) => new Promise((_resolve, reject) => {
    options.signal.addEventListener('abort', () => reject(options.signal.reason));
  }));
  vi.stubGlobal('fetch', fetch);
  const { api } = await import('../src/api');
  const controller = new AbortController();
  const result = api('/api/nodes/node/metrics', 'GET', undefined, controller.signal);
  const failed = expect(result).rejects.toMatchObject({ name: 'AbortError' });
  controller.abort();
  await failed;
  expect(fetch.mock.calls[0][1].signal).toBe(controller.signal);
});

it('does not cache a late aborted session or prevent a subsequent form save', async () => {
  let resolve!: (response: Response) => void;
  const pending = new Promise<Response>(yes => { resolve = yes; });
  const fetch = vi.fn().mockReturnValueOnce(pending)
    .mockResolvedValueOnce(new Response(JSON.stringify({ csrf_token: 'current' })))
    .mockResolvedValueOnce(new Response(JSON.stringify({ id: 'service' })));
  vi.stubGlobal('fetch', fetch);
  const { api, getSession } = await import('../src/api');
  const controller = new AbortController();
  const result = getSession(true, controller.signal);
  const failed = expect(result).rejects.toMatchObject({ name: 'AbortError' });
  controller.abort();
  resolve(new Response(JSON.stringify({ csrf_token: 'obsolete' })));
  await failed;
  await api('/api/services', 'POST', { name: 'Saved' });
  expect(fetch).toHaveBeenCalledTimes(3);
  expect(fetch.mock.calls[2][1].headers['X-CSRF-Token']).toBe('current');
});
