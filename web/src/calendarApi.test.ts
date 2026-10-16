import { describe, expect, it } from 'vitest';
import { canAddEvents, holdsOf, type CalendarInfo } from './calendarApi';

const cal: CalendarInfo = { id: 'c', name: 'C', color: '', description: '', kind: 'personal', role: 'owner', dav_path: '', components: ['VEVENT'] };

describe('canAddEvents', () => {
  it('takes events only where the calendar holds them and the user may write', () => {
    expect(canAddEvents(cal)).toBe(true);
    expect(canAddEvents({ ...cal, components: ['VEVENT', 'VTODO'] })).toBe(true);
    expect(canAddEvents({ ...cal, components: ['VTODO'] })).toBe(false);
    expect(canAddEvents({ ...cal, role: 'reader' })).toBe(false);
    expect(canAddEvents({ ...cal, subscription: { host: 'h', refreshed_at: null, checked_at: null } })).toBe(false);
  });
  it('reads an unknown choice as events', () => {
    expect(holdsOf('tasks')).toBe('tasks');
    expect(holdsOf('both')).toBe('both');
    expect(holdsOf('VJOURNAL')).toBe('events');
  });
});
