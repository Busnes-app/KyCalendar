import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  CalendarClock, CalendarDays, CheckCircle2, ChevronDown, Inbox, Layers, ListTodo, Mail, Plus, Repeat, Search,
  SlidersHorizontal, Star, CornerDownLeft, Sun, Hourglass, AlertCircle, Bookmark,
} from 'lucide-react';
import { MoreMenu } from '../components/MoreMenu';
import { EmptyState, Modal } from '../components/ui';
import { TaskDialog, estimateText } from '../components/TaskDialog';
import { ApiError, createCalendar, deleteCalendar, listCalendars, patchCalendar, type CalendarInfo } from '../calendarApi';
import {
  createTask, createView, deleteView, inbox, listAreas, listTasks, listViews, patchTask, saveAreas,
  type Area, type PriorityName, type SavedView, type Task, type TaskBody, type TaskFilter,
} from '../tasksApi';
import { dateOnly, parseQuick } from '../taskParse';
import { describe, dueLabel, priorityName } from '../taskRepeat';
import { useNarrow } from '../useNarrow';
import '../styles/calendar.css';
import '../styles/tasks.css';

export { TASKS_CHANGED } from '../components/CaptureBox';
import { TASKS_CHANGED } from '../components/CaptureBox';

type Smart = 'today' | 'upcoming' | 'overdue' | 'someday' | 'flagged' | 'completed';
export type TaskView = { kind: 'smart'; id: Smart } | { kind: 'list' | 'area' | 'view'; id: string };
const SMART: { id: Smart; label: string; icon: typeof Sun }[] = [
  { id: 'today', label: 'Today', icon: Sun },
  { id: 'upcoming', label: 'Upcoming', icon: CalendarDays },
  { id: 'overdue', label: 'Overdue', icon: AlertCircle },
  { id: 'someday', label: 'Someday', icon: Hourglass },
  { id: 'flagged', label: 'Flagged from mail', icon: Mail },
  { id: 'completed', label: 'Completed', icon: CheckCircle2 },
];
const VIEW_KEY = 'kycalendar.tasksView';
const SORTS = [{ v: 'smart', label: 'Smart (overdue, due, priority)' }, { v: 'due', label: 'Due date' }, { v: 'priority', label: 'Priority' }, { v: 'created', label: 'Newest' }, { v: 'title', label: 'Title' }] as const;

const isTasks = (c: CalendarInfo) => c.components.includes('VTODO');
const dayOf = (v?: string) => (v ? (v.length === 10 ? v : dateOnly(new Date(v))) : '');

function savedView(): TaskView {
  try {
    const v = JSON.parse(localStorage.getItem(VIEW_KEY) ?? '');
    if (v && typeof v.id === 'string' && ['smart', 'list', 'area', 'view'].includes(v.kind)) return v;
  } catch { /* the default */ }
  return { kind: 'smart', id: 'today' };
}

// inView answers whether an open task belongs to a smart view, by the viewer's local day.
function inView(t: Task, id: Smart, today: string): boolean {
  const due = dayOf(t.due), start = dayOf(t.start);
  const in14 = dateOnly(new Date(Date.now() + 14 * 86400000));
  switch (id) {
    case 'today': return (!!due && due <= today) || start === today;
    case 'upcoming': return (!!due && due > today && due <= in14) || (!due && !!start && start > today && start <= in14);
    case 'overdue': return !!due && (t.due!.length === 10 ? due < today : new Date(t.due!) < new Date());
    case 'someday': return !due;
    default: return true;
  }
}

export function TasksPage({ sidebarSlot = null, drive = '' }: { sidebarSlot?: HTMLElement | null; drive?: string }) {
  const isNarrow = useNarrow();
  const [calendars, setCalendars] = useState<CalendarInfo[]>([]);
  const [inboxId, setInboxId] = useState('');
  const [areas, setAreas] = useState<Area[]>([]);
  const [views, setViews] = useState<SavedView[]>([]);
  const [view, setView] = useState<TaskView>(savedView);
  const [open, setOpen] = useState<Task[]>([]); // every open task, for counts and smart views
  const [shown, setShown] = useState<Task[] | null>(null); // a server-filtered result, when filtering
  const [q, setQ] = useState('');
  const [filter, setFilter] = useState<TaskFilter>({});
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [sort, setSort] = useState<TaskFilter['sort']>('smart');
  const [selected, setSelected] = useState(0);
  const [dialog, setDialog] = useState<{ task: Task | null; initial?: TaskBody } | null>(null);
  const [error, setError] = useState('');
  const [picker, setPicker] = useState(false);
  const [managing, setManaging] = useState(false);
  const [naming, setNaming] = useState<'list' | 'view' | null>(null);
  const search = useRef<HTMLInputElement>(null);
  const listEl = useRef<HTMLUListElement>(null);

  const lists = useMemo(() => calendars.filter(isTasks), [calendars]);
  const listName = (id: string) => (id === inboxId ? 'Inbox' : lists.find((c) => c.id === id)?.name ?? '');

  const loadMeta = useCallback(async () => {
    try {
      const [cals, box, as, vs] = await Promise.all([listCalendars(), inbox(), listAreas(), listViews()]);
      setCalendars(cals.some((c) => c.id === box) ? cals : await listCalendars());
      setInboxId(box);
      setAreas(as);
      setViews(vs);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Tasks could not be loaded.');
    }
  }, []);

  // The server filter this view and the filter bar ask for; null when the open list answers it.
  const serverFilter = useMemo((): TaskFilter | null => {
    const base: TaskFilter = { ...filter, q: q.trim() || undefined, sort };
    if (view.kind === 'view') {
      const v = views.find((x) => x.id === view.id);
      return v ? { ...v.query, ...base, q: base.q ?? v.query.q } : base;
    }
    if (view.kind === 'smart' && view.id === 'completed') return { ...base, status: ['completed'], sort: sort === 'smart' ? undefined : sort };
    if (view.kind === 'smart' && view.id === 'flagged') return { ...base, source: 'kypost' };
    if (view.kind === 'area') return { ...base, area: view.id };
    if (view.kind === 'list') return { ...base, lists: [view.id] };
    const active = base.q || filter.tags?.length || filter.priority?.length || filter.due_from || filter.due_to || filter.status?.length || filter.lists?.length || sort !== 'smart';
    return active ? base : null;
  }, [view, views, filter, q, sort]);

  const load = useCallback(async () => {
    setError('');
    try {
      setOpen(await listTasks({}));
      setShown(serverFilter ? await listTasks(serverFilter) : null);
    } catch (e) {
      setError(e instanceof ApiError && e.code === 'rate_limited' ? 'Too many searches; wait a minute.' : e instanceof Error ? e.message : 'Tasks could not be loaded.');
    }
  }, [serverFilter]);

  useEffect(() => { void loadMeta(); }, [loadMeta]);
  useEffect(() => {
    const t = setTimeout(() => void load(), q ? 300 : 0);
    return () => clearTimeout(t);
  }, [load, q]);
  useEffect(() => {
    const changed = () => { void load(); void loadMeta(); };
    window.addEventListener(TASKS_CHANGED, changed);
    return () => window.removeEventListener(TASKS_CHANGED, changed);
  }, [load, loadMeta]);
  useEffect(() => { localStorage.setItem(VIEW_KEY, JSON.stringify(view)); setSelected(0); }, [view]);

  const today = dateOnly(new Date());
  const rows = useMemo(() => {
    const base = shown ?? open;
    return view.kind === 'smart' && view.id !== 'completed' && view.id !== 'flagged' ? base.filter((t) => inView(t, view.id as Smart, today)) : base;
  }, [shown, open, view, today]);
  // Subtasks list under their parent when it is shown; the rest are top level.
  const ordered = useMemo(() => {
    const key = (t: Task) => `${t.calendar_id}/${t.uid}`;
    const byParent = new Map<string, Task[]>();
    const here = new Set(rows.map(key));
    for (const t of rows) {
      const p = t.parent ? `${t.calendar_id}/${t.parent}` : '';
      if (p && here.has(p)) byParent.set(p, [...(byParent.get(p) ?? []), t]);
    }
    const out: { task: Task; depth: number }[] = [];
    const walk = (t: Task, depth: number) => {
      out.push({ task: t, depth });
      if (depth < 2) for (const c of byParent.get(key(t)) ?? []) walk(c, depth + 1);
    };
    for (const t of rows) if (!(t.parent && here.has(`${t.calendar_id}/${t.parent}`))) walk(t, 0);
    return out;
  }, [rows]);

  const groups = useMemo(() => {
    if (!(view.kind === 'smart' && view.id === 'upcoming')) return [{ label: '', items: ordered }];
    const m = new Map<string, typeof ordered>();
    for (const r of ordered) {
      const d = dayOf(r.task.due ?? r.task.start);
      m.set(d, [...(m.get(d) ?? []), r]);
    }
    return [...m.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([d, items]) => ({
      label: new Date(`${d}T12:00`).toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long' }), items,
    }));
  }, [ordered, view]);
  const flat = groups.flatMap((g) => g.items);

  const title = view.kind === 'smart' ? SMART.find((s) => s.id === view.id)!.label
    : view.kind === 'list' ? listName(view.id) : view.kind === 'area' ? areas.find((a) => a.id === view.id)?.name ?? 'Area'
      : views.find((v) => v.id === view.id)?.name ?? 'View';
  const writable = lists.filter((c) => c.role !== 'reader' && !c.subscription);
  const targetList = view.kind === 'list' && writable.some((c) => c.id === view.id) ? view.id : inboxId || writable[0]?.id || '';
  const currentList = view.kind === 'list' ? lists.find((c) => c.id === view.id) : undefined;

  async function toggle(t: Task) {
    try {
      await patchTask(t, { status: t.status === 'COMPLETED' ? 'NEEDS-ACTION' : 'COMPLETED' });
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'The task could not be saved.');
    }
  }

  // Keyboard: n new, x complete, e edit, / search, j and k move. Each has a visible control.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement;
      if (e.metaKey || e.ctrlKey || e.altKey || el.closest('input, textarea, select, dialog, [contenteditable]')) return;
      const cur = flat[selected]?.task;
      if (e.key === '/') { e.preventDefault(); search.current?.focus(); }
      else if (e.key === 'j' || e.key === 'k') {
        e.preventDefault();
        const next = Math.max(0, Math.min(flat.length - 1, selected + (e.key === 'j' ? 1 : -1)));
        setSelected(next);
        listEl.current?.querySelectorAll<HTMLElement>('.tk-open')[next]?.focus();
      } else if (e.key === 'x' && cur?.editable) { e.preventDefault(); void toggle(cur); }
      else if (e.key === 'e' && cur) { e.preventDefault(); setDialog({ task: cur }); }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });

  const counts = useMemo(() => {
    const c: Record<string, number> = {};
    for (const s of ['today', 'overdue'] as Smart[]) c[s] = open.filter((t) => inView(t, s, today)).length;
    for (const t of open) c[t.calendar_id] = (c[t.calendar_id] ?? 0) + 1;
    c.flagged = open.filter((t) => t.source === 'kypost').length;
    return c;
  }, [open, today]);

  const go = (v: TaskView) => { setView(v); setQ(''); setFilter({}); setPicker(false); };
  const navItem = (v: TaskView, label: string, Icon: typeof Sun, count?: number, extra?: string) => {
    const current = v.kind === view.kind && v.id === view.id;
    return (
      <li key={`${v.kind}:${v.id}`}>
        <button type="button" className="ky-nav-item tk-nav" aria-current={current ? 'page' : undefined} onClick={() => go(v)}>
          <Icon aria-hidden="true" />
          <span>{label}</span>
          {extra && <span className="tk-nav-note">{extra}</span>}
          {!!count && <span className="tk-count" aria-label={`${count} open`}>{count}</span>}
        </button>
      </li>
    );
  };
  const personal = lists.filter((c) => c.kind === 'personal' && c.id !== inboxId);
  const inArea = new Set(areas.flatMap((a) => a.lists));
  const sidebarBody = (
    <nav className="kc-sidebar tk-sidebar" aria-label="Task lists">
      <div className="kc-sidebar-head">
        <h2>Tasks</h2>
        <MoreMenu label="Add a list or area" icon={Plus}>
          {(close) => (
            <>
              <button type="button" role="menuitem" className="app-menu-item" onClick={() => { close(); setNaming('list'); }}><ListTodo size={16} aria-hidden="true" /> New list</button>
              <button type="button" role="menuitem" className="app-menu-item" onClick={() => { close(); setManaging(true); }}><Layers size={16} aria-hidden="true" /> Areas…</button>
            </>
          )}
        </MoreMenu>
      </div>
      <ul className="tk-navlist">
        {inboxId && navItem({ kind: 'list', id: inboxId }, 'Inbox', Inbox, counts[inboxId])}
        {SMART.map((s) => navItem({ kind: 'smart', id: s.id }, s.label, s.icon, s.id === 'today' || s.id === 'overdue' || s.id === 'flagged' ? counts[s.id] : undefined))}
      </ul>
      {areas.map((a) => (
        <div key={a.id} className="tk-group">
          <h3><button type="button" className="tk-group-link" aria-current={view.kind === 'area' && view.id === a.id ? 'page' : undefined} onClick={() => go({ kind: 'area', id: a.id })}>{a.name}</button></h3>
          <ul className="tk-navlist">{a.lists.map((id) => lists.find((c) => c.id === id)).filter((c): c is CalendarInfo => !!c && c.id !== inboxId).map((c) => navItem({ kind: 'list', id: c.id }, c.name, ListTodo, counts[c.id], c.kind === 'group' ? 'Shared' : undefined))}</ul>
        </div>
      ))}
      {personal.some((c) => !inArea.has(c.id)) && (
        <div className="tk-group">
          <h3>Lists</h3>
          <ul className="tk-navlist">{personal.filter((c) => !inArea.has(c.id)).map((c) => navItem({ kind: 'list', id: c.id }, c.name, ListTodo, counts[c.id]))}</ul>
        </div>
      )}
      {lists.some((c) => c.kind === 'group' && !inArea.has(c.id)) && (
        <div className="tk-group">
          <h3>Shared</h3>
          <ul className="tk-navlist">{lists.filter((c) => c.kind === 'group' && !inArea.has(c.id)).map((c) => navItem({ kind: 'list', id: c.id }, c.name, ListTodo, counts[c.id], c.role === 'reader' ? 'Read-only' : undefined))}</ul>
        </div>
      )}
      {views.length > 0 && (
        <div className="tk-group">
          <h3>Saved views</h3>
          <ul className="tk-navlist">{views.map((v) => navItem({ kind: 'view', id: v.id }, v.name, Bookmark))}</ul>
        </div>
      )}
    </nav>
  );
  const sidebar = (
    <details className="kc-picker" open={!isNarrow || picker}>
      <summary onClick={(e) => { e.preventDefault(); setPicker(!picker); }}>Lists <ChevronDown size={16} aria-hidden="true" /></summary>
      <div className="kc-picker-panel">{sidebarBody}</div>
    </details>
  );

  // Quick add: what the field parses into, previewed before saving.
  const [text, setText] = useState('');
  const parsed = useMemo(() => parseQuick(text, writable.map((c) => ({ id: c.id, name: c.id === inboxId ? 'Inbox' : c.name }))), [text, writable, inboxId]);
  async function quickAdd(e: React.FormEvent) {
    e.preventDefault();
    if (!text.trim() || !targetList) return;
    const due = parsed.due ?? (view.kind === 'smart' && view.id === 'today' ? today : undefined);
    try {
      await createTask(parsed.list?.id ?? targetList, { title: parsed.title, due, priority: parsed.priority, tags: parsed.tags.length ? parsed.tags : undefined, rrule: parsed.rrule });
      setText('');
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'The task could not be added.');
    }
  }

  const filtering = !!(q || filter.tags?.length || filter.priority?.length || filter.due_from || filter.due_to || filter.status?.length || filter.lists?.length);
  const subtasksOf = (t: Task) => open.concat(shown ?? []).filter((s, i, all) => s.parent === t.uid && s.calendar_id === t.calendar_id && all.findIndex((x) => x.uid === s.uid && x.calendar_id === s.calendar_id) === i);

  return (
    <section className="tk-layout" aria-labelledby="tk-title">
      <header className="tk-head">
        <div>
          <h1 id="tk-title" className="ky-page-title">{title}</h1>
          <p className="tk-meta">
            {flat.length === 1 ? '1 task' : `${flat.length} tasks`}
            {currentList?.kind === 'group' && ` · shared list${currentList.role === 'reader' ? ', read-only' : ''}`}
          </p>
        </div>
        <div className="tk-head-actions">
          {(view.kind === 'view' || (currentList && currentList.kind === 'personal' && currentList.id !== inboxId && currentList.role === 'owner')) && (
            <MoreMenu label={`${title} options`}>
              {(close) => view.kind === 'view' ? (
                <button type="button" role="menuitem" className="app-menu-item" onClick={() => { close(); if (window.confirm(`Delete the view “${title}”?`)) void deleteView(view.id).then(() => { go({ kind: 'smart', id: 'today' }); void loadMeta(); }); }}>Delete view</button>
              ) : (
                <>
                  <button type="button" role="menuitem" className="app-menu-item" onClick={() => { close(); const n = window.prompt('Rename list', title); if (n?.trim()) void patchCalendar(currentList!.id, { name: n.trim() }).then(loadMeta); }}>Rename list</button>
                  <button type="button" role="menuitem" className="app-menu-item" onClick={() => { close(); if (window.confirm(`Delete ${title} and every task in it?`)) void deleteCalendar(currentList!.id).then(() => { go({ kind: 'smart', id: 'today' }); void loadMeta(); }); }}>Delete list</button>
                </>
              )}
            </MoreMenu>
          )}
        </div>
      </header>

      <form className="tk-quick" onSubmit={quickAdd}>
        <Plus aria-hidden="true" />
        <input id="tk-quick" aria-label="Quick add a task" aria-describedby="tk-quick-preview" placeholder="Add a task: “Call Ana tomorrow 3pm !high #home”"
          value={text} maxLength={1000} onChange={(e) => setText(e.target.value)} disabled={!targetList} />
        <button type="submit" className="btn-ghost kc-icon tk-quick-go" aria-label="Add task" title="Add (Enter)" disabled={!text.trim() || !targetList}><CornerDownLeft size={16} aria-hidden="true" /></button>
        <div id="tk-quick-preview" className="tk-chips" aria-live="polite">
          {text.trim() && (
            <>
              <span className="tk-chip tk-chip-title">{parsed.title}</span>
              {parsed.chips.map((c) => <span key={c} className="tk-chip">{c}</span>)}
              {!parsed.list && <span className="tk-chip tk-chip-quiet">In {listName(targetList)}</span>}
            </>
          )}
        </div>
      </form>

      {!(view.kind === 'smart' && view.id === 'today') && <div className="tk-tools">
        <label className="tk-search">
          <Search aria-hidden="true" />
          <input ref={search} type="search" aria-label="Search tasks" placeholder="Search title, notes and tags" value={q} maxLength={200} onChange={(e) => setQ(e.target.value)} />
        </label>
        <button type="button" className="btn-secondary btn-sm" aria-expanded={filtersOpen} aria-controls="tk-filters" onClick={() => setFiltersOpen(!filtersOpen)}>
          <SlidersHorizontal size={14} aria-hidden="true" /> Filters{filtering && filter !== undefined && Object.values(filter).some((v) => (Array.isArray(v) ? v.length : v)) ? ' (on)' : ''}
        </button>
        <label className="tk-sort">
          <span>Sort</span>
          <select value={sort} onChange={(e) => setSort(e.target.value as TaskFilter['sort'])}>
            {SORTS.map((s) => <option key={s.v} value={s.v}>{s.label}</option>)}
          </select>
        </label>
        {filtering && view.kind !== 'view' && <button type="button" className="btn-ghost btn-sm" onClick={() => setNaming('view')}><Star size={14} aria-hidden="true" /> Save view</button>}
      </div>}
      {filtersOpen && (
        <div id="tk-filters" className="tk-filters" role="group" aria-label="Filters">
          <label>Priority
            <select value={filter.priority?.[0] ?? ''} onChange={(e) => setFilter({ ...filter, priority: e.target.value ? [e.target.value as PriorityName] : undefined })}>
              <option value="">Any</option><option value="high">High</option><option value="medium">Medium</option><option value="low">Low</option><option value="none">None</option>
            </select>
          </label>
          <label>Tag<input value={filter.tags?.[0] ?? ''} maxLength={64} onChange={(e) => setFilter({ ...filter, tags: e.target.value.trim() ? [e.target.value.trim()] : undefined })} /></label>
          {view.kind === 'smart' && (
            <label>List
              <select value={filter.lists?.[0] ?? ''} onChange={(e) => setFilter({ ...filter, lists: e.target.value ? [e.target.value] : undefined })}>
                <option value="">All lists</option>
                {lists.map((c) => <option key={c.id} value={c.id}>{listName(c.id)}</option>)}
              </select>
            </label>
          )}
          <label>Due from<input type="date" value={filter.due_from ?? ''} onChange={(e) => setFilter({ ...filter, due_from: e.target.value || undefined })} /></label>
          <label>Due to<input type="date" value={filter.due_to ?? ''} onChange={(e) => setFilter({ ...filter, due_to: e.target.value || undefined })} /></label>
          <label>Status
            <select value={filter.status?.[0] ?? ''} onChange={(e) => setFilter({ ...filter, status: e.target.value ? [e.target.value as 'needs-action'] : undefined })}>
              <option value="">Open</option><option value="needs-action">Not started</option><option value="in-process">In progress</option><option value="completed">Done</option><option value="cancelled">Cancelled</option>
            </select>
          </label>
          <button type="button" className="btn-ghost btn-sm" onClick={() => setFilter({})}>Clear</button>
        </div>
      )}

      {error && <p role="alert">{error}</p>}
      {flat.length === 0 && !error && (
        <EmptyState title={q ? 'No tasks match' : view.kind === 'smart' && view.id === 'completed' ? 'Nothing completed yet' : 'Nothing here'}>
          {q ? 'Try fewer words or another view.' : 'Add one above, or press n anywhere.'}
        </EmptyState>
      )}
      <ul className="tk-list" ref={listEl} aria-label={`${title} tasks`}>
        {groups.map((g) => (
          <li key={g.label || 'all'} className="tk-day">
            {g.label && <h2 className="tk-day-head">{g.label}</h2>}
            <ul>
              {g.items.map(({ task: t, depth }) => {
                const i = flat.findIndex((r) => r.task === t);
                const due = dueLabel(t.due);
                const prio = priorityName(t.priority);
                const late = due?.overdue && t.status !== 'COMPLETED';
                const repeat = describe(t.rrule, t.due ? new Date(t.due.length === 10 ? `${t.due}T12:00` : t.due) : new Date());
                return (
                  <li key={`${t.calendar_id}/${t.uid}`} className="tk-row" data-priority={prio.toLowerCase() || undefined} data-depth={depth || undefined}
                    data-done={t.status === 'COMPLETED' || undefined} aria-selected={i === selected}>
                    <input type="checkbox" className="tk-check" checked={t.status === 'COMPLETED'} disabled={!t.editable}
                      aria-label={`${t.status === 'COMPLETED' ? 'Reopen' : 'Complete'} ${t.title}`} onChange={() => void toggle(t)} />
                    <button type="button" className="tk-open" onClick={() => { setSelected(i); setDialog({ task: t }); }} onFocus={() => setSelected(i)}>
                      <span className="tk-title">{t.title}</span>
                      <span className="tk-row-meta">
                        {due && <span className={late ? 'tk-due tk-late' : due.today ? 'tk-due tk-today' : 'tk-due'}><CalendarClock size={13} aria-hidden="true" />{late && 'Overdue · '}{due.text}</span>}
                        {prio === 'High' && <span className="tk-prio">High</span>}
                        {t.status === 'IN-PROCESS' && <span className="tk-state">In progress</span>}
                        {!!t.subtasks && (
                          <span className="tk-subs">{t.subtasks_done ?? 0} of {t.subtasks} done
                            <progress className="tk-bar" max={t.subtasks} value={t.subtasks_done ?? 0} aria-hidden="true" />
                          </span>
                        )}
                        {repeat && <span className="tk-repeat" title={repeat}><Repeat size={13} aria-label={repeat} /></span>}
                        {!!t.estimate_min && <span className="tk-est">{estimateText(t.estimate_min)}</span>}
                        {t.source === 'kypost' && <span className="tk-mail"><Mail size={13} aria-hidden="true" /> From mail</span>}
                        {view.kind !== 'list' && <span className="tk-list-name">{listName(t.calendar_id)}</span>}
                        {t.tags?.map((tag) => <span key={tag} className="tk-tag">#{tag}</span>)}
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </li>
        ))}
      </ul>
      <p className="tk-keys" aria-label="Keyboard shortcuts"><kbd>n</kbd> new · <kbd>x</kbd> complete · <kbd>e</kbd> edit · <kbd>/</kbd> search · <kbd>j</kbd>/<kbd>k</kbd> move</p>

      {dialog && (
        <TaskDialog key={dialog.task ? dialog.task.etag : 'new'} task={dialog.task} lists={lists.map((c) => (c.id === inboxId ? { ...c, name: 'Inbox' } : c))} defaultList={targetList}
          subtasks={dialog.task ? subtasksOf(dialog.task) : []} drive={drive} initial={dialog.initial}
          onClose={() => setDialog(null)} onChanged={() => void load()} />
      )}
      {naming && (
        <NameDialog title={naming === 'list' ? 'New list' : 'Save view'} label={naming === 'list' ? 'List name' : 'View name'} onClose={() => setNaming(null)}
          onSave={async (name) => {
            if (naming === 'list') {
              const c = await createCalendar({ name, components: ['VTODO'] });
              await loadMeta();
              go({ kind: 'list', id: c.id });
            } else {
              const v = await createView(name, { ...filter, q: q.trim() || undefined, sort, lists: view.kind === 'list' ? [view.id] : filter.lists, area: view.kind === 'area' ? view.id : undefined });
              await loadMeta();
              go({ kind: 'view', id: v.id });
            }
          }} />
      )}
      {managing && <AreasDialog areas={areas} lists={lists.filter((c) => c.id !== inboxId)} onClose={() => setManaging(false)} onSaved={(a) => { setAreas(a); setManaging(false); }} />}
      {sidebarSlot && createPortal(sidebar, sidebarSlot)}
    </section>
  );
}

function NameDialog({ title, label, onSave, onClose }: { title: string; label: string; onSave: (name: string) => Promise<void>; onClose: () => void }) {
  const [name, setName] = useState('');
  const [error, setError] = useState('');
  return (
    <Modal title={title} onClose={onClose}>
      <form className="kc-form" onSubmit={(e) => { e.preventDefault(); void onSave(name.trim()).then(onClose, (err) => setError(err instanceof Error ? err.message : 'Could not save')); }}>
        <label htmlFor="tk-name">{label}</label>
        <input id="tk-name" value={name} maxLength={100} required autoFocus onChange={(e) => setName(e.target.value)} />
        {error && <p role="alert">{error}</p>}
        <div className="modal-actions">
          <button type="button" className="btn-secondary" onClick={onClose}>Cancel</button>
          <button type="submit" disabled={!name.trim()}>Save</button>
        </div>
      </form>
    </Modal>
  );
}

// AreasDialog groups lists into areas (KyCalendar-only; apps see plain lists).
function AreasDialog({ areas, lists, onSaved, onClose }: { areas: Area[]; lists: CalendarInfo[]; onSaved: (a: Area[]) => void; onClose: () => void }) {
  const [draft, setDraft] = useState<Area[]>(areas);
  const [name, setName] = useState('');
  const [error, setError] = useState('');
  const areaOf = (id: string) => draft.find((a) => a.lists.includes(id))?.id ?? '';
  const assign = (list: string, area: string) => setDraft(draft.map((a) => ({ ...a, lists: a.id === area ? [...a.lists.filter((x) => x !== list), list] : a.lists.filter((x) => x !== list) })));
  return (
    <Modal title="Areas" description="Areas group your lists, such as Work, Personal, Health or Learning. Only KyCalendar shows them." onClose={onClose}>
      <form className="kc-form" onSubmit={(e) => { e.preventDefault(); void saveAreas(draft).then(onSaved, (err) => setError(err instanceof Error ? err.message : 'Could not save')); }}>
        <ul className="tk-areas">
          {draft.map((a) => (
            <li key={a.id} className="tk-line">
              <input aria-label="Area name" value={a.name} maxLength={100} onChange={(e) => setDraft(draft.map((x) => (x.id === a.id ? { ...x, name: e.target.value } : x)))} />
              <button type="button" className="btn-ghost btn-sm" onClick={() => setDraft(draft.filter((x) => x.id !== a.id))}>Remove</button>
            </li>
          ))}
        </ul>
        <div className="tk-line">
          <input aria-label="New area" placeholder="New area" value={name} maxLength={100} onChange={(e) => setName(e.target.value)} />
          <button type="button" className="btn-secondary btn-sm" disabled={!name.trim() || draft.length >= 20}
            onClick={() => { setDraft([...draft, { id: `area_${Math.random().toString(36).slice(2, 10)}`, name: name.trim(), lists: [] }]); setName(''); }}>Add</button>
        </div>
        {draft.length > 0 && lists.length > 0 && (
          <fieldset className="tk-assign">
            <legend>Lists</legend>
            {lists.map((c) => (
              <label key={c.id} className="tk-line">
                <span>{c.name}</span>
                <select value={areaOf(c.id)} onChange={(e) => assign(c.id, e.target.value)}>
                  <option value="">No area</option>
                  {draft.map((a) => <option key={a.id} value={a.id}>{a.name || 'Untitled'}</option>)}
                </select>
              </label>
            ))}
          </fieldset>
        )}
        {error && <p role="alert">{error}</p>}
        <div className="modal-actions">
          <button type="button" className="btn-secondary" onClick={onClose}>Cancel</button>
          <button type="submit">Save</button>
        </div>
      </form>
    </Modal>
  );
}
