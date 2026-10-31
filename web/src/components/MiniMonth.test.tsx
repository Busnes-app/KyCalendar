import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MiniMonth, addMonths } from './MiniMonth';

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const day = (name: string) => screen.getByRole('button', { name });

describe('MiniMonth', () => {
  it('names the grid by its month, marks today and highlights the visible week', () => {
    vi.useFakeTimers({ now: new Date(2026, 9, 28, 10, 15), toFake: ['Date'] });
    render(<MiniMonth range={{ start: new Date(2026, 9, 25), end: new Date(2026, 10, 1) }} onPick={() => {}} />);
    const grid = screen.getByRole('grid', { name: 'October 2026' });
    expect(within(grid).getAllByRole('gridcell')).toHaveLength(42);
    const selected = within(grid).getAllByRole('gridcell', { selected: true });
    expect(selected.map((c) => c.textContent)).toEqual(['25', '26', '27', '28', '29', '30', '31']);
    expect(day('Wednesday, October 28, 2026').getAttribute('aria-current')).toBe('date');
    // Today is in view, so it holds the grid's one tab stop.
    expect(grid.querySelectorAll('button[tabindex="0"]')).toHaveLength(1);
    expect(day('Wednesday, October 28, 2026').tabIndex).toBe(0);
  });

  it('moves focus by arrow keys across months and picks with a click', () => {
    vi.useFakeTimers({ now: new Date(2026, 9, 28, 10, 15), toFake: ['Date'] });
    const onPick = vi.fn();
    render(<MiniMonth range={{ start: new Date(2026, 9, 25), end: new Date(2026, 10, 1) }} onPick={onPick} />);
    day('Wednesday, October 28, 2026').focus();
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(day('Wednesday, November 4, 2026'));
    expect(screen.getByRole('grid', { name: 'November 2026' })).toBeTruthy();
    fireEvent.keyDown(document.activeElement!, { key: 'Home' });
    expect(document.activeElement).toBe(day('Sunday, November 1, 2026'));
    fireEvent.keyDown(document.activeElement!, { key: 'PageUp' });
    expect(document.activeElement).toBe(day('Thursday, October 1, 2026'));
    fireEvent.click(day('Friday, October 9, 2026'));
    expect(onPick).toHaveBeenCalledWith(new Date(2026, 9, 9));
  });

  it('pages months with its arrows without moving the main view', () => {
    const onPick = vi.fn();
    render(<MiniMonth range={{ start: new Date(2026, 0, 1), end: new Date(2026, 1, 1) }} onPick={onPick} />);
    fireEvent.click(screen.getByRole('button', { name: 'Previous month' }));
    expect(screen.getByRole('grid', { name: 'December 2025' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Next month' }));
    fireEvent.click(screen.getByRole('button', { name: 'Next month' }));
    expect(screen.getByRole('grid', { name: 'February 2026' })).toBeTruthy();
    expect(onPick).not.toHaveBeenCalled();
  });

  it('keeps the day of the month where it exists', () => {
    expect(addMonths(new Date(2026, 0, 31), 1)).toEqual(new Date(2026, 1, 28));
    expect(addMonths(new Date(2024, 0, 31), 1)).toEqual(new Date(2024, 1, 29));
    expect(addMonths(new Date(2026, 4, 15), -12)).toEqual(new Date(2025, 4, 15));
  });
});
