import { describe, expect, it } from 'vitest';
import { habitGrid } from './habitGrid';

// Wednesday 18 Nov 2026, noon local.
const now = new Date(2026, 10, 18, 12);

describe('habit grid', () => {
  it('leaves today open, not missed, until it ends', () => {
    const g = habitGrid({ freq: 'daily', current: 0, longest: 1, days: ['2026-11-16'] }, '2026-11-16T08:00:00Z', now);
    expect(g.weeks).toBe(1);
    expect(g.cells.map((c) => c.state)).toEqual(['done', 'missed', 'now', 'future', 'future', 'future', 'future']);
    expect(g.rate).toBe(50); // today is not counted until it ends
    expect(g.week).toBe('1 of 3');
  });

  it('counts today once it is done', () => {
    const g = habitGrid({ freq: 'daily', current: 1, longest: 1, days: ['2026-11-18'] }, '2026-11-18T08:00:00Z', now);
    expect(g.cells.map((c) => c.state)).toEqual(['before', 'before', 'done', 'future', 'future', 'future', 'future']);
    expect(g.rate).toBe(100);
  });

  it('leaves the current week open for a weekly habit', () => {
    const g = habitGrid({ freq: 'weekly', current: 1, longest: 1, days: ['2026-11-04'] }, '2026-11-02T08:00:00Z', now);
    expect(g.weeks).toBe(3);
    expect(g.cells.map((c) => c.state)).toEqual(['done', 'missed', 'now']);
    expect(g.week).toBe('Not yet');
  });

  it('starts at the week the habit began, at most 12 weeks back, across a DST change', () => {
    const g = habitGrid({ freq: 'daily', current: 0, longest: 0, days: [] }, '2026-10-20T08:00:00Z', now);
    expect(g.weeks).toBe(5); // weeks of 19 Oct .. 16 Nov, over the end of summer time
    expect(g.first.getDay()).toBe(1);
    expect(habitGrid({ freq: 'daily', current: 0, longest: 0, days: [] }, '2025-01-01T08:00:00Z', now).weeks).toBe(12);
  });
});
