import { useEffect, useState } from 'react';
import { Plus } from 'lucide-react';
import { CopyButton, EmptyState, Modal } from '../components/ui';
import { JSON_HEADERS, failure, send } from '../admin';

export interface Window {
  day: number;
  start: string;
  end: string;
}

export interface BookingType {
  id: string;
  enabled: boolean;
  url: string;
  title: string;
  duration_min: number;
  zone: string;
  windows: Window[];
  buffer_before_min: number;
  buffer_after_min: number;
  notice_min: number;
  horizon_days: number;
  daily_cap: number;
  calendar_id: string;
}

interface Listing {
  available: boolean;
  reason: string;
  reason_text: string;
  types: BookingType[];
}

interface Calendar {
  id: string;
  name: string;
  kind: string;
  components?: string[];
  subscription?: unknown;
}

const ROUTE = '/api/booking-types';
export const DAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
// Monday first, as most calendars here show the week.
const WEEK = [1, 2, 3, 4, 5, 6, 0];

const readListing = async (res: Response): Promise<Listing | null> => {
  const b = await res.json().catch(() => null);
  return b && typeof b.available === 'boolean' && Array.isArray(b.types) ? b : null;
};

const browserZone = () => Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
const zones = (): string[] => {
  const all = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.('timeZone') ?? [];
  return all.includes('UTC') ? all : ['UTC', ...all];
};

/** A new type: weekdays 09:00 to 17:00 in the browser's zone, 30 minutes, the server's defaults. */
export function blankType(calendarId: string): Omit<BookingType, 'id' | 'url'> {
  return {
    enabled: true,
    title: '',
    duration_min: 30,
    zone: browserZone(),
    windows: [1, 2, 3, 4, 5].map((day) => ({ day, start: '09:00', end: '17:00' })),
    buffer_before_min: 0,
    buffer_after_min: 0,
    notice_min: 240,
    horizon_days: 30,
    daily_cap: 0,
    calendar_id: calendarId,
  };
}

/** What the hours look like in one line: "Mon–Fri 09:00–17:00". */
export function hoursText(ws: Window[]): string {
  const byHours = new Map<string, number[]>();
  for (const w of ws) {
    const k = `${w.start}–${w.end}`;
    byHours.set(k, [...(byHours.get(k) ?? []), w.day]);
  }
  return [...byHours.entries()]
    .map(([hours, days]) => `${WEEK.filter((d) => days.includes(d)).map((d) => DAYS[d].slice(0, 3)).join(', ')} ${hours}`)
    .join('; ');
}

type Draft = Omit<BookingType, 'id' | 'url'>;

const num = (v: string) => (v === '' ? 0 : Number(v));

/** The editor: every rule of one booking type. One window per weekday. */
function TypeForm({ draft, calendars, available, onSave, onCancel, saving, error }: {
  draft: Draft;
  calendars: Calendar[];
  available: boolean;
  onSave: (d: Draft) => void;
  onCancel: () => void;
  saving: boolean;
  error: string;
}) {
  const [d, setD] = useState<Draft>(draft);
  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setD((x) => ({ ...x, [k]: v }));
  const windowFor = (day: number) => d.windows.find((w) => w.day === day);
  const setWindow = (day: number, w: Window | null) =>
    set('windows', [...d.windows.filter((x) => x.day !== day), ...(w ? [w] : [])].sort((a, b) => a.day - b.day));
  const zoneList = zones();
  return (
    <form className="form form-wide" onSubmit={(e) => { e.preventDefault(); onSave(d); }}>
      <div className="field">
        <label htmlFor="bt-title">Title</label>
        <input id="bt-title" required maxLength={100} value={d.title} onChange={(e) => set('title', e.target.value)} placeholder="30-minute call" />
        <span className="field-hint">Visitors see this and your name, nothing else about you.</span>
      </div>
      <div className="form-row">
        <div className="field field-narrow">
          <label htmlFor="bt-length">Length (minutes)</label>
          <input id="bt-length" type="number" min={5} max={480} required value={d.duration_min} onChange={(e) => set('duration_min', num(e.target.value))} />
        </div>
        <div className="field">
          <label htmlFor="bt-calendar">Add bookings to</label>
          <select id="bt-calendar" required value={d.calendar_id} onChange={(e) => set('calendar_id', e.target.value)}>
            {calendars.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
          </select>
        </div>
      </div>
      <fieldset>
        <legend>Weekly hours</legend>
        <div className="field">
          <label htmlFor="bt-zone">Time zone</label>
          <select id="bt-zone" value={d.zone} onChange={(e) => set('zone', e.target.value)}>
            {(zoneList.includes(d.zone) ? zoneList : [d.zone, ...zoneList]).map((z) => <option key={z} value={z}>{z}</option>)}
          </select>
        </div>
        <ul className="book-hours">
          {WEEK.map((day) => {
            const w = windowFor(day);
            return (
              <li key={day}>
                <label className="kc-check">
                  <input type="checkbox" checked={!!w} onChange={(e) => setWindow(day, e.target.checked ? { day, start: '09:00', end: '17:00' } : null)} />
                  {DAYS[day]}
                </label>
                {w ? (
                  <span className="book-hours-times">
                    <input type="time" aria-label={`${DAYS[day]} from`} required value={w.start} onChange={(e) => setWindow(day, { ...w, start: e.target.value })} />
                    <span aria-hidden="true">–</span>
                    <input type="time" aria-label={`${DAYS[day]} until`} required value={w.end === '24:00' ? '23:59' : w.end} onChange={(e) => setWindow(day, { ...w, end: e.target.value })} />
                  </span>
                ) : (
                  <span className="field-hint">Unavailable</span>
                )}
              </li>
            );
          })}
        </ul>
        <span className="field-hint">Free time comes from all your own calendars: anything busy there is never offered.</span>
      </fieldset>
      <div className="form-row">
        <div className="field field-narrow">
          <label htmlFor="bt-before">Gap before (minutes)</label>
          <input id="bt-before" type="number" min={0} max={240} value={d.buffer_before_min} onChange={(e) => set('buffer_before_min', num(e.target.value))} />
        </div>
        <div className="field field-narrow">
          <label htmlFor="bt-after">Gap after (minutes)</label>
          <input id="bt-after" type="number" min={0} max={240} value={d.buffer_after_min} onChange={(e) => set('buffer_after_min', num(e.target.value))} />
        </div>
      </div>
      <div className="form-row">
        <div className="field field-narrow">
          <label htmlFor="bt-notice">Minimum notice (hours)</label>
          <input id="bt-notice" type="number" min={0} max={720} step="any" value={d.notice_min / 60} onChange={(e) => set('notice_min', Math.round(num(e.target.value) * 60))} />
        </div>
        <div className="field field-narrow">
          <label htmlFor="bt-horizon">Up to (days ahead)</label>
          <input id="bt-horizon" type="number" min={1} max={60} required value={d.horizon_days} onChange={(e) => set('horizon_days', num(e.target.value))} />
        </div>
        <div className="field field-narrow">
          <label htmlFor="bt-cap">Most per day</label>
          <input id="bt-cap" type="number" min={0} max={100} value={d.daily_cap} onChange={(e) => set('daily_cap', num(e.target.value))} aria-describedby="bt-cap-hint" />
          <span className="field-hint" id="bt-cap-hint">0 means no limit.</span>
        </div>
      </div>
      <label className="kc-check">
        <input type="checkbox" role="switch" checked={d.enabled} disabled={!available && !d.enabled} onChange={(e) => set('enabled', e.target.checked)} />
        Take bookings at this page's link
      </label>
      {error && <p role="alert">{error}</p>}
      <div className="form-actions">
        <button type="submit" disabled={saving}>Save</button>
        <button type="button" className="btn-secondary" onClick={onCancel}>Cancel</button>
      </div>
    </form>
  );
}

/** Settings → Booking pages: the member's booking types, their links and rules. */
export default function BookingPages() {
  const [listing, setListing] = useState<Listing | null>(null);
  const [calendars, setCalendars] = useState<Calendar[]>([]);
  const [error, setError] = useState('');
  const [editing, setEditing] = useState<{ id?: string; draft: Draft } | null>(null);
  const [formError, setFormError] = useState('');
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState('');

  const load = async () => {
    const res = await send(ROUTE);
    const l = res?.ok ? await readListing(res) : null;
    if (l) setListing(l);
    else setError('Could not load your booking pages.');
  };

  useEffect(() => {
    void load();
    void send('/api/calendars').then(async (res) => {
      const list = res?.ok ? await res.json().catch(() => []) : [];
      setCalendars((Array.isArray(list) ? list : []).filter((c: Calendar) => c.kind === 'personal' && !c.subscription && (c.components ?? ['VEVENT']).includes('VEVENT')));
    });
  }, []);

  const save = async (d: Draft) => {
    setSaving(true);
    setFormError('');
    const res = await send(editing?.id ? `${ROUTE}/${editing.id}` : ROUTE, { method: editing?.id ? 'PUT' : 'POST', headers: JSON_HEADERS, body: JSON.stringify(d) });
    setSaving(false);
    if (!res?.ok) {
      setFormError((await failure(res)).error ?? 'Could not save the booking page.');
      return;
    }
    setEditing(null);
    setMessage('Saved.');
    await load();
  };

  const act = async (t: BookingType, what: 'rotate' | 'delete') => {
    const ask = what === 'rotate'
      ? `Give “${t.title}” a new link? The current link stops working at once.`
      : `Delete “${t.title}”? Its link stops working. Bookings already made stay in your calendar.`;
    if (!window.confirm(ask)) return;
    const res = await send(what === 'rotate' ? `${ROUTE}/${t.id}/rotate` : `${ROUTE}/${t.id}`, { method: what === 'rotate' ? 'POST' : 'DELETE' });
    if (!res?.ok) {
      setError((await failure(res)).error ?? 'That did not work. Reload and try again.');
      return;
    }
    setMessage(what === 'rotate' ? 'New link made. Share it again.' : 'Deleted.');
    await load();
  };

  if (!listing) return error ? <p role="alert">{error}</p> : <p className="field-hint">Loading…</p>;
  return (
    <>
      {!listing.available && (
        <p role="note">Booking pages are off for you: {listing.reason_text}.</p>
      )}
      {error && <p role="alert">{error}</p>}
      {message && <p role="status">{message}</p>}
      <div className="card">
        <div className="book-card-head">
          <h2 className="section-title">Your booking pages</h2>
          <button
            type="button"
            className="btn-sm"
            disabled={!calendars.length}
            onClick={() => { setFormError(''); setEditing({ draft: blankType(calendars[0]?.id ?? '') }); }}
          >
            <Plus size={14} aria-hidden="true" /> New booking page
          </button>
        </div>
        {listing.types.length === 0 ? (
          <EmptyState compact title="No booking pages yet">
            A booking page lets people outside your organisation pick a free time with you. They confirm by email, then get an ordinary invitation.
          </EmptyState>
        ) : (
          <ul className="book-types">
            {listing.types.map((t) => (
              <li key={t.id}>
                <div className="book-type-main">
                  <strong>{t.title}</strong>
                  <span className="ky-status" data-ky-status={t.enabled && listing.available ? 'success' : undefined}>
                    {t.enabled && listing.available ? 'Taking bookings' : 'Off'}
                  </span>
                  <span className="field-hint">{t.duration_min} minutes · {hoursText(t.windows)} ({t.zone})</span>
                  <span className="book-link">
                    <code>{t.url}</code>
                    <CopyButton text={t.url} label={`Copy the link for ${t.title}`} />
                  </span>
                </div>
                <div className="row-actions">
                  <button type="button" className="btn-secondary btn-sm" onClick={() => { setFormError(''); setEditing({ id: t.id, draft: t }); }}>Edit</button>
                  <button type="button" className="btn-secondary btn-sm" onClick={() => void act(t, 'rotate')}>New link</button>
                  <button type="button" className="btn-danger btn-sm" onClick={() => void act(t, 'delete')}>Delete</button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>
      {editing && (
        <Modal wide title={editing.id ? 'Edit booking page' : 'New booking page'} onClose={() => setEditing(null)}>
          <TypeForm draft={editing.draft} calendars={calendars} available={listing.available} onSave={(d) => void save(d)} onCancel={() => setEditing(null)} saving={saving} error={formError} />
        </Modal>
      )}
    </>
  );
}
