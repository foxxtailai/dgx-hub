import { render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ConnectXStatus } from '../src/ConnectXStatus';
import type { Metrics, Node } from '../src/types';

const peer: Node = { id: 'b', name: 'Spark Beta', color: 'blue', ssh_target: 'spark-b', data_path: '' };
const metrics: Metrics = {
  node_id: 'a', status: 'online', stale: false, cpu: null, gpu: null, temperature_c: null, power_w: null, memory: null, disks: [], errors: {},
  connectx: { status: 'connected', interfaces: [], peers: [{ node_id: 'b', links: ['enp1s0f0np0', 'enP2p1s0f0np0'].flatMap(source => ['enp1s0f0np0', 'enP2p1s0f0np0'].map(dest => ({ interface: source, peer_interface: dest, speed_mbps: 200000 }))) }] },
};

describe('ConnectX connection', () => {
  it('shows the peer once without presenting reachable interface pairs as physical channels', () => {
    render(<ConnectXStatus metrics={metrics} nodes={[peer]} />);
    const section = within(screen.getByRole('region', { name: 'ConnectX connection' }));
    expect(section.getByText('Connected')).toBeTruthy();
    expect(section.getByText('Spark Beta').closest('.node-color-blue')).toBeTruthy();
    expect(section.getAllByText('Spark Beta')).toHaveLength(1);
    expect(section.queryByText(/enp1s0f0np0|enP2p1s0f0np0/)).toBeNull();
    expect(section.queryByText(/Gb\/s/)).toBeNull();
  });

  it.each(['disconnected', 'link_up', 'unknown', 'not_detected'] as const)('renders %s without inventing a peer', status => {
    render(<ConnectXStatus metrics={{ ...metrics, connectx: { ...metrics.connectx!, status, peers: [] } }} nodes={[peer]} />);
    expect(screen.queryByText('Connected')).toBeNull();
    expect(screen.queryByText('Spark Beta')).toBeNull();
  });

  it.each([{ status: 'offline', stale: true }, { status: 'error', stale: false }, { status: 'online', stale: true }])('hides cached connections on $status / stale=$stale', state => {
    render(<ConnectXStatus metrics={{ ...metrics, ...state }} nodes={[peer]} />);
    expect(screen.getByText('Unavailable')).toBeTruthy();
    expect(screen.queryByText('Spark Beta')).toBeNull();
  });

  it('removes deleted peers and updates the status after a link drop', () => {
    const view = render(<ConnectXStatus metrics={metrics} nodes={[peer]} />);
    view.rerender(<ConnectXStatus metrics={metrics} nodes={[]} />);
    expect(screen.queryByText('Spark Beta')).toBeNull();
    expect(screen.getByText('Link up · peer unconfirmed')).toBeTruthy();
    view.rerender(<ConnectXStatus metrics={{ ...metrics, connectx: { ...metrics.connectx!, status: 'disconnected', peers: [] } }} nodes={[peer]} />);
    expect(screen.getByText('Disconnected')).toBeTruthy();
  });

  it('shows checking before the first reading', () => {
    render(<ConnectXStatus nodes={[]} />);
    expect(screen.getByText('Checking…')).toBeTruthy();
  });
});
