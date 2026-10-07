import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('../components/CaptchaWidget', () => ({ CaptchaWidget: () => null }));

import { Login } from './Login';

afterEach(cleanup);

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
});
