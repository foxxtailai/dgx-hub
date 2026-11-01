import { useCallback, useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { api } from './api';
import { Modal } from './Modal';
import { t } from './strings';
import type { Action, ActionInput, ActionRun, ActionStep, Node } from './types';

const errorText = (error: unknown) => error instanceof Error ? error.message : t.requestFailed;
const freshStep = (): ActionStep => ({ name: '', command: '', timeout_seconds: 60, on_error: 'stop' });
const elapsed = (duration: number, started?: string, running = false) => `${((running && started ? Math.max(0, Date.now() - new Date(started).getTime()) : duration) / 1000).toFixed(1)} s`;
const statusLabel = (status: string) => t.actionStatuses[status] || status;

function ActionForm({ action, node, nodes, onSave, onBack }: { action?: Action; node: Node; nodes: Node[]; onSave: (value: ActionInput) => Promise<void>; onBack: () => void }) {
  const [value, setValue] = useState<ActionInput>(() => action ? { ...action, steps: action.steps.map(step => ({ ...step })), node_ids: [...action.node_ids] } : { name: '', description: '', steps: [freshStep()], node_ids: [node.id], confirm_before_run: false });
  const [busy, setBusy] = useState(false); const [error, setError] = useState('');
  function changeStep(index: number, patch: Partial<ActionStep>) { setValue(current => ({ ...current, steps: current.steps.map((step, i) => i === index ? { ...step, ...patch } : step) })); }
  function moveStep(index: number, offset: number) {
    setValue(current => { const steps = [...current.steps]; [steps[index], steps[index + offset]] = [steps[index + offset], steps[index]]; return { ...current, steps }; });
  }
  async function submit(event: FormEvent) {
    event.preventDefault(); setError('');
    const cleaned = { ...value, name: value.name.trim(), description: value.description.trim(), steps: value.steps.map(step => ({ ...step, name: step.name.trim() })) };
    if (!cleaned.name || !cleaned.steps.length || cleaned.steps.length > 50 || cleaned.steps.some(step => !step.command.trim() || step.command.includes('\0') || !Number.isInteger(step.timeout_seconds) || step.timeout_seconds < 1 || step.timeout_seconds > 86400)) return setError(t.invalidAction);
    setBusy(true);
    try { await onSave(cleaned); } catch (err) { setError(errorText(err)); setBusy(false); }
  }
  return <form className="action-form" onSubmit={submit}>
    <fieldset disabled={busy} className="action-fieldset">
      <label className="field">{t.name}<input autoFocus required maxLength={100} value={value.name} placeholder={t.actionNamePlaceholder} onChange={event => setValue(current => ({ ...current, name: event.target.value }))} /></label>
      <label className="field">{t.description}<span className="optional">{t.optional}</span><textarea maxLength={2000} rows={2} value={value.description} placeholder={t.actionDescriptionPlaceholder} onChange={event => setValue(current => ({ ...current, description: event.target.value }))} /></label>
      <div className="action-section-heading"><h3>{t.actionSteps}</h3><span>{value.steps.length}/50</span></div>
      <p className="hint action-context-note">{t.independentSteps}</p>
      <div className="action-steps">{value.steps.map((step, index) => <section className="action-step-editor" key={index} aria-label={`${t.step} ${index + 1}`}>
        <div className="action-step-toolbar"><strong>{t.step} {index + 1}</strong><div><button type="button" className="icon-button" disabled={index === 0} title={t.moveUp} aria-label={`${t.moveUp} ${t.step} ${index + 1}`} onClick={() => moveStep(index, -1)}>↑</button><button type="button" className="icon-button" disabled={index === value.steps.length - 1} title={t.moveDown} aria-label={`${t.moveDown} ${t.step} ${index + 1}`} onClick={() => moveStep(index, 1)}>↓</button><button type="button" className="icon-button danger-hover" disabled={value.steps.length === 1} title={t.removeStep} aria-label={`${t.removeStep} ${index + 1}`} onClick={() => setValue(current => ({ ...current, steps: current.steps.filter((_, i) => i !== index) }))}>×</button></div></div>
        <label className="field">{t.stepName}<span className="optional">{t.optional}</span><input maxLength={100} value={step.name} onChange={event => changeStep(index, { name: event.target.value })} /></label>
        <label className="field">{t.commandLabel}<textarea required className="action-command-input" rows={3} maxLength={16384} spellCheck={false} autoCapitalize="none" value={step.command} onChange={event => changeStep(index, { command: event.target.value })} /></label>
        <div className="form-row"><label className="field action-timeout">{t.timeoutSeconds}<input required type="number" min={1} max={86400} step={1} value={Number.isNaN(step.timeout_seconds) ? '' : step.timeout_seconds} onChange={event => changeStep(index, { timeout_seconds: event.target.value === '' ? Number.NaN : Number(event.target.value) })} /></label><label className="field grow">{t.onError}<select value={step.on_error} onChange={event => changeStep(index, { on_error: event.target.value as ActionStep['on_error'] })}><option value="stop">{t.stopSequence}</option><option value="continue">{t.continueSequence}</option></select></label></div>
      </section>)}</div>
      <button type="button" className="button button-secondary" disabled={value.steps.length >= 50} onClick={() => setValue(current => ({ ...current, steps: [...current.steps, freshStep()] }))}>+ {t.addStep}</button>
      <div className="action-section-heading action-assignment-heading"><h3>{t.assignedNodes}</h3></div>
      <div className="action-node-picker">{nodes.map(item => <label className="action-checkbox" key={item.id}><input type="checkbox" checked={value.node_ids.includes(item.id)} onChange={event => setValue(current => ({ ...current, node_ids: event.target.checked ? [...current.node_ids, item.id] : current.node_ids.filter(id => id !== item.id) }))} /><span>{item.name}<code>{item.ssh_target}</code></span></label>)}</div>
      {!value.node_ids.length && <p className="hint">{t.noAssignedNodes}</p>}
      <label className="action-checkbox action-confirm-toggle"><input type="checkbox" checked={value.confirm_before_run} onChange={event => setValue(current => ({ ...current, confirm_before_run: event.target.checked }))} /><span>{t.confirmBeforeRun}</span></label>
    </fieldset>
    {error && <div className="form-error" role="alert">{error}</div>}
    <div className="modal-footer"><button type="button" className="button button-secondary" disabled={busy} onClick={onBack}>{t.cancel}</button><button className="button button-primary" disabled={busy}>{busy ? t.saving : action ? t.save : t.newAction}</button></div>
  </form>;
}

function runLogs(run: ActionRun) {
  return [`${run.action_name} — ${run.node_name}`, `${statusLabel(run.status)} · ${new Date(run.started_at).toLocaleString()} · ${elapsed(run.duration_ms, run.started_at, run.status === 'running')}`, run.error,
    ...run.steps.map((step, index) => [`${t.step} ${index + 1}${step.name ? `: ${step.name}` : ''} — ${statusLabel(step.status)}`, step.command,
      `${t.duration}: ${elapsed(step.duration_ms, step.started_at, step.status === 'running')} · ${t.exitCode}: ${step.exit_code ?? '—'}`, step.error,
      `${t.stdout}:\n${step.stdout}${step.stdout_truncated ? `\n[${t.outputTruncated}]` : ''}`, `${t.stderr}:\n${step.stderr}${step.stderr_truncated ? `\n[${t.outputTruncated}]` : ''}`].filter(Boolean).join('\n'))].filter(Boolean).join('\n\n');
}

function RunDetail({ run, busy, onCancel }: { run: ActionRun; busy: boolean; onCancel: () => void }) {
  const [copyState, setCopyState] = useState('');
  const currentStep = run.steps.findIndex(step => step.status === 'running');
  return <div className="action-run-detail">
    <div className="action-run-heading"><div><h3>{run.action_name}</h3><p>{run.node_name}</p></div><span className={`action-status action-status-${run.status}`} role="status">{statusLabel(run.status)}</span></div>
    <dl className="action-run-metadata"><div><dt>{t.startedAt}</dt><dd>{new Date(run.started_at).toLocaleString()}</dd></div><div><dt>{t.duration}</dt><dd>{elapsed(run.duration_ms, run.started_at, run.status === 'running')}</dd></div>{run.finished_at && <div><dt>{t.finishedAt}</dt><dd>{new Date(run.finished_at).toLocaleString()}</dd></div>}</dl>
    <div className="action-run-controls"><button className="button button-secondary" onClick={async () => { try { await navigator.clipboard.writeText(runLogs(run)); setCopyState(t.logsCopied); } catch { setCopyState(t.copyLogsFailed); } }}>{t.copyLogs}</button>{run.status === 'running' && <button className="button button-danger" disabled={busy} onClick={onCancel}>{busy ? t.cancelling : t.cancelRun}</button>}</div>
    {copyState && <p className="hint action-copy-feedback" role="status">{copyState}</p>}
    <p className="hint action-context-note">{t.cancelLimit}</p>
    {run.error && <div className="form-error" role="alert">{run.error}</div>}
    {run.status === 'running' && <p className="action-current-step" role="status">{currentStep >= 0 ? `${t.currentStep}: ${currentStep + 1} / ${run.steps.length}` : `${run.steps.filter(step => ['success', 'failed'].includes(step.status)).length} / ${run.steps.length} ${t.completedSteps}`}</p>}
    <div className="action-step-results">{run.steps.map((step, index) => <section className={`action-step-result action-result-${step.status}`} key={index}>
      <div className="action-step-toolbar"><h4>{t.step} {index + 1}{step.name && ` · ${step.name}`}</h4><span className={`action-status action-status-${step.status}`}>{statusLabel(step.status)}</span></div>
      <pre className="action-command-preview">{step.command}</pre>
      <div className="action-step-metadata"><span>{t.duration}: {elapsed(step.duration_ms, step.started_at, step.status === 'running')}</span><span>{t.exitCode}: {step.exit_code ?? '—'}</span><span>{t.timeoutSeconds}: {step.timeout_seconds}</span></div>
      {step.error && <p className="field-error">{step.error}</p>}
      <div className="action-output"><h5>{t.stdout}</h5><pre tabIndex={0}>{step.stdout || t.noOutput}</pre>{step.stdout_truncated && <p className="field-error">{t.outputTruncated}</p>}</div>
      <div className="action-output action-stderr"><h5>{t.stderr}</h5><pre tabIndex={0}>{step.stderr || t.noOutput}</pre>{step.stderr_truncated && <p className="field-error">{t.outputTruncated}</p>}</div>
    </section>)}</div>
  </div>;
}

type View = { kind: 'list' } | { kind: 'edit'; action?: Action } | { kind: 'history'; action?: Action } | { kind: 'run'; id: string } | { kind: 'confirm' | 'delete'; action: Action };

export function NodeActions({ node, nodes, online, onClose }: { node: Node; nodes: Node[]; online: boolean; onClose: () => void }) {
  const [actions, setActions] = useState<Action[]>([]); const [runs, setRuns] = useState<ActionRun[]>([]);
  const [view, setView] = useState<View>({ kind: 'list' }); const [loading, setLoading] = useState(true); const [error, setError] = useState(''); const [busy, setBusy] = useState(false);
  const [detailID, setDetailID] = useState('');
  const [detailRevision, setDetailRevision] = useState(0);
  const alive = useRef(true); const operation = useRef(false);
  const navigate = (next: View) => { setError(''); setView(next); };
  const updateRun = useCallback((run: ActionRun) => setRuns(current => [run, ...current.filter(item => item.id !== run.id)].sort((a, b) => b.started_at.localeCompare(a.started_at)).slice(0, 100)), []);
  const load = useCallback(async () => {
    try {
      const [catalogue, history] = await Promise.all([api<{ actions: Action[] }>('/api/actions'), api<{ runs: ActionRun[] }>(`/api/action-runs?node_id=${encodeURIComponent(node.id)}`)]);
      if (alive.current) {
        setActions(catalogue.actions || []); setRuns(history.runs || []); setError('');
        // History contains summaries. Reload the selected run's output after a refresh.
        setDetailID(''); setDetailRevision(current => current + 1);
      }
    } catch (err) { if (alive.current) setError(errorText(err)); }
    finally { if (alive.current) setLoading(false); }
  }, [node.id]);
  useEffect(() => { alive.current = true; void load(); return () => { alive.current = false; }; }, [load]);
  const requestedDetailID = view.kind === 'run' ? view.id : '';
  useEffect(() => {
    if (!requestedDetailID) return;
    let cancelled = false;
    void api<ActionRun>(`/api/action-runs/${requestedDetailID}`).then(run => { if (!cancelled) { updateRun(run); setDetailID(run.id); } }).catch(err => { if (!cancelled) setError(errorText(err)); });
    return () => { cancelled = true; };
  }, [requestedDetailID, detailRevision, updateRun]);
  const activeIDs = runs.filter(run => run.status === 'running').map(run => run.id).sort().join(',');
  useEffect(() => {
    if (!activeIDs) return;
    let cancelled = false; let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      const results = await Promise.allSettled(activeIDs.split(',').map(id => api<ActionRun>(`/api/action-runs/${id}`)));
      if (cancelled) return;
      results.forEach(result => { if (result.status === 'fulfilled') updateRun(result.value); else setError(errorText(result.reason)); });
      timer = setTimeout(poll, 500);
    };
    timer = setTimeout(poll, 500);
    return () => { cancelled = true; clearTimeout(timer); };
  }, [activeIDs, updateRun]);
  const activeRun = runs.find(run => run.status === 'running');
  const assigned = actions.filter(action => action.node_ids.includes(node.id));
  const available = actions.filter(action => !action.node_ids.includes(node.id));
  const selectedRun = view.kind === 'run' ? runs.find(run => run.id === view.id) : undefined;
  const close = () => { if (!operation.current) onClose(); };
  async function mutate(work: () => Promise<void>) {
    if (operation.current) return;
    operation.current = true; setBusy(true); setError('');
    try { await work(); } catch (err) { if (alive.current) setError(errorText(err)); }
    finally { operation.current = false; if (alive.current) setBusy(false); }
  }
  async function start(action: Action, confirmed: boolean) {
    await mutate(async () => {
      const run = await api<ActionRun>(`/api/nodes/${node.id}/actions/${action.id}/runs`, 'POST', { confirmed });
      if (alive.current) { updateRun(run); setView({ kind: 'run', id: run.id }); }
    });
  }
  function requestStart(action: Action) {
    if (!online || activeRun || operation.current) return;
    if (action.confirm_before_run) navigate({ kind: 'confirm', action }); else void start(action, false);
  }
  async function save(value: ActionInput) {
    if (operation.current) return;
    operation.current = true; setBusy(true);
    try {
      const existing = view.kind === 'edit' ? view.action : undefined;
      const saved = await api<Action>(existing ? `/api/actions/${existing.id}` : '/api/actions', existing ? 'PUT' : 'POST', value);
      if (alive.current) { setActions(current => [...current.filter(action => action.id !== saved.id), saved].sort((a, b) => a.name.localeCompare(b.name))); navigate({ kind: 'list' }); }
    } finally { operation.current = false; if (alive.current) setBusy(false); }
  }
  const title = view.kind === 'edit' ? view.action ? t.editAction : t.newAction : view.kind === 'confirm' ? t.confirmRun : view.kind === 'delete' ? t.deleteAction : view.kind === 'history' ? `${t.history}${view.action ? ` · ${view.action.name}` : ''}` : view.kind === 'run' ? t.runDetails : `${node.name} · ${t.actions}`;
  const description = view.kind === 'edit' ? t.actionFormDescription : view.kind === 'confirm' ? t.confirmRunDescription : view.kind === 'history' ? t.actionHistoryDescription : view.kind === 'list' ? t.nodeActionsDescription : undefined;
  return <Modal title={title} description={description} onClose={close} wide>
    {error && <div className="form-error action-panel-error" role="alert">{error}<button className="button button-quiet" disabled={busy} onClick={() => void load()}>{t.refresh}</button></div>}
    {loading ? <p className="hint" role="status">{t.statusLoading}…</p> : <>
      {view.kind === 'list' && <>
        <div className="action-panel-toolbar"><button className="button button-primary" disabled={busy} onClick={() => navigate({ kind: 'edit' })}>+ {t.newAction}</button><button className="button button-secondary" onClick={() => navigate({ kind: 'history' })}>{t.allHistory}</button></div>
        {!online && <p className="action-node-warning" role="status">{t.offlineAction}</p>}
        {activeRun && <div className="action-active-run"><div><strong>{activeRun.action_name}</strong><span>{t.nodeActionBusy}</span></div><button className="button button-secondary" onClick={() => navigate({ kind: 'run', id: activeRun.id })}>{t.viewRun}</button></div>}
        <div className="action-section-heading"><h3>{t.assignedActions}</h3><span>{assigned.length}</span></div>
        {!assigned.length && <div className="action-empty"><strong>{t.actionsEmpty}</strong><p>{t.catalogueEmpty}</p></div>}
        <div className="action-catalogue">{assigned.map(action => <article className="action-catalogue-item" key={action.id}><div><h3>{action.name}</h3>{action.description && <p>{action.description}</p>}<span>{action.steps.length} {t.actionSteps.toLowerCase()}</span></div><div className="action-item-controls"><button className="button button-primary" disabled={!online || !!activeRun || busy} title={!online ? t.offlineAction : activeRun ? t.nodeActionBusy : undefined} onClick={() => requestStart(action)}>{t.run}</button><button className="button button-secondary" disabled={busy} onClick={() => navigate({ kind: 'edit', action })}>{t.edit}</button><button className="button button-quiet" onClick={() => navigate({ kind: 'history', action })}>{t.history}</button><button className="button button-quiet danger-hover" disabled={busy} onClick={() => navigate({ kind: 'delete', action })}>{t.remove}</button></div></article>)}</div>
        {available.length > 0 && <><div className="action-section-heading"><h3>{t.otherActions}</h3><span>{available.length}</span></div><div className="action-catalogue">{available.map(action => <article className="action-catalogue-item" key={action.id}><div><h3>{action.name}</h3>{action.description && <p>{action.description}</p>}</div><div className="action-item-controls"><button className="button button-secondary" disabled={busy} onClick={() => void mutate(async () => { const saved = await api<Action>(`/api/actions/${action.id}`, 'PUT', { ...action, node_ids: [...action.node_ids, node.id] }); if (alive.current) setActions(current => current.map(item => item.id === saved.id ? saved : item)); })}>{t.assignAction}</button><button className="button button-quiet" disabled={busy} onClick={() => navigate({ kind: 'edit', action })}>{t.edit}</button><button className="button button-quiet danger-hover" disabled={busy} onClick={() => navigate({ kind: 'delete', action })}>{t.remove}</button></div></article>)}</div></>}
      </>}
      {view.kind === 'edit' && <ActionForm key={view.action?.id || 'new'} action={view.action} node={node} nodes={nodes} onSave={save} onBack={() => navigate({ kind: 'list' })} />}
      {view.kind === 'delete' && <><div className="delete-content"><div className="delete-target"><strong>{view.action.name}</strong></div><p>{t.deleteActionDescription}</p></div><div className="modal-footer"><button className="button button-secondary" disabled={busy} onClick={() => navigate({ kind: 'list' })}>{t.cancel}</button><button className="button button-danger" disabled={busy} onClick={() => void mutate(async () => { await api(`/api/actions/${view.action.id}`, 'DELETE'); if (alive.current) { setActions(current => current.filter(action => action.id !== view.action.id)); navigate({ kind: 'list' }); } })}>{busy ? t.deleting : t.remove}</button></div></>}
      {view.kind === 'confirm' && <><div className="delete-target"><strong>{view.action.name}</strong><span>→ {node.name}</span></div><ol className="action-confirm-steps">{view.action.steps.map((step, index) => <li key={index}>{step.name && <strong>{step.name}</strong>}<pre className="action-command-preview">{step.command}</pre></li>)}</ol>{!online && <p className="action-node-warning">{t.offlineAction}</p>}<div className="modal-footer"><button className="button button-secondary" disabled={busy} onClick={() => navigate({ kind: 'list' })}>{t.cancel}</button><button className="button button-primary" disabled={busy || !online || !!activeRun} onClick={() => void start(view.action, true)}>{busy ? t.runningAction : t.run}</button></div></>}
      {view.kind === 'history' && <><div className="action-panel-toolbar"><button className="button button-secondary" onClick={() => navigate({ kind: 'list' })}>← {t.backToActions}</button><button className="button button-quiet" onClick={() => void load()}>{t.refresh}</button></div><div className="action-history">{runs.filter(run => !view.action || run.action_id === view.action.id).map(run => <button className="action-history-row" key={run.id} onClick={() => navigate({ kind: 'run', id: run.id })}><span><strong>{run.action_name}</strong><time dateTime={run.started_at}>{new Date(run.started_at).toLocaleString()}</time></span><span className={`action-status action-status-${run.status}`}>{statusLabel(run.status)}</span><span>{elapsed(run.duration_ms, run.started_at, run.status === 'running')}</span></button>)}{!runs.some(run => !view.action || run.action_id === view.action.id) && <p className="action-empty">{t.historyEmpty}</p>}</div></>}
      {view.kind === 'run' && <><div className="action-panel-toolbar"><button className="button button-secondary" onClick={() => navigate({ kind: 'list' })}>← {t.backToActions}</button><button className="button button-quiet" onClick={() => navigate({ kind: 'history' })}>{t.history}</button></div>{selectedRun && detailID === selectedRun.id ? <RunDetail key={selectedRun.id} run={selectedRun} busy={busy} onCancel={() => void mutate(async () => { const run = await api<ActionRun>(`/api/action-runs/${selectedRun.id}/cancel`, 'POST'); if (alive.current) updateRun(run); })} /> : <p className="hint" role="status">{t.statusLoading}…</p>}</>}
    </>}
  </Modal>;
}
