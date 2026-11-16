import { describe, expect, it } from 'vitest';
import { fit, gaps, suggestions, hm } from './dayPlan';

const t = (h: number, m = 0) => new Date(2026, 10, 16, h, m).getTime();
const day = [t(8), t(18)] as const;
const events = [{ start: t(9, 30), end: t(10) }, { start: t(12, 30), end: t(13, 15) }];

describe('dayPlan', () => {
  it('finds labelled gaps around events', () => {
    const g = gaps(...day, events);
    expect(g.map((x) => x.minutes)).toEqual([90, 150, 285]);
  });
  it('fits a task in the first gap after now, snapped to 15 minutes', () => {
    const g = gaps(...day, events);
    expect(fit(g, 30)).toBe(t(8));
    expect(fit(g, 30, t(9, 7))).toBe(t(10));
    expect(fit(g, 600)).toBeNull();
  });
  it('suggests a task that fits each gap', () => {
    const g = gaps(...day, events);
    const s = suggestions(g, [{ id: 'big', minutes: 200 }, { id: 'small', minutes: 15 }], t(8));
    expect(s.map((x) => x.id)).toEqual(['small', 'big']);
  });
  it('says lengths as words', () => {
    expect([hm(15), hm(60), hm(105)]).toEqual(['15m', '1h', '1h 45m']);
  });
});
