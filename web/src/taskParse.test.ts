import { describe, expect, it } from 'vitest';
import { parseQuick } from './taskParse';

const lists = [{ id: 'l1', name: 'Errands' }, { id: 'l2', name: 'Work stuff' }];
// Sunday 15 November 2026, 10:00 local.
const now = new Date(2026, 10, 15, 10, 0);
const p = (s: string) => parseQuick(s, lists, now);

describe('parseQuick', () => {
  it('keeps plain text as the title', () => {
    expect(p('Buy milk')).toMatchObject({ title: 'Buy milk', tags: [], chips: [] });
    expect(p('Read chapter 3')).toMatchObject({ title: 'Read chapter 3' });
  });
  it('reads dates and times', () => {
    expect(p('Call Ana tomorrow 3pm').due).toMatch(/^2026-11-16T15:00:00[+-]\d\d:\d\d$/);
    expect(p('Call Ana tomorrow 3pm').title).toBe('Call Ana');
    expect(p('Pay rent next fri').due).toBe('2026-11-27');
    expect(p('Pay rent fri').due).toBe('2026-11-20');
    expect(p('Pay rent on monday').due).toBe('2026-11-16');
    expect(p('Dentist nov 20').due).toBe('2026-11-20');
    expect(p('Dentist 3 jan').due).toBe('2027-01-03');
    expect(p('Report in 2 weeks').due).toBe('2026-11-29');
    expect(p('Report 2026-12-01 at 9:30').due).toMatch(/^2026-12-01T09:30/);
    expect(p('Lunch at noon').due).toMatch(/^2026-11-15T12:00/);
  });
  it('reads repeats', () => {
    expect(p('Water plants every 2 weeks')).toMatchObject({ title: 'Water plants', rrule: 'FREQ=WEEKLY;INTERVAL=2', due: '2026-11-15' });
    expect(p('Standup every weekday 9am').rrule).toBe('FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR');
    expect(p('Gym every mon and thu').rrule).toBe('FREQ=WEEKLY;BYDAY=MO,TH');
    expect(p('Gym every mon and thu').due).toBe('2026-11-16');
    expect(p('Invoice monthly').rrule).toBe('FREQ=MONTHLY');
    expect(p('Review every other month').rrule).toBe('FREQ=MONTHLY;INTERVAL=2');
  });
  it('reads priority, tags and list', () => {
    expect(p('Ship it !high #work #Release @errands')).toMatchObject({ title: 'Ship it', priority: 1, tags: ['work', 'Release'], list: lists[0] });
    expect(p('Draft @work_stuff !low').list).toEqual(lists[1]);
    expect(p('Draft @nowhere').title).toBe('Draft @nowhere');
  });
  it('never leaves an empty title', () => {
    expect(p('tomorrow')).toEqual({ title: 'tomorrow', tags: [], chips: [] });
    expect(p('#tag !high')).toEqual({ title: '#tag !high', tags: [], chips: [] });
  });
  it('does not read words as days', () => {
    expect(p('Month end report').due).toBeUndefined();
    expect(p('Sunny walk').due).toBeUndefined();
    expect(p('Room 12').due).toBeUndefined();
  });
});
