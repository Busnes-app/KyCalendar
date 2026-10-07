import { useCallback, useEffect, useRef, useState } from 'react';
import FullCalendar from '@fullcalendar/react';
import dayGridPlugin from '@fullcalendar/daygrid';
import timeGridPlugin from '@fullcalendar/timegrid';
import listPlugin from '@fullcalendar/list';
import interactionPlugin from '@fullcalendar/interaction';
import type { DateSelectArg, EventClickArg, EventDropArg, EventInput } from '@fullcalendar/core';
import type { EventResizeDoneArg } from '@fullcalendar/interaction';
import { ApiError, browserZone, listCalendars, listEvents, updateEvent, type CalendarInfo, type EventInfo } from '../calendarApi';
import { CalendarSidebar } from '../components/CalendarSidebar';
import { EventDialog } from '../components/EventDialog';
import { ScopeDialog } from '../components/ScopeDialog';
import { addDays, bodyFromForm, emptyForm, formFromEvent, localDate, localDateTime, parseLocal, validZone, type FormState } from '../eventForm';
import '../styles/calendar.css';

type MoveArg = EventDropArg | EventResizeDoneArg;
const HOUR = 3_600_000;

// shiftBy applies a FullCalendar delta: years, months and days on the local calendar, then milliseconds.
export function shiftBy(d: Date, delta: { years: number; months: number; days: number; milliseconds: number }): Date {
  return new Date(d.getFullYear() + delta.years, d.getMonth() + delta.months, d.getDate() + delta.days, d.getHours(), d.getMinutes(), d.getSeconds(), d.getMilliseconds() + delta.milliseconds);
}

export const HIDDEN_KEY = 'kycalendar.hiddenCalendars';

export function loadHidden(): Set<string> {
  try {
    const v = JSON.parse(localStorage.getItem(HIDDEN_KEY) ?? '[]');
    return new Set(Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []);
  } catch {
    return new Set();
  }
}

export function saveHidden(s: Set<string>) {
  try {
    localStorage.setItem(HIDDEN_KEY, JSON.stringify([...s]));
  } catch {
    // storage disabled or full: visibility still toggles for this session
  }
}

// toFcEvent hands FullCalendar an API instance; titles stay text (FullCalendar escapes them).
export function toFcEvent(ev: EventInfo, color: string): EventInput {
  return {
    id: `${ev.calendar_id}|${ev.uid}|${ev.recurrence_id ?? ''}`,
    title: ev.title || '(no title)',
    start: ev.start,
    end: ev.end,
    allDay: ev.all_day,
    editable: ev.editable && !ev.partial,
    backgroundColor: color,
    borderColor: color,
    extendedProps: { info: ev },
  };
}

const narrow = () => typeof window.matchMedia === 'function' && window.matchMedia('(max-width: 720px)').matches;

export function CalendarPage() {
  const [calendars, setCalendars] = useState<CalendarInfo[]>([]);
  const [hidden, setHidden] = useState<Set<string>>(loadHidden);
  const [error, setError] = useState<string | null>(null);
  const [dialog, setDialog] = useState<{ key: number; event?: EventInfo; initial: FormState } | null>(null);
  const [pending, setPending] = useState<MoveArg | null>(null);
  const dragError = useRef<string | null>(null);
  const opens = useRef(0);
  const ref = useRef<FullCalendar>(null);

  const loadCalendars = useCallback(async () => {
    try {
      setCalendars(await listCalendars());
      setError(null);
    } catch {
      setError('Could not load your calendars. Reload the page to try again.');
    }
  }, []);
  useEffect(() => {
    void loadCalendars();
  }, [loadCalendars]);

  const refetch = () => ref.current?.getApi().refetchEvents();

  const writable = calendars.filter((c) => c.role !== 'reader');

  // Stable identity: FullCalendar re-fetches whenever this function changes.
  const fetchEvents = useCallback(
    (info: { start: Date; end: Date }, success: (e: EventInput[]) => void, failure: (e: Error) => void) => {
      const visible = calendars.filter((c) => !hidden.has(c.id)).map((c) => c.id);
      if (visible.length === 0) {
        success([]);
        return;
      }
      const colorOf = (id: string) => calendars.find((c) => c.id === id)?.color || 'var(--ky-accent)';
      listEvents(info.start, info.end, browserZone(), visible)
        .then((evs) => {
          setError(null);
          success(evs.map((ev) => toFcEvent(ev, colorOf(ev.calendar_id))));
        })
        .catch((e: Error) => {
          setError(e.message || 'Could not load events.');
          failure(e);
        });
    },
    [calendars, hidden],
  );

  function toggle(id: string) {
    const next = new Set(hidden);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    saveHidden(next);
    setHidden(next);
  }

  const openCreate = (start: Date, end: Date, allDay: boolean) => {
    if (writable.length === 0) return;
    const target = writable.find((c) => !hidden.has(c.id)) ?? writable[0];
    setDialog({ key: ++opens.current, initial: emptyForm(start, end, allDay, target.id) });
  };
  const openEvent = (ev: EventInfo) => setDialog({ key: ++opens.current, event: ev, initial: formFromEvent(ev, 'this') });

  async function saveMove(arg: MoveArg, scope: 'this' | 'all') {
    const ev = arg.event.extendedProps.info as EventInfo;
    const zone = validZone(ev.zone) ? ev.zone! : browserZone();
    const allDay = arg.event.allDay;
    const fail = (msg: string) => {
      arg.revert();
      dragError.current = msg;
      setError(msg);
    };
    if (scope === 'all' && ev.recurring && allDay !== ev.all_day) {
      fail('Change all-day for a repeating event in the event form.');
      return;
    }
    // Ends here are exclusive. FullCalendar nulls end for all-day and zero-length events; an all-day event made timed gets an hour.
    const newStart = arg.event.start!;
    const newEnd = arg.event.end ?? (allDay ? addDays(newStart, 1) : arg.oldEvent.allDay ? new Date(newStart.getTime() + HOUR) : newStart);
    const base = formFromEvent(ev, scope);
    let start = newStart;
    let end = newEnd;
    if (scope === 'all' && allDay === ev.all_day) {
      // Apply FullCalendar's calendar deltas to the series' wall-clock times (DST-safe).
      const [ds, de] = 'delta' in arg ? [arg.delta, arg.delta] : [arg.startDelta, arg.endDelta];
      start = shiftBy(parseLocal(base.start), ds);
      end = shiftBy(allDay ? addDays(parseLocal(base.end), 1) : parseLocal(base.end), de);
    }
    const form = {
      ...base,
      allDay,
      freq: ev.recurring && scope === 'all' ? ('custom' as const) : base.freq,
      start: allDay ? localDate(start) : localDateTime(start),
      end: allDay ? localDate(addDays(end, -1)) : localDateTime(end),
    };
    const body = bodyFromForm(form, zone, ev.recurring ? scope : 'all', ev.recurrence_id);
    if (typeof body === 'string') {
      fail(body);
      return;
    }
    try {
      await updateEvent(ev, body);
      const mine = dragError.current;
      dragError.current = null;
      if (mine) setError((cur) => (cur === mine ? null : cur));
      refetch();
    } catch (e) {
      fail(e instanceof ApiError && e.status === 412 ? 'This event changed elsewhere; the calendar has been reloaded.' : (e as Error).message || 'Could not move the event');
      refetch();
    }
  }

  const onMove = (arg: MoveArg) => {
    if ((arg.event.extendedProps.info as EventInfo).recurring) setPending(arg);
    else void saveMove(arg, 'all');
  };

  return (
    <section className="page kc-layout">
      <CalendarSidebar calendars={calendars} hidden={hidden} onToggle={toggle} onChanged={loadCalendars} />
      <div className="kc-calendar">
        {error && <p role="alert">{error}</p>}
        <button type="button" disabled={writable.length === 0} onClick={() => { const s = new Date(); s.setMinutes(0, 0, 0); s.setHours(s.getHours() + 1); openCreate(s, new Date(s.getTime() + 3600_000), false); }}>
          New event
        </button>
        <FullCalendar
          ref={ref}
          plugins={[dayGridPlugin, timeGridPlugin, listPlugin, interactionPlugin]}
          initialView={narrow() ? 'listWeek' : 'timeGridWeek'}
          headerToolbar={{ left: 'prev,next today', center: 'title', right: 'dayGridMonth,timeGridWeek,timeGridDay,listWeek' }}
          timeZone="local"
          height="auto"
          nowIndicator
          editable
          eventInteractive
          selectable={writable.length > 0}
          selectMirror
          events={fetchEvents}
          select={(arg: DateSelectArg) => openCreate(arg.start, arg.end, arg.allDay)}
          eventClick={(arg: EventClickArg) => openEvent(arg.event.extendedProps.info as EventInfo)}
          eventDrop={onMove}
          eventResize={onMove}
        />
        {pending && (
          <ScopeDialog onChoose={(s) => { const p = pending; setPending(null); if (s) void saveMove(p, s); else p.revert(); }} />
        )}
        {dialog && (
          <EventDialog
            key={dialog.key}
            calendars={calendars}
            event={dialog.event}
            initial={dialog.initial}
            onClose={() => setDialog(null)}
            onDone={() => { setDialog(null); refetch(); }}
          />
        )}
      </div>
    </section>
  );
}
