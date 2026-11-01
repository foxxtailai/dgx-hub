import { StrictMode } from 'react';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import App from '../src/App';
import { api, getSession } from '../src/api';
import type { Action, ActionRun, Metrics, Node, Service } from '../src/types';

vi.mock('../src/api', () => ({ api: vi.fn(), getSession: vi.fn() }));
const node: Node = { id: 'node', name: 'Elly', color: 'green', ssh_target: 'elly', data_path: '' };
const service: Service = { id: 'service', name: 'Notebook', node_id: node.id, remote_host: '127.0.0.1', remote_port: 8080, scheme: 'http', path: '/' };
const action: Action = { id: 'action', name: 'Check', description: '', steps: [{ name: '', command: 'echo hello', timeout_seconds: 60, on_error: 'stop' }], node_ids: [node.id], confirm_before_run: false };
const initialMetrics: Metrics = { node_id: node.id, status: 'online', stale: false, last_success: '2026-11-01T08:00:00Z', cpu: 20, gpu: 10, temperature_c: 62.4, power_w: 45.67, memory: null, disks: [], errors: {} };
type Request = { path: string; signal?: AbortSignal };
let requests: Request[];
let metric: Metrics;
let runs: ActionRun[];
let nextMetric: Promise<Metrics> | undefined;
let nextNodes: Promise<{ nodes: Node[] }> | undefined;
let savedService: Service;
const metricsCalls = () => requests.filter(request => request.path.endsWith('/metrics'));
const configCalls = () => requests.filter(request => ['/api/nodes', '/api/services'].includes(request.path));
const settle = () => act(async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); });
const advance = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms); });
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
async function open(name: string) {
  fireEvent.click(screen.getByRole('button', { name }));
  await settle();
  return within(screen.getByRole('dialog'));
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllMocks();
  requests = []; metric = { ...initialMetrics }; runs = []; nextMetric = undefined; nextNodes = undefined; savedService = { ...service };
  vi.mocked(getSession).mockResolvedValue({ csrf_token: 'csrf', launch_token: 'launch', version: 'test' });
  vi.mocked(api).mockImplementation(async (path, method = 'GET', body, signal) => {
    requests.push({ path, signal });
    if (path === '/api/nodes') { const result = nextNodes; nextNodes = undefined; return result || { nodes: [node] }; }
    if (path === '/api/services') return { services: [savedService] };
    if (path.endsWith('/metrics')) { const result = nextMetric; nextMetric = undefined; return result || metric; }
    if (path === '/api/services/service' && method === 'PATCH') { savedService = { ...service, ...body as object }; return savedService; }
    if (path.endsWith('/check')) return { ok: true, code: 'ok', message: 'Ready', steps: [] };
    if (path === '/api/actions') return { actions: [action] };
    if (path.startsWith('/api/action-runs?')) return { runs };
    if (path === '/api/action-runs/run') return runs[0];
    if (path === '/api/action-runs/run/cancel') { runs = [{ ...runs[0], status: 'cancelled' }]; return runs[0]; }
    throw new Error(`Unexpected request: ${method} ${path}`);
  });
});

describe('dashboard polling', () => {
  it('keeps both sensor slots visible when one sensor is unavailable', async () => {
    metric = { ...metric, temperature_c: null, power_w: 0, errors: { temperature: 'Not supported' } };
    render(<App />); await settle();
    expect(screen.getByText('Device Temp').parentElement?.textContent).toContain('N/A');
    expect(screen.getByText('Device Power').parentElement?.textContent).toContain('0.0 W');
    expect(screen.getByText('Device Temp').parentElement?.title).toBe('Not supported');
  });

  it('updates only metrics on the normal timer and keeps the workspace visible', async () => {
    render(<App />); await settle();
    expect(screen.getByText('62 °C')).toBeTruthy();
    expect(screen.getByText('45.7 W')).toBeTruthy();
    const configs = configCalls().length;
    metric = { ...metric, temperature_c: 0, power_w: 0 };
    await advance(10_000);
    expect(metricsCalls()).toHaveLength(2);
    expect(configCalls()).toHaveLength(configs);
    expect(getSession).toHaveBeenCalledTimes(1);
    expect(screen.getByText('0 °C')).toBeTruthy();
    expect(screen.getByText('0.0 W')).toBeTruthy();
    expect(screen.queryByText('Loading your workspace…')).toBeNull();
  });

  it.each(['Add node', 'Edit Elly', 'Delete Elly', 'Add service', 'Edit Notebook', 'Delete Notebook', 'SSH setup', 'Actions'])(
    'pauses throughout %s and refreshes exactly once on close', async name => {
      render(<App />); await settle();
      const dialog = await open(name);
      const before = requests.length;
      await advance(35_000);
      expect(requests).toHaveLength(before);
      expect(screen.getByText('Dashboard updates paused while this window is open.')).toBeTruthy();
      fireEvent.click(dialog.getByRole('button', { name: 'Close' }));
      await settle();
      expect(metricsCalls()).toHaveLength(2);
      await advance(9_999);
      expect(metricsCalls()).toHaveLength(2);
      await advance(1);
      expect(metricsCalls()).toHaveLength(3);
    },
  );

  it.each(['success', 'failure'])('ignores a late metrics %s after the editor opens', async outcome => {
    render(<App />); await settle();
    const pending = deferred<Metrics>(); nextMetric = pending.promise;
    await advance(10_000);
    const inFlight = metricsCalls().at(-1)!;
    const dialog = await open('Edit Notebook');
    expect(inFlight.signal?.aborted).toBe(true);
    const input = dialog.getByLabelText('Name') as HTMLInputElement;
    fireEvent.change(input, { target: { value: 'Unfinished name' } });
    input.focus(); input.setSelectionRange(3, 8);
    if (outcome === 'success') pending.resolve({ ...metric, temperature_c: 99 });
    else pending.reject(new Error('Late failure'));
    await settle(); await advance(30_000);
    expect(input.value).toBe('Unfinished name');
    expect(document.activeElement).toBe(input);
    expect([input.selectionStart, input.selectionEnd]).toEqual([3, 8]);
    expect(screen.queryByText('99 °C')).toBeNull();
    expect(screen.queryByText('Late failure')).toBeNull();
    expect(metricsCalls()).toHaveLength(2);
  });

  it('ignores late configuration and does not overlap manual refreshes', async () => {
    render(<App />); await settle();
    const pending = deferred<{ nodes: Node[] }>(); nextNodes = pending.promise;
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await settle();
    const inFlight = requests.filter(request => request.path === '/api/nodes').at(-1)!;
    const dialog = await open('Edit Notebook');
    expect(inFlight.signal?.aborted).toBe(true);
    pending.resolve({ nodes: [] }); await settle();
    expect(screen.queryByText('Make yourself at home.')).toBeNull();
    fireEvent.click(dialog.getByRole('button', { name: 'Cancel' })); await settle();
    expect(metricsCalls()).toHaveLength(2);
    await advance(10_000);
    expect(metricsCalls()).toHaveLength(3);
  });

  it('saves input and resumes one cycle without accumulating timer ticks', async () => {
    render(<App />); await settle();
    const dialog = await open('Edit Notebook');
    fireEvent.change(dialog.getByLabelText('Name'), { target: { value: 'New notebook' } });
    await advance(60_000);
    fireEvent.click(dialog.getByRole('button', { name: 'Save changes' })); await settle();
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.getByRole('heading', { name: 'New notebook' })).toBeTruthy();
    expect(metricsCalls()).toHaveLength(2);
    await advance(10_000);
    expect(metricsCalls()).toHaveLength(3);
  });

  it('keeps Action edits intact while active run polling and cancellation continue', async () => {
    runs = [{ id: 'run', action_id: action.id, action_name: action.name, node_id: node.id, node_name: node.name, status: 'running', started_at: '2026-11-01T08:00:00Z', duration_ms: 0, error: '', steps: [] }];
    render(<App />); await settle();
    let dialog = await open('Actions');
    fireEvent.click(dialog.getByRole('button', { name: 'Edit' })); await settle();
    const input = dialog.getByLabelText('Command') as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: 'echo edited\nprintf done' } });
    input.focus(); input.setSelectionRange(5, 11);
    await advance(20_000);
    expect(input.value).toBe('echo edited\nprintf done');
    expect(document.activeElement).toBe(input);
    expect([input.selectionStart, input.selectionEnd]).toEqual([5, 11]);
    expect(metricsCalls()).toHaveLength(1);
    expect(requests.filter(request => request.path === '/api/action-runs/run').length).toBeGreaterThan(2);
    fireEvent.click(dialog.getByRole('button', { name: 'Cancel' })); await settle();
    fireEvent.click(dialog.getByRole('button', { name: 'View run' })); await settle();
    dialog = within(screen.getByRole('dialog'));
    fireEvent.click(dialog.getByRole('button', { name: 'Cancel run' })); await settle();
    expect(requests.some(request => request.path.endsWith('/cancel'))).toBe(true);
    expect(dialog.getByText('Cancelled')).toBeTruthy();
    expect(metricsCalls()).toHaveLength(1);
  });

  it('has one live polling loop under StrictMode and cancels it on unmount', async () => {
    const app = render(<StrictMode><App /></StrictMode>); await settle();
    expect(metricsCalls()).toHaveLength(1);
    await advance(20_000);
    expect(metricsCalls()).toHaveLength(3);
    const pending = deferred<Metrics>(); nextMetric = pending.promise;
    await advance(10_000);
    const inFlight = metricsCalls().at(-1)!;
    app.unmount();
    expect(inFlight.signal?.aborted).toBe(true);
    pending.resolve(metric); await settle(); await advance(30_000);
    expect(metricsCalls()).toHaveLength(4);
  });

  it('waits for a slow metrics request before scheduling the next tick', async () => {
    render(<App />); await settle();
    const pending = deferred<Metrics>(); nextMetric = pending.promise;
    await advance(30_000);
    expect(metricsCalls()).toHaveLength(2);
    pending.resolve(metric); await settle();
    await advance(9_999);
    expect(metricsCalls()).toHaveLength(2);
    await advance(1);
    expect(metricsCalls()).toHaveLength(3);
  });
});
