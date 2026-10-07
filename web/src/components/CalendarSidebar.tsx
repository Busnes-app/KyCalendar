import { useState } from 'react';
import { createCalendar, deleteCalendar, patchCalendar, type CalendarInfo } from '../calendarApi';

interface Props {
  calendars: CalendarInfo[];
  hidden: Set<string>;
  onToggle: (id: string) => void;
  onChanged: () => void;
}

const canManage = (c: CalendarInfo) => c.role === 'owner' || c.role === 'manager';

export function CalendarSidebar({ calendars, hidden, onToggle, onChanged }: Props) {
  const [name, setName] = useState('');
  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState({ name: '', color: '' });
  const [error, setError] = useState<string | null>(null);

  async function run(action: () => Promise<unknown>, failure: string) {
    setError(null);
    try {
      await action();
      onChanged();
      return true;
    } catch (e) {
      setError(e instanceof Error && e.message ? e.message : failure);
      return false;
    }
  }

  return (
    <aside className="kc-sidebar" aria-label="Calendars">
      <h2>Calendars</h2>
      {error && <p role="alert">{error}</p>}
      <ul>
        {calendars.map((c) => (
          <li key={c.id}>
            <label>
              <input type="checkbox" aria-label={`Show ${c.name}`} checked={!hidden.has(c.id)} onChange={() => onToggle(c.id)} />
              <span className="kc-swatch" style={{ background: c.color || 'var(--ky-accent)' }} aria-hidden="true" />
              {c.name}
            </label>
            {canManage(c) && editing !== c.id && (
              <button type="button" aria-label={`Edit ${c.name}`} onClick={() => { setEditing(c.id); setDraft({ name: c.name, color: c.color || '#e8590c' }); }}>
                Edit
              </button>
            )}
            {c.kind === 'personal' && c.role === 'owner' && (
              <button
                type="button"
                aria-label={`Delete ${c.name}`}
                onClick={() => window.confirm(`Delete ${c.name} and every event in it?`) && void run(() => deleteCalendar(c.id), 'Could not delete the calendar')}
              >
                Delete
              </button>
            )}
            {editing === c.id && (
              <form
                onSubmit={async (e) => {
                  e.preventDefault();
                  if (await run(() => patchCalendar(c.id, draft), 'Could not save the calendar')) setEditing(null);
                }}
              >
                <label htmlFor={`name-${c.id}`}>Name</label>
                <input id={`name-${c.id}`} value={draft.name} maxLength={255} required onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
                <label htmlFor={`color-${c.id}`}>Colour</label>
                <input id={`color-${c.id}`} type="color" value={draft.color} onChange={(e) => setDraft({ ...draft, color: e.target.value })} />
                <button type="submit">Save</button>
                <button type="button" onClick={() => setEditing(null)}>Cancel</button>
              </form>
            )}
          </li>
        ))}
      </ul>
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          if (await run(() => createCalendar({ name }), 'Could not create the calendar')) setName('');
        }}
      >
        <label htmlFor="kc-new-calendar">New calendar name</label>
        <input id="kc-new-calendar" value={name} maxLength={255} required onChange={(e) => setName(e.target.value)} />
        <button type="submit">Add calendar</button>
      </form>
    </aside>
  );
}
