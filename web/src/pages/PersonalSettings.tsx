import { useEffect, useState } from 'react';
import { PageHeader } from '../components/ui';
import { Appearance } from './Appearance';
import AppPasswords from './AppPasswords';
import BookingPages from './BookingPages';
import { PasswordForm } from './ChangePassword';
import Encryption, { encryptionStatus } from './Encryption';
import { StatusSettings } from './StatusSettings';

const SECTIONS = [
  { id: 'account', label: 'Account' },
  // App passwords are for everyday users' calendar clients; the API refuses administrators.
  { id: 'devices', label: 'Phones & apps', everyday: true },
  { id: 'booking', label: 'Booking pages', everyday: true },
  { id: 'status', label: 'Status', everyday: true },
  { id: 'appearance', label: 'Appearance' },
];

/** The Settings sections this user has, in tab order. */
export const settingsSections = (user: any) => SECTIONS.filter((s) => !s.everyday || user.role !== 'admin');

const ROLE: Record<string, string> = { admin: 'Administrator' };

/** Settings: the page head, one tab per section, then the open section. */
export function PersonalSettings({ section, user, onPasswordChanged }: { section: string; user: any; onPasswordChanged: () => void }) {
  // Encryption has a tab only where the server turned it on, and never for administrators.
  const [encryption, setEncryption] = useState(false);
  useEffect(() => {
    if (user.role !== 'admin') void encryptionStatus().then((s) => setEncryption(!!s?.enabled));
  }, [user.role]);
  const sections = encryption ? [...settingsSections(user), { id: 'encryption', label: 'Encryption' }] : settingsSections(user);
  const open = sections.find((s) => s.id === section) ?? sections[0];
  // A local account's password is its own; an SSO account's belongs to its provider.
  const local = user.sso_provider === 'local';
  const role = ROLE[user.role] ?? 'Everyday account';
  // Each section opens with its own description, so the tabs stay put when switching.
  const intro = {
    account: local
      ? 'Who you are signed in as, and your password.'
      : 'Who you are signed in as. Your sign-in provider looks after your password and two-step verification.',
    devices: 'Each device or AI agent gets its own app password. Revoke one and only that one stops working.',
    booking: 'Share a link where people outside your organisation pick a free time with you. Only free times show; nothing else in your calendar does.',
    status: 'What colleagues see beside your name in KyCalendar and KyPost. It follows your calendar; KyMessages keeps it.',
    encryption: 'Encrypt event and task content at rest, unlock this browser, and see how long older app passwords keep working.',
    appearance: 'Pick a palette and how it handles light and dark. Match system switches with your device’s setting; Light or Dark keeps one. Your choice applies straight away and is saved in this browser only.',
  }[open.id];
  return (
    <>
      <PageHeader title="Settings" lede={sections.length > 2 ? 'Your account, the phones and apps that sync with it, your booking pages, and how KyCalendar looks.' : 'Your account, and how KyCalendar looks.'} />
      <nav className="ky-settings-tabs" aria-label="Settings sections">
        {sections.map((s) => (
          <a key={s.id} className="ky-nav-item" href={`#/settings/${s.id}`} aria-current={s === open ? 'page' : undefined}>{s.label}</a>
        ))}
      </nav>
      <section aria-label={open.label}>
        <p className="settings-intro">{intro}</p>
        {open.id === 'account' && (
          <>
            <div className="card">
              <h2 className="section-title">Account</h2>
              <dl className="facts">
                {/* Never the same word twice: a name that repeats the username or role is left out. */}
                {user.display_name && ![user.username, role].includes(user.display_name) && <><dt>Name</dt><dd>{user.display_name}</dd></>}
                <dt>Username</dt><dd>{user.username}</dd>
                {user.email && <><dt>Email</dt><dd>{user.email}</dd></>}
                <dt>Role</dt><dd>{role}</dd>
              </dl>
            </div>
            {local && (
              <div className="card">
                <h2 className="section-title">Change password</h2>
                <PasswordForm inSettings onComplete={onPasswordChanged} />
              </div>
            )}
          </>
        )}
        {open.id === 'devices' && <AppPasswords username={user.username} />}
        {open.id === 'booking' && <BookingPages />}
        {open.id === 'status' && <StatusSettings />}
        {open.id === 'appearance' && <Appearance />}
        {open.id === 'encryption' && <Encryption />}
      </section>
    </>
  );
}
