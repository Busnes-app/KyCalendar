import { useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import { secureFetch } from '../api';

interface ChangePasswordProps {
  onComplete: () => void;
  onLogout: () => void;
}

/** The change-password form, for the forced replacement page and for Account settings. */
export function PasswordForm({ onComplete, inSettings = false, children }: { onComplete: () => void; inSettings?: boolean; children?: ReactNode }) {
  const [currentPassword, setCurrentPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmation, setConfirmation] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError('');
    if (newPassword !== confirmation) { setError('The new passwords do not match.'); return; }
    if (currentPassword === newPassword) { setError('Choose a different password.'); return; }
    setBusy(true);
    try {
      const response = await secureFetch('/api/auth/change-password', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
      });
      if (!response.ok) {
        const body: unknown = await response.json();
        throw new Error(typeof body === 'object' && body !== null && 'error' in body && typeof body.error === 'string'
          ? body.error : 'Password change failed. Please try again.');
      }
      setCurrentPassword(''); setNewPassword(''); setConfirmation('');
      onComplete();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'Password change failed. Please try again.');
    } finally { setBusy(false); }
  }

  const field = inSettings ? 'field' : 'ky-auth-field';
  return (
    <form onSubmit={submit} className={inSettings ? 'form' : 'ky-auth-card'}>
      <p className={inSettings ? 'section-lede' : 'card-meta'}>Use at least 12 characters. You will sign in again afterward{inSettings ? ', and your other sessions and app passwords stop working' : ''}.</p>
      <div className={field}>
        <label htmlFor="current-password">Current password</label>
        <input id="current-password" type="password" autoComplete="current-password" required maxLength={1024}
          value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} />
      </div>
      <div className={field}>
        <label htmlFor="new-password">New password</label>
        <input id="new-password" type="password" autoComplete="new-password" required minLength={12} maxLength={1024}
          value={newPassword} onChange={(event) => setNewPassword(event.target.value)} />
      </div>
      <div className={field}>
        <label htmlFor="confirm-password">Confirm new password</label>
        <input id="confirm-password" type="password" autoComplete="new-password" required minLength={12} maxLength={1024}
          value={confirmation} onChange={(event) => setConfirmation(event.target.value)} />
      </div>
      {error && <p role="alert">{error}</p>}
      {inSettings
        ? <div className="form-actions"><button type="submit" disabled={busy}>{busy ? 'Changing password…' : 'Change password'}</button></div>
        : <button type="submit" className="ky-auth-submit" disabled={busy}>{busy ? 'Changing password…' : 'Change password'}</button>}
      {children}
    </form>
  );
}

export function ChangePassword({ onComplete, onLogout }: ChangePasswordProps) {
  return (
    <main className="ky-auth">
      <div />
      <div className="ky-auth-body">
        <div className="ky-auth-brand">
          <img className="brand-mark" src="/app-icon.png" width={56} height={56} alt="" />
          <h1>Change your password</h1>
          <p>Replace your temporary password to continue.</p>
        </div>
        <PasswordForm onComplete={onComplete}>
          <button type="button" className="ky-auth-link" onClick={onLogout}>Sign out</button>
        </PasswordForm>
      </div>
    </main>
  );
}
