import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { TaskDialog } from './TaskDialog';
import type { CalendarInfo } from '../calendarApi';
import type { Task } from '../tasksApi';

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const lists: CalendarInfo[] = [
  { id: 'cal_t', name: 'Errands', color: '', description: '', kind: 'personal', role: 'owner', dav_path: '', components: ['VTODO'] },
  { id: 'cal_r', name: 'Team', color: '', description: '', kind: 'group', role: 'reader', dav_path: '', components: ['VTODO'] },
];
const task: Task = {
  calendar_id: 'cal_t', uid: 'u1', etag: 'e1', title: 'Pack', status: 'NEEDS-ACTION', priority: 5,
  due: '2026-11-20T09:00:00+01:00', tags: ['trip'], rrule: 'FREQ=MONTHLY;BYDAY=MO;BYSETPOS=1', editable: true,
  notes: '<img src=x onerror=alert(1)>',
};

function capture() {
  const calls: { method: string; url: string; body?: Record<string, unknown>; ifMatch?: string | null }[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    calls.push({ method: init?.method ?? 'GET', url: String(input), body: init?.body ? JSON.parse(String(init.body)) : undefined, ifMatch: new Headers(init?.headers).get('If-Match') });
    return new Response(JSON.stringify({ ...task, etag: 'e2' }), { status: init?.method === 'POST' ? 201 : 200 });
  });
  return calls;
}

describe('TaskDialog', () => {
  it('sends only the fields that changed, with If-Match, and keeps a custom rule', async () => {
    const calls = capture();
    const onClose = vi.fn();
    render(<TaskDialog task={task} lists={lists} defaultList="cal_t" subtasks={[]} onClose={onClose} onChanged={vi.fn()} />);
    expect(screen.getByLabelText('Repeat')).toHaveProperty('value', 'custom');
    fireEvent.change(screen.getByLabelText('Title'), { target: { value: 'Pack the bag' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(calls).toHaveLength(1);
    expect(calls[0]).toMatchObject({ method: 'PATCH', ifMatch: '"e1"', body: { title: 'Pack the bag' } });
  });

  it('is read-only on a list the user only reads, and renders notes as text', () => {
    capture();
    render(<TaskDialog task={{ ...task, calendar_id: 'cal_r', editable: false }} lists={lists} defaultList="cal_t" subtasks={[]} onClose={vi.fn()} onChanged={vi.fn()} />);
    expect(screen.queryByRole('button', { name: 'Save' })).toBeNull();
    expect(screen.queryByRole('button', { name: /Delete/ })).toBeNull();
    expect(screen.getByLabelText('Title')).toHaveProperty('disabled', true);
    expect(document.querySelector('img')).toBeNull();
  });

  it('creates in a writable list only', async () => {
    const calls = capture();
    render(<TaskDialog task={null} lists={lists} defaultList="cal_t" subtasks={[]} onClose={vi.fn()} onChanged={vi.fn()} />);
    expect(screen.queryByRole('option', { name: 'Team' })).toBeNull();
    fireEvent.change(screen.getByLabelText('Title'), { target: { value: 'Milk' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add task' }));
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0]).toMatchObject({ method: 'POST', url: '/api/calendars/cal_t/tasks', body: { title: 'Milk' } });
  });
});
