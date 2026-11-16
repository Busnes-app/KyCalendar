import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { CalendarDays, Check, ChevronLeft, ChevronRight, Lightbulb, Plus } from 'lucide-react';
import { browserZone, listCalendars, listEvents, type CalendarInfo, type EventInfo } from '../calendarApi';
import { listTasks, patchTask, taskStats, type Status, type Task, type TaskStats } from '../tasksApi';
import { dateOnly, localRFC3339 } from '../taskParse';
import { dueLabel, priorityName } from '../taskRepeat';
import { fit, gaps, hm, suggestions, SNAP, type Block } from '../dayPlan';
import { estimateText } from '../components/TaskDialog';

const MIN = 60000;
const HOUR_PX = 56;
const DEFAULT_MIN = 30;

export const lengthOf = (t: Task) => t.duration_min || t.estimate_min || DEFAULT_MIN;
const hourLabel = (ms: number) => new Date(ms).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit', hourCycle: 'h23' });
const clock = (ms: number) => new Date(ms).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit', hourCycle: 'h23' });
// placedAt is when a task is placed today (a start time on that day), else null.
export function placedAt(t: Task, day: string): number | null {
  if (!t.start || t.start.length === 10) return null;
  const s = new Date(t.start);
  return dateOnly(s) === day ? s.getTime() : null;
}

interface DayData {
  events: EventInfo[]; // today's timed events
  allDay: EventInfo[];
  open: Task[];
  doneToday: Task[];
  stats: TaskStats | null;
  calendars: CalendarInfo[];
}

// useDay loads one local day: events from every visible calendar, open tasks, today's completions.
export function useDay(reloadKey: number) {
  const [data, setData] = useState<DayData | null>(null);
  const [error, setError] = useState('');
  const [now, setNow] = useState(Date.now());
  useEffect(() => { const i = setInterval(() => setNow(Date.now()), 60000); return () => clearInterval(i); }, []);
  const load = useCallback(async () => {
    try {
      const d = new Date();
      const start = new Date(d.getFullYear(), d.getMonth(), d.getDate());
      const end = new Date(d.getFullYear(), d.getMonth(), d.getDate() + 1);
      const cals = await listCalendars();
      const evCals = cals.filter((c) => c.components.includes('VEVENT')).map((c) => c.id);
      const [events, open, done, stats] = await Promise.all([
        evCals.length ? listEvents(start, end, browserZone(), evCals) : Promise.resolve([] as EventInfo[]),
        listTasks({}), listTasks({ status: ['completed'], sort: 'created' }), taskStats().catch(() => null),
      ]);
      const today = dateOnly(start);
      setData({
        events: events.filter((e) => !e.all_day && !e.cancelled), allDay: events.filter((e) => e.all_day),
        open, doneToday: done.filter((t) => t.completed_at && dateOnly(new Date(t.completed_at)) === today), stats, calendars: cals,
      });
      setError('');
    } catch (e) {
      setError(e instanceof Error ? e.message : 'The day could not be loaded.');
    }
  }, []);
  useEffect(() => { void load(); }, [load, reloadKey]);
  return { data, error, now, reload: load };
}

// The planned day, derived: window, busy blocks, placed tasks, gaps, candidates.
export function useModel(data: DayData | null, now: number) {
  return useMemo(() => {
    if (!data) return null;
    const d = new Date(now);
    const today = dateOnly(d);
    const at = (h: number) => new Date(d.getFullYear(), d.getMonth(), d.getDate(), h).getTime();
    const events = data.events.map((e) => ({ e, start: new Date(e.start).getTime(), end: new Date(e.end).getTime() }));
    const placed = [...data.open, ...data.doneToday].map((t) => ({ t, start: placedAt(t, today) })).filter((x): x is { t: Task; start: number } => x.start !== null)
      .map((x) => ({ ...x, end: x.start + lengthOf(x.t) * MIN })).sort((a, b) => a.start - b.start);
    const earliest = Math.min(at(8), ...events.map((x) => x.start), ...placed.map((x) => x.start));
    const latest = Math.max(at(18), ...events.map((x) => x.end), ...placed.map((x) => x.end));
    const from = Math.max(at(0), Math.floor(earliest / 3600000) * 3600000);
    const to = Math.min(at(24), Math.ceil(latest / 3600000) * 3600000);
    const eventBlocks: Block[] = events.map((x) => ({ start: x.start, end: x.end }));
    const busy: Block[] = [...eventBlocks, ...placed.map((x) => ({ start: x.start, end: x.end }))];
    const free = gaps(from, to, busy);
    const freeAll = gaps(from, to, eventBlocks).reduce((n, g) => n + g.minutes, 0);
    const plannedMin = placed.reduce((n, x) => n + lengthOf(x.t), 0);
    const placedKeys = new Set(placed.map((x) => key(x.t)));
    const due = (t: Task) => (t.due ? (t.due.length === 10 ? t.due : dateOnly(new Date(t.due))) : '');
    const rank = (t: Task) => (due(t) && due(t) < today ? 0 : due(t) === today ? 1 : 2);
    const candidates = data.open.filter((t) => !placedKeys.has(key(t)) && t.editable)
      .sort((a, b) => rank(a) - rank(b) || (due(a) || '9').localeCompare(due(b) || '9') || (a.priority || 10) - (b.priority || 10));
    const todays = new Map<string, Task>();
    for (const x of placed) todays.set(key(x.t), x.t);
    for (const t of data.open) if (rank(t) < 2) todays.set(key(t), t);
    for (const t of data.doneToday) todays.set(key(t), t);
    const all = [...todays.values()];
    const done = all.filter((t) => t.status === 'COMPLETED').length;
    const leftMin = all.filter((t) => t.status !== 'COMPLETED').reduce((n, t) => n + lengthOf(t), 0);
    return { today, from, to, events, placed, free, freeAll, plannedMin, candidates, total: all.length, done, leftMin, eventBlocks };
  }, [data, now]);
}
export type Model = NonNullable<ReturnType<typeof useModel>>;
export const key = (t: Task) => `${t.calendar_id}/${t.uid}`;

export async function place(t: Task, start: number, minutes = lengthOf(t)) {
  return patchTask(t, { start: localRFC3339(new Date(start)), duration_min: minutes });
}

function useSuggestion(m: Model, now: number) {
  const list = useMemo(() => suggestions(m.free, m.candidates.map((t) => ({ id: key(t), minutes: lengthOf(t) })), now), [m, now]);
  const [i, setI] = useState(0);
  const s = list.length ? list[Math.min(i, list.length - 1)] : null;
  const task = s ? m.candidates.find((t) => key(t) === s.id) : undefined;
  return { list, i: Math.min(i, Math.max(0, list.length - 1)), setI, s, task };
}

function SuggestionLine({ m, now, onDone, setError }: { m: Model; now: number; onDone: () => void; setError: (e: string) => void }) {
  const { list, i, setI, s, task } = useSuggestion(m, now);
  if (!s || !task) return null;
  return (
    <div className="td-suggest" role="status">
      <Lightbulb aria-hidden="true" />
      <span>{hm(s.gap)} free at {clock(s.start)} fits <strong>{task.title}</strong> ({estimateText(lengthOf(task))})</span>
      <button type="button" className="btn-ghost btn-sm" onClick={() => void place(task, s.start).then(onDone, (e) => setError(e.message))}>Place it</button>
      {list.length > 1 && (
        <span className="td-stepper">
          <button type="button" className="btn-ghost kc-icon" aria-label="Previous suggestion" disabled={i === 0} onClick={() => setI(i - 1)}><ChevronLeft size={14} aria-hidden="true" /></button>
          <span aria-label={`Suggestion ${i + 1} of ${list.length}`}>{i + 1}/{list.length}</span>
          <button type="button" className="btn-ghost kc-icon" aria-label="Next suggestion" disabled={i >= list.length - 1} onClick={() => setI(i + 1)}><ChevronRight size={14} aria-hidden="true" /></button>
        </span>
      )}
    </div>
  );
}

// DoneBox completes or reopens a task; the full status list is in the task's detail.
function DoneBox({ t, onChange }: { t: Task; onChange: (s: Status) => void }) {
  const done = t.status === 'COMPLETED';
  return (
    <input type="checkbox" className="tk-check" checked={done} disabled={!t.editable} aria-label={`${done ? 'Reopen' : 'Complete'} ${t.title}`}
      onChange={() => onChange(done ? 'NEEDS-ACTION' : 'COMPLETED')} />
  );
}

// TodayView: progress, the Next card, the day in time order, and what is not planned yet.
export function TodayView({ m, data, now, listName, onOpen, onPlan, reload }: {
  m: Model; data: DayData; now: number; listName: (id: string) => string; onOpen: (t: Task) => void; onPlan: () => void; reload: () => void;
}) {
  const [error, setError] = useState('');
  const run = (p: Promise<unknown>) => void p.then(reload, (e) => setError(e instanceof Error ? e.message : 'Could not save'));
  const next = m.placed.find((x) => x.t.status !== 'COMPLETED' && x.end > now);
  const pushLater = (x: { t: Task; start: number; end: number }) => {
    const others = [...m.eventBlocks, ...m.placed.filter((p) => p !== x).map((p) => ({ start: p.start, end: p.end }))];
    const s = fit(gaps(m.from, m.to, others), lengthOf(x.t), Math.max(now, x.end));
    if (s === null) setError('No later gap fits it today.'); else run(place(x.t, s));
  };
  type Row = { at: number; kind: 'event'; e: EventInfo; end: number } | { at: number; kind: 'task'; t: Task; end: number };
  // The Next task shows only in its card; tasks done before now fold into one line.
  const doneEarlier = m.placed.filter((x) => x.t.status === 'COMPLETED' && x.start < now);
  const rows: Row[] = [
    ...m.events.map((x) => ({ at: x.start, kind: 'event' as const, e: x.e, end: x.end })),
    ...m.placed.filter((x) => x !== next && !doneEarlier.includes(x)).map((x) => ({ at: x.start, kind: 'task' as const, t: x.t, end: x.end })),
  ].sort((a, b) => a.at - b.at);
  const nowIndex = rows.findIndex((r) => r.at > now);
  const unplanned = m.candidates.filter((t) => { const d = dueLabel(t.due, new Date(now)); return d && (d.overdue || d.today); });
  const pct = m.total ? Math.round((m.done / m.total) * 100) : 0;
  const nowLine = (
    <li className="td-now" aria-label={`Now, ${clock(now)}`}><span>{clock(now)}</span></li>
  );
  return (
    <div className="td">
      <div className="td-progress">
        <p><strong>{m.done} of {m.total} done</strong> · {hm(m.leftMin)} left{data.stats && <> · {data.stats.done_this_week} done this week{data.stats.streak > 1 && ` · ${data.stats.streak}-day streak`}</>}</p>
        <progress max={Math.max(1, m.total)} value={m.done} aria-label={`${pct}% of today's tasks done`} />
        <div className="td-progress-actions">
          <button type="button" className="btn-secondary btn-sm" onClick={onPlan}>Plan my day</button>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      {next && (
        <section className="td-next" aria-label="Next">
          <div>
            <p className="td-next-when">{next.start <= now ? 'Now' : 'Next'} · {clock(next.start)} – {clock(next.end)}</p>
            <h2>{next.t.title}</h2>
            <p className="td-next-meta">{listName(next.t.calendar_id)}{(() => { const e = m.events.find((x) => x.start >= next.end); return e ? ` · ${e.e.title} at ${clock(e.start)}` : ''; })()}</p>
          </div>
          <div className="td-next-actions">
            <button type="button" className="btn-ghost" onClick={() => pushLater(next)} disabled={!next.t.editable}>Push to later</button>
            <button type="button" onClick={() => run(patchTask(next.t, { status: 'COMPLETED' }))} disabled={!next.t.editable}><Check size={16} aria-hidden="true" /> Mark done</button>
          </div>
        </section>
      )}
      {rows.length === 0 && <p className="td-empty">Nothing planned yet. <button type="button" className="btn-ghost btn-sm" onClick={onPlan}>Plan my day</button></p>}
      <ol className="td-rows" aria-label="Today in time order">
        {doneEarlier.length > 0 && (
          <li className="td-earlier">
            <details>
              <summary>{doneEarlier.length} done earlier</summary>
              <ul>
                {doneEarlier.map((x) => (
                  <li key={key(x.t)} className="td-task" data-done>
                    <span className="td-time">{clock(x.start)} · {hm(lengthOf(x.t))}</span>
                    <span className="td-task-line">
                      <DoneBox t={x.t} onChange={(st) => run(patchTask(x.t, { status: st }))} />
                      <button type="button" className="td-task-title" onClick={() => onOpen(x.t)}>{x.t.title}</button>
                    </span>
                  </li>
                ))}
              </ul>
            </details>
          </li>
        )}
        {rows.map((r, i) => {
          const line = i === nowIndex ? nowLine : null;
          if (r.kind === 'event') {
            return [line, (
              <li key={`e-${r.e.calendar_id}-${r.e.uid}-${r.e.recurrence_id ?? ''}`} className="td-event">
                <span className="td-time">{clock(r.at)}–{clock(r.end)}</span>
                <span className="td-event-title"><CalendarDays size={15} aria-hidden="true" /> {r.e.title || 'Busy'}</span>
              </li>
            )];
          }
          const prio = priorityName(r.t.priority);
          return [line, (
            <li key={`t-${key(r.t)}`} className="td-task" data-done={r.t.status === 'COMPLETED' || undefined}>
              <span className="td-time">{clock(r.at)} · {hm(lengthOf(r.t))}</span>
              <span className="td-task-line">
                <DoneBox t={r.t} onChange={(s) => run(patchTask(r.t, { status: s }))} />
                <button type="button" className="td-task-title" onClick={() => onOpen(r.t)}>
                  {r.t.title}
                  {prio === 'High' && <span className="td-prio">High</span>}
                  {r.t.status === 'IN-PROCESS' && <span className="td-state">In progress</span>}
                </button>
              </span>
            </li>
          )];
        })}
        {nowIndex === -1 && rows.length > 0 && nowLine}
      </ol>
      {unplanned.length > 0 && (
        <section className="td-unplanned" aria-labelledby="td-unplanned">
          <h2 id="td-unplanned">Due today, not planned</h2>
          <ul>
            {unplanned.map((t) => {
              const d = dueLabel(t.due, new Date(now));
              return (
                <li key={key(t)} className="td-task">
                  <span className={d?.overdue ? 'td-time tk-late' : 'td-time'}>{d?.overdue ? 'Overdue' : 'Today'}</span>
                  <span className="td-task-line">
                    <DoneBox t={t} onChange={(s) => run(patchTask(t, { status: s }))} />
                    <button type="button" className="td-task-title" onClick={() => onOpen(t)}>{t.title}{priorityName(t.priority) === 'High' && <span className="td-prio">High</span>}</button>
                  </span>
                </li>
              );
            })}
          </ul>
        </section>
      )}
    </div>
  );
}

// PlanView: the day's timeline (events read-only, placed tasks movable) beside the candidates.
export function PlanView({ m, now, listName, isNarrow, onOpen, onDone, reload }: {
  m: Model; now: number; listName: (id: string) => string; isNarrow: boolean; onOpen: (t: Task) => void; onDone: () => void; reload: () => void;
}) {
  const [error, setError] = useState('');
  const [tab, setTab] = useState<'plan' | 'candidates'>('plan');
  const [more, setMore] = useState(false);
  const [drag, setDrag] = useState<{ k: string; start: number; minutes: number } | null>(null);
  const grid = useRef<HTMLDivElement>(null);
  const run = (p: Promise<unknown>) => void p.then(reload, (e) => setError(e instanceof Error ? e.message : 'Could not save'));
  const y = (ms: number) => ((ms - m.from) / 3600000) * HOUR_PX;
  const timeAt = (clientY: number) => {
    const r = grid.current!.getBoundingClientRect();
    const ms = m.from + ((clientY - r.top) / HOUR_PX) * 3600000;
    return Math.max(m.from, Math.min(m.to - SNAP * MIN, Math.round(ms / (SNAP * MIN)) * SNAP * MIN));
  };
  const hours: number[] = [];
  for (let t = m.from; t <= m.to; t += 3600000) hours.push(t);
  const addFirst = (t: Task) => {
    const s = fit(m.free, lengthOf(t), now > m.from ? now : m.from);
    if (s === null) setError(`No free gap fits ${estimateText(lengthOf(t))} today.`); else run(place(t, s));
  };

  // Moving and resizing a placed task by pointer; arrows do the same from the keyboard.
  const startDrag = (e: React.PointerEvent, x: { t: Task; start: number }, mode: 'move' | 'resize') => {
    if (!x.t.editable || x.t.status === 'COMPLETED') return;
    e.preventDefault();
    const y0 = e.clientY, s0 = x.start, len0 = lengthOf(x.t);
    const el = e.currentTarget as HTMLElement;
    el.setPointerCapture(e.pointerId);
    let cur = { k: key(x.t), start: s0, minutes: len0 };
    const move = (ev: PointerEvent) => {
      const dm = Math.round(((ev.clientY - y0) / HOUR_PX) * 60 / SNAP) * SNAP;
      cur = mode === 'move' ? { ...cur, start: Math.max(m.from, Math.min(m.to - len0 * MIN, s0 + dm * MIN)) } : { ...cur, minutes: Math.max(SNAP, Math.min(24 * 60, len0 + dm)) };
      setDrag(cur);
    };
    const up = () => {
      el.removeEventListener('pointermove', move);
      el.removeEventListener('pointerup', up);
      setDrag(null);
      if (cur.start !== s0 || cur.minutes !== len0) run(place(x.t, cur.start, cur.minutes));
    };
    el.addEventListener('pointermove', move);
    el.addEventListener('pointerup', up);
  };
  const keyMove = (e: React.KeyboardEvent, x: { t: Task; start: number }) => {
    if (!x.t.editable || !['ArrowUp', 'ArrowDown'].includes(e.key)) return;
    e.preventDefault();
    const d = (e.key === 'ArrowUp' ? -SNAP : SNAP);
    if (e.shiftKey) run(place(x.t, x.start, Math.max(SNAP, lengthOf(x.t) + d)));
    else run(place(x.t, Math.max(m.from, x.start + d * MIN)));
  };

  const shown = more ? m.candidates : m.candidates.slice(0, 6);
  const candidates = (
    <section className="tp-candidates" aria-labelledby="tp-cand">
      <h2 id="tp-cand">What could you do today?</h2>
      <SuggestionLine m={m} now={Math.max(now, m.from)} onDone={reload} setError={setError} />
      {m.candidates.length === 0 && <p className="td-empty">Nothing waiting. Add a task to plan it.</p>}
      <ul>
        {shown.map((t) => {
          const d = dueLabel(t.due, new Date(now));
          return (
            <li key={key(t)} className="tp-cand" draggable
              onDragStart={(e) => { e.dataTransfer.setData('text/x-task', key(t)); e.dataTransfer.effectAllowed = 'move'; }}>
              <button type="button" className="td-task-title" onClick={() => onOpen(t)}>{t.title}</button>
              <span className="tp-cand-meta">
                {d && <span className={d.overdue ? 'tk-late' : ''}>{d.overdue ? `Overdue · ${d.text}` : `Due ${d.text}`}</span>}
                <span>{listName(t.calendar_id)}</span>
                {!!(t.duration_min || t.estimate_min) && <span>{estimateText(lengthOf(t))}</span>}
                {priorityName(t.priority) === 'High' && <span className="td-prio">High</span>}
              </span>
              <button type="button" className="btn-ghost kc-icon tp-add" aria-label={`Place ${t.title} in the first free gap`} onClick={() => addFirst(t)}><Plus size={16} aria-hidden="true" /></button>
            </li>
          );
        })}
      </ul>
      {m.candidates.length > 6 && <button type="button" className="btn-ghost btn-sm" onClick={() => setMore(!more)}>{more ? 'Show fewer' : `Show ${m.candidates.length - 6} more`}</button>}
    </section>
  );
  const timeline = (
    <section className="tp-timeline" aria-label="Today's timeline">
      <div className="tp-grid" ref={grid} style={{ height: y(m.to) }}
        onDragOver={(e) => { if (e.dataTransfer.types.includes('text/x-task')) e.preventDefault(); }}
        onDrop={(e) => {
          const k = e.dataTransfer.getData('text/x-task');
          const t = m.candidates.find((c) => key(c) === k);
          if (t) { e.preventDefault(); run(place(t, timeAt(e.clientY))); }
        }}>
        {hours.map((h) => <div key={h} className="tp-hour" style={{ top: y(h) }}><span>{hourLabel(h)}</span></div>)}
        {m.free.map((g) => (
          <div key={`g${g.start}`} className="tp-gap" style={{ top: y(g.start), height: y(g.end) - y(g.start) }}>{g.minutes >= 30 && <span>{hm(g.minutes)} free</span>}</div>
        ))}
        {m.events.map((x) => (
          <div key={`e${x.e.calendar_id}${x.e.uid}${x.e.recurrence_id ?? ''}`} className="tp-event" style={{ top: y(x.start), height: Math.max(20, y(x.end) - y(x.start)) }}>
            <CalendarDays size={13} aria-hidden="true" />
            <span className="tp-block-title">{x.e.title || 'Busy'}</span>
            <span className="tp-block-time">{clock(x.start)}–{clock(x.end)}</span>
          </div>
        ))}
        {m.placed.map((x) => {
          const d = drag?.k === key(x.t) ? drag : null;
          const start = d?.start ?? x.start;
          const minutes = d?.minutes ?? lengthOf(x.t);
          const prio = priorityName(x.t.priority);
          return (
            <div key={`t${key(x.t)}`} className="tp-task" role="group" tabIndex={0} data-priority={prio.toLowerCase() || undefined} data-done={x.t.status === 'COMPLETED' || undefined}
              aria-label={`${x.t.title}, ${clock(start)}, ${hm(minutes)}${prio ? `, ${prio} priority` : ''}. Arrow keys move it, Shift with arrows changes its length.`}
              style={{ top: y(start), height: Math.max(22, (minutes / 60) * HOUR_PX) }}
              onPointerDown={(e) => { if (!(e.target as Element).closest('button, .tp-resize')) startDrag(e, x, 'move'); }}
              onKeyDown={(e) => keyMove(e, x)} onDoubleClick={() => onOpen(x.t)}>
              <span className="tp-mark" aria-hidden="true">{x.t.status === 'COMPLETED' && <Check size={12} />}</span>
              <button type="button" className="tp-block-title" onClick={() => onOpen(x.t)}>{x.t.title}</button>
              <span className="tp-block-time">{clock(start)} · {hm(minutes)}{prio === 'High' && ' · High'}</span>
              <span className="tp-resize" aria-hidden="true" onPointerDown={(e) => { e.stopPropagation(); startDrag(e, x, 'resize'); }} />
            </div>
          );
        })}
        {now >= m.from && now <= m.to && <div className="tp-now" style={{ top: y(now) }} aria-label={`Now, ${clock(now)}`}><span>{clock(now)}</span></div>}
      </div>
    </section>
  );
  return (
    <div className="tp">
      <div className="tp-head">
        <p><strong>{hm(m.plannedMin)} planned</strong> of {hm(m.freeAll)} free</p>
        <button type="button" className="btn-ghost btn-sm" onClick={onDone}><ChevronLeft size={14} aria-hidden="true" /> Back to Today</button>
      </div>
      {error && <p role="alert">{error}</p>}
      {isNarrow ? (
        <>
          <div className="tp-tabs" role="tablist" aria-label="Plan">
            <button type="button" role="tab" aria-selected={tab === 'plan'} onClick={() => setTab('plan')}>Plan</button>
            <button type="button" role="tab" aria-selected={tab === 'candidates'} onClick={() => setTab('candidates')}>Candidates ({m.candidates.length})</button>
          </div>
          <div role="tabpanel">{tab === 'plan' ? timeline : candidates}</div>
        </>
      ) : <div className="tp-cols">{candidates}{timeline}</div>}
    </div>
  );
}
