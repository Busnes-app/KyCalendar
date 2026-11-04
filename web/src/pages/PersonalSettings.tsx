import { PageHeader } from '../components/ui';
import { Appearance } from './Appearance';
import AppPasswords from './AppPasswords';
import { PasswordForm } from './ChangePassword';

const SECTIONS = [
  { id: 'account', label: 'Account' },
  // App passwords are for everyday users' calendar clients; the API refuses administrators.
  { id: 'devices', label: 'Phones & apps', everyday: true },
  { id: 'appearance', label: 'Appearance' },
];

/** The Settings sections this user has, in tab order. */
export const settingsSections = (user: any) => SECTIONS.filter((s) => !s.everyday || user.role !== 'admin');

const ROLE: Record<string, string> = { admin: 'Administrator' };

/** Settings: the page head, one tab per section, then the open section. */
export function PersonalSettings({ section, user, onPasswordChanged }: { section: string; user: any; onPasswordChanged: () => void }) {
  const sections = settingsSections(user);
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
    appearance: 'Pick a palette and how it handles light and dark. Match system switches with your device’s setting; Light or Dark keeps one. Your choice applies straight away and is saved in this browser only.',
  }[open.id];
  return (
    <>
      <PageHeader title="Settings" lede={sections.length > 2 ? 'Your account, the phones and apps that sync with it, and how KyCalendar looks.' : 'Your account, and how KyCalendar looks.'} />
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
        {open.id === 'appearance' && <Appearance />}
      </section>
    </>
  );
}
