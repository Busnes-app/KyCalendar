import { useCallback, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import FullCalendar from '@fullcalendar/react';
import { ChevronDown, Plus } from 'lucide-react';
import dayGridPlugin from '@fullcalendar/daygrid';
import timeGridPlugin from '@fullcalendar/timegrid';
import listPlugin from '@fullcalendar/list';
import interactionPlugin from '@fullcalendar/interaction';
import type { DatesSetArg, DateSelectArg, EventClickArg, EventDropArg, EventInput } from '@fullcalendar/core';
import type { EventResizeDoneArg } from '@fullcalendar/interaction';
import { ApiError, browserZone, canAddEvents, listCalendars, listEvents, updateEvent, type CalendarInfo, type EventInfo } from '../calendarApi';
import { calendarColors, textOn } from '../calendarColors';
import { CalendarSidebar } from '../components/CalendarSidebar';
import { EventDialog } from '../components/EventDialog';
import { useScrollCue } from '../components/AppHeader';
import { ScopeDialog } from '../components/ScopeDialog';
import { WEEKDAYS, addDays, bodyFromForm, emptyForm, formFromEvent, localDate, localDateTime, parseLocal, validZone, type FormState } from '../eventForm';
import '../styles/calendar.css';

type MoveArg = EventDropArg | EventResizeDoneArg;
const HOUR = 3_600_000;

// shiftBy applies a FullCalendar delta: years, months and days on the local calendar, then milliseconds.
export function shiftBy(d: Date, delta: { years: number; months: number; days: number; milliseconds: number }): Date {
  return new Date(d.getFullYear() + delta.years, d.getMonth() + delta.months, d.getDate() + delta.days, d.getHours(), d.getMinutes(), d.getSeconds(), d.getMilliseconds() + delta.milliseconds);
}

// shiftRepeat is the rule for a series whose start moves `shift` weekdays (null: same date).
// The stored rule is kept ('custom') unless the date moves: weekly weekdays then rotate, a
// rule derived from DTSTART follows it, and a custom rule cannot move (null).
export function shiftRepeat(f: Pick<FormState, 'freq' | 'weekdays'>, shift: number | null): Pick<FormState, 'freq' | 'weekdays'> | null {
  if (shift === null) return { freq: 'custom', weekdays: f.weekdays };
  if (f.freq === 'custom') return null;
  if (f.freq !== 'weekly' || f.weekdays.length === 0) return { freq: 'custom', weekdays: f.weekdays };
  const n = ((shift % 7) + 7) % 7;
  return { freq: 'weekly', weekdays: f.weekdays.map((d) => WEEKDAYS[(WEEKDAYS.indexOf(d) + n) % 7]) };
}

// intlZone returns zone if Intl can resolve it (a Windows name cannot), else null.
export function intlZone(zone: string): string | null {
  try {
    new Intl.DateTimeFormat('en', { timeZone: zone });
    return zone;
  } catch {
    return null;
  }
}

// dayNumber is d's calendar date in zone (browser-local when undefined) as consecutive days.
export function dayNumber(d: Date, zone?: string): number {
  const p = Object.fromEntries(new Intl.DateTimeFormat('en-US', { timeZone: zone, year: 'numeric', month: 'numeric', day: 'numeric' }).formatToParts(d).map((x) => [x.type, x.value]));
  return Date.UTC(Number(p.year), Number(p.month) - 1, Number(p.day)) / 86_400_000;
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

const eventId = (ev: EventInfo) => `${ev.calendar_id}|${ev.uid}|${ev.recurrence_id ?? ''}`;

// toFcEvent hands FullCalendar an API instance; titles stay text (FullCalendar escapes them).
export function toFcEvent(ev: EventInfo, color: string): EventInput {
  return {
    id: eventId(ev),
    title: ev.title || '(no title)',
    start: ev.start,
    end: ev.end,
    allDay: ev.all_day,
    editable: ev.editable && !ev.partial,
    backgroundColor: color,
    borderColor: color,
    textColor: textOn(color),
    extendedProps: { info: ev },
  };
}

const NARROW = '(max-width: 720px)';
const narrow = () => typeof window.matchMedia === 'function' && window.matchMedia(NARROW).matches;

// useNarrow follows the phone breakpoint, where the calendar list folds into a header disclosure.
function useNarrow() {
  const [isNarrow, setNarrow] = useState(narrow);
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return;
    const m = window.matchMedia(NARROW);
    const sync = () => setNarrow(m.matches);
    m.addEventListener('change', sync);
    return () => m.removeEventListener('change', sync);
  }, []);
  return isNarrow;
}
// Day and week views fill the window below the page heading and scroll inside it, opening on
// the working day; the night hours stay a scroll away.
const TIME_GRID_HEIGHT = 'max(30rem, calc(100vh - 8rem))';
const SCROLL_TIME = '07:00:00';

// scheduling and me (the user's email) let the event dialog offer invitations. sidebarSlot is the
// app sidebar's slot: the calendar list always renders there, never as a second column.
export function CalendarPage({ scheduling = false, me = '', sidebarSlot = null }: { scheduling?: boolean; me?: string; sidebarSlot?: HTMLElement | null }) {
  const [calendars, setCalendars] = useState<CalendarInfo[]>([]);
  const [hidden, setHidden] = useState<Set<string>>(loadHidden);
  const [error, setError] = useState<string | null>(null);
  // Event fetch state, so an empty view never claims "no events" while loading or after a failure.
  const [loadingEvents, setLoadingEvents] = useState(false);
  const [eventsFailed, setEventsFailed] = useState(false);
  const [dialog, setDialog] = useState<{ key: number; event?: EventInfo; initial: FormState } | null>(null);
  const [pending, setPending] = useState<MoveArg | null>(null);
  const dragError = useRef<string | null>(null);
  const opens = useRef(0);
  const ref = useRef<FullCalendar>(null);
  const calendarElement = useRef<HTMLDivElement>(null);
  const newEvent = useRef<HTMLButtonElement>(null);
  const focusAfterReload = useRef<string | null>(null);
  const [timeGrid, setTimeGrid] = useState(() => !narrow());
  const isNarrow = useNarrow();
  const [picker, setPicker] = useState(false);
  const summary = useRef<HTMLElement>(null);
  // The hour grid scrolls under its sticky day headings and all-day strip; faded edges say so.
  const hourCue = useScrollCue();
  const datesSet = useCallback((arg: DatesSetArg) => {
    setTimeGrid(arg.view.type.startsWith('timeGrid'));
    // Day and Week reuse FullCalendar's time-grid DOM, without another viewDidMount call.
    requestAnimationFrame(() => {
      hourCue(calendarElement.current?.querySelector<HTMLElement>('.fc-timegrid .fc-scrollgrid-section-liquid .fc-scroller') ?? null);
      const grid = calendarElement.current?.querySelector<HTMLElement>('.fc-timegrid');
      if (!grid) return;
      if (arg.view.type === 'timeGridWeek') {
        grid.tabIndex = 0;
        grid.setAttribute('role', 'region');
        grid.setAttribute('aria-label', 'Week calendar');
      } else {
        grid.removeAttribute('tabindex');
        grid.removeAttribute('role');
        grid.removeAttribute('aria-label');
      }
    });
  }, [hourCue]);
  // A view switched from month or list renders before its fixed height applies, so FullCalendar's
  // own scrollTime finds nothing to scroll: open on the working day once the height is in place.
  useEffect(() => {
    if (timeGrid) ref.current?.getApi().scrollToTime(SCROLL_TIME);
  }, [timeGrid]);

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
  // Refetch on every close: after a 412 the cached etag is stale and reopening would conflict again.
  const closeDialog = () => {
    focusAfterReload.current = dialog?.event ? eventId(dialog.event) : null;
    setDialog(null);
    refetch();
  };

  // Refetch replaces event nodes. Restore the same logical event after rendering, or New event
  // after deletion, instead of focusing the dialog's detached original opener.
  const restoreEventFocus = useCallback(() => {
    const id = focusAfterReload.current;
    if (id === null) return;
    requestAnimationFrame(() => {
      if (focusAfterReload.current !== id) return;
      focusAfterReload.current = null;
      const events = calendarElement.current?.querySelectorAll<HTMLElement>('[data-event-id]');
      const target = Array.from(events ?? []).find((el) => el.dataset.eventId === id);
      (target ?? newEvent.current)?.focus({ preventScroll: true });
    });
  }, []);

  const writable = calendars.filter(canAddEvents);

  // Stable identity: FullCalendar re-fetches whenever this function changes.
  const fetchEvents = useCallback(
    (info: { start: Date; end: Date }, success: (e: EventInput[]) => void, failure: (e: Error) => void) => {
      const visible = calendars.filter((c) => !hidden.has(c.id)).map((c) => c.id);
      if (visible.length === 0) {
        success([]);
        return;
      }
      const colors = calendarColors(calendars);
      const colorOf = (id: string) => colors.get(id) ?? 'var(--ky-accent)';
      listEvents(info.start, info.end, browserZone(), visible)
        .then((evs) => {
          setError(null);
          setEventsFailed(false);
          success(evs.map((ev) => toFcEvent(ev, colorOf(ev.calendar_id))));
          restoreEventFocus();
        })
        .catch((e: Error) => {
          setError(e.message || 'Could not load events.');
          setEventsFailed(true);
          failure(e);
          restoreEventFocus();
        });
    },
    [calendars, hidden, restoreEventFocus],
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
    focusAfterReload.current = null;
    const target = writable.find((c) => !hidden.has(c.id)) ?? writable[0];
    setDialog({ key: ++opens.current, initial: emptyForm(start, end, allDay, target.id) });
  };
  const openEvent = (ev: EventInfo) => {
    focusAfterReload.current = null;
    setDialog({ key: ++opens.current, event: ev, initial: formFromEvent(ev, 'this') });
  };

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
    let repeat = { freq: base.freq, weekdays: base.weekdays };
    if (scope === 'all' && allDay === ev.all_day) {
      // Apply FullCalendar's calendar deltas to the series' wall-clock times (DST-safe).
      const [ds, de] = 'delta' in arg ? [arg.delta, arg.delta] : [arg.startDelta, arg.endDelta];
      const seriesStart = parseLocal(base.start);
      start = shiftBy(seriesStart, ds);
      end = shiftBy(allDay ? addDays(parseLocal(base.end), 1) : parseLocal(base.end), de);
      if (ev.recurring) {
        // The server reads DTSTART and BYDAY in the series' zone; all-day dates are local. Without
        // a TZID the series is UTC or floating (browser-local) and the API can't say which, so
        // both readings must agree.
        const zones: (string | null | undefined)[] = allDay ? [undefined] : ev.zone ? [intlZone(ev.zone)] : ['UTC', undefined];
        const unknown = zones.includes(null) && start.getTime() !== seriesStart.getTime();
        const shifts = zones.map((z) => (z === null ? 0 : dayNumber(start, z) - dayNumber(seriesStart, z)));
        const r = unknown || shifts.some((s) => s !== shifts[0]) ? null : shiftRepeat(base, shifts[0] || null);
        if (!r) {
          fail('Change the days of a repeating event in the event form.');
          return;
        }
        repeat = r;
      }
    }
    const form = {
      ...base,
      ...repeat,
      allDay,
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

  // One portal target at every width, so crossing the breakpoint never remounts the list and its
  // half-filled forms. Wide screens force the disclosure open and hide its summary.
  const sidebar = (
    <details
      className="kc-picker"
      open={!isNarrow || picker}
      // Escape inside a dialog the list opened closes only that dialog (React events bubble out of portals).
      onKeyDown={(e) => { if (e.key === 'Escape' && isNarrow && picker && !(e.target as Element).closest('dialog, [popover]')) { setPicker(false); summary.current?.focus(); } }}
    >
      <summary ref={summary} onClick={(e) => { e.preventDefault(); setPicker(!picker); }}>
        Calendars
        <ChevronDown size={16} aria-hidden="true" />
      </summary>
      {/* Using the list keeps it open when the screen narrows. */}
      <div className="kc-picker-panel" onFocus={() => setPicker(true)}>
        <CalendarSidebar calendars={calendars} hidden={hidden} onToggle={toggle} onChanged={loadCalendars} />
      </div>
    </details>
  );

  return (
    <section className="kc-layout">
      <div className="kc-calendar" ref={calendarElement}>
        {error && <p role="alert">{error}</p>}
        <div className="kc-workspace-heading"><h1>Calendar</h1>
        <button ref={newEvent} type="button" className="kc-new" disabled={writable.length === 0} onClick={() => { const s = new Date(); s.setMinutes(0, 0, 0); s.setHours(s.getHours() + 1); openCreate(s, new Date(s.getTime() + 3600_000), false); }}>
          <Plus size={16} aria-hidden="true" />
          New event
        </button>
        <p>Times shown in {browserZone()}<span role="status">{loadingEvents ? ' · Loading events…' : ''}</span></p></div>
        <p className="kc-week-hint">Scroll across the week to see other days.</p>
        <FullCalendar
          ref={ref}
          plugins={[dayGridPlugin, timeGridPlugin, listPlugin, interactionPlugin]}
          initialView={narrow() ? 'listWeek' : 'timeGridWeek'}
          headerToolbar={{ left: 'prev,next today', center: 'title', right: 'dayGridMonth,timeGridWeek,timeGridDay,listWeek' }}
          timeZone="local"
          height={timeGrid ? TIME_GRID_HEIGHT : 'auto'}
          scrollTime={SCROLL_TIME}
          // Compact ranges ("9 – 9:20"): the hour axis already says am or pm.
          views={{ timeGrid: { dayHeaderFormat: { weekday: 'short' }, dayHeaderContent: (arg) => <span className="kc-day-head">{arg.text}<b>{arg.date.getDate()}</b></span>, eventMinHeight: 20, eventTimeFormat: { hour: 'numeric', minute: '2-digit', omitZeroMinute: true, meridiem: false } } }}
          defaultRangeSeparator=" – "
          datesSet={datesSet}
          loading={setLoadingEvents}
          // Grid events are focusable anchors without href; as buttons their whole text (time and
          // title, including visually clipped content) is their accessible name.
          eventDidMount={(arg) => {
            if (arg.el.tagName === 'A') arg.el.setAttribute('role', 'button');
            const target = arg.el.querySelector<HTMLElement>('a, [tabindex]') ?? arg.el;
            target.dataset.eventId = arg.event.id;
          }}
          noEventsContent={loadingEvents ? 'Loading events…' : eventsFailed ? 'Events could not be loaded.' : 'No events to display'}
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
            onClose={closeDialog}
            onDone={closeDialog}
            scheduling={scheduling}
            me={me}
          />
        )}
      </div>
      {sidebarSlot && createPortal(sidebar, sidebarSlot)}
    </section>
  );
}
