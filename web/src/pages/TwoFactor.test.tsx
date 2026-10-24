import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import TwoFactor from './TwoFactor';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); document.cookie = 'ky_csrf=; Max-Age=0'; });

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

it('enrols with the password, shows the setup key, confirms a code and shows recovery codes once', async () => {
  const calls: [string, any, string | null][] = [];
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push([String(input), JSON.parse(String(init?.body)), new Headers(init?.headers).get('X-CSRF-Token')]);
    if (String(input) === '/api/auth/mfa/enroll') return json({ secret: 'JBSWY3DPEHPK3PXP', otpauth_uri: 'otpauth://totp/KyCalendar:tess?secret=JBSWY3DPEHPK3PXP' });
    return json({ totp_enabled: true, recovery_codes: ['AAAA-BBBB', 'CCCC-DDDD'] });
  }));
  document.cookie = 'ky_csrf=session-token';
  const onChange = vi.fn();
  render(<TwoFactor enabled={false} onChange={onChange} />);
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'SuperSecretPass123!' } });
  fireEvent.click(screen.getByRole('button', { name: 'Set up' }));
  expect(await screen.findByText('JBSWY3DPEHPK3PXP')).toBeTruthy();
  expect(screen.getByRole('link', { name: 'setup link' }).getAttribute('href')).toMatch(/^otpauth:\/\/totp\//);
  fireEvent.change(screen.getByLabelText('Code from the app'), { target: { value: ' 123456 ' } });
  fireEvent.click(screen.getByRole('button', { name: 'Turn on' }));
  expect((await screen.findByRole('status')).textContent).toContain('AAAA-BBBB');
  expect(onChange).toHaveBeenCalledWith(true);
  expect(calls).toEqual([
    ['/api/auth/mfa/enroll', { password: 'SuperSecretPass123!' }, 'session-token'],
    ['/api/auth/mfa/enroll/confirm', { code: '123456' }, 'session-token'],
  ]);
});

it('keeps TOTP off and says why when the code is wrong', async () => {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => String(input) === '/api/auth/mfa/enroll'
    ? json({ secret: 'S', otpauth_uri: 'otpauth://totp/x?secret=S' })
    : json({ error: 'Invalid TOTP verification code' }, 401)));
  const onChange = vi.fn();
  render(<TwoFactor enabled={false} onChange={onChange} />);
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'pw' } });
  fireEvent.click(screen.getByRole('button', { name: 'Set up' }));
  fireEvent.change(await screen.findByLabelText('Code from the app'), { target: { value: '000000' } });
  fireEvent.click(screen.getByRole('button', { name: 'Turn on' }));
  expect((await screen.findByRole('alert')).textContent).toContain('Invalid TOTP verification code');
  expect(onChange).not.toHaveBeenCalled();
});

it('turns off only with the password and a code', async () => {
  const fetcher = vi.fn(async () => json({ totp_enabled: false }));
  vi.stubGlobal('fetch', fetcher);
  const onChange = vi.fn();
  render(<TwoFactor enabled onChange={onChange} />);
  expect(screen.queryByRole('button', { name: 'Set up' })).toBeNull();
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'pw' } });
  fireEvent.change(screen.getByLabelText('Authenticator or recovery code'), { target: { value: '654321' } });
  fireEvent.click(screen.getByRole('button', { name: 'Turn off' }));
  await waitFor(() => expect(onChange).toHaveBeenCalledWith(false));
  expect(fetcher).toHaveBeenCalledWith('/api/auth/mfa/disable', expect.objectContaining({ body: JSON.stringify({ password: 'pw', code: '654321' }) }));
});
