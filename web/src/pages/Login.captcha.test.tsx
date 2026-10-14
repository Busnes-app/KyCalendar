import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useEffect } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';

let mounts = 0;
vi.mock('../components/CaptchaWidget', () => ({
  CaptchaWidget: ({ onToken }: { onToken: (t: string) => void }) => {
    useEffect(() => {
      mounts += 1;
      onToken(`tok${mounts}`);
    }, []);
    return null;
  },
}));

import { Login } from './Login';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('Login captcha', () => {
  it('solves a fresh captcha after a failed attempt, since a solution is single-use', async () => {
    const sent: string[] = [];
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (_input, init) => {
      sent.push(JSON.parse(String(init?.body)).captcha_token);
      return new Response(JSON.stringify({ error: 'Invalid credentials' }), { status: 401 });
    });
    render(<Login appName="Cal" onSuccess={() => {}} />);
    fireEvent.change(screen.getByPlaceholderText('admin'), { target: { value: 'alice' } });
    fireEvent.change(screen.getByPlaceholderText('••••••••••••'), { target: { value: 'wrong' } });
    fireEvent.submit(screen.getByPlaceholderText('admin').closest('form')!);
    await screen.findByText('Invalid credentials');
    fireEvent.submit(screen.getByPlaceholderText('admin').closest('form')!);
    await waitFor(() => expect(sent).toHaveLength(2));
    expect(sent[0]).not.toBe(sent[1]);
  });
});
