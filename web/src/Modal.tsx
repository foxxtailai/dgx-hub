import { useEffect, useId, useRef } from 'react';
import type { ReactNode } from 'react';
import { t } from './strings';

export function Modal({ title, description, onClose, children, wide = false }: { title: ReactNode; description?: string; onClose: () => void; children: ReactNode; wide?: boolean }) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleID = useId(); const descriptionID = useId();
  useEffect(() => { const dialog = ref.current!; dialog.showModal(); return () => dialog.close(); }, []);
  return <dialog ref={ref} className={`modal ${wide ? 'modal-wide' : ''}`} aria-labelledby={titleID} aria-describedby={description ? descriptionID : undefined} onCancel={event => { event.preventDefault(); onClose(); }} onClick={event => {
    if (event.target !== event.currentTarget) return;
    const rect = event.currentTarget.getBoundingClientRect();
    if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) onClose();
  }}>
    <div className="modal-header"><div><h2 id={titleID}>{title}</h2>{description && <p id={descriptionID}>{description}</p>}</div><button className="icon-button" onClick={onClose} aria-label={t.close}><svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" aria-hidden="true"><path d="m6 6 12 12M6 18 18 6" /></svg></button></div>
    {children}
  </dialog>;
}
