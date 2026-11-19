import React, { Suspense, lazy, useEffect, useState } from 'react';
import { AppHeader } from './components/AppHeader';
import { LicenseFooter } from './components/LicenseFooter';
import { AppRail, loadSuiteApps, type SuiteApp } from './components/AppRail';
import { driveOrigin } from './drivePicker';
import { Dashboard } from './pages/Dashboard';
import { Login } from './pages/Login';
import { ChangePassword } from './pages/ChangePassword';
import { Backup } from './pages/Backup';
import { SCIMAdmin } from './pages/SCIMAdmin';
import { Settings } from './pages/Settings';
import { PersonalSettings, settingsSections } from './pages/PersonalSettings';
import GroupCalendars from './pages/GroupCalendars';
import Groups from './pages/Groups';
import People from './pages/People';
import Resources from './pages/Resources';
import RoomRequests from './pages/RoomRequests';
import SignIn from './pages/SignIn';
import './styles/theme.css';
import './styles/ui.css';
import './ky-ui/tokens.css';
import './ky-ui/navigation.css';
import './ky-ui/shell.css';
import './ky-ui/appearance.css';
import './ky-ui/appearance-themes.css';
import { secureFetch } from './api';

// FullCalendar is the bulk of the bundle; load it only for the calendar (same-origin chunk).
const CalendarPage = lazy(() => import('./pages/CalendarPage').then((m) => ({ default: m.CalendarPage })));
const TasksPage = lazy(() => import('./pages/TasksPage').then((m) => ({ default: m.TasksPage })));
// The capture box parses and formats with the calendar's formatter: loaded on first use.
const CaptureBox = lazy(() => import('./components/CaptureBox').then((m) => ({ default: m.CaptureBox })));

const ADMIN_TABS = ['dashboard', 'people', 'groups', 'group-calendars', 'resources', 'signin', 'scim', 'backup', 'server-settings'];
// Pages that became Settings sections keep their old addresses.
const MOVED: Record<string, string> = {
  settings: 'settings/account',
  security: 'settings/account',
  devices: 'settings/devices',
  appearance: 'settings/appearance',
};

// Each page is a hash URL (#/calendar, #/settings/devices), so links and Back work.
function hashTab(): string | null {
  const id = location.hash.replace(/^#\/?/, '');
  if (MOVED[id]) history.replaceState(null, '', `#/${MOVED[id]}`);
  return MOVED[id] ?? (id || null);
}

export const App: React.FC = () => {
  const [user, setUser] = useState<any>(null);
  const [notice, setNotice] = useState('');
  // After signing out, the next SSO sign-in is a step-up: the provider checks the credentials again.
  const [signedOut, setSignedOut] = useState(false);
  const [loading, setLoading] = useState<boolean>(true);
  // null until a page is chosen: the landing page follows the role, so there is no flash.
  const [chosenTab, setChosenTab] = useState<string | null>(hashTab);
  const [settings, setSettings] = useState<any>(null);
  const [sidebarSlot, setSidebarSlot] = useState<HTMLDivElement | null>(null);
  const [apps, setApps] = useState<SuiteApp[]>([]);
  const [capturing, setCapturing] = useState(false);

  useEffect(() => {
    const sync = () => setChosenTab(hashTab());
    window.addEventListener('hashchange', sync);
    return () => window.removeEventListener('hashchange', sync);
  }, []);

  // A failed session lookup (503, or no answer) is not a sign-out: the sign-in form is only
  // for a real "authenticated: false".
  const [unavailable, setUnavailable] = useState(false);
  const checkAuth = async () => {
    setLoading(true);
    setUnavailable(false);
    try {
      const [authResp, setResp] = await Promise.all([fetch('/api/auth/me'), fetch('/api/settings').catch(() => null)]);
      if (setResp?.ok) {
        setSettings(await setResp.json());
      }
      if (!authResp.ok) {
        setUnavailable(true);
        return;
      }
      const a = await authResp.json();
      if (a.authenticated) {
        setUser(a.user);
      }
    } catch (err) {
      console.error('Initialization error:', err);
      setUnavailable(true);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void checkAuth();
  }, []);

  // The app rail: once per signed-in user, never for a pending password change.
  const railUser = user && !user.must_change_password ? user.id : null;
  useEffect(() => {
    setApps([]);
    if (!railUser) return;
    let current = true;
    void loadSuiteApps().then((list) => { if (current) setApps(list); });
    return () => { current = false; };
  }, [railUser]);

  // The capture box is one key away on every everyday screen: n, outside fields and dialogs.
  const everyday = !!user && user.role !== 'admin' && !user.must_change_password;
  useEffect(() => {
    if (!everyday) return;
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement;
      if (e.key !== 'n' || e.metaKey || e.ctrlKey || e.altKey || el.closest('input, textarea, select, dialog, [contenteditable], [popover]') || document.querySelector('dialog[open]')) return;
      e.preventDefault();
      setCapturing(true);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [everyday]);

  // /api/settings returns more fields once authenticated, so re-read it after login.
  const loadSettings = async () => {
    const resp = await fetch('/api/settings');
    if (resp.ok) {
      const s = await resp.json();
      setSettings(s);
    }
  };

  const handleLogout = async () => {
    await secureFetch('/api/auth/logout', { method: 'POST' });
    setSignedOut(true);
    setUser(null);
    history.replaceState(null, '', location.pathname);
    setChosenTab(null);
  };

  if (loading) {
    return (
      <div className="app-loading" role="status">
        Loading {settings?.app_name || 'KyCalendar'}…
      </div>
    );
  }

  if (unavailable) {
    return (
      <main className="ky-auth">
        <div className="ky-auth-body">
          <div className="ky-auth-brand">
            <img className="brand-mark" src="/app-icon.png" width={56} height={56} alt="" />
            <h1>{settings?.app_name || 'KyCalendar'}</h1>
          </div>
          <div className="ky-auth-card" role="alert">
            <p>{settings?.app_name || 'KyCalendar'} is temporarily unavailable. Your session is kept; try again in a moment.</p>
            <button type="button" className="ky-auth-submit" onClick={() => void checkAuth()}>Try again</button>
          </div>
        </div>
      </main>
    );
  }

  if (!user) {
    return (
      <Login
        appName={settings?.app_name || 'KyCalendar'}
        signinName={settings?.signin_name}
        stepUp={signedOut}
        notice={notice}
        onSuccess={(u) => {
          setNotice('');
          setUser(u);
          void loadSettings();
        }}
      />
    );
  }

  const passwordChanged = () => {
    setUser(null);
    setNotice('Password changed. Sign in with your new password.');
  };
  if (user.must_change_password) {
    return <ChangePassword onLogout={handleLogout} onComplete={passwordChanged} />;
  }

  const isAdmin = user.role === 'admin';
  const tabs = [...(isAdmin ? ADMIN_TABS : ['calendar', 'tasks', ...(settings?.resource_approver === true ? ['room-requests'] : [])]), ...settingsSections(user).map((s) => `settings/${s.id}`)];
  // An unknown address lands on the role's first page, or Settings' first section.
  const fallback = chosenTab?.startsWith('settings/') ? 'settings/account' : tabs[0];
  const activeTab = chosenTab && tabs.includes(chosenTab) ? chosenTab : fallback;
  const go = (tab: string) => { location.hash = `/${tab}`; };

  return (
    <div className="ky-app">
      <AppHeader
        appName={settings?.app_name || 'KyCalendar'}
        activeTab={activeTab}
        onTabChange={go}
        user={user}
        onLogout={handleLogout}
        sidebarSlot={setSidebarSlot}
        version={settings?.version}
        apps={apps}
        approver={settings?.resource_approver === true}
        onCapture={isAdmin ? undefined : () => setCapturing(true)}
      />

      <main className="ky-content">
        {activeTab === 'calendar' && <Suspense fallback={<p className="app-loading">Loading calendar…</p>}><CalendarPage scheduling={settings?.scheduling === true} meetings={settings?.meetings === true && apps.some((a) => a.id === 'kymessage')} chat={settings?.meetings === true && apps.some((a) => a.id === 'kymessage-chat')} status={settings?.meetings === true} me={user.email ?? ''} drive={driveOrigin(apps)} sidebarSlot={sidebarSlot} /></Suspense>}
        {activeTab === 'tasks' && <Suspense fallback={<p className="app-loading">Loading tasks…</p>}><TasksPage drive={driveOrigin(apps)} sidebarSlot={sidebarSlot} /></Suspense>}
        {capturing && <Suspense fallback={null}><CaptureBox onClose={() => setCapturing(false)} /></Suspense>}
        {activeTab.startsWith('settings/') && (
          <PersonalSettings section={activeTab.slice('settings/'.length)} user={user} onPasswordChanged={passwordChanged} />
        )}
        {activeTab === 'dashboard' && <Dashboard settings={settings} user={user} onNavigate={go} />}
        {activeTab === 'people' && <People me={user.id} />}
        {activeTab === 'groups' && <Groups />}
        {activeTab === 'group-calendars' && <GroupCalendars />}
        {activeTab === 'resources' && <Resources />}
        {activeTab === 'room-requests' && <RoomRequests />}
        {activeTab === 'signin' && <SignIn />}
        {activeTab === 'scim' && <SCIMAdmin />}
        {activeTab === 'backup' && <Backup />}
        {activeTab === 'server-settings' && <Settings settings={settings} />}
        <LicenseFooter className="ky-content-foot" version={settings?.version}>
          <AppRail apps={apps} />
        </LicenseFooter>
      </main>
    </div>
  );
};


