import { describe, expect, it } from 'vitest';
import { ACCENT, PALETTE, calendarColors, nextColor, textOn } from './calendarColors';

describe('calendar colours', () => {
  it('gives a new calendar the first colour its neighbours do not use', () => {
    expect(nextColor([])).toBe(PALETTE[0]);
    expect(nextColor([PALETTE[0], '#BF3F18'])).toBe(PALETTE[2]);
    expect(nextColor([], 2)).toBe(PALETTE[2]);
  });

  it('shows calendars stored without a colour in distinct colours, the first in the accent', () => {
    const shown = calendarColors([
      { id: 'a', color: '' },
      { id: 'b', color: PALETTE[1] },
      { id: 'c', color: '' },
    ]);
    expect(new Set(shown.values()).size).toBe(3);
    expect(shown.get('a')).toBe(ACCENT);
    expect(shown.get('b')).toBe(PALETTE[1]);
    expect(shown.get('c')).toBe(PALETTE[0]);
  });

  it('keeps event text readable on any calendar colour', () => {
    for (const c of PALETTE) expect(textOn(c)).toBe('#ffffff');
    expect(textOn('#f5e663')).toBe('#182326');
    expect(textOn('var(--ky-accent)')).toBeUndefined();
  });
});
