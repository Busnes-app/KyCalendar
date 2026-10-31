import React, { Suspense, lazy, useEffect, useState } from 'react';
import { AppHeader } from './components/AppHeader';
import { Dashboard } from './pages/Dashboard';
import { Login } from './pages/Login';
import { ChangePassword } from './pages/ChangePassword';
import { Backup } from './pages/Backup';
import { SCIMAdmin } from './pages/SCIMAdmin';
import { Settings } from './pages/Settings';
import { Appearance } from './pages/Appearance';
import AppPasswords from './pages/AppPasswords';
import GroupCalendars from './pages/GroupCalendars';
import Groups from './pages/Groups';
import People from './pages/People';
import SignIn from './pages/SignIn';
import TwoFactor from './pages/TwoFactor';
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

export const App: React.FC = () => {
  const [user, setUser] = useState<any>(null);
  const [notice, setNotice] = useState('');
  const [loading, setLoading] = useState<boolean>(true);
  // null until the user picks a tab: the landing tab follows the role, so there is no flash.
  const [chosenTab, setActiveTab] = useState<string | null>(null);
  const [settings, setSettings] = useState<any>(null);
  const [sidebarSlot, setSidebarSlot] = useState<HTMLDivElement | null>(null);

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
    setActiveTab(null);
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
  const activeTab = chosenTab ?? (isAdmin ? 'dashboard' : 'calendar');

  return (
    <div className="app-shell">
      <AppHeader
        appName={settings?.app_name || 'KyCalendar'}
        activeTab={activeTab}
        onTabChange={(tab) => setActiveTab(tab)}
        user={user}
        onLogout={handleLogout}
        sidebarSlot={setSidebarSlot}
        version={settings?.version}
      />

      <main className="app-main">
        {activeTab === 'calendar' && !isAdmin && <Suspense fallback={<p className="app-loading">Loading calendar…</p>}><CalendarPage scheduling={settings?.scheduling === true} me={user.email ?? ''} sidebarSlot={sidebarSlot} /></Suspense>}
        {activeTab === 'devices' && !isAdmin && <AppPasswords username={user.username} />}
        {activeTab === 'appearance' && <Appearance />}
        {activeTab === 'security' && user.sso_provider === 'local' && (
          <TwoFactor enabled={user.totp_enabled === true} onChange={(on) => setUser({ ...user, totp_enabled: on })} />
        )}
        {/* Admin pages: the API refuses everyday users anyway; never render them either. */}
        {isAdmin && activeTab === 'dashboard' && <Dashboard settings={settings} user={user} onNavigate={(tab) => setActiveTab(tab)} />}
        {isAdmin && activeTab === 'people' && <People me={user.id} />}
        {isAdmin && activeTab === 'groups' && <Groups />}
        {isAdmin && activeTab === 'group-calendars' && <GroupCalendars />}
        {isAdmin && activeTab === 'signin' && <SignIn />}
        {isAdmin && activeTab === 'scim' && <SCIMAdmin />}
        {isAdmin && activeTab === 'backup' && <Backup />}
        {isAdmin && activeTab === 'settings' && <Settings settings={settings} />}
      </main>
    </div>
  );
};

function useResponses(r1: Response, r2: Response): [Response, Response] {
  return [r1, r2];
}
