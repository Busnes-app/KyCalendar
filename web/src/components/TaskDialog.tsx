import { useMemo, useState } from 'react';
import { ExternalLink, Paperclip, Plus, Trash2, X } from 'lucide-react';
import { Modal } from './ui';
import { pickFromDrive } from '../drivePicker';
import type { CalendarInfo } from '../calendarApi';
import { createTask, deleteTask, imageURL, patchTask, type FileLink, type Reminder, type Status, type Task, type TaskBody } from '../tasksApi';
import { dateOnly, localRFC3339 } from '../taskParse';
import { buildRule, presetLabel, readRule, type Preset, type Rule } from '../taskRepeat';
import { linkify } from '../linkify';

const STATUS: { value: Status; label: string }[] = [
  { value: 'NEEDS-ACTION', label: 'Not started' },
  { value: 'IN-PROCESS', label: 'In progress' },
  { value: 'COMPLETED', label: 'Done' },
  { value: 'CANCELLED', label: 'Cancelled' },
];
const PRIORITY = [{ value: 0, label: 'None' }, { value: 9, label: 'Low' }, { value: 5, label: 'Medium' }, { value: 1, label: 'High' }];
const ESTIMATES = [15, 30, 45, 60, 90, 120];
const REMINDERS = [{ v: 0, label: 'At the due time' }, { v: 15, label: '15 minutes before' }, { v: 60, label: '1 hour before' }, { v: 1440, label: '1 day before' }];
const PRESETS: Preset[] = ['none', 'daily', 'weekdays', 'weekly', 'monthly', 'monthly-nth', 'monthly-last', 'yearly'];

export const estimateText = (m: number) => (m < 60 ? `${m}m` : m % 60 ? `${Math.floor(m / 60)}h${String(m % 60).padStart(2, '0')}` : `${m / 60}h`);

// A when field: a date, and a time unless it is a whole-day value.
interface When { date: string; time: string }
function whenOf(v: string | undefined): When {
  if (!v) return { date: '', time: '' };
  if (v.length === 10) return { date: v, time: '' };
  const d = new Date(v);
  return { date: dateOnly(d), time: `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}` };
}
function wireOf(w: When): string {
  if (!w.date) return '';
  if (!w.time) return w.date;
  const [y, m, d] = w.date.split('-').map(Number);
  const [h, min] = w.time.split(':').map(Number);
  return localRFC3339(new Date(y, m - 1, d, h, min));
}
const anchorOf = (w: When) => (w.date ? new Date(`${w.date}T12:00`) : new Date());

interface Props {
  task: Task | null; // null: a new task
  lists: CalendarInfo[];
  defaultList: string;
  subtasks: Task[];
  drive?: string;
  initial?: TaskBody;
  onClose: () => void;
  onChanged: () => void;
}

// TaskDialog shows and edits every field of a task, in the shared Modal. Save sends only what
// changed, so properties the user did not touch stay exactly as the task's app wrote them.
export function TaskDialog({ task, lists, defaultList, subtasks, drive = '', initial = {}, onClose, onChanged }: Props) {
  const readOnly = task ? !task.editable : false;
  const start: TaskBody = task ?? initial;
  const [list, setList] = useState(task?.calendar_id ?? defaultList);
  const [title, setTitle] = useState(start.title ?? '');
  const [notes, setNotes] = useState(start.notes ?? '');
  const [status, setStatus] = useState<Status>(start.status ?? 'NEEDS-ACTION');
  const [priority, setPriority] = useState(start.priority ?? 0);
  const [due, setDue] = useState(whenOf(start.due));
  const [begin, setBegin] = useState(whenOf(start.start));
  const [estimate, setEstimate] = useState(start.estimate_min ?? 0);
  const [tags, setTags] = useState((start.tags ?? []).join(', '));
  const [rule, setRule] = useState<Rule>(readRule(start.rrule));
  const [fromDone, setFromDone] = useState(start.repeat_from_completion ?? false);
  const [reminders, setReminders] = useState<Reminder[]>(start.reminders ?? []);
  const [files, setFiles] = useState<FileLink[]>(start.files ?? []);
  const [newSub, setNewSub] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const anchor = anchorOf(due.date ? due : begin);
  const writable = lists.filter((c) => c.role !== 'reader' && !c.subscription);

  const body = useMemo((): TaskBody => {
    const b: TaskBody = {};
    const tagList = tags.split(',').map((t) => t.trim()).filter(Boolean);
    const rrule = buildRule(rule, anchor);
    const put = <K extends keyof TaskBody>(k: K, v: TaskBody[K], was: TaskBody[K]) => {
      if (JSON.stringify(v ?? '') !== JSON.stringify(was ?? '')) b[k] = v;
    };
    put('title', title.trim(), task?.title);
    put('notes', notes, task?.notes ?? '');
    put('status', status, task?.status ?? 'NEEDS-ACTION');
    put('priority', priority, task?.priority ?? 0);
    if (wireOf(due) !== (task ? wireOf(whenOf(task.due)) : '')) b.due = wireOf(due);
    if (wireOf(begin) !== (task ? wireOf(whenOf(task.start)) : '')) b.start = wireOf(begin);
    put('estimate_min', estimate, task?.estimate_min ?? 0);
    put('tags', tagList, task?.tags ?? []);
    put('rrule', rrule, task?.rrule ?? '');
    put('repeat_from_completion', rrule ? fromDone : false, task?.repeat_from_completion ?? false);
    put('reminders', reminders, task?.reminders ?? []);
    put('files', files, task?.files ?? []);
    return b;
  }, [title, notes, status, priority, due, begin, estimate, tags, rule, fromDone, reminders, files, task, anchor]);

  async function act(f: () => Promise<unknown>) {
    setBusy(true);
    setError('');
    try {
      await f();
      onChanged();
      return true;
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Something went wrong');
      return false;
    } finally {
      setBusy(false);
    }
  }

  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (!title.trim()) { setError('A task needs a title.'); return; }
    const ok = await act(() => (task ? (Object.keys(body).length ? patchTask(task, body) : Promise.resolve()) : createTask(list, { ...initial, ...body, title: title.trim() })));
    if (ok) onClose();
  }

  async function addSubtask(e: React.FormEvent) {
    e.preventDefault();
    if (!task || !newSub.trim()) return;
    if (await act(() => createTask(task.calendar_id, { title: newSub.trim(), parent: task.uid }))) setNewSub('');
  }

  const repeatOn = rule.preset !== 'none';
  return (
    <Modal title={task ? (readOnly ? 'Task' : 'Edit task') : 'New task'} onClose={onClose} wide closable>
      <form className="kc-form tk-form" onSubmit={save}>
        {readOnly && <p className="kc-hint" role="note">This list is read-only for you.</p>}
        <label htmlFor="tk-title-input">Title</label>
        <input id="tk-title-input" value={title} maxLength={1000} required disabled={readOnly} autoFocus={!task} onChange={(e) => setTitle(e.target.value)} />
        {!task && (
          <>
            <label htmlFor="tk-list">List</label>
            <select id="tk-list" value={list} onChange={(e) => setList(e.target.value)}>
              {writable.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
            </select>
          </>
        )}
        {task?.source === 'kypost' && task.url && (
          <p className="tk-source"><a href={task.url} target="_blank" rel="noopener noreferrer"><ExternalLink size={14} aria-hidden="true" /> Open the email in KyPost</a></p>
        )}
        <div className="kc-pair">
          <div className="field">
            <label htmlFor="tk-status">Status</label>
            <select id="tk-status" value={status} disabled={readOnly} onChange={(e) => setStatus(e.target.value as Status)}>
              {STATUS.map((s) => <option key={s.value} value={s.value}>{s.label}</option>)}
            </select>
          </div>
          <fieldset className="tk-segmented" disabled={readOnly}>
            <legend>Priority</legend>
            {PRIORITY.map((p) => (
              <label key={p.value} data-checked={priority === p.value || undefined}>
                <input type="radio" name="tk-priority" checked={priority === p.value} onChange={() => setPriority(p.value)} />
                {p.label}
              </label>
            ))}
          </fieldset>
        </div>
        <div className="tk-whens">
          <div className="field">
            <label htmlFor="tk-due">Due</label>
            <div className="tk-when">
              <input id="tk-due" type="date" value={due.date} disabled={readOnly} onChange={(e) => setDue({ ...due, date: e.target.value })} />
              <input type="time" aria-label="Due time (optional)" value={due.time} disabled={readOnly || !due.date} onChange={(e) => setDue({ ...due, time: e.target.value })} />
            </div>
          </div>
          <div className="field">
            <label htmlFor="tk-start">Start <span className="field-optional">(hidden until then)</span></label>
            <div className="tk-when">
              <input id="tk-start" type="date" value={begin.date} disabled={readOnly} onChange={(e) => setBegin({ ...begin, date: e.target.value })} />
              <input type="time" aria-label="Start time (optional)" value={begin.time} disabled={readOnly || !begin.date} onChange={(e) => setBegin({ ...begin, time: e.target.value })} />
            </div>
          </div>
        </div>
        <div className="kc-pair">
          <div className="field">
            <label htmlFor="tk-estimate">Estimate</label>
            <select id="tk-estimate" value={ESTIMATES.includes(estimate) || !estimate ? String(estimate) : 'custom'} disabled={readOnly}
              onChange={(e) => setEstimate(e.target.value === 'custom' ? (estimate || 20) : Number(e.target.value))}>
              <option value="0">None</option>
              {ESTIMATES.map((m) => <option key={m} value={m}>{estimateText(m)}</option>)}
              <option value="custom">Custom…</option>
            </select>
            {estimate > 0 && !ESTIMATES.includes(estimate) && (
              <input type="number" aria-label="Estimate in minutes" min={1} max={1440} value={estimate} disabled={readOnly} onChange={(e) => setEstimate(Math.max(0, Math.min(1440, Number(e.target.value) || 0)))} />
            )}
          </div>
          <div className="field">
            <label htmlFor="tk-tags">Tags <span className="field-optional">(comma separated)</span></label>
            <input id="tk-tags" value={tags} disabled={readOnly} onChange={(e) => setTags(e.target.value)} />
          </div>
        </div>
        <label htmlFor="tk-repeat">Repeat</label>
        <select id="tk-repeat" value={rule.preset} disabled={readOnly || (!due.date && !begin.date && !repeatOn)}
          onChange={(e) => setRule({ ...rule, preset: e.target.value as Preset })}>
          {PRESETS.map((p) => <option key={p} value={p}>{presetLabel(p, anchor)}</option>)}
          {rule.preset === 'custom' && <option value="custom">{presetLabel('custom', anchor)}</option>}
        </select>
        {!due.date && !begin.date && !repeatOn && <p className="kc-hint">Set a due or start date to repeat.</p>}
        {repeatOn && rule.preset !== 'custom' && (
          <div className="kc-pair">
            {rule.preset !== 'weekdays' && (
              <div className="field">
                <label htmlFor="tk-every">Every</label>
                <input id="tk-every" type="number" min={1} max={999} value={rule.interval} disabled={readOnly} onChange={(e) => setRule({ ...rule, interval: Math.max(1, Number(e.target.value) || 1) })} />
              </div>
            )}
            <div className="field">
              <label htmlFor="tk-ends">Ends</label>
              <select id="tk-ends" value={rule.until !== undefined ? 'until' : rule.count ? 'count' : 'never'} disabled={readOnly}
                onChange={(e) => setRule({ ...rule, until: e.target.value === 'until' ? (rule.until ?? dateOnly(new Date(Date.now() + 90 * 86400000))) : undefined, count: e.target.value === 'count' ? (rule.count || 10) : undefined })}>
                <option value="never">Never</option>
                <option value="until">On a date</option>
                <option value="count">After a number of times</option>
              </select>
              {rule.until !== undefined && <input type="date" aria-label="Last date" value={rule.until} disabled={readOnly} onChange={(e) => setRule({ ...rule, until: e.target.value })} />}
              {rule.count !== undefined && rule.until === undefined && <input type="number" aria-label="Times" min={1} max={999} value={rule.count} disabled={readOnly} onChange={(e) => setRule({ ...rule, count: Math.max(1, Number(e.target.value) || 1) })} />}
            </div>
          </div>
        )}
        {repeatOn && (
          <label className="kc-check"><input type="checkbox" checked={fromDone} disabled={readOnly} onChange={(e) => setFromDone(e.target.checked)} /> Repeat from the completion date</label>
        )}
        <fieldset className="tk-reminders" disabled={readOnly}>
          <legend>Reminders</legend>
          {reminders.map((r, i) => (
            <div key={i} className="tk-line">
              {r.at ? <span>{new Date(r.at).toLocaleString()}</span> : (
                <select aria-label={`Reminder ${i + 1}`} value={String(r.before_min ?? 0)} onChange={(e) => setReminders(reminders.map((x, j) => (j === i ? { before_min: Number(e.target.value) } : x)))}>
                  {!REMINDERS.some((o) => o.v === (r.before_min ?? 0)) && <option value={String(r.before_min)}>{r.before_min} minutes before</option>}
                  {REMINDERS.map((o) => <option key={o.v} value={o.v}>{o.label}</option>)}
                </select>
              )}
              <button type="button" className="btn-ghost kc-icon" aria-label={`Remove reminder ${i + 1}`} onClick={() => setReminders(reminders.filter((_, j) => j !== i))}><X size={14} aria-hidden="true" /></button>
            </div>
          ))}
          {reminders.length < 10 && (due.date || begin.date) && (
            <button type="button" className="btn-ghost btn-sm tk-add-reminder" onClick={() => setReminders([...reminders, { before_min: 15 }])}><Plus size={14} aria-hidden="true" /> Add reminder</button>
          )}
        </fieldset>
        {task?.images?.map((img, i) => (
          <figure key={i} className="tk-image">
            <img src={imageURL(task, i)} alt={img.name || `Image ${i + 1}`} />
            {img.name && <figcaption>{img.name}</figcaption>}
          </figure>
        ))}
        <label htmlFor="tk-notes">Notes</label>
        {readOnly ? <p className="tk-notes">{linkify(task?.notes ?? "")}</p> : <textarea id="tk-notes" value={notes} maxLength={65536} onChange={(e) => setNotes(e.target.value)} />}
        {(files.length > 0 || (drive && !readOnly)) && (
          <fieldset>
            <legend>Files</legend>
            <ul className="tk-files">
              {files.map((f) => (
                <li key={f.url}>
                  <a href={f.url} target="_blank" rel="noopener noreferrer"><Paperclip size={14} aria-hidden="true" /> {f.name}</a>
                  {!readOnly && <button type="button" className="btn-ghost kc-icon" aria-label={`Remove ${f.name}`} onClick={() => setFiles(files.filter((x) => x.url !== f.url))}><X size={14} aria-hidden="true" /></button>}
                </li>
              ))}
            </ul>
            {drive && !readOnly && (
              <button type="button" className="btn-secondary btn-sm" onClick={() => void pickFromDrive(drive).then((p) => setFiles((now) => [...now, ...p.filter((x) => !now.some((f) => f.url === x.url))].slice(0, 20)))}>
                Add file from KyDrive
              </button>
            )}
          </fieldset>
        )}
        {task && (
          <fieldset className="tk-subtasks">
            <legend>Subtasks{subtasks.length > 0 && ` · ${subtasks.filter((s) => s.status === 'COMPLETED').length} of ${subtasks.length} done`}</legend>
            <ul>
              {subtasks.map((s) => (
                <li key={s.uid}>
                  <label className="kc-check">
                    <input type="checkbox" checked={s.status === 'COMPLETED'} disabled={!s.editable || busy}
                      onChange={(e) => void act(() => patchTask(s, { status: e.target.checked ? 'COMPLETED' : 'NEEDS-ACTION' }))} />
                    {s.title}
                  </label>
                </li>
              ))}
            </ul>
            {!readOnly && (
              <div className="tk-line">
                <input aria-label="New subtask" placeholder="Add a subtask" value={newSub} maxLength={1000}
                  onChange={(e) => setNewSub(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter') void addSubtask(e); }} />
                <button type="button" className="btn-secondary btn-sm" disabled={!newSub.trim() || busy} onClick={(e) => void addSubtask(e)}>Add</button>
              </div>
            )}
          </fieldset>
        )}
        {error && <p role="alert">{error}</p>}
        <div className="modal-actions tk-sticky-actions">
          {task && !readOnly && (
            <button type="button" className="btn-danger push-start" disabled={busy}
              onClick={() => window.confirm(`Delete “${task.title}”?${subtasks.length ? ' Its subtasks stay.' : ''}`) && void act(() => deleteTask(task)).then((ok) => ok && onClose())}>
              <Trash2 size={14} aria-hidden="true" /> Delete
            </button>
          )}
          <button type="button" className="btn-secondary" onClick={onClose}>{readOnly ? 'Close' : 'Cancel'}</button>
          {!readOnly && <button type="submit" disabled={busy}>{task ? 'Save' : 'Add task'}</button>}
        </div>
      </form>
    </Modal>
  );
}
