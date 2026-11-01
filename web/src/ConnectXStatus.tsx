import { NodeName, nodeColorClass } from './NodeName';
import { t } from './strings';
import type { Metrics, Node } from './types';

export function ConnectXStatus({ metrics, nodes }: { metrics?: Metrics; nodes: Node[] }) {
  const current = metrics?.status === 'online' && !metrics.stale;
  const cx = metrics?.connectx;
  const peers = current ? (cx?.peers || []).flatMap(peer => {
    const node = nodes.find(node => node.id === peer.node_id && node.id !== metrics?.node_id);
    return node ? [{ ...peer, node }] : [];
  }) : [];
  const status = !current ? 'unknown' : cx?.status === 'connected' && !peers.length ? 'link_up' : cx?.status || 'unknown';
  const checking = !metrics || metrics.status === 'connecting';
  const detail = metrics?.errors?.connectx || (status === 'link_up' ? t.connectXUnconfirmed : status === 'not_detected' ? t.connectXNotDetected : undefined);
  return <section className={`connectx connectx-${status}`} aria-label={t.connectXConnection} title={detail}>
    <div className="connectx-heading"><span>{t.connectX}</span><span className="connectx-status"><i />{checking ? t.connectXChecking : t.connectXStatuses[status]}</span></div>
    {status === 'connected' && <ul className="connectx-peers">{peers.map(peer => <li key={peer.node_id}>
      <span className={`connectx-peer-name ${nodeColorClass(peer.node)}`}><span aria-hidden="true">↔</span><NodeName node={peer.node} /></span>
    </li>)}</ul>}
  </section>;
}
