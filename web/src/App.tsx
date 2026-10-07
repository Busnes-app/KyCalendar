import React, { Suspense, lazy, useEffect, useState } from 'react';
import { AppHeader } from './components/AppHeader';
import { Dashboard } from './pages/Dashboard';
import { Login } from './pages/Login';
import { ChangePassword } from './pages/ChangePassword';
import { Backup } from './pages/Backup';
import { SCIMAdmin } from './pages/SCIMAdmin';
import { Settings } from './pages/Settings';
import AppPasswords from './pages/AppPasswords';
import GroupCalendars from './pages/GroupCalendars';
import './styles/theme.css';
import './ky-ui/tokens.css';
import './ky-ui/navigation.css';
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
      <div style={{ minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'var(--bg)', color: 'var(--ink)' }}>
        Loading {settings?.app_name || 'Busnes.app'}...
      </div>
    );
  }

  if (!user) {
    return (
      <>
      {notice && <p role="status" style={{ padding: 16 }}>{notice}</p>}
      <Login
        appName={settings?.app_name || 'Busnes.app'}
        onSuccess={(u) => {
          setNotice('');
          setUser(u);
          void loadSettings();
        }}
      />
      </>
    );
  }

  if (user.must_change_password) {
    return <ChangePassword onLogout={handleLogout} onComplete={() => {
      setUser(null);
      setNotice('Password changed. Sign in with your new password.');
    }} />;
  }

  const activeTab = chosenTab ?? (user.role === 'admin' ? 'dashboard' : 'calendar');

  return (
    <div className="app-shell">
      <AppHeader
        appName={settings?.app_name || 'Busnes.app'}
        activeTab={activeTab}
        onTabChange={(tab) => setActiveTab(tab)}
        user={user}
        onLogout={handleLogout}
      />

      <main className="app-main">
        {activeTab === 'calendar' && user.role !== 'admin' && <Suspense fallback={<p>Loading calendar…</p>}><CalendarPage /></Suspense>}
        {activeTab === 'dashboard' && <Dashboard settings={settings} user={user} onNavigate={(tab) => setActiveTab(tab)} />}
        {activeTab === 'scim' && <SCIMAdmin />}
        {activeTab === 'backup' && <Backup />}
        {activeTab === 'settings' && <Settings settings={settings} />}
        {activeTab === 'group-calendars' && user.role === 'admin' && <GroupCalendars />}
        {activeTab === 'devices' && user && <AppPasswords username={user.username} />}
      </main>
    </div>
  );
};

function useResponses(r1: Response, r2: Response): [Response, Response] {
  return [r1, r2];
}
