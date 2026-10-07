import { useEffect, useRef } from 'react';

export const MOVE_ALL_WARNING = 'Moving all events removes changes made to single events, including deleted ones.';

export function ScopeDialog({ onChoose }: { onChoose: (scope: 'this' | 'all' | null) => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const first = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const d = dialog.current!;
    const prev = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    d.showModal();
    first.current?.focus();
    return () => {
      d.close();
      prev?.focus();
    };
  }, []);

  return (
    <dialog ref={dialog} aria-labelledby="kc-scope-title" className="kc-dialog" onCancel={(e) => { e.preventDefault(); onChoose(null); }}>
      <h2 id="kc-scope-title">Change a repeating event</h2>
      <p>{MOVE_ALL_WARNING}</p>
      <button type="button" ref={first} onClick={() => onChoose('this')}>This event</button>
      <button type="button" onClick={() => onChoose('all')}>All events</button>
      <button type="button" onClick={() => onChoose(null)}>Cancel</button>
    </dialog>
  );
}
