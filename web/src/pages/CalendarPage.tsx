import { useCallback, useEffect, useRef, useState } from 'react';
import FullCalendar from '@fullcalendar/react';
import dayGridPlugin from '@fullcalendar/daygrid';
import timeGridPlugin from '@fullcalendar/timegrid';
import listPlugin from '@fullcalendar/list';
import interactionPlugin from '@fullcalendar/interaction';
import type { DateSelectArg, EventClickArg, EventDropArg, EventInput } from '@fullcalendar/core';
import type { EventResizeDoneArg } from '@fullcalendar/interaction';
import { browserZone, listCalendars, listEvents, type CalendarInfo, type EventInfo } from '../calendarApi';
import { CalendarSidebar } from '../components/CalendarSidebar';
import { EventDialog } from '../components/EventDialog';
import { emptyForm, formFromEvent, type FormState } from '../eventForm';
import '../styles/calendar.css';

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
  const [dialog, setDialog] = useState<{ event?: EventInfo; initial: FormState } | null>(null);
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

  // Task 6 also calls this after moves.
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
    setDialog({ initial: emptyForm(start, end, allDay, target.id) });
  };
  const openEvent = (ev: EventInfo) => setDialog({ event: ev, initial: formFromEvent(ev, 'this') });
  const onMove = (arg: EventDropArg | EventResizeDoneArg) => arg.revert();

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
        {dialog && (
          <EventDialog
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
