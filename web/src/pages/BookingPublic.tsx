import React, { useEffect, useState } from 'react';
import { AlertCircle, CalendarCheck } from 'lucide-react';
import { ThemeSwitcher } from '../components/ThemeSwitcher';
import { JSON_HEADERS, failure, send } from '../admin';
import { ensureCSRF } from '../api';

export interface Slot {
  start: string;
  end: string;
}

interface Page {
  host: string;
  title: string;
  duration_min: number;
  slots: Slot[];
}

interface Booking {
  status: 'held' | 'confirmed' | 'expired' | 'cancelled';
  host: string;
  title: string;
  start: string;
  end: string;
  slots?: Slot[];
}

const visitorZone = () => Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
const dayLabel = (iso: string) => new Date(iso).toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long' });
const timeLabel = (iso: string) => new Date(iso).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
const whenLabel = (s: Slot) => `${dayLabel(s.start)}, ${timeLabel(s.start)} – ${timeLabel(s.end)}`;

// A visitor opening a mailed link has no CSRF token yet: fetch one first.
const post = async (path: string, body: unknown) => {
  await ensureCSRF().catch(() => {});
  return send(path, { method: 'POST', headers: JSON_HEADERS, body: JSON.stringify(body) });
};

/** The link's ID and token, from its fragment: the server never sees them in a URL. */
export function linkFromHash(hash: string): { id: string; token: string } | null {
  const m = /^#([0-9a-f-]{36})\.([0-9a-f]{64})$/.exec(hash);
  return m ? { id: m[1], token: m[2] } : null;
}

/** Slots grouped by the visitor's local day, in order. */
export function byDay(slots: Slot[]): [string, Slot[]][] {
  const days = new Map<string, Slot[]>();
  for (const s of slots) {
    const d = dayLabel(s.start);
    days.set(d, [...(days.get(d) ?? []), s]);
  }
  return [...days.entries()];
}

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <main className="ky-auth book-public">
      <div className="ky-auth-theme">
        <ThemeSwitcher />
      </div>
      <div className="ky-auth-body">{children}</div>
    </main>
  );
}

function Head({ title, host, sub }: { title: string; host: string; sub?: string }) {
  return (
    <div className="ky-auth-brand">
      <h1>{title}</h1>
      <p>with {host}{sub ? ` · ${sub}` : ''}</p>
    </div>
  );
}

function Problem({ text }: { text: string }) {
  return (
    <div role="alert" className="alert alert-error auth-error">
      <AlertCircle size={16} aria-hidden="true" />
      <span>{text}</span>
    </div>
  );
}

function SlotPicker({ slots, onPick }: { slots: Slot[]; onPick: (s: Slot) => void }) {
  if (!slots.length) return <p className="field-hint">No free times in the coming weeks. Try again later.</p>;
  return (
    <div className="book-days">
      <p className="field-hint">Times are in your time zone, {visitorZone()}.</p>
      {byDay(slots).map(([day, list]) => (
        <section key={day} aria-label={day}>
          <h2>{day}</h2>
          <div className="book-slots">
            {list.map((s) => (
              <button key={s.start} type="button" className="btn-secondary btn-sm" onClick={() => onPick(s)}>{timeLabel(s.start)}</button>
            ))}
          </div>
        </section>
      ))}
    </div>
  );
}

function Book({ slug }: { slug: string }) {
  const [page, setPage] = useState<Page | null>(null);
  const [error, setError] = useState('');
  const [slot, setSlot] = useState<Slot | null>(null);
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState('');

  const load = async () => {
    const res = await send(`/api/book/${encodeURIComponent(slug)}`);
    const p = res?.ok ? await res.json().catch(() => null) : null;
    if (p && Array.isArray(p.slots)) setPage(p);
    else setError(res?.status === 404 ? 'This booking page does not exist or is turned off.' : (await failure(res)).error ?? 'This page cannot load right now.');
  };
  useEffect(() => { void load(); }, [slug]);

  const hold = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!slot) return;
    setBusy(true);
    setError('');
    const res = await post(`/api/book/${encodeURIComponent(slug)}/hold`, { start: slot.start, name, email, note });
    setBusy(false);
    if (res?.ok) {
      setSent(email);
      return;
    }
    const f = await failure(res);
    setError(f.error ?? 'The time could not be held. Try again.');
    if (f.code === 'slot_unavailable') {
      setSlot(null);
      void load();
    }
  };

  if (!page) return error ? <Problem text={error} /> : <p className="field-hint">Loading…</p>;
  return (
    <>
      <Head title={page.title} host={page.host} sub={`${page.duration_min} minutes`} />
      <div className="ky-auth-card">
        {sent ? (
          <div role="status" className="book-done">
            <CalendarCheck size={20} aria-hidden="true" />
            <h2>Check your email</h2>
            <p>We sent a link to {sent}. Open it within 15 minutes to confirm {slot && whenLabel(slot)}. Until then the time is held for you.</p>
          </div>
        ) : slot ? (
          <form onSubmit={hold}>
            <h2>{whenLabel(slot)}</h2>
            {error && <Problem text={error} />}
            <div className="ky-auth-field">
              <label htmlFor="book-name">Your name</label>
              <input id="book-name" required maxLength={100} autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} />
            </div>
            <div className="ky-auth-field">
              <label htmlFor="book-email">Your email</label>
              <input id="book-email" type="email" required maxLength={254} autoComplete="email" value={email} onChange={(e) => setEmail(e.target.value)} />
            </div>
            <div className="field">
              <label htmlFor="book-note">Anything to add? <span className="field-optional">(optional)</span></label>
              <textarea id="book-note" rows={3} maxLength={1000} value={note} onChange={(e) => setNote(e.target.value)} />
            </div>
            <p className="field-hint">We email you a link to confirm. Nothing is booked until you do.</p>
            <div className="form-actions">
              <button type="submit" disabled={busy}>Hold this time</button>
              <button type="button" className="btn-secondary" onClick={() => { setSlot(null); setError(''); }}>Back</button>
            </div>
          </form>
        ) : (
          <>
            {error && <Problem text={error} />}
            <SlotPicker slots={page.slots} onPick={setSlot} />
          </>
        )}
      </div>
    </>
  );
}

/** Loads the booking behind a mailed link. */
function useLinked(route: string) {
  const link = linkFromHash(location.hash);
  const [b, setB] = useState<Booking | null>(null);
  const [error, setError] = useState(link ? '' : 'This link is incomplete. Open it straight from the email.');
  const load = async () => {
    if (!link) return;
    const res = await post(route, link);
    const v = res?.ok ? await res.json().catch(() => null) : null;
    if (v?.status) setB(v);
    else setError((await failure(res)).error ?? 'This link cannot be opened right now.');
  };
  useEffect(() => { void load(); }, []);
  return { link, b, setB, error, setError, load };
}

function Confirm() {
  const { link, b, setB, error, setError } = useLinked('/api/book/confirm/view');
  const [busy, setBusy] = useState(false);
  const confirm = async () => {
    setBusy(true);
    const res = await post('/api/book/confirm', link);
    setBusy(false);
    const v = res?.ok ? await res.json().catch(() => null) : null;
    if (v?.status) setB(v);
    else setError((await failure(res)).error ?? 'The booking could not be confirmed. Try again.');
  };
  if (!b) return error ? <Problem text={error} /> : <p className="field-hint">Loading…</p>;
  return (
    <>
      <Head title={b.title} host={b.host} />
      <div className="ky-auth-card">
        <h2>{whenLabel(b)}</h2>
        {error && <Problem text={error} />}
        {b.status === 'held' && (
          <>
            <p>Confirm to book this time. You will get an invitation for your calendar.</p>
            <div className="form-actions">
              <button type="button" disabled={busy} onClick={() => void confirm()}>Confirm booking</button>
            </div>
          </>
        )}
        {b.status === 'confirmed' && <p role="status">Booked. Your invitation is on its way; it has a link to cancel or change the time.</p>}
        {b.status === 'expired' && <p role="alert">This hold has expired and the time was released. Book again from the booking page.</p>}
      </div>
    </>
  );
}

function Manage() {
  const { link, b, setB, error, setError } = useLinked('/api/book/manage');
  const [moving, setMoving] = useState(false);
  const [busy, setBusy] = useState(false);
  const act = async (path: string, extra: object = {}) => {
    setBusy(true);
    setError('');
    const res = await post(path, { ...link, ...extra });
    setBusy(false);
    const v = res?.ok ? await res.json().catch(() => null) : null;
    if (v?.status) {
      setB({ ...v, slots: b?.slots });
      setMoving(false);
    } else setError((await failure(res)).error ?? 'That did not work. Try again.');
  };
  if (!b) return error ? <Problem text={error} /> : <p className="field-hint">Loading…</p>;
  return (
    <>
      <Head title={b.title} host={b.host} />
      <div className="ky-auth-card">
        <h2>{whenLabel(b)}</h2>
        {error && <Problem text={error} />}
        {b.status === 'cancelled' && <p role="status">This booking is cancelled.</p>}
        {b.status === 'confirmed' && !moving && (
          <div className="form-actions">
            <button type="button" className="btn-secondary" disabled={busy} onClick={() => setMoving(true)}>Choose another time</button>
            <button
              type="button"
              className="btn-danger"
              disabled={busy}
              onClick={() => { if (window.confirm('Cancel this booking?')) void act('/api/book/manage/cancel'); }}
            >
              Cancel booking
            </button>
          </div>
        )}
        {b.status === 'confirmed' && moving && (
          <>
            <SlotPicker slots={b.slots ?? []} onPick={(s) => void act('/api/book/manage/reschedule', { start: s.start })} />
            <div className="form-actions">
              <button type="button" className="btn-secondary" onClick={() => setMoving(false)}>Keep this time</button>
            </div>
          </>
        )}
      </div>
    </>
  );
}

/** The public booking pages: /book/<slug>, /book/c (confirm) and /book/m (manage). No sign-in. */
export function BookingPublic({ path }: { path: string }) {
  const slug = path.slice('/book/'.length);
  return <Shell>{slug === 'c' ? <Confirm /> : slug === 'm' ? <Manage /> : <Book slug={slug} />}</Shell>;
}
