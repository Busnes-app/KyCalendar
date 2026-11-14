import { useEffect, useRef, useState } from 'react';
import { ApiError, browserZone, canAddEvents, createEvent, deleteEvent, rsvp, searchPeople, updateEvent, type Answer, type CalendarInfo, type EventInfo, type Freq, type Person } from '../calendarApi';
import { WEEKDAYS, bodyFromForm, formFromEvent, validZone, type FormState } from '../eventForm';
import { linkify } from '../linkify';
import { FreeBusyStrip } from './FreeBusyStrip';
import { MOVE_ALL_WARNING } from './ScopeDialog';

interface Props {
  calendars: CalendarInfo[];
  event?: EventInfo;
  initial: FormState;
  initialPeople?: string[]; // guests for a new event (the "New meeting" link)
  onDone: () => void;
  onClose: () => void;
  scheduling?: boolean; // server sends invitations
  meetings?: boolean; // KyMessages is paired and in the user's suite apps
  me?: string; // signed-in user's email
}

const PRESETS: { value: Freq; label: string }[] = [
  { value: '', label: 'Does not repeat' },
  { value: 'daily', label: 'Daily' },
  { value: 'weekly', label: 'Weekly' },
  { value: 'monthly', label: 'Monthly' },
  { value: 'yearly', label: 'Yearly' },
];

const ANSWERS = new Map([['ACCEPTED', 'Accepted'], ['TENTATIVE', 'Maybe'], ['DECLINED', 'Declined'], ['DELEGATED', 'Delegated']]);
const answerText = (status?: string) => ANSWERS.get(status ?? '') ?? 'Awaiting reply';
const BUTTONS: [Answer, string][] = [['ACCEPTED', 'Accept'], ['TENTATIVE', 'Maybe'], ['DECLINED', 'Decline']];
const sameEmail = (a: string, b: string) => a.toLowerCase() === b.toLowerCase();

const MEETING_ERRORS: Record<string, string> = {
  no_user: 'The video meeting could not be set up: you have no KyMessages account yet.',
  no_account: 'The video meeting could not be set up: your account did not sign in through KyIdentity.',
};
const meetingError = (code: string) => MEETING_ERRORS[code] ?? `The video meeting could not be set up (${code}).`;

function message(e: unknown, fallback: string): string {
  if (e instanceof ApiError && e.status === 412) return 'This event changed elsewhere. Close and reopen it to see the latest version.';
  return e instanceof Error && e.message ? e.message : fallback;
}

export function EventDialog({ calendars, event, initial, initialPeople = [], onDone, onClose, scheduling = false, meetings = false, me = '' }: Props) {
  // An object with no recurrence_id cannot be edited per occurrence: no choice, series scope.
  const choose = !!event?.recurring && !!event.recurrence_id;
  const [scope, setScope] = useState<'this' | 'all'>(choose ? 'this' : 'all');
  const [form, setForm] = useState<FormState>(() => (event && !choose ? formFromEvent(event, 'all') : initial));
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const dialog = useRef<HTMLDialogElement>(null);
  const first = useRef<HTMLInputElement>(null);
  const closeBtn = useRef<HTMLButtonElement>(null);
  const readOnly = event !== undefined && !event.editable;
  const invited = scheduling && !!event?.my_status;
  // Only the organizer (or anyone, before there is one) manages people; the server lists the organizer itself.
  const organizes = scheduling && !readOnly && !invited && !!me && (!event?.organizer || sameEmail(event.organizer, me));
  const [loaded] = useState(() => (event?.attendees ?? []).filter((a) => !sameEmail(a.email, me)));
  const [people, setPeople] = useState(() => [...loaded, ...initialPeople.filter((e) => !sameEmail(e, me)).map((email) => ({ email, status: 'NEEDS-ACTION' }))]);
  const [invitee, setInvitee] = useState('');
  // The video meeting switch: a new event follows its guests until the user chooses.
  const [meetingChoice, setMeetingChoice] = useState<boolean | null>(event ? !!event.meeting : null);
  const meeting = meetingChoice ?? people.length > 0;
  const canMeet = meetings && !readOnly && !invited && (!event?.organizer || sameEmail(event.organizer, me));
  const [suggestions, setSuggestions] = useState<Person[]>([]);
  // Organisation people for the Invite field, after a short pause in typing; free text still works.
  useEffect(() => {
    const q = invitee.trim();
    if (!organizes || q.length < 2) {
      setSuggestions([]);
      return;
    }
    let live = true;
    const t = setTimeout(() => void searchPeople(q).then((p) => { if (live) setSuggestions(p); }), 200);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [invitee, organizes]);

  useEffect(() => {
    const d = dialog.current!;
    const prev = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    d.showModal();
    (readOnly ? closeBtn : first).current?.focus();
    return () => {
      d.close();
      prev?.focus();
    };
  }, [readOnly]);

  const writable = calendars.filter(canAddEvents);
  const zone = validZone(event?.zone) ? event!.zone! : browserZone();
  const set = (patch: Partial<FormState>) => setForm({ ...form, ...patch });

  function chooseScope(next: 'this' | 'all') {
    setScope(next);
    if (!event) return;
    const { start, end, allDay, freq, weekdays } = formFromEvent(event, next);
    setForm({ ...form, start, end, allDay, freq, weekdays });
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
    const body = bodyFromForm(form, zone, event ? scope : undefined, event?.recurrence_id);
    if (typeof body === 'string') {
      setError(body);
      return;
    }
    if (organizes && scope === 'all' && (people.length !== loaded.length || people.some((p, i) => p.email !== loaded[i].email))) body.attendees = people.map((p) => p.email);
    if (canMeet && scope === 'all' && meeting !== !!event?.meeting) body.meeting = meeting;
    void act(() => (event ? updateEvent(event, body) : createEvent(form.calendarId, body)), 'Could not save the event');
  }

  function invite() {
    const email = invitee.trim();
    if (!/^[^\s@]+@[^\s@]+$/.test(email)) {
      setError('Enter an email address like name@example.com');
      return;
    }
    setError(null);
    setInvitee('');
    if (!sameEmail(email, me) && !people.some((p) => sameEmail(p.email, email))) setPeople([...people, { email, status: 'NEEDS-ACTION' }]);
  }

  function answer(partstat: Answer) {
    if (!event) return;
    // An occurrence answer only where the user can pick "This event"; otherwise the series.
    const rid = !readOnly && choose && scope === 'this' ? event.recurrence_id : undefined;
    void act(() => rsvp(event.calendar_id, event.uid, event.etag, partstat, rid), 'Could not send your answer');
  }

  function remove() {
    if (!event) return;
    if (!window.confirm(scope === 'this' ? 'Delete this occurrence?' : event.recurring ? 'Delete every occurrence of this event?' : 'Delete this event?')) return;
    void act(() => deleteEvent(event, scope), 'Could not delete the event');
  }

  // Join for anyone the event carries a room for; the organizer sees the room's state too.
  const room = (
    <>
      {event?.meeting_url?.startsWith('https://') && <p><button type="button" className="btn-secondary" onClick={() => window.open(event.meeting_url, '_blank', 'noopener,noreferrer')}>Join video meeting</button></p>}
      {canMeet && event?.meeting && !event.meeting_url && !event.meeting_error && <p className="kc-hint" role="status">Meeting link pending</p>}
      {canMeet && event?.meeting && event.meeting_error && <p role="alert">{meetingError(event.meeting_error)}</p>}
    </>
  );

  const invitation = invited && (
    <>
      {event!.organizer && <p>Organized by {event!.organizer}</p>}
      <ul className="kc-people">
        {(event!.attendees ?? []).map((p) => <li key={p.email}><span>{p.email}</span> <span className="kc-hint">{answerText(p.status)}</span></li>)}
      </ul>
      {!event!.cancelled && <div className="kc-actions" role="group" aria-label="Your answer">
        {BUTTONS.map(([value, label]) => (
          <button key={value} type="button" className="btn-secondary kc-answer" aria-pressed={event!.my_status === value} disabled={busy} onClick={() => answer(value)}>{label}</button>
        ))}
      </div>}
    </>
  );

  return (
    <dialog ref={dialog} aria-labelledby="kc-event-title" className="kc-dialog" onCancel={(e) => { e.preventDefault(); onClose(); }}>
      <h2 id="kc-event-title">{event ? (readOnly ? form.title || '(no title)' : 'Edit event') : 'New event'}</h2>
      {error && <p role="alert">{error}</p>}
      {event?.cancelled && <p role="note"><strong>Cancelled.</strong> This event was called off.</p>}
      {readOnly ? (
        <div>
          <p>{event!.all_day ? `${form.start} – ${form.end}` : `${new Date(event!.start).toLocaleString()} – ${new Date(event!.end).toLocaleString()}`}</p>
          {form.location && <p>{linkify(form.location)}</p>}
          {form.description && <p className="kc-description">{linkify(form.description)}</p>}
          {room}
          {invitation}
          <div className="modal-actions">
            <button type="button" ref={closeBtn} onClick={onClose}>Close</button>
          </div>
        </div>
      ) : (
        <form className="kc-form" onSubmit={save}>
          {choose && (
            <fieldset>
              <legend>Change</legend>
              <label className="kc-check"><input type="radio" name="scope" checked={scope === 'this'} onChange={() => chooseScope('this')} /> This event</label>
              <label className="kc-check"><input type="radio" name="scope" checked={scope === 'all'} onChange={() => chooseScope('all')} /> All events</label>
            </fieldset>
          )}
          {event?.recurring && scope === 'all' && <p role="note">{MOVE_ALL_WARNING}</p>}
          {room}
          {invitation}
          <label htmlFor="kc-title">Title</label>
          <input id="kc-title" ref={first} value={form.title} maxLength={1000} onChange={(e) => set({ title: e.target.value })} />
          <label className="kc-check"><input type="checkbox" checked={form.allDay} onChange={(e) => set({ allDay: e.target.checked, start: form.start.slice(0, 10) + (e.target.checked ? '' : 'T09:00'), end: form.end.slice(0, 10) + (e.target.checked ? '' : 'T10:00') })} /> All day</label>
          <div className="kc-pair">
            <div className="field">
              <label htmlFor="kc-start">Start</label>
              <input id="kc-start" type={form.allDay ? 'date' : 'datetime-local'} value={form.start} required onChange={(e) => set({ start: e.target.value })} />
            </div>
            <div className="field">
              <label htmlFor="kc-end">End</label>
              <input id="kc-end" type={form.allDay ? 'date' : 'datetime-local'} value={form.end} required onChange={(e) => set({ end: e.target.value })} />
            </div>
          </div>
          <label htmlFor="kc-location">Location <span className="field-optional">(optional)</span></label>
          <input id="kc-location" value={form.location} maxLength={1000} onChange={(e) => set({ location: e.target.value })} />
          <label htmlFor="kc-description">Description <span className="field-optional">(optional)</span></label>
          <textarea id="kc-description" value={form.description} maxLength={65536} onChange={(e) => set({ description: e.target.value })} />
          {!event && (
            <>
              <label htmlFor="kc-calendar">Calendar</label>
              <select id="kc-calendar" value={form.calendarId} onChange={(e) => set({ calendarId: e.target.value })}>
                {writable.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
              </select>
            </>
          )}
          {scope === 'all' && (
            form.freq === 'custom' ? (
              <p className="kc-hint">Repeats: <span>Custom (edit on your device)</span></p>
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
                      <label key={d} className="kc-check">
                        <input type="checkbox" checked={form.weekdays.includes(d)} onChange={(e) => set({ weekdays: e.target.checked ? [...form.weekdays, d] : form.weekdays.filter((x) => x !== d) })} /> {d}
                      </label>
                    ))}
                  </fieldset>
                )}
              </>
            )
          )}
          {scope === 'this' && event && form.freq === 'custom' && <p className="kc-hint">Repeats: <span>Custom (edit on your device)</span></p>}
          {organizes && scope === 'this' && <p className="kc-hint">To change who is invited, choose All events: guests are the same for every event.</p>}
          {organizes && scope === 'all' && (
            <>
              <label htmlFor="kc-invite">People</label>
              {people.length > 0 && (
                <ul className="kc-people">
                  {people.map((p) => (
                    <li key={p.email}>
                      <span>{p.email}</span> <span className="kc-hint">{answerText(p.status)}</span>
                      <button type="button" className="btn-ghost btn-danger btn-sm kc-push" aria-label={`Remove ${p.email}`} onClick={() => setPeople(people.filter((x) => x !== p))}>Remove</button>
                    </li>
                  ))}
                </ul>
              )}
              <div className="kc-invite">
                <input id="kc-invite" list="kc-invite-people" inputMode="email" autoComplete="off" placeholder="name@example.com" maxLength={320} value={invitee} onChange={(e) => setInvitee(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); invite(); } }} />
                <button type="button" className="btn-secondary" onClick={invite}>Invite</button>
                <datalist id="kc-invite-people">
                  {suggestions.map((p) => <option key={p.email} value={p.email}>{p.name}</option>)}
                </datalist>
              </div>
              <FreeBusyStrip emails={people.map((p) => p.email)} start={form.start} end={form.end} allDay={form.allDay} />
            </>
          )}
          {canMeet && scope === 'all' && (
            <>
              <label className="kc-check"><input type="checkbox" role="switch" checked={meeting} onChange={(e) => setMeetingChoice(e.target.checked)} /> Video meeting</label>
              <p className="kc-hint">People outside your organisation cannot join yet.</p>
            </>
          )}
          <div className="modal-actions">
            {event && <button type="button" className="btn-danger push-start" disabled={busy} onClick={remove}>Delete</button>}
            <button type="button" className="btn-secondary" onClick={onClose}>Cancel</button>
            <button type="submit" disabled={busy}>Save</button>
          </div>
        </form>
      )}
    </dialog>
  );
}
