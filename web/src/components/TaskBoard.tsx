import { useState } from 'react';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import type { Status, Task } from '../tasksApi';
import { dueLabel, priorityName } from '../taskRepeat';

const COLUMNS: { status: Status; label: string; empty: string }[] = [
  { status: 'NEEDS-ACTION', label: 'Not started', empty: 'Nothing waiting' },
  { status: 'IN-PROCESS', label: 'In progress', empty: 'Nothing in progress' },
  { status: 'COMPLETED', label: 'Done', empty: 'Nothing done yet' },
];

// TaskBoard is the Kanban layout: one column per STATUS. Dragging a card to another column, or
// its arrow buttons, sets the status through the normal task PATCH. Cancelled tasks are not shown.
export function TaskBoard({ tasks, listName, onOpen, onMove }: {
  tasks: Task[]; listName: (id: string) => string; onOpen: (t: Task) => void; onMove: (t: Task, s: Status) => void;
}) {
  const [over, setOver] = useState<Status | null>(null);
  const key = (t: Task) => `${t.calendar_id}/${t.uid}`;
  return (
    <div className="tb" role="list" aria-label="Board">
      {COLUMNS.map((col, ci) => {
        const items = tasks.filter((t) => t.status === col.status);
        return (
          <section key={col.status} className="tb-col" role="listitem" aria-labelledby={`tb-${col.status}`} data-over={over === col.status || undefined}
            onDragOver={(e) => { if (e.dataTransfer.types.includes('text/x-task')) { e.preventDefault(); setOver(col.status); } }}
            onDragLeave={() => setOver(null)}
            onDrop={(e) => {
              setOver(null);
              const t = tasks.find((x) => key(x) === e.dataTransfer.getData('text/x-task'));
              if (t && t.status !== col.status) { e.preventDefault(); onMove(t, col.status); }
            }}>
            <h2 id={`tb-${col.status}`}>{col.label} <span className="tb-count">{items.length}</span></h2>
            {items.length === 0 && <p className="tb-empty">{col.empty}</p>}
            <ul>
              {items.map((t) => {
                const due = dueLabel(t.due);
                return (
                  <li key={key(t)} className="tb-card" draggable={t.editable} data-done={t.status === 'COMPLETED' || undefined}
                    onDragStart={(e) => { e.dataTransfer.setData('text/x-task', key(t)); e.dataTransfer.effectAllowed = 'move'; }}>
                    <button type="button" className="tb-title" onClick={() => onOpen(t)}>{t.title}</button>
                    <span className="tb-meta">
                      {due && t.status !== 'COMPLETED' && <span className={due.overdue ? 'tk-late' : ''}>{due.overdue ? 'Overdue' : due.text}</span>}
                      {listName(t.calendar_id) && <span>{listName(t.calendar_id)}</span>}
                      {!!t.subtasks && <span>{t.subtasks_done ?? 0} of {t.subtasks} done</span>}
                      {priorityName(t.priority) === 'High' && <span className="tk-prio">High</span>}
                    </span>
                    {t.editable && (
                      <span className="tb-move">
                        {ci > 0 && <button type="button" className="btn-ghost kc-icon" aria-label={`Move ${t.title} to ${COLUMNS[ci - 1].label}`} onClick={() => onMove(t, COLUMNS[ci - 1].status)}><ChevronLeft size={14} aria-hidden="true" /></button>}
                        {ci < COLUMNS.length - 1 && <button type="button" className="btn-ghost kc-icon" aria-label={`Move ${t.title} to ${COLUMNS[ci + 1].label}`} onClick={() => onMove(t, COLUMNS[ci + 1].status)}><ChevronRight size={14} aria-hidden="true" /></button>}
                      </span>
                    )}
                  </li>
                );
              })}
            </ul>
          </section>
        );
      })}
    </div>
  );
}
