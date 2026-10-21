import { useState } from 'react';
import { Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react';
import { calendarColors, nextColor } from '../calendarColors';
import {
  ApiError, HOLDS, createCalendar, holdsOf, deleteCalendar, patchCalendar, refreshSubscription, subscribeCalendar, updateSubscriptionLink, type CalendarInfo, type Holds,
} from '../calendarApi';

interface Props {
  calendars: CalendarInfo[];
  hidden: Set<string>;
  onToggle: (id: string) => void;
  onChanged: () => void;
}

const canManage = (c: CalendarInfo) => c.role === 'owner' || c.role === 'manager';

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
export function ago(iso: string, now = Date.now()): string {
  const s = Math.round((Date.parse(iso) - now) / 1000);
  for (const [unit, size] of [['day', 86_400], ['hour', 3_600], ['minute', 60]] as const) {
    if (Math.abs(s) >= size) return rtf.format(Math.round(s / size), unit);
  }
  return 'just now';
}

// SubscriptionForm subscribes to an ICS link, or replaces the link of `calendar`.
function SubscriptionForm({ calendar, color, onSaved, onCancel }: { calendar?: CalendarInfo; color: string; onSaved: () => void; onCancel: () => void }) {
  const [form, setForm] = useState({ url: '', name: '', color });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const id = calendar ? `link-${calendar.id}` : 'kc-sub';

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await (calendar ? updateSubscriptionLink(calendar.id, form.url) : subscribeCalendar(form.url, form.name, form.color));
      onSaved();
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : 'Could not load the link');
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="kc-form kc-cal-edit" aria-label={calendar ? `Change link of ${calendar.name}` : 'Subscribe to a calendar'} onSubmit={submit}>
      {!calendar && <h3 className="kc-form-title">Subscribe to a calendar</h3>}
      {error && <p role="alert">{error}</p>}
      <label htmlFor={`${id}-url`}>Link</label>
      <input id={`${id}-url`} type="url" placeholder="https:// or webcal://" value={form.url} required onChange={(e) => setForm({ ...form, url: e.target.value })} />
      {!calendar && (
        <>
          <p className="kc-hint">In Outlook: Settings → Calendar → Shared calendars → Publish a calendar, choose “Can view all details”, then copy the ICS link.</p>
          <label htmlFor={`${id}-name`}>Name</label>
          <input id={`${id}-name`} value={form.name} maxLength={255} required onChange={(e) => setForm({ ...form, name: e.target.value })} />
          <label htmlFor={`${id}-color`}>Colour</label>
          <input id={`${id}-color`} type="color" value={form.color} onChange={(e) => setForm({ ...form, color: e.target.value })} />
        </>
      )}
      <p className="kc-hint">Anyone with the link can read this calendar. Events are read-only here, and anything you connect to KyCalendar, your AI agent included, can see them.</p>
      <div className="kc-actions">
        <button type="submit" className="btn-sm" disabled={busy}>{calendar ? 'Save link' : 'Subscribe'}</button>
        <button type="button" className="btn-ghost btn-sm" onClick={onCancel}>Cancel</button>
      </div>
    </form>
  );
}

export function CalendarSidebar({ calendars, hidden, onToggle, onChanged }: Props) {
  const [name, setName] = useState('');
  const [holds, setHolds] = useState<Holds>('events');
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState({ name: '', color: '' });
  const [error, setError] = useState<string | null>(null);
  const [subscribing, setSubscribing] = useState(false);
  const [relinking, setRelinking] = useState<string | null>(null);
  const colors = calendarColors(calendars);
  const colorOf = (c: CalendarInfo) => colors.get(c.id) ?? 'var(--ky-accent)';
  // A new calendar takes a colour none of the visible ones use.
  const fresh = () => nextColor([...colors.values()]);

  async function run(action: () => Promise<unknown>, failure: string) {
    setError(null);
    try {
      await action();
      setError(null);
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
            <div className="kc-cal-row">
              <label className="kc-cal-name">
                <input type="checkbox" aria-label={`Show ${c.name}`} checked={!hidden.has(c.id)} onChange={() => onToggle(c.id)} />
                <span className="kc-swatch" style={{ background: colorOf(c) }} aria-hidden="true" />
                <span title={c.name}>{c.name}</span>
              </label>
              <span className="kc-cal-actions">
                {canManage(c) && editing !== c.id && (
                  <button type="button" className="btn-ghost kc-icon" aria-label={`Edit ${c.name}`} title="Edit" onClick={() => { setEditing(c.id); setDraft({ name: c.name, color: colorOf(c) }); }}>
                    <Pencil size={14} aria-hidden="true" />
                  </button>
                )}
                {c.kind === 'personal' && c.role === 'owner' && (
                  <button
                    type="button"
                    className="btn-ghost btn-danger kc-icon"
                    aria-label={`Delete ${c.name}`}
                    title="Delete"
                    onClick={() => window.confirm(`Delete ${c.name} and every event in it?`) && void run(() => deleteCalendar(c.id), 'Could not delete the calendar')}
                  >
                    <Trash2 size={14} aria-hidden="true" />
                  </button>
                )}
              </span>
            </div>
            {!c.components.includes('VEVENT') && <div className="kc-sub"><span>Tasks · open in a tasks app</span></div>}
            {c.subscription && (
              <div className="kc-sub">
                <span>Subscribed · {c.subscription.host}</span>
                <span>{c.subscription.refreshed_at ? `Updated ${ago(c.subscription.refreshed_at)}` : 'Not yet updated'}</span>
                {c.subscription.error && <span role="alert">{c.subscription.error}</span>}
                <div className="kc-actions">
                  <button
                    type="button"
                    className="btn-secondary btn-sm"
                    aria-label={`Refresh ${c.name}`}
                    onClick={() => void run(() => refreshSubscription(c.id).catch((e) => {
                      throw e instanceof ApiError && e.status === 429 ? new Error(`${c.name} was refreshed less than a minute ago; try again shortly.`) : e;
                    }), 'Could not refresh the calendar')}
                  >
                    <RefreshCw size={12} aria-hidden="true" />
                    Refresh
                  </button>
                  {relinking !== c.id && (
                    <button type="button" className="btn-ghost btn-sm" aria-label={`Change link of ${c.name}`} onClick={() => setRelinking(c.id)}>Change link</button>
                  )}
                </div>
              </div>
            )}
            {relinking === c.id && (
              <SubscriptionForm calendar={c} color={colorOf(c)} onSaved={() => { setRelinking(null); onChanged(); }} onCancel={() => setRelinking(null)} />
            )}
            {editing === c.id && (
              <form
                className="kc-form kc-cal-edit"
                onSubmit={async (e) => {
                  e.preventDefault();
                  // An unchanged colour is not sent, so a calendar keeps whatever it had stored.
                  if (await run(() => patchCalendar(c.id, draft.color === colorOf(c) ? { name: draft.name } : draft), 'Could not save the calendar')) setEditing(null);
                }}
              >
                <label htmlFor={`name-${c.id}`}>Name</label>
                <input id={`name-${c.id}`} value={draft.name} maxLength={255} required onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
                <label htmlFor={`color-${c.id}`}>Colour</label>
                <input id={`color-${c.id}`} type="color" value={draft.color} onChange={(e) => setDraft({ ...draft, color: e.target.value })} />
                <div className="kc-actions">
                  <button type="submit" className="btn-sm">Save</button>
                  <button type="button" className="btn-ghost btn-sm" onClick={() => setEditing(null)}>Cancel</button>
                </div>
              </form>
            )}
          </li>
        ))}
      </ul>
      {adding ? (
        <form
          className="kc-form kc-add"
          aria-label="New calendar"
          onSubmit={async (e) => {
            e.preventDefault();
            if (await run(() => createCalendar({ name, components: HOLDS[holds].components, color: fresh() }), 'Could not create the calendar')) {
              setName('');
              setAdding(false);
            }
          }}
        >
          <h3 className="kc-form-title">New calendar</h3>
          <label htmlFor="kc-new-calendar">New calendar name</label>
          <input id="kc-new-calendar" value={name} maxLength={255} required autoFocus onChange={(e) => setName(e.target.value)} />
          <label htmlFor="kc-new-holds">Holds</label>
          <select id="kc-new-holds" value={holds} onChange={(e) => setHolds(holdsOf(e.target.value))}>
            {Object.entries(HOLDS).map(([k, h]) => <option key={k} value={k}>{h.label}</option>)}
          </select>
          <div className="kc-actions">
            <button type="submit" className="btn-sm">Add calendar</button>
            <button type="button" className="btn-ghost btn-sm" onClick={() => setAdding(false)}>Cancel</button>
          </div>
        </form>
      ) : subscribing ? null : (
        <div className="kc-sidebar-actions">
          <button type="button" className="btn-secondary btn-sm" onClick={() => setAdding(true)}>
            <Plus size={14} aria-hidden="true" />
            New calendar
          </button>
          <button type="button" className="btn-ghost btn-sm" onClick={() => setSubscribing(true)}>Subscribe to a calendar</button>
        </div>
      )}
      {subscribing && <SubscriptionForm color={fresh()} onSaved={() => { setSubscribing(false); onChanged(); }} onCancel={() => setSubscribing(false)} />}
    </aside>
  );
}
