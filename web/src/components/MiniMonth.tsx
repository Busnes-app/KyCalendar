import { useEffect, useId, useRef, useState } from 'react';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { addDays } from '../eventForm';

const monthName = new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric' });
const dayName = new Intl.DateTimeFormat(undefined, { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric' });
const weekdayShort = new Intl.DateTimeFormat(undefined, { weekday: 'narrow' });
const weekdayLong = new Intl.DateTimeFormat(undefined, { weekday: 'long' });

const midnight = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate());
// addMonths keeps the day of the month where it exists (Jan 31 + 1 is Feb 28 or 29).
export function addMonths(d: Date, n: number): Date {
  const last = new Date(d.getFullYear(), d.getMonth() + n + 1, 0).getDate();
  return new Date(d.getFullYear(), d.getMonth() + n, Math.min(d.getDate(), last));
}

// The grid key moves of the WAI-ARIA date picker pattern; Shift+PageUp/PageDown page a year.
function moveBy(d: Date, key: string, shift: boolean): Date | null {
  switch (key) {
    case 'ArrowLeft': return addDays(d, -1);
    case 'ArrowRight': return addDays(d, 1);
    case 'ArrowUp': return addDays(d, -7);
    case 'ArrowDown': return addDays(d, 7);
    case 'Home': return addDays(d, -d.getDay());
    case 'End': return addDays(d, 6 - d.getDay());
    case 'PageUp': return addMonths(d, shift ? -12 : -1);
    case 'PageDown': return addMonths(d, shift ? 12 : 1);
    default: return null;
  }
}

// MiniMonth is the sidebar's month grid: today marked, the calendar's visible range [start, end)
// highlighted, ‹ › to page, and a click or Enter on a day hands that day to onPick. Weeks start
// on Sunday, like the main calendar.
export function MiniMonth({ range, onPick }: { range: { start: Date; end: Date } | null; onPick: (d: Date) => void }) {
  const today = midnight(new Date());
  // The roving-focus day; the month on show is its month.
  const [focus, setFocus] = useState(today);
  const keyed = useRef(false);
  const grid = useRef<HTMLTableElement>(null);
  const heading = useId();
  const start = range?.start.getTime();
  const end = range?.end.getTime() ?? 0;

  // Follow the main calendar: keep a day already in view, else today if in view, else the first.
  useEffect(() => {
    if (start === undefined) return;
    const inView = (t: number) => t >= start && t < end;
    const t = midnight(new Date()).getTime();
    setFocus((f) => (inView(f.getTime()) ? f : new Date(inView(t) ? t : start)));
  }, [start, end]);
  useEffect(() => {
    if (!keyed.current) return;
    keyed.current = false;
    grid.current?.querySelector<HTMLElement>('button[tabindex="0"]')?.focus();
  }, [focus]);

  const first = new Date(focus.getFullYear(), focus.getMonth(), 1);
  const gridStart = addDays(first, -first.getDay());
  // Six weeks always, so paging never changes the sidebar's height.
  const weeks = Array.from({ length: 6 }, (_, w) => Array.from({ length: 7 }, (_, d) => addDays(gridStart, w * 7 + d)));

  return (
    <section className="kc-mini" aria-labelledby={heading}>
      <div className="kc-mini-head">
        <h2 id={heading} aria-live="polite">{monthName.format(focus)}</h2>
        <button type="button" className="btn-ghost" aria-label="Previous month" onClick={() => setFocus(addMonths(focus, -1))}>
          <ChevronLeft size={16} aria-hidden="true" />
        </button>
        <button type="button" className="btn-ghost" aria-label="Next month" onClick={() => setFocus(addMonths(focus, 1))}>
          <ChevronRight size={16} aria-hidden="true" />
        </button>
      </div>
      <table
        ref={grid}
        role="grid"
        aria-labelledby={heading}
        onKeyDown={(e) => {
          const next = moveBy(focus, e.key, e.shiftKey);
          if (!next) return;
          e.preventDefault();
          keyed.current = true;
          setFocus(next);
        }}
      >
        <thead>
          <tr>
            {weeks[0].map((d) => (
              <th key={d.getDay()} scope="col" abbr={weekdayLong.format(d)}>{weekdayShort.format(d)}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {weeks.map((week) => (
            <tr key={week[0].getTime()}>
              {week.map((d) => {
                const t = d.getTime();
                const inRange = start !== undefined && t >= start && t < end;
                return (
                  <td key={t} role="gridcell" aria-selected={inRange} data-range={inRange || undefined} data-other={d.getMonth() !== focus.getMonth() || undefined}>
                    <button
                      type="button"
                      className="btn-ghost"
                      tabIndex={t === focus.getTime() ? 0 : -1}
                      aria-label={dayName.format(d)}
                      aria-current={t === today.getTime() ? 'date' : undefined}
                      onClick={() => { setFocus(d); onPick(d); }}
                    >
                      {d.getDate()}
                    </button>
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
