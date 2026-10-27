import { useCallback, useEffect, useId, useRef, useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import { api, getSession } from './api';
import { t } from './strings';
import type { Metrics, Node, NodeInput, Service, ServiceInput, Session, SSHCheck, TerminalResult } from './types';

type IconName = 'plus' | 'grid' | 'server' | 'link' | 'terminal' | 'arrow' | 'edit' | 'trash' | 'close' | 'refresh' | 'shield' | 'copy' | 'check' | 'alert' | 'chevron';
function Icon({ name, size = 18, className = '' }: { name: IconName; size?: number; className?: string }) {
  const paths: Record<IconName, ReactNode> = {
    plus: <path d="M12 5v14M5 12h14" />, grid: <><rect x="3" y="3" width="7" height="7" rx="1.5" /><rect x="14" y="3" width="7" height="7" rx="1.5" /><rect x="3" y="14" width="7" height="7" rx="1.5" /><rect x="14" y="14" width="7" height="7" rx="1.5" /></>,
    server: <><rect x="3" y="3" width="18" height="7" rx="2" /><rect x="3" y="14" width="18" height="7" rx="2" /><path d="M7 6.5h.01M7 17.5h.01M16 6.5h2M16 17.5h2" /></>,
    link: <><path d="m10 13 4-4m-5 7-1 1a4.2 4.2 0 0 1-6-6l4-4a4.2 4.2 0 0 1 6 0m0 1 1-1a4.2 4.2 0 0 1 6 6l-4 4a4.2 4.2 0 0 1-6 0" /></>,
    terminal: <><rect x="3" y="4" width="18" height="16" rx="2" /><path d="m7 9 3 3-3 3m6 0h4" /></>,
    arrow: <path d="M7 17 17 7M7 7h10v10" />, edit: <><path d="m15 5 4 4M4 20l5-1L20 8a2.8 2.8 0 0 0-4-4L5 15z" /></>,
    trash: <><path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7M14 10v7" /></>, close: <path d="m6 6 12 12M6 18 18 6" />,
    refresh: <><path d="M20 7v5h-5M4 17v-5h5M6 6a8 8 0 0 1 13 2M5 16a8 8 0 0 0 13 2" /></>,
    shield: <><path d="m12 3 8 3v6c0 5-8 9-8 9s-8-4-8-9V6z" /><path d="m8.5 12 2.5 2.5 4.5-5" /></>,
    copy: <><rect x="8" y="8" width="12" height="13" rx="2" /><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3" /></>,
    check: <path d="m5 12 4 4L19 6" />, alert: <><path d="m12 3 10 18H2zM12 9v5M12 17h.01" /></>, chevron: <path d="m9 5 7 7-7 7" />,
  };
  return <svg className={className} width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{paths[name]}</svg>;
}

const errorText = (error: unknown) => error instanceof Error ? error.message : t.requestFailed;
const percent = (value: number | null | undefined) => value == null || !Number.isFinite(value) ? t.na : `${Math.round(value)}%`;
function bytes(value: number): string {
  if (!Number.isFinite(value) || value < 0) return t.na;
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  const order = value === 0 ? 0 : Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** order).toFixed(order >= 3 ? 1 : 0)} ${units[order]}`;
}
function endpoint(service: Service) {
  const host = service.remote_host.includes(':') && !service.remote_host.startsWith('[') ? `[${service.remote_host}]` : service.remote_host;
  return `${service.scheme}://${host}:${service.remote_port}${service.path || '/'}`;
}

function Modal({ title, description, onClose, children, wide = false }: { title: string; description?: string; onClose: () => void; children: ReactNode; wide?: boolean }) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleID = useId(); const descriptionID = useId();
  useEffect(() => { const dialog = ref.current!; dialog.showModal(); return () => dialog.close(); }, []);
  return <dialog ref={ref} className={`modal ${wide ? 'modal-wide' : ''}`} aria-labelledby={titleID} aria-describedby={description ? descriptionID : undefined} onCancel={event => { event.preventDefault(); onClose(); }} onClick={event => {
    if (event.target !== event.currentTarget) return;
    const rect = event.currentTarget.getBoundingClientRect();
    if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) onClose();
  }}>
    <div className="modal-header"><div><h2 id={titleID}>{title}</h2>{description && <p id={descriptionID}>{description}</p>}</div><button className="icon-button" onClick={onClose} aria-label={t.close}><Icon name="close" /></button></div>
    {children}
  </dialog>;
}

function CopyCommand({ command }: { command: string }) {
  const [copied, setCopied] = useState(false); const [failed, setFailed] = useState(false);
  const resetTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(resetTimer.current), []);
  return <><div className="command"><code tabIndex={0}>{command}</code><button className="icon-button" aria-label={copied ? t.copied : t.copy} title={copied ? t.copied : t.copy} onClick={async () => {
    try { await navigator.clipboard.writeText(command); setCopied(true); setFailed(false); clearTimeout(resetTimer.current); resetTimer.current = setTimeout(() => setCopied(false), 2000); }
    catch { setFailed(true); }
  }}><Icon name={copied ? 'check' : 'copy'} /></button></div>{failed && <p className="field-error">{t.copyFailed}</p>}</>;
}

function Meter({ label, value, detail, error }: { label: string; value: number | null | undefined; detail?: string; error?: string }) {
  const valid = value != null && Number.isFinite(value);
  return <div className="metric" title={error || detail}><div className="metric-label"><span>{label}</span><strong>{percent(value)}</strong></div><div className={`meter ${!valid ? 'meter-empty' : ''}`} aria-hidden="true"><progress max={100} value={valid ? Math.min(100, Math.max(0, value)) : 0} /></div>{detail && <div className="metric-detail">{detail}</div>}</div>;
}

function NodeCard({ node, metrics, onEdit, onDelete, onSetup, onTerminal }: { node: Node; metrics?: Metrics; onEdit: () => void; onDelete: () => void; onSetup: () => void; onTerminal: () => void }) {
  const status = metrics?.status || 'connecting';
  const errors = Object.entries(metrics?.errors || {}).filter(([field, value]) => field !== 'ssh_code' && value);
  const needsSetup = ['authentication_required', 'host_key_required', 'ssh_missing', 'ssh_config_error'].includes(metrics?.errors?.ssh_code || '');
  const when = metrics?.last_success ? new Date(metrics.last_success) : null;
  return <article className={`node-card ${metrics?.stale ? 'node-stale' : ''}`}>
    <div className="node-top"><div className="node-avatar"><Icon name="server" size={22} /></div><div className="node-identity"><h3>{node.name}</h3><code>{node.ssh_target}</code></div><span className={`status status-${status}`}><i />{t.statuses[status] || t.statuses.unknown}</span></div>
    <div className="metrics-grid"><Meter label={t.cpu} value={metrics?.cpu} error={metrics?.errors?.cpu} /><Meter label={t.gpu} value={metrics?.gpu} error={metrics?.errors?.gpu} /></div>
    <Meter label={t.memory} value={metrics?.memory?.percent} error={metrics?.errors?.memory} detail={metrics?.memory ? `${bytes(metrics.memory.used)} / ${bytes(metrics.memory.total)}` : undefined} />
    <div className="shared-memory-note">{t.unified}</div>
    {metrics?.disks?.length ? metrics.disks.map(disk => <Meter key={`${disk.mount}:${disk.path}`} label={`${t.disk} · ${disk.path}`} value={disk.percent} detail={`${bytes(disk.used)} / ${bytes(disk.total)}`} />) : <Meter label={`${t.disk} · /`} value={null} error={metrics?.errors?.disk} />}
    <div className="node-reading"><span title={when?.toLocaleString('en-US')}>{metrics?.stale ? <><Icon name="alert" size={12} />{t.stale}</> : when ? `${t.lastSuccess} ${when.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false })}` : t.noReading}</span></div>
    {errors.length > 0 && <details className="metric-errors"><summary>{needsSetup ? t.setupRequired : t.metricErrors}<Icon name="chevron" size={14} /></summary><ul>{errors.map(([field, message]) => <li key={field}><span>{field}</span>{message}</li>)}</ul></details>}
    <div className="node-actions"><button className="button button-secondary terminal-button" onClick={onTerminal}><Icon name="terminal" size={16} />{t.terminal}</button><button className={`button button-quiet ${needsSetup ? 'setup-highlight' : ''}`} onClick={onSetup} title={t.checkConnection}>{t.setup}</button><div className="node-edit-actions"><button className="icon-button" onClick={onEdit} aria-label={`${t.edit} ${node.name}`} title={t.edit}><Icon name="edit" size={15} /></button><button className="icon-button danger-hover" onClick={onDelete} aria-label={`${t.remove} ${node.name}`} title={t.remove}><Icon name="trash" size={15} /></button></div></div>
  </article>;
}

function NodeForm({ node, onClose, onSave }: { node?: Node; onClose: () => void; onSave: (value: NodeInput) => Promise<void> }) {
  const [value, setValue] = useState<NodeInput>({ name: node?.name || '', ssh_target: node?.ssh_target || '', data_path: node?.data_path || '' });
  const [busy, setBusy] = useState(false); const [error, setError] = useState('');
  const change = (key: keyof NodeInput, input: string) => setValue(current => ({ ...current, [key]: input }));
  async function submit(event: FormEvent) {
    event.preventDefault(); setError('');
    const cleaned = { name: value.name.trim(), ssh_target: value.ssh_target.trim(), data_path: value.data_path.trim() };
    if (!cleaned.name || !cleaned.ssh_target) return setError(t.required);
    if (cleaned.ssh_target.startsWith('-') || /[\s\x00-\x1f\x7f]/.test(cleaned.ssh_target)) return setError(t.invalidTarget);
    if (cleaned.data_path && !cleaned.data_path.startsWith('/')) return setError(t.invalidDataPath);
    setBusy(true);
    try { await onSave(cleaned); } catch (err) { setError(errorText(err)); setBusy(false); }
  }
  return <Modal title={node ? t.editNode : t.newNode} description={t.nodeFormDescription} onClose={() => !busy && onClose()}><form onSubmit={submit}>
    <label className="field">{t.name}<input autoFocus required maxLength={100} value={value.name} placeholder={t.nodeNamePlaceholder} onChange={event => change('name', event.target.value)} /></label>
    <label className="field">{t.sshTarget}<input required maxLength={255} value={value.ssh_target} placeholder={t.sshTargetPlaceholder} autoCapitalize="none" spellCheck={false} onChange={event => change('ssh_target', event.target.value)} /><span className="hint">{t.sshTargetHint}</span></label>
    <label className="field">{t.dataPath}<span className="optional">{t.optional}</span><input maxLength={4096} value={value.data_path} placeholder={t.dataPathPlaceholder} spellCheck={false} onChange={event => change('data_path', event.target.value)} /><span className="hint">{t.dataPathHint}</span></label>
    {error && <div className="form-error" role="alert"><Icon name="alert" size={16} />{error}</div>}
    <div className="modal-footer"><button type="button" className="button button-secondary" disabled={busy} onClick={onClose}>{t.cancel}</button><button className="button button-primary" disabled={busy}>{busy ? t.saving : node ? t.save : t.addNode}</button></div>
  </form></Modal>;
}

function ServiceForm({ service, nodes, onClose, onSave }: { service?: Service; nodes: Node[]; onClose: () => void; onSave: (value: ServiceInput) => Promise<void> }) {
  const [value, setValue] = useState<ServiceInput>({ name: service?.name || '', node_id: service?.node_id || nodes[0]?.id || '', remote_host: service?.remote_host || '127.0.0.1', remote_port: service?.remote_port || 8080, scheme: service?.scheme || 'http', path: service?.path || '/' });
  const [port, setPort] = useState(String(value.remote_port));
  const [busy, setBusy] = useState(false); const [error, setError] = useState('');
  const change = (key: keyof ServiceInput, input: string) => setValue(current => ({ ...current, [key]: input }));
  async function submit(event: FormEvent) {
    event.preventDefault(); setError('');
    const cleaned = { ...value, name: value.name.trim(), remote_host: value.remote_host.trim(), path: value.path.trim() || '/', remote_port: Number(port) };
    if (!cleaned.name || !cleaned.node_id || !cleaned.remote_host) return setError(t.required);
    if (!Number.isInteger(cleaned.remote_port) || cleaned.remote_port < 1 || cleaned.remote_port > 65535) return setError(t.invalidPort);
    if (!cleaned.path.startsWith('/') || cleaned.path.startsWith('//')) return setError(t.invalidPath);
    if (/[\s/\\?#]/.test(cleaned.remote_host) || cleaned.remote_host.includes('://')) return setError(t.invalidHost);
    setBusy(true); try { await onSave(cleaned); } catch (err) { setError(errorText(err)); setBusy(false); }
  }
  return <Modal title={service ? t.editService : t.newService} description={t.serviceFormDescription} onClose={() => !busy && onClose()}><form onSubmit={submit}>
    <label className="field">{t.name}<input autoFocus required maxLength={100} value={value.name} placeholder={t.serviceNamePlaceholder} onChange={event => change('name', event.target.value)} /></label>
    <label className="field">{t.node}<select required value={value.node_id} onChange={event => change('node_id', event.target.value)}><option value="" disabled>{t.selectNode}</option>{nodes.map(node => <option key={node.id} value={node.id}>{node.name}</option>)}</select></label>
    <div className="form-row"><label className="field grow">{t.host}<input required maxLength={253} value={value.remote_host} spellCheck={false} onChange={event => change('remote_host', event.target.value)} /></label><label className="field port-field">{t.port}<input required type="number" min={1} max={65535} step={1} value={port} onChange={event => setPort(event.target.value)} /></label></div><p className="hint remote-host-hint">{t.hostHint}</p>
    <div className="form-row"><label className="field protocol-field">{t.protocol}<select value={value.scheme} onChange={event => change('scheme', event.target.value)}><option value="http">HTTP</option><option value="https">HTTPS</option></select></label><label className="field grow">{t.path}<input maxLength={2048} value={value.path} spellCheck={false} onChange={event => change('path', event.target.value)} /></label></div><p className="hint remote-host-hint">{t.pathHint}</p>
    {error && <div className="form-error" role="alert"><Icon name="alert" size={16} />{error}</div>}
    <div className="modal-footer"><button type="button" className="button button-secondary" disabled={busy} onClick={onClose}>{t.cancel}</button><button className="button button-primary" disabled={busy}>{busy ? t.saving : service ? t.save : t.addService}</button></div>
  </form></Modal>;
}

function SSHGuide({ node, onClose, onTerminal, onChecked }: { node: Node; onClose: () => void; onTerminal: () => void; onChecked: () => void }) {
  const [check, setCheck] = useState<SSHCheck>(); const [busy, setBusy] = useState(true); const [error, setError] = useState('');
  const alive = useRef(true);
  const setupRequired = check && ['authentication_required', 'host_key_required', 'ssh_missing', 'ssh_config_error'].includes(check.code);
  const perform = useCallback(async () => {
    setBusy(true); setError('');
    try { const result = await api<SSHCheck>(`/api/nodes/${node.id}/check`, 'POST'); if (alive.current) { setCheck(result); onChecked(); } }
    catch (err) { if (alive.current) setError(errorText(err)); }
    finally { if (alive.current) setBusy(false); }
  }, [node.id, onChecked]);
  useEffect(() => { alive.current = true; void perform(); return () => { alive.current = false; }; }, [perform]);
  return <Modal title={t.setupTitle} description={t.setupSubtitle} onClose={onClose} wide>
    <div className="setup-node"><Icon name="server" size={19} /><strong>{node.name}</strong><code>{node.ssh_target}</code></div>
    <div className={`setup-result ${busy ? '' : check?.ok ? 'setup-success' : 'setup-warning'}`} role="status"><Icon name={busy ? 'refresh' : check?.ok ? 'check' : 'alert'} className={busy ? 'spin' : ''} size={21} /><div><strong>{busy ? t.checking : check?.ok ? t.connectedTitle : setupRequired ? t.setupRequired : t.connectionFailed}</strong><p>{busy ? t.checkingDescription : error || check?.message}</p></div></div>
    {!busy && check?.steps?.length ? <ol className="setup-steps">{check.steps.map((step, index) => <li key={index}><span className="step-number">{index + 1}</span><div><h3>{step.title}</h3><p>{step.description}</p>{step.command && <CopyCommand command={step.command} />}</div></li>)}</ol> : null}
    {!busy && check?.command && <CopyCommand command={check.command} />}
    <div className="modal-footer setup-footer"><button className="button button-secondary" onClick={onTerminal}><Icon name="terminal" size={16} />{t.openTerminal}</button><button className="button button-primary" disabled={busy} onClick={() => void perform()}><Icon name="refresh" size={16} className={busy ? 'spin' : ''} />{busy ? t.checkingLabel : t.checkAgain}</button></div>
  </Modal>;
}

type Editor = { type: 'node'; node?: Node } | { type: 'service'; service?: Service } | { type: 'delete-node'; node: Node } | { type: 'delete-service'; service: Service } | { type: 'setup'; node: Node };
type Notice = { text: string; error?: boolean; command?: string };

export default function App() {
  const [nodes, setNodes] = useState<Node[]>([]); const [services, setServices] = useState<Service[]>([]);
  const [metrics, setMetrics] = useState<Record<string, Metrics>>({}); const [session, setSession] = useState<Session>();
  const [loading, setLoading] = useState(true); const [refreshing, setRefreshing] = useState(false); const [connectionError, setConnectionError] = useState('');
  const [editor, setEditor] = useState<Editor>(); const [notice, setNotice] = useState<Notice>(); const [deleting, setDeleting] = useState(false); const [deleteError, setDeleteError] = useState('');
  const inFlight = useRef(false); const queuedRefresh = useRef(false); const alive = useRef(true);
  const refresh = useCallback(async () => {
    if (inFlight.current) { queuedRefresh.current = true; return; }
    inFlight.current = true; setRefreshing(true);
    try {
      const [sessionResult, nodeResult, serviceResult] = await Promise.allSettled([getSession(true), api<{ nodes: Node[] }>('/api/nodes'), api<{ services: Service[] }>('/api/services')]);
      if (!alive.current) return;
      if (sessionResult.status === 'fulfilled') setSession(sessionResult.value);
      if (serviceResult.status === 'fulfilled') setServices(serviceResult.value.services || []);
      if (nodeResult.status === 'rejected') throw nodeResult.reason;
      const currentNodes = nodeResult.value.nodes || []; setNodes(currentNodes);
      setConnectionError(serviceResult.status === 'rejected' ? errorText(serviceResult.reason) : sessionResult.status === 'rejected' ? errorText(sessionResult.reason) : '');
      await Promise.all(currentNodes.map(async node => {
        try { const result = await api<Metrics>(`/api/nodes/${node.id}/metrics`); if (alive.current) setMetrics(current => ({ ...current, [node.id]: result })); }
        catch (err) { if (alive.current) setMetrics(current => ({ ...current, [node.id]: { ...(current[node.id] || { node_id: node.id, cpu: null, gpu: null, memory: null, disks: [] }), status: 'error', stale: Boolean(current[node.id]?.last_success), errors: { ssh: errorText(err) } } })); }
      }));
    } catch (err) { if (alive.current) setConnectionError(errorText(err)); }
    finally {
      inFlight.current = false;
      if (alive.current) {
        setLoading(false); setRefreshing(false);
        if (queuedRefresh.current) { queuedRefresh.current = false; void refresh(); }
      }
    }
  }, []);
  useEffect(() => {
    alive.current = true; let timer: ReturnType<typeof setTimeout>; let cancelled = false;
    const tick = async () => { await refresh(); if (!cancelled) timer = setTimeout(tick, 10_000); };
    void tick();
    return () => { cancelled = true; alive.current = false; clearTimeout(timer); };
  }, [refresh]);
  useEffect(() => { if (!notice || notice.command || notice.error) return; const timer = setTimeout(() => setNotice(undefined), 4500); return () => clearTimeout(timer); }, [notice]);
  const afterCheck = useCallback(() => { void refresh(); }, [refresh]);
  async function terminal(node: Node) {
    try { const result = await api<TerminalResult>(`/api/nodes/${node.id}/terminal`, 'POST'); setNotice(result.ok ? { text: t.terminalOpened } : { text: result.error || t.terminalFailed, error: true, command: result.command || `ssh ${node.ssh_target}` }); }
    catch (err) { setNotice({ text: errorText(err), error: true, command: `ssh ${node.ssh_target}` }); }
  }
  function openEditor(value: Editor) { setDeleteError(''); setEditor(value); }
  async function saveNode(value: NodeInput) {
    const existing = editor?.type === 'node' ? editor.node : undefined;
    await api<Node>(existing ? `/api/nodes/${existing.id}` : '/api/nodes', existing ? 'PATCH' : 'POST', value);
    setEditor(undefined); setNotice({ text: t.nodeSaved }); await refresh();
  }
  async function saveService(value: ServiceInput) {
    const existing = editor?.type === 'service' ? editor.service : undefined;
    await api<Service>(existing ? `/api/services/${existing.id}` : '/api/services', existing ? 'PATCH' : 'POST', value);
    setEditor(undefined); setNotice({ text: t.serviceSaved }); await refresh();
  }
  async function remove() {
    if (editor?.type !== 'delete-node' && editor?.type !== 'delete-service') return;
    setDeleting(true); setDeleteError('');
    try { await api(editor.type === 'delete-node' ? `/api/nodes/${editor.node.id}` : `/api/services/${editor.service.id}`, 'DELETE'); setEditor(undefined); setNotice({ text: t.deleted }); await refresh(); }
    catch (err) { setDeleteError(errorText(err)); } finally { setDeleting(false); }
  }
  const online = nodes.filter(node => metrics[node.id]?.status === 'online').length;
  const deleteServices = editor?.type === 'delete-node' ? services.filter(service => service.node_id === editor.node.id) : [];
  return <div className="app-shell">
    <aside className="sidebar"><a className="brand" href="#"><span className="brand-mark"><Icon name="plus" size={27} /></span>{t.app}</a><div className="sidebar-label">{t.workspace}</div><a href="#" className="nav-item active"><Icon name="grid" size={18} />{t.overview}</a><a href="#nodes" className="nav-item"><Icon name="server" size={18} />{t.nodes}<span>{nodes.length}</span></a><a href="#services" className="nav-item"><Icon name="link" size={18} />{t.services}<span>{services.length}</span></a><div className="sidebar-bottom"><div className="local-icon"><Icon name="shield" size={17} /></div><div><strong>{t.local}</strong><p>{t.localDetail}</p></div></div><div className="sidebar-version">{session?.version ? `v${session.version.replace(/^v/, '')}` : t.app}</div></aside>
    <div className="main-shell"><header className="topbar"><span className="breadcrumb">{t.workspace}<Icon name="chevron" size={13} /><strong>{t.overview}</strong></span><span className="ssh-badge"><Icon name="shield" size={14} />{t.encrypted}</span></header>
      <main><div className="page-heading"><div className="heading-copy"><p className="eyebrow">{t.tagline}</p><h1>{t.title}</h1><p>{t.subtitle}</p></div><button className="button button-secondary refresh-button" onClick={() => void refresh()} disabled={refreshing}><Icon name="refresh" className={refreshing ? 'spin' : ''} size={16} />{refreshing ? t.refreshing : t.refresh}</button></div>
        {connectionError && <div className="connection-error" role="alert"><Icon name="alert" size={18} /><span>{connectionError}</span><button className="button button-quiet" onClick={() => void refresh()}>{t.refresh}</button></div>}
        {loading ? <div className="workspace-loading" role="status"><Icon name="refresh" className="spin" size={24} /><span>{t.loading}</span></div> : <>
          <section id="nodes" className="section"><div className="section-heading"><div className="section-title"><div><h2>{t.nodes}<span className="count">{nodes.length}</span></h2><p>{t.nodesDescription}</p></div>{nodes.length > 0 && <span className="section-stat"><i className={online ? 'dot-online' : ''} />{online}/{nodes.length} {t.connected}</span>}</div><button className="button button-primary" onClick={() => openEditor({ type: 'node' })}><Icon name="plus" size={17} />{t.addNode}</button></div>
            {nodes.length ? <div className="nodes-grid">{nodes.map(node => <NodeCard key={node.id} node={node} metrics={metrics[node.id]} onEdit={() => openEditor({ type: 'node', node })} onDelete={() => openEditor({ type: 'delete-node', node })} onSetup={() => openEditor({ type: 'setup', node })} onTerminal={() => void terminal(node)} />)}</div> : <div className="empty-state"><div className="empty-art"><Icon name="server" size={34} /><span>+</span></div><h3>{t.noNodesTitle}</h3><p>{t.noNodesDescription}</p><button className="button button-secondary" onClick={() => openEditor({ type: 'node' })}><Icon name="plus" size={16} />{t.addNode}</button></div>}
          </section>
          <section id="services" className="section services-section"><div className="section-heading"><div className="section-title"><div><h2>{t.services}<span className="count">{services.length}</span></h2><p>{t.servicesDescription}</p></div></div><button className="button button-secondary" disabled={!nodes.length} title={!nodes.length ? t.addNodeFirst : undefined} onClick={() => openEditor({ type: 'service' })}><Icon name="plus" size={17} />{t.addService}</button></div>
            {services.length ? <div className="services-table"><div className="service-table-head"><span>{t.name}</span><span>{t.node}</span><span>{t.endpoint}</span><span className="sr-only">{t.actions}</span></div>{services.map(service => {
              const serviceNode = nodes.find(node => node.id === service.node_id);
              return <article className="service-row" key={service.id}><div className="service-identity"><span className="service-icon"><Icon name="link" size={18} /></span><h3>{service.name}</h3></div><span className="service-node"><i className={metrics[service.node_id]?.status === 'online' ? 'dot-online' : ''} />{serviceNode?.name || t.unknownNode}</span><code className="service-endpoint" title={endpoint(service)}>{endpoint(service)}</code><div className="service-actions"><button className="icon-button" title={t.edit} aria-label={`${t.edit} ${service.name}`} onClick={() => openEditor({ type: 'service', service })}><Icon name="edit" size={15} /></button><button className="icon-button danger-hover" title={t.remove} aria-label={`${t.remove} ${service.name}`} onClick={() => openEditor({ type: 'delete-service', service })}><Icon name="trash" size={15} /></button>{session ? <a className="button button-open" href={`/services/${encodeURIComponent(service.id)}/open?token=${encodeURIComponent(session.launch_token)}`} target="_blank" rel="noopener noreferrer">{t.open}<Icon name="arrow" size={15} /></a> : <button className="button button-open" disabled>{t.open}<Icon name="arrow" size={15} /></button>}</div></article>;
            })}<div className="table-footnote"><Icon name="shield" size={13} />{t.tunnelNote}</div></div> : <div className="empty-state empty-services"><div className="empty-art"><Icon name="link" size={32} /></div><h3>{t.noServicesTitle}</h3><p>{nodes.length ? t.noServicesDescription : t.addNodeFirst}</p>{nodes.length > 0 && <button className="button button-secondary" onClick={() => openEditor({ type: 'service' })}><Icon name="plus" size={16} />{t.addService}</button>}</div>}
          </section>
        </>}
        <footer className="page-footer"><span><Icon name="shield" size={13} />{t.footer}</span><span>{nodes.length} {t.countNodes}<i />{services.length} {t.countServices}</span></footer>
      </main>
    </div>
    {editor?.type === 'node' && <NodeForm node={editor.node} onClose={() => setEditor(undefined)} onSave={saveNode} />}
    {editor?.type === 'service' && <ServiceForm service={editor.service} nodes={nodes} onClose={() => setEditor(undefined)} onSave={saveService} />}
    {editor?.type === 'setup' && <SSHGuide node={editor.node} onClose={() => setEditor(undefined)} onTerminal={() => void terminal(editor.node)} onChecked={afterCheck} />}
    {(editor?.type === 'delete-node' || editor?.type === 'delete-service') && <Modal title={editor.type === 'delete-node' ? t.deleteNodeTitle : t.deleteServiceTitle} onClose={() => !deleting && setEditor(undefined)}><div className="delete-content"><div className="delete-target"><Icon name={editor.type === 'delete-node' ? 'server' : 'link'} /><strong>{editor.type === 'delete-node' ? editor.node.name : editor.service.name}</strong></div><p>{editor.type === 'delete-node' ? deleteServices.length ? t.deleteNodeDescription : t.deleteNodeEmptyDescription : t.deleteServiceDescription}</p>{deleteServices.length > 0 && <ul className="delete-services">{deleteServices.map(service => <li key={service.id}><Icon name="link" size={14} />{service.name}</li>)}</ul>}<p className="hint">{t.remoteUnaffected}</p>{deleteError && <div role="alert" className="form-error">{deleteError}</div>}</div><div className="modal-footer"><button className="button button-secondary" disabled={deleting} onClick={() => setEditor(undefined)}>{t.cancel}</button><button className="button button-danger" disabled={deleting} onClick={() => void remove()}>{deleting ? t.deleting : t.remove}</button></div></Modal>}
    {notice && <div className={`toast ${notice.error ? 'toast-error' : ''}`} role={notice.error ? 'alert' : 'status'}><Icon name={notice.error ? 'alert' : 'check'} size={19} /><div><p>{notice.text}</p>{notice.command && <CopyCommand command={notice.command} />}</div><button className="icon-button" aria-label={t.dismiss} onClick={() => setNotice(undefined)}><Icon name="close" size={16} /></button></div>}
  </div>;
}
