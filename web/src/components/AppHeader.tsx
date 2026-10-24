import React from 'react';
import { Smartphone, LogOut, Users, Settings as SettingsIcon, LayoutDashboard, Archive, CalendarDays, UsersRound, UserRound, KeyRound, ShieldCheck } from 'lucide-react';
import { ThemeSwitcher } from './ThemeSwitcher';

interface AppHeaderProps {
  appName: string;
  activeTab: string;
  onTabChange: (tab: string) => void;
  user: any;
  onLogout: () => void;
}

export const AppHeader: React.FC<AppHeaderProps> = ({ appName, activeTab, onTabChange, user, onLogout }) => {
  // Administrators run the instance and never see calendars; everyday users see nothing else.
  const navItems = user?.role === 'admin'
    ? [
        { id: 'dashboard', label: 'Overview', icon: LayoutDashboard },
        { id: 'people', label: 'People', icon: UserRound },
        { id: 'groups', label: 'Groups', icon: UsersRound },
        { id: 'group-calendars', label: 'Group calendars', icon: CalendarDays },
        { id: 'signin', label: 'Sign-in', icon: KeyRound },
        { id: 'scim', label: 'Directory & SCIM', icon: Users },
        { id: 'backup', label: 'Backups', icon: Archive },
        { id: 'settings', label: 'Settings', icon: SettingsIcon },
      ]
    : [
        { id: 'calendar', label: 'Calendar', icon: CalendarDays },
        { id: 'devices', label: 'Phones & apps', icon: Smartphone },
      ];
  // Two-step verification guards the local password; an SSO account's belongs to its provider.
  if (user?.sso_provider === 'local') navItems.push({ id: 'security', label: 'Two-step verification', icon: ShieldCheck });

  return (
    <>
      <header className="app-header">
        <div className="app-brand">
            <img src="/app-icon.png" width={28} height={28} alt="" />
            <span>{appName || 'KyCalendar'}</span>
          </div>

          <nav className="app-nav" aria-label="Primary">
            {navItems.map((item) => {
              const Icon = item.icon;
              const active = activeTab === item.id;
              return (
                <button
                  key={item.id}
                  onClick={() => onTabChange(item.id)}
                  className={active ? 'ky-nav-item active' : 'ky-nav-item'}
                  aria-current={active ? 'page' : undefined}
                >
                  <Icon size={16} aria-hidden="true" />
                  <span>{item.label}</span>
                </button>
              );
            })}
          </nav>
        <div className="app-header-actions">
          <ThemeSwitcher />

          {user && (
            <div className="app-user">
              <div className="app-user-copy">
                <div className="app-user-name">{user.display_name || user.username}</div>
                <div className="app-user-role">{user.role === 'admin' ? 'Administrator' : 'Everyday account'}</div>
              </div>
              <button
                type="button"
                className="btn-secondary app-logout"
                onClick={onLogout}
                title="Sign out"
                aria-label="Sign out"
              >
                <LogOut size={16} aria-hidden="true" />
              </button>
            </div>
          )}
        </div>
      </header>
    </>
  );
};
