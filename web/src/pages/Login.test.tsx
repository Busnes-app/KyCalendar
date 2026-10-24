import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('../components/CaptchaWidget', () => ({ CaptchaWidget: () => null }));

import { Login } from './Login';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); document.cookie = 'ky_csrf=; Max-Age=0'; });

describe('Login', () => {
  it('offers single sign-on only when a provider is configured', () => {
    const { rerender } = render(<Login appName="Cal" onSuccess={() => {}} />);
    expect(screen.queryByRole('link', { name: /Continue with/ })).toBeNull();
    rerender(<Login appName="Cal" onSuccess={() => {}} signinName="Acme" />);
    expect(screen.getByRole('link', { name: 'Continue with Acme' }).getAttribute('href')).toBe('/api/sso/kysignon/login');
  });

  it('renders the provider name as text, not markup', () => {
    render(<Login appName="Cal" onSuccess={() => {}} signinName="<b>x</b>" />);
    expect(screen.getByRole('link', { name: 'Continue with <b>x</b>' })).toBeTruthy();
  });

  it('names its fields and carries KyCalendar copy, not the template', () => {
    render(<Login appName="KyCalendar" onSuccess={() => {}} notice="Password changed. Sign in with your new password." />);
    expect(screen.getByLabelText('Username')).toBeTruthy();
    expect(screen.getByLabelText('Password')).toBeTruthy();
    expect(screen.getByRole('status').textContent).toMatch(/Password changed/);
    expect(document.body.textContent).not.toMatch(/Mobile First|Base Platform/);
  });

  it('sends the pre-session CSRF token, fetching one first when the browser has none', async () => {
    const sent: [string, string | null][] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      sent.push([String(input), new Headers(init?.headers).get('X-CSRF-Token')]);
      if (String(input) === '/api/auth/me') {
        document.cookie = 'ky_csrf=pre-session-token';
        return new Response(JSON.stringify({ authenticated: false }), { status: 200 });
      }
      return new Response(JSON.stringify({ mfa_required: true, mfa_token: 'm' }), { status: 200 });
    }));
    render(<Login appName="Cal" onSuccess={() => {}} />);
    fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'alice' } });
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'pw' } });
    fireEvent.submit(screen.getByLabelText('Username').closest('form')!);
    fireEvent.change(await screen.findByLabelText('Authenticator code'), { target: { value: '123456' } });
    fireEvent.submit(screen.getByLabelText('Authenticator code').closest('form')!);
    await waitFor(() => expect(sent).toHaveLength(3));
    expect(sent).toEqual([
      ['/api/auth/me', null],
      ['/api/auth/login', 'pre-session-token'],
      ['/api/auth/mfa/totp', 'pre-session-token'],
    ]);
  });
});
