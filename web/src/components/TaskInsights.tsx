import { useEffect, useRef, useState } from 'react';
import { AlertCircle, Trash2 } from 'lucide-react';
import { EmptyState, Modal } from './ui';
import type { CalendarInfo } from '../calendarApi';
import {
  deleteGoal, goalProgress, habit, insights, listGoals, saveGoal,
  type Area, type Goal, type GoalProgress, type Habit, type Insights, type Task,
} from '../tasksApi';
import { dateOnly } from '../taskParse';
import { rangeOf } from '../taskRepeat';
import { hm } from '../dayPlan';

const workday = () => { const d = new Date(); return rangeOf(new Date(d.getFullYear(), d.getMonth(), d.getDate(), 8), new Date(d.getFullYear(), d.getMonth(), d.getDate(), 18)); };
const shortDate = (d: string) => new Date(`${d}T12:00`).toLocaleDateString(undefined, { day: 'numeric', month: 'short' });

// InsightsView: completions per week, planned against free hours, open and overdue per list.
// Marks use the accent (this week and today in full) and each list's colour; every figure is
// also in words or a table, so nothing is told by colour alone.
export function InsightsView({ lists, colors, open, overdue }: { lists: CalendarInfo[]; colors: Map<string, string>; open: (id: string) => number; overdue: (id: string) => number }) {
  const [data, setData] = useState<Insights | null>(null);
  const [error, setError] = useState('');
  const [table, setTable] = useState(false);
  const [W, setW] = useState(560); // the chart's width in pixels, so its text stays 12px
  const plot = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = plot.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setW(Math.max(240, Math.round(el.clientWidth))));
    ro.observe(el);
    return () => ro.disconnect();
  }, [data, table]);
  useEffect(() => { void insights().then(setData, (e) => setError(e instanceof Error ? e.message : 'Insights could not be loaded.')); }, []);
  if (error) return <p role="alert">{error}</p>;
  if (!data) return <p className="td-empty">Loading…</p>;
  const maxDone = Math.max(1, ...data.weeks.map((w) => w.done));
  const total = data.weeks.reduce((n, w) => n + w.done, 0);
  const last = data.weeks.length - 1;
  const H = 160, top = 18, gap = W < 480 ? 4 : 8, bw = (W - gap * last) / data.weeks.length;
  const today = dateOnly(new Date());
  const maxOpen = Math.max(1, ...lists.map((l) => open(l.id)));
  return (
    <div className="ti">
      <section className="card ti-card" aria-labelledby="ti-weeks">
        <div className="ti-head">
          <h2 id="ti-weeks">Completed per week</h2>
          <button type="button" className="btn-ghost btn-sm" aria-pressed={table} onClick={() => setTable(!table)}>{table ? 'Show chart' : 'Show table'}</button>
        </div>
        <p className="ti-sum"><strong>{data.weeks[last].done}</strong> this week · {total} in 12 weeks</p>
        {table ? (
          <table className="ti-table"><thead><tr><th scope="col">Week of</th><th scope="col">Done</th></tr></thead>
            <tbody>{data.weeks.map((w, i) => <tr key={w.start} data-now={i === last || undefined}><td>{shortDate(w.start)}{i === last && ' (this week)'}</td><td>{w.done}</td></tr>)}</tbody></table>
        ) : (
          <div className="ti-plot" ref={plot}><svg className="ti-bars" viewBox={`0 0 ${W} ${top + H + 22}`} role="img" aria-label={`Tasks completed per week: ${data.weeks.map((w) => `${shortDate(w.start)} ${w.done}`).join(', ')}`}>
            <line x1="0" x2={W} y1={top + H} y2={top + H} className="ti-base" />
            {data.weeks.map((w, i) => {
              const h = w.done ? Math.max(4, (w.done / maxDone) * (H - 4)) : 3; // a zero week is a stub on the baseline
              const x = i * (bw + gap);
              return (
                <g key={w.start}>
                  <rect x={x} y={0} width={bw} height={top + H} className="ti-hit"><title>{`Week of ${shortDate(w.start)}: ${w.done} done`}</title></rect>
                  <rect x={x} y={top + H - h} width={bw} height={h} rx={w.done ? 4 : 1} className={i === last ? 'ti-bar ti-bar-now' : w.done ? 'ti-bar' : 'ti-bar ti-bar-zero'} />
                  {!!w.done && <text x={x + bw / 2} y={top + H - h - 5} className="ti-value">{w.done}</text>}
                  {i % (W < 480 ? 4 : 3) === 0 && i < last - 2 && <text x={i === 0 ? 0 : x + bw / 2} y={top + H + 17} className={i === 0 ? 'ti-label ti-label-start' : 'ti-label'}>{shortDate(w.start)}</text>}
                  {i === last && <text x={W} y={top + H + 17} className="ti-label ti-label-now">This week</text>}
                </g>
              );
            })}
          </svg></div>
        )}
      </section>
      <section className="card ti-card" aria-labelledby="ti-days">
        <h2 id="ti-days">This week: planned of free</h2>
        <p className="ti-key"><span className="ti-swatch ti-swatch-planned" aria-hidden="true" /> Planned <span className="ti-swatch ti-swatch-free" aria-hidden="true" /> Free ({workday()}, less events)</p>
        <table className="ti-table ti-days">
          <thead><tr><th scope="col">Day</th><th scope="col">Planned of free</th><th scope="col"><span className="sr-only">Bar</span></th></tr></thead>
          <tbody>
            {data.days.map((d) => (
              <tr key={d.date} data-now={d.date === today || undefined}>
                <td>{new Date(`${d.date}T12:00`).toLocaleDateString(undefined, { weekday: 'short', day: 'numeric' })}{d.date === today && <span className="ti-today"> Today</span>}</td>
                <td>{hm(d.planned_min)} of {hm(d.free_min)}</td>
                <td><span className="ti-bullet" aria-hidden="true"><span style={{ width: `${d.free_min ? Math.min(100, (d.planned_min / d.free_min) * 100) : 0}%` }} /></span></td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
      <section className="card ti-card" aria-labelledby="ti-lists">
        <h2 id="ti-lists">Open and overdue by list</h2>
        <table className="ti-table ti-lists">
          <thead><tr><th scope="col">List</th><th scope="col">Open</th><th scope="col"><span className="sr-only">Open bar</span></th><th scope="col">Overdue</th></tr></thead>
          <tbody>{lists.map((l) => {
            const c = colors.get(l.id) ?? 'var(--accent)';
            const late = overdue(l.id);
            return (
              <tr key={l.id}>
                <td><span className="ti-list" style={{ '--list': c } as React.CSSProperties}><span className="ti-swatch" aria-hidden="true" />{l.name}</span></td>
                <td>{open(l.id)}</td>
                <td><span className="ti-meter" style={{ '--list': c } as React.CSSProperties} aria-hidden="true"><span style={{ width: `${(open(l.id) / maxOpen) * 100}%` }} /></span></td>
                <td>{late ? <span className="ti-late"><AlertCircle size={14} aria-hidden="true" />{late} overdue</span> : <span className="ti-none">0</span>}</td>
              </tr>
            );
          })}</tbody>
        </table>
      </section>
    </div>
  );
}

// HabitPanel: a habit's numbers and its days from the week it began, at most 12 weeks (one
// column per week, Monday on top; a weekly habit is one cell per week). Done cells take the
// habit's colour, missed ones a tint of it inside a full-colour edge, and days of the first week
// before the habit began a dashed outline. The same facts are in words.
export function HabitPanel({ task, color = 'var(--accent)' }: { task: Task; color?: string }) {
  const [h, setH] = useState<Habit | null>(null);
  useEffect(() => { void habit(task).then(setH, () => setH(null)); }, [task]);
  if (!h) return null;
  const done = new Set(h.days);
  const today = new Date();
  const todayKey = dateOnly(today);
  const monday = new Date(today.getFullYear(), today.getMonth(), today.getDate() - ((today.getDay() + 6) % 7));
  // The habit began at the earlier of its creation and its first completion.
  const began = [task.created ? dateOnly(new Date(task.created)) : todayKey, ...h.days].sort()[0];
  const b = new Date(`${began}T12:00`);
  const weeks = Math.min(12, Math.max(1, Math.round((monday.getTime() - new Date(b.getFullYear(), b.getMonth(), b.getDate() - ((b.getDay() + 6) % 7)).getTime()) / (7 * 86400000)) + 1));
  const first = new Date(monday.getFullYear(), monday.getMonth(), monday.getDate() - 7 * (weeks - 1));
  const days = Array.from({ length: weeks * 7 }, (_, i) => new Date(first.getFullYear(), first.getMonth(), first.getDate() + i));
  const weekly = h.freq === 'weekly';
  const cells = weekly
    ? Array.from({ length: weeks }, (_, w) => {
      const span = days.slice(w * 7, w * 7 + 7).map(dateOnly);
      return { key: span[0], label: `Week of ${days[w * 7].toLocaleDateString()}`, done: span.some((k) => done.has(k)), before: span[6] < began, future: span[0] > todayKey };
    })
    : days.map((d) => { const k = dateOnly(d); return { key: k, label: d.toLocaleDateString(), done: done.has(k), before: k < began, future: k > todayKey }; });
  const counted = cells.filter((c) => !c.future && !c.before);
  const hits = counted.filter((c) => c.done).length;
  const unit = weekly ? 'week' : 'day';
  const plural = (n: number) => `${n} ${unit}${n === 1 ? '' : 's'}`;
  const rate = counted.length ? Math.round((hits / counted.length) * 100) : 0;
  const thisWeek = days.slice(-7).map(dateOnly);
  const week = weekly ? (thisWeek.some((k) => done.has(k)) ? 'Done' : 'Not yet') : `${thisWeek.filter((k) => done.has(k)).length} of ${thisWeek.filter((k) => k <= todayKey).length}`;
  const summary = `${hits} of ${plural(counted.length)}, streak ${plural(h.current)}, longest ${h.longest}`;
  const range = `${shortDate(dateOnly(first))} – ${shortDate(todayKey)}`;
  const state = (c: typeof cells[number]) => (c.future ? 'to come' : c.before ? 'before the habit began' : c.done ? 'done' : 'missed');
  return (
    <section className="th" aria-label={`${task.title}: habit`} style={{ '--habit': color, '--weeks': weeks } as React.CSSProperties}>
      <div className="th-body">
        <dl className="th-stats">
          <div><dt>Current streak</dt><dd>{plural(h.current)}</dd></div>
          <div><dt>Longest</dt><dd>{plural(h.longest)}</dd></div>
          <div><dt>Completion</dt><dd>{rate}%</dd></div>
          <div><dt>This week</dt><dd>{week}</dd></div>
        </dl>
        <div className="th-chart">
          <div className="th-plot">
            {!weekly && <span className="th-weekdays" aria-hidden="true"><span>Mon</span><span /><span>Wed</span><span /><span>Fri</span><span /><span>Sun</span></span>}
            <div className="th-grid" data-weekly={weekly || undefined} role="img" aria-label={`${task.title}, ${range}: ${summary}`}>
              {cells.map((c) => <span key={c.key} className="th-day" data-done={c.done || undefined} data-before={c.before || undefined} data-future={c.future || undefined} title={`${c.label}: ${state(c)}`} />)}
            </div>
          </div>
          <p className="th-key"><span>{range}</span><span><span className="th-day th-key-swatch" data-done aria-hidden="true" /> Done</span><span><span className="th-day th-key-swatch" aria-hidden="true" /> Missed</span>
            {cells.some((c) => c.before) && <span><span className="th-day th-key-swatch" data-before aria-hidden="true" /> Before it began</span>}</p>
        </div>
      </div>
    </section>
  );
}

// HabitsView: one card per habit among the open tasks, in its list's colour.
export function HabitsView({ tasks, colors, onOpen }: { tasks: Task[]; colors: Map<string, string>; onOpen: (t: Task) => void }) {
  const habits = tasks.filter((t) => t.habit);
  if (habits.length === 0) return <EmptyState title="No habits yet">Open a daily or weekly repeating task and turn on “Track as a habit”.</EmptyState>;
  return (
    <ul className="th-list">
      {habits.map((t) => (
        <li key={`${t.calendar_id}/${t.uid}`} className="card th-card">
          <h2><button type="button" className="td-task-title" onClick={() => onOpen(t)}>{t.title}</button></h2>
          <HabitPanel task={t} color={colors.get(t.calendar_id)} />
        </li>
      ))}
    </ul>
  );
}

// GoalsView: the user's goals, each with how its lists are moving; add, edit and delete.
export function GoalsView({ lists, colors, areas, open, creating, onCreated }: { lists: CalendarInfo[]; colors: Map<string, string>; areas: Area[]; open: (id: string) => number; creating: boolean; onCreated: () => void }) {
  const [goals, setGoals] = useState<Goal[] | null>(null);
  const [progress, setProgress] = useState<Record<string, GoalProgress>>({});
  const [editing, setEditing] = useState<Goal | 'new' | null>(null);
  const [error, setError] = useState('');
  const load = () => listGoals().then(async (gs) => {
    setGoals(gs);
    const entries = await Promise.all(gs.map(async (g) => [g.id, await goalProgress(g.id)] as const));
    setProgress(Object.fromEntries(entries));
  }, (e) => setError(e instanceof Error ? e.message : 'Goals could not be loaded.'));
  useEffect(() => { void load(); }, []);
  useEffect(() => { if (creating) { setEditing('new'); onCreated(); } }, [creating, onCreated]);
  return (
    <div className="tg">
      {error && <p role="alert">{error}</p>}
      {goals?.length === 0 && <EmptyState title="No goals yet">A goal follows the lists and areas behind it over a season, quarter or year.</EmptyState>}
      {goals?.map((g) => { const rows = progress[g.id]?.lists ?? []; const most = Math.max(1, ...rows.map((l) => l.done)); return (
        <section key={g.id} className="card ti-card" aria-labelledby={`goal-${g.id}`}>
          <div className="ti-head">
            <div>
              <h2 id={`goal-${g.id}`}><button type="button" className="td-task-title" onClick={() => setEditing(g)}>{g.title}</button></h2>
              <p className="ti-sum">{shortDate(g.starts)} – {shortDate(g.ends)}</p>
            </div>
          </div>
          <table className="ti-table">
            <thead><tr><th scope="col">List</th><th scope="col">Done in period</th><th scope="col">Open</th><th scope="col">Last done</th></tr></thead>
            <tbody>
              {rows.map((l) => (
                <tr key={l.list_id}>
                  <td><span className="tg-list" style={{ '--list': colors.get(l.list_id) ?? 'var(--accent)' } as React.CSSProperties}>
                    <span className="ti-list"><span className="ti-swatch" aria-hidden="true" />{lists.find((c) => c.id === l.list_id)?.name ?? l.name}</span>
                    <span className="ti-meter" aria-hidden="true"><span style={{ width: `${(l.done / most) * 100}%` }} /></span>
                    {l.stalled && <span className="tg-stalled">No progress in 14 days</span>}
                  </span></td>
                  <td>{l.done}</td><td>{open(l.list_id)}</td>
                  <td>{l.last_done ? new Date(l.last_done).toLocaleDateString(undefined, { day: 'numeric', month: 'short' }) : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      ); })}
      {editing && <GoalDialog goal={editing === 'new' ? null : editing} lists={lists} areas={areas} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); void load(); }} />}
    </div>
  );
}

const PERIODS: { v: Goal['period']; label: string; months: number }[] = [
  { v: 'quarter', label: 'Quarter', months: 3 }, { v: 'season', label: 'Season', months: 3 }, { v: 'year', label: 'Year', months: 12 }, { v: 'custom', label: 'Custom', months: 1 },
];

function GoalDialog({ goal, lists, areas, onClose, onSaved }: { goal: Goal | null; lists: CalendarInfo[]; areas: Area[]; onClose: () => void; onSaved: () => void }) {
  const today = new Date();
  const [g, setG] = useState<Omit<Goal, 'id'> & { id?: string }>(goal ?? {
    title: '', period: 'quarter', starts: dateOnly(today), ends: dateOnly(new Date(today.getFullYear(), today.getMonth() + 3, today.getDate() - 1)), areas: [], lists: [],
  });
  const [error, setError] = useState('');
  const toggle = (k: 'areas' | 'lists', id: string) => setG({ ...g, [k]: g[k].includes(id) ? g[k].filter((x) => x !== id) : [...g[k], id] });
  return (
    <Modal title={goal ? 'Edit goal' : 'New goal'} onClose={onClose} closable>
      <form className="kc-form" onSubmit={(e) => { e.preventDefault(); void saveGoal(g).then(onSaved, (err) => setError(err instanceof Error ? err.message : 'Could not save')); }}>
        <label htmlFor="goal-title">Goal</label>
        <input id="goal-title" value={g.title} maxLength={200} required autoFocus onChange={(e) => setG({ ...g, title: e.target.value })} />
        <label htmlFor="goal-period">Period</label>
        <select id="goal-period" value={g.period} onChange={(e) => {
          const p = PERIODS.find((x) => x.v === e.target.value)!;
          const s = new Date(`${g.starts}T12:00`);
          setG({ ...g, period: p.v, ends: p.v === 'custom' ? g.ends : dateOnly(new Date(s.getFullYear(), s.getMonth() + p.months, s.getDate() - 1)) });
        }}>
          {PERIODS.map((p) => <option key={p.v} value={p.v}>{p.label}</option>)}
        </select>
        <div className="kc-pair">
          <div className="field"><label htmlFor="goal-starts">Starts</label><input id="goal-starts" type="date" value={g.starts} required onChange={(e) => setG({ ...g, starts: e.target.value })} /></div>
          <div className="field"><label htmlFor="goal-ends">Ends</label><input id="goal-ends" type="date" value={g.ends} required onChange={(e) => setG({ ...g, ends: e.target.value })} /></div>
        </div>
        {areas.length > 0 && (
          <fieldset><legend>Areas</legend>
            {areas.map((a) => <label key={a.id} className="kc-check"><input type="checkbox" checked={g.areas.includes(a.id)} onChange={() => toggle('areas', a.id)} /> {a.name}</label>)}
          </fieldset>
        )}
        <fieldset><legend>Lists</legend>
          {lists.map((c) => <label key={c.id} className="kc-check"><input type="checkbox" checked={g.lists.includes(c.id)} onChange={() => toggle('lists', c.id)} /> {c.name}</label>)}
        </fieldset>
        {error && <p role="alert">{error}</p>}
        <div className="modal-actions tk-sticky-actions">
          {goal && <button type="button" className="btn-danger push-start" onClick={() => window.confirm(`Delete the goal “${goal.title}”?`) && void deleteGoal(goal.id).then(onSaved)}><Trash2 size={14} aria-hidden="true" /> Delete</button>}
          <button type="button" className="btn-secondary" onClick={onClose}>Cancel</button>
          <button type="submit">Save</button>
        </div>
      </form>
    </Modal>
  );
}
