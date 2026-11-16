import { useEffect, useState, type ReactNode } from 'react';
import { X } from 'lucide-react';
import { findResources, type Bookable, type EventInfo, type ResourceRef } from '../calendarApi';

const KIND = { room: 'Room', equipment: 'Equipment' } as const;

function minutes(detail?: string): string {
  const m = Number(detail);
  if (!m) return '';
  return m % 60 === 0 ? `${m / 60} h` : `${m} min`;
}

/** What KyCalendar answered for a room, in words. */
export function answerText(r: ResourceRef): string {
  if (r.status === 'accepted') return 'Booked';
  if (r.status === 'pending') return 'Waiting for approval';
  switch (r.reason) {
    case 'busy': return `Declined: already booked on ${r.detail}`;
    case 'not_allowed': return 'Declined: you may not book it';
    case 'disabled': return 'Declined: it cannot be booked now';
    case 'beyond_horizon': return `Declined: it can be booked only until ${r.detail}`;
    case 'too_long': return `Declined: bookings last at most ${minutes(r.detail)}`;
    case 'past': return 'Declined: the event is over';
    case 'too_many_occurrences': return 'Declined: too many occurrences to check';
    case 'rejected': return 'Declined by an approver';
    default: return 'Declined';
  }
}

/** One chosen room: its name (whole), the event's place only when Location does not already say it, and the answer. */
function RoomRow({ r, place, state, children }: { r: ResourceRef; place: string; state: string; children?: ReactNode }) {
  const where = r.location && !place.toLowerCase().includes(r.location.toLowerCase()) ? `, ${r.location}` : '';
  return (
    <li className="kc-room">
      <span className="kc-room-name">{r.name || 'Room'}{where}</span>
      <span className="kc-room-state">{state}</span>
      {children}
    </li>
  );
}

/** The event's rooms, read only: on an invitation or an event the user cannot edit. */
export function RoomList({ rooms, place = '' }: { rooms: ResourceRef[]; place?: string }) {
  if (rooms.length === 0) return null;
  return (
    <>
      <h3 className="kc-label" id="kc-rooms-view">Rooms and equipment</h3>
      <ul className="kc-people" aria-labelledby="kc-rooms-view">
        {rooms.map((r) => <RoomRow key={r.id} r={r} place={place} state={answerText(r)} />)}
      </ul>
    </>
  );
}

interface Props {
  rooms: ResourceRef[];
  onChange: (rooms: ResourceRef[]) => void;
  start: Date;
  end: Date;
  event?: EventInfo;
  repeats: boolean;
  place: string;
}

/** The event's rooms with Add room: a finder of the rooms free at the event's time that the user may book. */
export function RoomPicker({ rooms, onChange, start, end, event, repeats, place }: Props) {
  const [open, setOpen] = useState(false);
  const [found, setFound] = useState<Bookable[] | null>(null);
  const [failed, setFailed] = useState(false);
  const [capacity, setCapacity] = useState(0);
  const [tags, setTags] = useState<string[]>([]);
  const valid = !Number.isNaN(start.getTime()) && !Number.isNaN(end.getTime()) && end > start;
  const startKey = start.getTime();
  const endKey = end.getTime();

  useEffect(() => {
    if (!open || !valid) return;
    let live = true;
    setFound(null);
    setFailed(false);
    findResources(new Date(startKey), new Date(endKey), event)
      .then((rs) => { if (live) setFound(rs); })
      .catch(() => { if (live) setFailed(true); });
    return () => {
      live = false;
    };
  }, [open, valid, startKey, endKey, event]);

  const allTags = [...new Set((found ?? []).flatMap((r) => r.tags))].sort((a, b) => a.localeCompare(b));
  const matches = (found ?? []).filter((r) => !rooms.some((x) => x.id === r.id) && (capacity <= 0 || r.capacity >= capacity) &&
    tags.every((t) => r.tags.some((x) => x.toLowerCase() === t.toLowerCase())));
  const free = matches.filter((r) => r.free);
  const busy = matches.length - free.length;
  const filtered = capacity > 0 || tags.length > 0;

  function add(r: Bookable) {
    onChange([...rooms, { id: r.id, name: r.name, kind: r.kind, location: r.location, status: r.approval === 'auto' ? 'accepted' : 'pending' }]);
    setOpen(false);
  }

  return (
    <>
      <h3 className="kc-label" id="kc-rooms">Rooms and equipment</h3>
      {rooms.length > 0 && (
        <ul className="kc-people" aria-labelledby="kc-rooms">
          {rooms.map((r) => (
            <RoomRow key={r.id} r={r} place={place} state={event?.resources?.some((x) => x.id === r.id) ? answerText(r) : 'Checked when you save'}>
              <button type="button" className="btn-ghost btn-danger" aria-label={`Remove ${r.name ?? 'room'}`} onClick={() => onChange(rooms.filter((x) => x !== r))}>Remove</button>
            </RoomRow>
          ))}
        </ul>
      )}
      {!open ? (
        <p><button type="button" className="btn-secondary" disabled={!valid || rooms.length >= 10} onClick={() => setOpen(true)}>Add room</button></p>
      ) : (
        <div className="kc-room-finder" role="group" aria-labelledby="kc-finder-title">
          <div className="kc-finder-head">
            <span className="kc-label" id="kc-finder-title">Find a room</span>
            <button type="button" className="ky-icon-button" aria-label="Close room finder" title="Close room finder" onClick={() => setOpen(false)}><X aria-hidden="true" /></button>
          </div>
          <div className="kc-pair">
            <div className="field">
              <label htmlFor="kc-room-capacity">People</label>
              <input id="kc-room-capacity" type="number" min={0} max={10000} value={capacity || ''} placeholder="Any" onChange={(e) => setCapacity(Number(e.target.value))} />
            </div>
            {allTags.length > 0 && (
              <fieldset>
                <legend>Needs</legend>
                {allTags.map((t) => (
                  <label key={t} className="kc-check">
                    <input type="checkbox" checked={tags.includes(t)} onChange={(e) => setTags(e.target.checked ? [...tags, t] : tags.filter((x) => x !== t))} /> {t}
                  </label>
                ))}
              </fieldset>
            )}
          </div>
          {failed && <p role="alert">Could not load rooms. Try again in a moment.</p>}
          {!failed && found === null && <p className="kc-hint" role="status">Finding free rooms…</p>}
          {found !== null && (free.length === 0 ? (
            <p className="kc-hint" role="status">{found.length === 0 ? 'There are no rooms or equipment you can book.' : filtered && matches.length === 0 ? 'Nothing matches these filters.' : 'Nothing that matches is free at this time.'}</p>
          ) : (
            <ul className="kc-people kc-finder" aria-label="Free rooms and equipment">
              {free.map((r) => (
                <li key={r.id}>
                  <span>
                    <strong className="kc-room-name">{r.name}</strong>
                    <span className="kc-hint">
                      {r.approval === 'approvers' && <><strong>Needs approval</strong>{' · '}</>}
                      {[KIND[r.kind], r.location, r.capacity > 0 ? `${r.capacity} people` : '', r.tags.join(', ')].filter(Boolean).join(' · ')}
                    </span>
                  </span>
                  <button type="button" className="btn-secondary btn-sm kc-push" aria-label={`Add ${r.name}`} onClick={() => add(r)}>Add</button>
                </li>
              ))}
            </ul>
          ))}
          {busy > 0 && <p className="kc-hint">{busy === 1 ? '1 more is busy then.' : `${busy} more are busy then.`}</p>}
          {repeats && <p className="kc-hint">For a repeating event, KyCalendar checks every occurrence when you save.</p>}
        </div>
      )}
    </>
  );
}
