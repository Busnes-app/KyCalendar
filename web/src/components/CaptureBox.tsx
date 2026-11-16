import { useEffect, useMemo, useState } from 'react';
import { Modal } from './ui';
import { createCalendar, listCalendars, type CalendarInfo } from '../calendarApi';
import { MAX_IMAGE_BYTES, createTask, inbox } from '../tasksApi';
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
  const [image, setImage] = useState<{ name: string; url: string } | null>(null);
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
      else if (image) await createTask(inboxId, { title: t, image: { name: image.name, data: image.url.slice(image.url.indexOf(',') + 1) } });
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

  // A pasted image makes an Inbox item with it attached; it stays in KyCalendar, no OCR.
  function paste(e: React.ClipboardEvent) {
    const file = [...e.clipboardData.files].find((f) => /^image\/(png|jpeg|webp)$/.test(f.type));
    if (!file || mode === 'list') return;
    e.preventDefault();
    if (file.size > MAX_IMAGE_BYTES) { setError('Images can be up to 512 KiB.'); return; }
    const now = new Date();
    const stamp = `${String(now.getHours()).padStart(2, '0')}:${String(now.getMinutes()).padStart(2, '0')}`;
    const reader = new FileReader();
    reader.onload = () => {
      setImage({ name: `Screenshot ${stamp}.${file.type.split('/')[1].replace('jpeg', 'jpg')}`, url: String(reader.result) });
      if (!text.trim()) setText(`Screenshot ${stamp}`);
      setError('');
    };
    reader.readAsDataURL(file);
  }

  return (
    <Modal title="Add" onClose={onClose}>
      <form className="kc-form tk-capture" onSubmit={save} onPaste={paste}>
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
          {mode === 'task' && text.trim() && !image && (
            <>
              <span className="tk-chip tk-chip-title">{parsed.title}</span>
              {parsed.chips.map((c) => <span key={c} className="tk-chip">{c}</span>)}
              {!parsed.list && <span className="tk-chip tk-chip-quiet">In Inbox</span>}
            </>
          )}
          {mode === 'inbox' && text.trim() && <span className="tk-chip tk-chip-quiet">In Inbox, no date</span>}
        </div>
        {image && mode !== 'list' && (
          <figure className="tk-image">
            <img src={image.url} alt={`Pasted image ${image.name}`} />
            <figcaption>{image.name} · goes to Inbox <button type="button" className="btn-ghost btn-sm" onClick={() => setImage(null)}>Remove</button></figcaption>
          </figure>
        )}
        {!image && mode !== 'list' && <p className="kc-hint">Paste an image to attach it.</p>}
        {error && <p role="alert">{error}</p>}
        <div className="modal-actions">
          <button type="button" className="btn-secondary" onClick={onClose}>Cancel</button>
          <button type="submit" disabled={!text.trim() || busy || (mode !== 'list' && !inboxId)}>Add</button>
        </div>
      </form>
    </Modal>
  );
}
