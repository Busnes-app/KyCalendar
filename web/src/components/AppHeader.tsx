import React from 'react';
import { Smartphone, LogOut, Users, Settings as SettingsIcon, LayoutDashboard, Archive, CalendarDays } from 'lucide-react';
import { ThemeSwitcher } from './ThemeSwitcher';

interface AppHeaderProps {
  appName: string;
  activeTab: string;
  onTabChange: (tab: string) => void;
  user: any;
  onLogout: () => void;
}

export const AppHeader: React.FC<AppHeaderProps> = ({ appName, activeTab, onTabChange, user, onLogout }) => {
  const navItems = [
    { id: 'dashboard', label: 'Overview', icon: LayoutDashboard },
    { id: 'scim', label: 'Directory & SCIM', icon: Users },
    { id: 'backup', label: 'KyBackup (Feature 0)', icon: Archive },
    { id: 'settings', label: 'Settings & DB', icon: SettingsIcon },
    ...(user?.role === 'admin' ? [{ id: 'group-calendars', label: 'Group calendars', icon: CalendarDays }] : []),
    ...(user?.role !== 'admin' ? [{ id: 'devices', label: 'Phones & apps', icon: Smartphone }] : []),
  ];

  return (
    <>
      <header className="app-header">
        <div className="app-brand">
            <img src="/app-icon.png" width={28} height={28} alt="" />
            <span>{appName || 'Busnes.app'}</span>
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
                  <Icon size={16} />
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
                <div style={{ fontWeight: 600, color: 'var(--ink-strong)' }}>{user.display_name || user.username}</div>
                <div style={{ fontSize: '11px', color: 'var(--ink)' }}>{user.role}</div>
              </div>
              <button
                className="btn-secondary app-logout"
                onClick={onLogout}
                title="Sign out"
                aria-label="Sign out"
              >
                <LogOut size={16} />
              </button>
            </div>
          )}
        </div>
      </header>
    </>
  );
};
