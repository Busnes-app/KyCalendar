import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { FlaggedMailDialog } from './FlaggedMailDialog';

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

function serve(prefs: { kypost_flags: boolean; kypost_linked: boolean }) {
  const calls: { method: string; body?: unknown }[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (_input, init) => {
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    calls.push({ method: init?.method ?? 'GET', body });
    return new Response(JSON.stringify(body ? { ...prefs, ...body } : prefs), { status: 200 });
  });
  return calls;
}

describe('FlaggedMailDialog', () => {
  it('shows only the note while KyPost is not linked', async () => {
    serve({ kypost_flags: true, kypost_linked: false });
    render(<FlaggedMailDialog onClose={vi.fn()} />);
    expect(await screen.findByRole('note')).toHaveProperty('textContent', expect.stringMatching(/^When KyPost is connected/));
    expect(screen.queryByRole('switch')).toBeNull();
  });

  it('shows the switch with no note when linked, and saves a change', async () => {
    const calls = serve({ kypost_flags: true, kypost_linked: true });
    render(<FlaggedMailDialog onClose={vi.fn()} />);
    const sw = await screen.findByRole('switch', { name: 'Make tasks from flagged mail' });
    expect(sw).toHaveProperty('checked', true);
    expect(screen.queryByRole('note')).toBeNull();
    fireEvent.click(sw);
    await waitFor(() => expect(calls.at(-1)).toMatchObject({ method: 'PUT', body: { kypost_flags: false } }));
    await waitFor(() => expect(sw).toHaveProperty('checked', false));
  });
});
