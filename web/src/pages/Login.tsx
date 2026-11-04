import React, { useState } from 'react';
import { Key, AlertCircle, LogIn } from 'lucide-react';
import { CaptchaWidget } from '../components/CaptchaWidget';
import { ThemeSwitcher } from '../components/ThemeSwitcher';
import { ensureCSRF, secureFetch } from '../api';

interface LoginProps {
  onSuccess: (user: any) => void;
  appName: string;
  /** The configured provider's button label; no button without one. */
  signinName?: string;
  /** The SSO link asks the provider to check the credentials again (after signing out). */
  stepUp?: boolean;
  /** A message from the previous screen, such as a completed password change. */
  notice?: string;
}

export const Login: React.FC<LoginProps> = ({ onSuccess, appName, signinName, stepUp = false, notice }) => {
  const [username, setUsername] = useState<string>('');
  const [password, setPassword] = useState<string>('');
  const [captchaToken, setCaptchaToken] = useState<string>('');
  // A solved captcha is single-use: a failed attempt remounts the widget for a fresh one.
  const [captchaKey, setCaptchaKey] = useState<number>(0);
  const [loading, setLoading] = useState<boolean>(false);
  const [error, setError] = useState<string>('');

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);

    try {
      await ensureCSRF();
      const resp = await secureFetch('/api/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          username,
          password,
          captcha_token: captchaToken || undefined,
        }),
      });

      const data = await resp.json();
      if (!resp.ok) {
        throw new Error(data.error || 'Login failed');
      }

      if (data.authenticated) {
        onSuccess(data.user);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed');
      setCaptchaToken('');
      setCaptchaKey((k) => k + 1);
    } finally {
      setLoading(false);
    }
  };

  return (
    <main className="ky-auth">
      <div className="ky-auth-theme">
        <ThemeSwitcher />
      </div>

      <div className="ky-auth-body">
        <div className="ky-auth-brand">
          <img className="brand-mark" src="/app-icon.png" width={56} height={56} alt="" />
          <h1>{appName || 'KyCalendar'}</h1>
          <p>Your calendars, on your own server.</p>
        </div>

        <div className="ky-auth-card">
          {notice && !error && <p role="status" className="alert alert-success">{notice}</p>}
          {error && (
            <div role="alert" className="alert alert-error auth-error">
              <AlertCircle size={16} aria-hidden="true" />
              <span>{error}</span>
            </div>
          )}

          <form onSubmit={handleLogin}>
            <div className="ky-auth-field">
              <label htmlFor="login-username">Username</label>
              <input
                id="login-username"
                type="text"
                autoComplete="username"
                autoCapitalize="none"
                spellCheck={false}
                autoFocus
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                required
              />
            </div>

            <div className="ky-auth-field">
              <label htmlFor="login-password">Password</label>
              <input
                id="login-password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>

            <CaptchaWidget key={captchaKey} onToken={setCaptchaToken} />

            <button type="submit" className="ky-auth-submit" disabled={loading}>
              <LogIn aria-hidden="true" />
              {loading ? 'Signing in…' : 'Sign in'}
            </button>

            {signinName && (
              <>
                <hr className="ky-auth-divider" />
                <a href={stepUp ? '/api/sso/kysignon/login?step_up=1' : '/api/sso/kysignon/login'} className="ky-auth-sso">
                  <Key aria-hidden="true" />
                  Continue with {signinName}
                </a>
              </>
            )}
          </form>
        </div>
      </div>
    </main>
  );
};
