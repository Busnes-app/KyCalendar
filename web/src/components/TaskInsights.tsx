import { useEffect, useState } from 'react';
import { Plus, Trash2 } from 'lucide-react';
import { EmptyState, Modal } from './ui';
import type { CalendarInfo } from '../calendarApi';
import {
  deleteGoal, goalProgress, habit, insights, listGoals, saveGoal,
  type Area, type Goal, type GoalProgress, type Habit, type Insights, type Task,
} from '../tasksApi';
import { dateOnly } from '../taskParse';
import { hm } from '../dayPlan';

const shortDate = (d: string) => new Date(`${d}T12:00`).toLocaleDateString(undefined, { day: 'numeric', month: 'short' });

// InsightsView: completions per week, open and overdue per list, planned against free hours.
// Each chart has a table view; the bars carry their numbers in a tooltip and the table.
export function InsightsView() {
  const [data, setData] = useState<Insights | null>(null);
  const [error, setError] = useState('');
  const [table, setTable] = useState(false);
  useEffect(() => { void insights().then(setData, (e) => setError(e instanceof Error ? e.message : 'Insights could not be loaded.')); }, []);
  if (error) return <p role="alert">{error}</p>;
  if (!data) return <p className="td-empty">Loading…</p>;
  const maxDone = Math.max(1, ...data.weeks.map((w) => w.done));
  const total = data.weeks.reduce((n, w) => n + w.done, 0);
  const W = 560, H = 140, gap = 6, bw = (W - gap * (data.weeks.length - 1)) / data.weeks.length;
  return (
    <div className="ti">
      <section aria-labelledby="ti-weeks">
        <div className="ti-head">
          <h2 id="ti-weeks">Completed per week</h2>
          <button type="button" className="btn-ghost btn-sm" aria-pressed={table} onClick={() => setTable(!table)}>{table ? 'Show chart' : 'Show table'}</button>
        </div>
        <p className="ti-sum">{total} in 12 weeks · {data.weeks[data.weeks.length - 1].done} this week</p>
        {table ? (
          <table className="ti-table"><thead><tr><th scope="col">Week of</th><th scope="col">Done</th></tr></thead>
            <tbody>{data.weeks.map((w) => <tr key={w.start}><td>{shortDate(w.start)}</td><td>{w.done}</td></tr>)}</tbody></table>
        ) : (
          <svg className="ti-bars" viewBox={`0 0 ${W} ${H + 20}`} role="img" aria-label={`Tasks completed per week: ${data.weeks.map((w) => `${shortDate(w.start)} ${w.done}`).join(', ')}`}>
            <line x1="0" x2={W} y1={H} y2={H} className="ti-base" />
            {data.weeks.map((w, i) => {
              const h = Math.max(w.done ? 4 : 0, (w.done / maxDone) * (H - 16));
              return (
                <g key={w.start}>
                  <rect x={i * (bw + gap)} y={0} width={bw} height={H} className="ti-hit"><title>{`Week of ${shortDate(w.start)}: ${w.done} done`}</title></rect>
                  <rect x={i * (bw + gap)} y={H - h} width={bw} height={h} rx="4" className="ti-bar" />
                  {(i === data.weeks.length - 1 || i % 3 === 0) && <text x={i * (bw + gap) + bw / 2} y={H + 15} className="ti-label">{shortDate(w.start)}</text>}
                </g>
              );
            })}
          </svg>
        )}
      </section>
      <section aria-labelledby="ti-days">
        <h2 id="ti-days">This week: planned of free</h2>
        <p className="ti-key"><span className="ti-swatch ti-swatch-planned" aria-hidden="true" /> Planned <span className="ti-swatch ti-swatch-free" aria-hidden="true" /> Free (08:00–18:00, less events)</p>
        <table className="ti-table ti-days">
          <thead><tr><th scope="col">Day</th><th scope="col">Planned of free</th><th scope="col"><span className="sr-only">Bar</span></th></tr></thead>
          <tbody>
            {data.days.map((d) => (
              <tr key={d.date}>
                <td>{new Date(`${d.date}T12:00`).toLocaleDateString(undefined, { weekday: 'short', day: 'numeric' })}</td>
                <td>{hm(d.planned_min)} of {hm(d.free_min)}</td>
                <td><span className="ti-bullet" aria-hidden="true"><span style={{ width: `${d.free_min ? Math.min(100, (d.planned_min / d.free_min) * 100) : 0}%` }} /></span></td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
      <section aria-labelledby="ti-lists">
        <h2 id="ti-lists">Open and overdue by list</h2>
        <table className="ti-table">
          <thead><tr><th scope="col">List</th><th scope="col">Open</th><th scope="col">Overdue</th></tr></thead>
          <tbody>{data.lists.map((l) => <tr key={l.id}><td>{l.name}</td><td>{l.open}</td><td className={l.overdue ? 'tk-late' : ''}>{l.overdue}</td></tr>)}</tbody>
        </table>
      </section>
    </div>
  );
}

// HabitPanel: in a habit's detail, its streaks and a 12-week grid of the days it was done.
export function HabitPanel({ task }: { task: Task }) {
  const [h, setH] = useState<Habit | null>(null);
  useEffect(() => { void habit(task).then(setH, () => setH(null)); }, [task]);
  if (!h) return null;
  const done = new Set(h.days);
  const today = new Date();
  const start = new Date(today.getFullYear(), today.getMonth(), today.getDate() - 83 - ((today.getDay() + 6) % 7));
  const days = Array.from({ length: 84 + ((today.getDay() + 6) % 7) + 1 }, (_, i) => new Date(start.getFullYear(), start.getMonth(), start.getDate() + i));
  const unit = h.freq === 'weekly' ? 'week' : 'day';
  return (
    <section className="th" aria-label="Habit">
      <p className="th-streaks"><strong>{h.current}-{unit} streak</strong> · longest {h.longest} {unit}{h.longest === 1 ? '' : 's'}</p>
      <div className="th-grid" role="img" aria-label={`Done on ${h.days.length} days in the last 12 weeks`}>
        {days.map((d) => { const k = dateOnly(d); return <span key={k} className="th-day" data-done={done.has(k) || undefined} title={`${d.toLocaleDateString()}${done.has(k) ? ': done' : ''}`} />; })}
      </div>
    </section>
  );
}

// HabitsView lists the habits among open tasks with their streaks.
export function HabitsView({ tasks, onOpen }: { tasks: Task[]; onOpen: (t: Task) => void }) {
  const habits = tasks.filter((t) => t.habit);
  if (habits.length === 0) return <EmptyState title="No habits yet">Open a repeating task and turn on “Track as a habit”.</EmptyState>;
  return (
    <ul className="th-list">
      {habits.map((t) => (
        <li key={`${t.calendar_id}/${t.uid}`}>
          <button type="button" className="td-task-title" onClick={() => onOpen(t)}>{t.title}</button>
          <HabitPanel task={t} />
        </li>
      ))}
    </ul>
  );
}

// GoalsView: the user's goals, each with how its lists are moving; add, edit and delete.
export function GoalsView({ lists, areas }: { lists: CalendarInfo[]; areas: Area[] }) {
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
  return (
    <div className="tg">
      <div className="ti-head"><span />
        <button type="button" className="btn-secondary btn-sm" onClick={() => setEditing('new')}><Plus size={14} aria-hidden="true" /> New goal</button>
      </div>
      {error && <p role="alert">{error}</p>}
      {goals?.length === 0 && <EmptyState title="No goals yet">A goal follows the lists and areas behind it over a season, quarter or year.</EmptyState>}
      {goals?.map((g) => (
        <section key={g.id} className="tg-goal" aria-labelledby={`goal-${g.id}`}>
          <div className="ti-head">
            <div>
              <h2 id={`goal-${g.id}`}><button type="button" className="td-task-title" onClick={() => setEditing(g)}>{g.title}</button></h2>
              <p className="ti-sum">{shortDate(g.starts)} – {shortDate(g.ends)}</p>
            </div>
          </div>
          <table className="ti-table">
            <thead><tr><th scope="col">List</th><th scope="col">Done in period</th><th scope="col">Open</th><th scope="col">Last done</th></tr></thead>
            <tbody>
              {(progress[g.id]?.lists ?? []).map((l) => (
                <tr key={l.list_id}>
                  <td>{l.name}{l.stalled && <span className="tg-stalled"> · No progress in 14 days</span>}</td>
                  <td>{l.done}</td><td>{l.open}</td>
                  <td>{l.last_done ? new Date(l.last_done).toLocaleDateString(undefined, { day: 'numeric', month: 'short' }) : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      ))}
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
