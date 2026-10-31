import React, { Suspense, lazy, useEffect, useState } from 'react';
import { AppHeader } from './components/AppHeader';
import { LicenseFooter } from './components/LicenseFooter';
import { AppRail, loadSuiteApps, type SuiteApp } from './components/AppRail';
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

const ADMIN_TABS = ['dashboard', 'people', 'groups', 'group-calendars', 'signin', 'scim', 'backup', 'server-settings'];
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
  const [loading, setLoading] = useState<boolean>(true);
  // null until a page is chosen: the landing page follows the role, so there is no flash.
  const [chosenTab, setChosenTab] = useState<string | null>(hashTab);
  const [settings, setSettings] = useState<any>(null);
  const [sidebarSlot, setSidebarSlot] = useState<HTMLDivElement | null>(null);
  const [apps, setApps] = useState<SuiteApp[]>([]);

  useEffect(() => {
    const sync = () => setChosenTab(hashTab());
    window.addEventListener('hashchange', sync);
    return () => window.removeEventListener('hashchange', sync);
  }, []);

  useEffect(() => {
    const checkAuth = async () => {
      try {
        const [authResp, setResp] = useResponses(
          await fetch('/api/auth/me'),
          await fetch('/api/settings')
        );

        if (setResp.ok) {
          const s = await setResp.json();
          setSettings(s);
        }

        if (authResp.ok) {
          const a = await authResp.json();
          if (a.authenticated) {
            setUser(a.user);
          }
        }
      } catch (err) {
        console.error('Initialization error:', err);
      } finally {
        setLoading(false);
      }
    };

    checkAuth();
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

  if (!user) {
    return (
      <Login
        appName={settings?.app_name || 'KyCalendar'}
        signinName={settings?.signin_name}
        notice={notice}
        onSuccess={(u) => {
          setNotice('');
          setUser(u);
          void loadSettings();
        }}
      />
    );
  }

  if (user.must_change_password) {
    return <ChangePassword onLogout={handleLogout} onComplete={() => {
      setUser(null);
      setNotice('Password changed. Sign in with your new password.');
    }} />;
  }

  const isAdmin = user.role === 'admin';
  const tabs = [...(isAdmin ? ADMIN_TABS : ['calendar']), ...settingsSections(user).map((s) => `settings/${s.id}`)];
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
      />

      <main className="ky-content">
        {activeTab === 'calendar' && <Suspense fallback={<p className="app-loading">Loading calendar…</p>}><CalendarPage scheduling={settings?.scheduling === true} me={user.email ?? ''} sidebarSlot={sidebarSlot} /></Suspense>}
        {activeTab.startsWith('settings/') && (
          <PersonalSettings section={activeTab.slice('settings/'.length)} user={user} onUserChange={setUser} />
        )}
        {activeTab === 'dashboard' && <Dashboard settings={settings} user={user} onNavigate={go} />}
        {activeTab === 'people' && <People me={user.id} />}
        {activeTab === 'groups' && <Groups />}
        {activeTab === 'group-calendars' && <GroupCalendars />}
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

function useResponses(r1: Response, r2: Response): [Response, Response] {
  return [r1, r2];
}
