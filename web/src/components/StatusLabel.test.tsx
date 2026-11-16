import { cleanup, render } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { StatusLabel, statusText } from './StatusLabel';

afterEach(cleanup);

describe('StatusLabel', () => {
  it('always says the status in words beside the dot', () => {
    render(<StatusLabel presence={{ state: 'away' }} />);
    const label = document.querySelector('.kc-status')!;
    expect(label.getAttribute('data-state')).toBe('away');
    expect(label.querySelector('.kc-status-text')?.textContent).toBe('Away');
    expect(label.querySelector('.kc-status-dot')?.getAttribute('aria-hidden')).toBe('true');
  });

  it('gives a meeting its end time and out-of-office its end date', () => {
    const end = new Date(2026, 10, 16, 15, 30);
    expect(statusText({ state: 'in_a_meeting', until: end.toISOString() })).toBe(`In a meeting until ${end.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`);
    expect(statusText({ state: 'out_of_office', until: end.toISOString() }, new Date(2026, 10, 1))).toMatch(/^Out of office until .*16/);
    expect(statusText({ state: 'busy', until: end.toISOString() })).toBe('Busy');
    expect(statusText({ state: 'unknown' })).toBe('Status unknown');
    expect(statusText({ state: 'bogus' as never })).toBe('Status unknown');
  });

  it('shows nothing for someone KyMessages did not answer for', () => {
    const { container } = render(<StatusLabel />);
    expect(container.textContent).toBe('');
  });
});
