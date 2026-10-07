import { useEffect, useRef, useState } from 'react';
import { ApiError, browserZone, createEvent, deleteEvent, updateEvent, type CalendarInfo, type EventInfo, type Freq } from '../calendarApi';
import { WEEKDAYS, bodyFromForm, formFromEvent, validZone, type FormState } from '../eventForm';
import { linkify } from '../linkify';

interface Props {
  calendars: CalendarInfo[];
  event?: EventInfo;
  initial: FormState;
  onDone: () => void;
  onClose: () => void;
}

const PRESETS: { value: Freq; label: string }[] = [
  { value: '', label: 'Does not repeat' },
  { value: 'daily', label: 'Daily' },
  { value: 'weekly', label: 'Weekly' },
  { value: 'monthly', label: 'Monthly' },
  { value: 'yearly', label: 'Yearly' },
];

function message(e: unknown, fallback: string): string {
  if (e instanceof ApiError && e.status === 412) return 'This event changed elsewhere. Close and reopen it to see the latest version.';
  return e instanceof Error && e.message ? e.message : fallback;
}

export function EventDialog({ calendars, event, initial, onDone, onClose }: Props) {
  const [form, setForm] = useState<FormState>(initial);
  const [scope, setScope] = useState<'this' | 'all'>('this');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const first = useRef<HTMLInputElement>(null);
  useEffect(() => first.current?.focus(), []);

  const writable = calendars.filter((c) => c.role !== 'reader');
  const readOnly = event !== undefined && !event.editable;
  const recurring = event?.recurring ?? false;
  const zone = validZone(event?.zone) ? event!.zone! : browserZone();
  const set = (patch: Partial<FormState>) => setForm({ ...form, ...patch });

  function chooseScope(next: 'this' | 'all') {
    setScope(next);
    if (event) setForm(formFromEvent(event, next));
  }

  async function act(fn: () => Promise<unknown>, fallback: string) {
    setError(null);
    setBusy(true);
    try {
      await fn();
      onDone();
    } catch (e) {
      setError(message(e, fallback));
    } finally {
      setBusy(false);
    }
  }

  function save(e: React.FormEvent) {
    e.preventDefault();
    const body = bodyFromForm(form, zone, event ? (recurring ? scope : 'all') : undefined, event?.recurrence_id);
    if (typeof body === 'string') {
      setError(body);
      return;
    }
    void act(() => (event ? updateEvent(event, body) : createEvent(form.calendarId, body)), 'Could not save the event');
  }

  function remove() {
    if (!event) return;
    const s = recurring ? scope : 'all';
    if (!window.confirm(s === 'this' ? 'Delete this occurrence?' : 'Delete this event?')) return;
    void act(() => deleteEvent(event, s), 'Could not delete the event');
  }

  return (
    <div role="dialog" aria-modal="true" aria-labelledby="kc-event-title" className="kc-dialog" onKeyDown={(e) => e.key === 'Escape' && onClose()}>
      <h2 id="kc-event-title">{event ? (readOnly ? form.title || '(no title)' : 'Edit event') : 'New event'}</h2>
      {error && <p role="alert">{error}</p>}
      {readOnly ? (
        <div>
          <p>{event!.all_day ? `${form.start} – ${form.end}` : `${new Date(event!.start).toLocaleString()} – ${new Date(event!.end).toLocaleString()}`}</p>
          {form.location && <p>{linkify(form.location)}</p>}
          {form.description && <p className="kc-description">{linkify(form.description)}</p>}
          <button type="button" onClick={onClose}>Close</button>
        </div>
      ) : (
        <form onSubmit={save}>
          {recurring && (
            <fieldset>
              <legend>Change</legend>
              <label><input type="radio" name="scope" checked={scope === 'this'} onChange={() => chooseScope('this')} /> This event</label>
              <label><input type="radio" name="scope" checked={scope === 'all'} onChange={() => chooseScope('all')} /> All events</label>
            </fieldset>
          )}
          <label htmlFor="kc-title">Title</label>
          <input id="kc-title" ref={first} value={form.title} maxLength={1000} onChange={(e) => set({ title: e.target.value })} />
          <label><input type="checkbox" checked={form.allDay} onChange={(e) => set({ allDay: e.target.checked, start: form.start.slice(0, 10) + (e.target.checked ? '' : 'T09:00'), end: form.end.slice(0, 10) + (e.target.checked ? '' : 'T10:00') })} /> All day</label>
          <label htmlFor="kc-start">Start</label>
          <input id="kc-start" type={form.allDay ? 'date' : 'datetime-local'} value={form.start} required onChange={(e) => set({ start: e.target.value })} />
          <label htmlFor="kc-end">End</label>
          <input id="kc-end" type={form.allDay ? 'date' : 'datetime-local'} value={form.end} required onChange={(e) => set({ end: e.target.value })} />
          <label htmlFor="kc-location">Location</label>
          <input id="kc-location" value={form.location} maxLength={1000} onChange={(e) => set({ location: e.target.value })} />
          <label htmlFor="kc-description">Description</label>
          <textarea id="kc-description" value={form.description} maxLength={65536} onChange={(e) => set({ description: e.target.value })} />
          {!event && (
            <>
              <label htmlFor="kc-calendar">Calendar</label>
              <select id="kc-calendar" value={form.calendarId} onChange={(e) => set({ calendarId: e.target.value })}>
                {writable.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
              </select>
            </>
          )}
          {(!recurring || scope === 'all') && (
            form.freq === 'custom' ? (
              <p>Repeats: <span>Custom (edit on your device)</span></p>
            ) : (
              <>
                <label htmlFor="kc-repeat">Repeat</label>
                <select id="kc-repeat" value={form.freq} onChange={(e) => set({ freq: e.target.value as Freq })}>
                  {PRESETS.map((p) => <option key={p.value} value={p.value}>{p.label}</option>)}
                </select>
                {form.freq === 'weekly' && (
                  <fieldset>
                    <legend>On</legend>
                    {WEEKDAYS.map((d) => (
                      <label key={d}>
                        <input type="checkbox" checked={form.weekdays.includes(d)} onChange={(e) => set({ weekdays: e.target.checked ? [...form.weekdays, d] : form.weekdays.filter((x) => x !== d) })} /> {d}
                      </label>
                    ))}
                  </fieldset>
                )}
              </>
            )
          )}
          {recurring && scope === 'this' && form.freq === 'custom' && <p>Repeats: <span>Custom (edit on your device)</span></p>}
          <button type="submit" disabled={busy}>Save</button>
          {event && <button type="button" disabled={busy} onClick={remove}>Delete</button>}
          <button type="button" onClick={onClose}>Cancel</button>
        </form>
      )}
    </div>
  );
}
