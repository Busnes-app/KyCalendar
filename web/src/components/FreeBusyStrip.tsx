import { useEffect, useState } from 'react';
import { freeBusy, type PersonBusy } from '../calendarApi';
import { addDays, parseLocal } from '../eventForm';

const DAY = 1440; // minutes: the strip's width in SVG units

const time = (d: Date) => d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });

/**
 * The chosen day's busy times for guests the server knows (contract F), one row each, with the
 * chosen time marked and a guest busy during it called out. Times only; nothing on failure.
 */
export function FreeBusyStrip({ emails, start, end, allDay }: { emails: string[]; start: string; end: string; allDay: boolean }) {
  const day = parseLocal(start.slice(0, 10));
  const dayEnd = addDays(day, 1);
  const [rows, setRows] = useState<PersonBusy[]>([]);
  const key = emails.join(',');
  const dayKey = day.getTime();

  useEffect(() => {
    if (!key) {
      setRows([]);
      return;
    }
    let live = true;
    const t = setTimeout(() => {
      freeBusy(key.split(','), new Date(dayKey), addDays(new Date(dayKey), 1))
        .then((p) => { if (live) setRows(p.filter((r) => !r.unknown)); })
        .catch(() => { if (live) setRows([]); });
    }, 300);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [key, dayKey]);

  if (rows.length === 0) return null;
  const chosenStart = allDay ? day : parseLocal(start);
  const chosenEnd = allDay ? addDays(parseLocal(end.slice(0, 10)), 1) : parseLocal(end);
  const minutes = (d: Date) => Math.max(0, Math.min(DAY, (d.getTime() - day.getTime()) / 60000));
  const valid = !Number.isNaN(chosenStart.getTime()) && !Number.isNaN(chosenEnd.getTime());

  return (
    <div className="kc-freebusy" role="group" aria-label={`Free and busy on ${day.toLocaleDateString()}`}>
      {rows.map((p) => {
        const busy = p.busy.map((b) => ({ ...b, s: new Date(b.start), e: new Date(b.end) })).filter((b) => b.e > day && b.s < dayEnd);
        const conflict = valid && busy.some((b) => b.s < chosenEnd && b.e > chosenStart);
        return (
          <div key={p.email} className="kc-fb-row" data-conflict={conflict || undefined}>
            <span className="kc-fb-who">{p.email}</span>
            <svg className="kc-fb-track" viewBox={`0 0 ${DAY} 12`} preserveAspectRatio="none" aria-hidden="true">
              {busy.map((b, i) => <rect key={i} className={b.tentative ? 'kc-fb-tentative' : 'kc-fb-busy'} x={minutes(b.s)} width={Math.max(4, minutes(b.e) - minutes(b.s))} y="0" height="12" />)}
              {valid && <rect className="kc-fb-chosen" x={minutes(chosenStart)} width={Math.max(4, minutes(chosenEnd) - minutes(chosenStart))} y="0" height="12" />}
            </svg>
            <span className="kc-hint">
              {conflict ? <strong>Busy at this time</strong> : 'Free at this time'}
              {busy.length > 0 && ` · busy ${busy.map((b) => `${time(b.s)}–${time(b.e)}${b.tentative ? ' (maybe)' : ''}`).join(', ')}`}
            </span>
          </div>
        );
      })}
    </div>
  );
}
