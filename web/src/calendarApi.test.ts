import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, canAddEvents, holdsOf, rsvp, type CalendarInfo } from './calendarApi';

afterEach(() => vi.restoreAllMocks());

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

describe('rsvp', () => {
  it('posts the answer with a strong If-Match and throws ApiError', async () => {
    const fetch = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response(JSON.stringify({ etag: 'e2' })));
    await expect(rsvp('c/1', 'u', 'e1', 'DECLINED')).resolves.toEqual({ etag: 'e2' });
    const [url, init] = fetch.mock.calls[0];
    expect(url).toBe('/api/events/c%2F1/u/rsvp');
    expect(new Headers(init?.headers).get('If-Match')).toBe('"e1"');
    expect(JSON.parse(String(init?.body))).toEqual({ partstat: 'DECLINED' });
    fetch.mockResolvedValueOnce(new Response(JSON.stringify({ error: 'changed', code: 'conflict' }), { status: 412 }));
    await expect(rsvp('c', 'u', 'e1', 'ACCEPTED', 'r1')).rejects.toMatchObject({ status: 412, code: 'conflict' } satisfies Partial<ApiError>);
  });
});
