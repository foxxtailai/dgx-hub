import { useCallback, useEffect, useRef, useState } from 'react';
import { api, getSession } from './api';
import { t } from './strings';
import type { Metrics, Node, Service, Session } from './types';

const message = (error: unknown) => error instanceof Error ? error.message : t.requestFailed;

export function useDashboard(paused: boolean) {
  const [nodes, setNodes] = useState<Node[]>([]);
  const [services, setServices] = useState<Service[]>([]);
  const [metrics, setMetrics] = useState<Record<string, Metrics>>({});
  const [session, setSession] = useState<Session>();
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [connectionError, setConnectionError] = useState('');
  const [revision, setRevision] = useState(0);
  const currentNodes = useRef<Node[]>([]);
  const configurationRequired = useRef(true);
  const manualRefresh = useRef(false);
  const cancel = useRef(() => {});

  // Invalidate synchronously, before opening a dialog or requesting a new cycle.
  // A transport may still deliver a response after abort, so each cycle also
  // checks its own cancellation flag before committing any state.
  const pause = useCallback(() => {
    cancel.current();
    configurationRequired.current = true;
  }, []);
  const refresh = useCallback(() => {
    pause();
    manualRefresh.current = true;
    setRevision(value => value + 1);
  }, [pause]);

  useEffect(() => {
    if (paused) return;
    let cancelled = false;
    let controller: AbortController;
    let timer: ReturnType<typeof setTimeout>;
    const stop = () => {
      cancelled = true;
      controller?.abort();
      clearTimeout(timer);
    };
    cancel.current = stop;
    const tick = async () => {
      controller = new AbortController();
      const { signal } = controller;
      const active = () => !cancelled && !signal.aborted;
      setRefreshing(manualRefresh.current);
      manualRefresh.current = false;
      try {
        if (configurationRequired.current) {
          const [sessionResult, nodeResult, serviceResult] = await Promise.allSettled([
            getSession(true, signal),
            api<{ nodes: Node[] }>('/api/nodes', 'GET', undefined, signal),
            api<{ services: Service[] }>('/api/services', 'GET', undefined, signal),
          ]);
          if (!active()) return;
          if (sessionResult.status === 'fulfilled') setSession(sessionResult.value);
          if (serviceResult.status === 'fulfilled') setServices(serviceResult.value.services || []);
          if (nodeResult.status === 'rejected') throw nodeResult.reason;
          currentNodes.current = nodeResult.value.nodes || [];
          setNodes(currentNodes.current);
          const ids = new Set(currentNodes.current.map(node => node.id));
          setMetrics(current => Object.fromEntries(Object.entries(current).filter(([id]) => ids.has(id))));
          configurationRequired.current = sessionResult.status === 'rejected' || serviceResult.status === 'rejected';
          setConnectionError(serviceResult.status === 'rejected' ? message(serviceResult.reason) : sessionResult.status === 'rejected' ? message(sessionResult.reason) : '');
        }
        await Promise.all(currentNodes.current.map(async node => {
          try {
            const result = await api<Metrics>(`/api/nodes/${node.id}/metrics`, 'GET', undefined, signal);
            if (active()) setMetrics(current => ({ ...current, [node.id]: result }));
          } catch (error) {
            if (active()) setMetrics(current => ({ ...current, [node.id]: {
              ...(current[node.id] || { node_id: node.id, cpu: null, gpu: null, temperature_c: null, power_w: null, memory: null, disks: [] }),
              status: 'error', stale: Boolean(current[node.id]?.last_success),
              errors: { ...current[node.id]?.errors, ssh: message(error) },
            } }));
          }
        }));
      } catch (error) {
        if (active()) setConnectionError(message(error));
      } finally {
        if (active()) {
          setLoading(false);
          setRefreshing(false);
          timer = setTimeout(() => void tick(), 10_000);
        }
      }
    };
    void tick();
    return stop;
  }, [paused, revision]);

  return { nodes, services, metrics, session, loading, refreshing, connectionError, refresh, pause };
}
