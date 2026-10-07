import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CalendarSidebar } from './CalendarSidebar';
import type { CalendarInfo } from '../calendarApi';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const cals: CalendarInfo[] = [
  { id: 'cal_p', name: 'Mine', color: '#00aa11', description: '', kind: 'personal', role: 'owner', dav_path: '' },
  { id: 'cal_m', name: 'Team', color: '', description: '', kind: 'group', role: 'manager', dav_path: '' },
  { id: 'cal_r', name: 'News', color: '', description: '', kind: 'group', role: 'reader', dav_path: '' },
];

describe('CalendarSidebar', () => {
  it('toggles visibility and offers edits only where the role allows', () => {
    const onToggle = vi.fn();
    render(<CalendarSidebar calendars={cals} hidden={new Set(['cal_r'])} onToggle={onToggle} onChanged={vi.fn()} />);
    expect((screen.getByLabelText('Show News') as HTMLInputElement).checked).toBe(false);
    fireEvent.click(screen.getByLabelText('Show Mine'));
    expect(onToggle).toHaveBeenCalledWith('cal_p');
    expect(screen.getByRole('button', { name: 'Edit Mine' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Edit Team' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Edit News' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Delete Mine' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Delete Team' })).toBeNull();
  });

  it('creates a personal calendar', async () => {
    const post = vi.fn((_init?: RequestInit) => new Response(JSON.stringify(cals[0]), { status: 201 }));
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (_i, init) => post(init));
    const onChanged = vi.fn();
    render(<CalendarSidebar calendars={cals} hidden={new Set()} onToggle={vi.fn()} onChanged={onChanged} />);
    fireEvent.change(screen.getByLabelText('New calendar name'), { target: { value: 'Work' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add calendar' }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });
});
