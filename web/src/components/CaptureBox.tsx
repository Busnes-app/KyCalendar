import { useEffect, useMemo, useState } from 'react';
import { Modal } from './ui';
import { createCalendar, listCalendars, type CalendarInfo } from '../calendarApi';
import { createTask, inbox } from '../tasksApi';
import { parseQuick } from '../taskParse';

export const TASKS_CHANGED = 'kycalendar:tasks-changed';
type Mode = 'task' | 'inbox' | 'list';

// CaptureBox is the one box for everything, from any screen: a task (parsed as quick add), an
// Inbox item (the text as typed, no date) or a new task list.
export function CaptureBox({ onClose }: { onClose: () => void }) {
  const [mode, setMode] = useState<Mode>('task');
  const [text, setText] = useState('');
  const [lists, setLists] = useState<CalendarInfo[]>([]);
  const [inboxId, setInboxId] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    void Promise.all([inbox(), listCalendars()]).then(([box, cals]) => {
      setInboxId(box);
      setLists(cals.filter((c) => c.components.includes('VTODO') && c.role !== 'reader' && !c.subscription));
    }, () => setError('Task lists could not be loaded.'));
  }, []);
  const refs = useMemo(() => lists.map((c) => ({ id: c.id, name: c.id === inboxId ? 'Inbox' : c.name })), [lists, inboxId]);
  const parsed = useMemo(() => parseQuick(text, refs), [text, refs]);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    const t = text.trim();
    if (!t || busy) return;
    setBusy(true);
    setError('');
    try {
      if (mode === 'list') await createCalendar({ name: t, components: ['VTODO'] });
      else if (mode === 'inbox') await createTask(inboxId, { title: t });
      else await createTask(parsed.list?.id ?? inboxId, { title: parsed.title, due: parsed.due, priority: parsed.priority, tags: parsed.tags.length ? parsed.tags : undefined, rrule: parsed.rrule });
      window.dispatchEvent(new Event(TASKS_CHANGED));
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not save');
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal title="Add" onClose={onClose}>
      <form className="kc-form tk-capture" onSubmit={save}>
        <fieldset className="tk-segmented">
          <legend className="sr-only">What to add</legend>
          {([['task', 'Task'], ['inbox', 'Inbox item'], ['list', 'List']] as const).map(([m, label]) => (
            <label key={m} data-checked={mode === m || undefined}>
              <input type="radio" name="capture-mode" checked={mode === m} onChange={() => setMode(m)} />
              {label}
            </label>
          ))}
        </fieldset>
        <label htmlFor="capture-text">{mode === 'list' ? 'List name' : mode === 'inbox' ? 'Title' : 'Task'}</label>
        <input id="capture-text" autoFocus value={text} maxLength={mode === 'list' ? 255 : 1000} onChange={(e) => setText(e.target.value)}
          placeholder={mode === 'task' ? 'Call Ana tomorrow 3pm !high #home @Errands' : ''} aria-describedby="capture-preview" />
        <div id="capture-preview" className="tk-chips" aria-live="polite">
          {mode === 'task' && text.trim() && (
            <>
              <span className="tk-chip tk-chip-title">{parsed.title}</span>
              {parsed.chips.map((c) => <span key={c} className="tk-chip">{c}</span>)}
              {!parsed.list && <span className="tk-chip tk-chip-quiet">In Inbox</span>}
            </>
          )}
          {mode === 'inbox' && text.trim() && <span className="tk-chip tk-chip-quiet">In Inbox, no date</span>}
        </div>
        {error && <p role="alert">{error}</p>}
        <div className="modal-actions">
          <button type="button" className="btn-secondary" onClick={onClose}>Cancel</button>
          <button type="submit" disabled={!text.trim() || busy || (mode !== 'list' && !inboxId)}>Add</button>
        </div>
      </form>
    </Modal>
  );
}
