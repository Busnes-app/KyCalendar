import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Dashboard } from './Dashboard';

afterEach(cleanup);

describe('Dashboard', () => {
  it('shows KyCalendar copy and only statuses the settings state', () => {
    render(<Dashboard settings={{ app_name: 'KyCalendar', scim_enabled: false }} user={{ display_name: 'Administrator' }} onNavigate={() => {}} />);
    expect(screen.getByRole('heading', { name: 'Welcome, Administrator' })).toBeTruthy();
    const text = document.body.textContent ?? '';
    expect(text).not.toMatch(/Ky Server Base|Verified Ready|zero-CGO|Pluggable|RFC 7643/);
    expect(screen.getByText('Passwords only')).toBeTruthy();
    expect(screen.getByText('Off')).toBeTruthy();
  });

  it('links to group calendars', () => {
    const onNavigate = vi.fn();
    render(<Dashboard settings={{}} user={{ username: 'admin' }} onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole('button', { name: /Manage group calendars/ }));
    expect(onNavigate).toHaveBeenCalledWith('group-calendars');
  });
});
