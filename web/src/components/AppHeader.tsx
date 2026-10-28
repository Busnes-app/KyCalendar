import React, { useCallback, useEffect, useState } from 'react';
import { Smartphone, LogOut, Users, Settings as SettingsIcon, LayoutDashboard, Archive, CalendarDays, UsersRound, UserRound, KeyRound, ShieldCheck, Palette } from 'lucide-react';

interface AppHeaderProps {
  appName: string;
  activeTab: string;
  onTabChange: (tab: string) => void;
  user: any;
  onLogout: () => void;
  /** Where a page renders its own sidebar content, below the navigation. */
  sidebarSlot?: (el: HTMLDivElement | null) => void;
}

// useScrollCue marks which ends of a scroll box hide content (`data-cue="start end"`), so CSS can
// fade those edges and show there is more to scroll to.
function useScrollCue() {
  const [el, setEl] = useState<HTMLElement | null>(null);
  useEffect(() => {
    if (!el) return;
    const update = () => {
      const start = el.scrollTop > 1 || el.scrollLeft > 1;
      const end = el.scrollTop + el.clientHeight < el.scrollHeight - 1 || el.scrollLeft + el.clientWidth < el.scrollWidth - 1;
      el.dataset.cue = `${start ? 'start' : ''} ${end ? 'end' : ''}`.trim();
    };
    update();
    el.addEventListener('scroll', update, { passive: true });
    const resized = new ResizeObserver(update);
    resized.observe(el);
    const changed = new MutationObserver(update);
    changed.observe(el, { childList: true, subtree: true });
    return () => {
      el.removeEventListener('scroll', update);
      resized.disconnect();
      changed.disconnect();
    };
  }, [el]);
  return setEl;
}

export const AppHeader: React.FC<AppHeaderProps> = ({ appName, activeTab, onTabChange, user, onLogout, sidebarSlot }) => {
  const navCue = useScrollCue();
  const slotCue = useScrollCue();
  const slotRef = useCallback((el: HTMLDivElement | null) => { sidebarSlot?.(el); slotCue(el); }, [sidebarSlot, slotCue]);
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
  navItems.push({ id: 'appearance', label: 'Appearance', icon: Palette });

  return (
    <>
      <header className="app-header">
        <div className="app-brand">
            <img src="/app-icon.png" width={28} height={28} alt="" />
            <span>{appName || 'KyCalendar'}</span>
          </div>

          <nav className="app-nav" aria-label="Primary" ref={navCue}>
            {navItems.map((item) => {
              const Icon = item.icon;
              const active = activeTab === item.id;
              return (
                <button
                  key={item.id}
                  onClick={() => onTabChange(item.id)}
                  onFocus={(e) => e.currentTarget.scrollIntoView({ block: 'nearest', inline: 'nearest' })}
                  className={active ? 'ky-nav-item active' : 'ky-nav-item'}
                  aria-current={active ? 'page' : undefined}
                >
                  <Icon size={16} aria-hidden="true" />
                  <span>{item.label}</span>
                </button>
              );
            })}
          </nav>
        <div className="app-sidebar-slot" ref={slotRef} />
        <div className="app-header-actions">
          {user && (
            <div className="app-user">
              <div className="app-user-copy">
                <div className="app-user-name">{user.display_name || user.username}</div>
                <div className="app-user-role">{user.role === 'admin' ? 'Administrator' : 'Everyday account'}</div>
              </div>
              <button type="button" className="btn-ghost app-logout" onClick={onLogout}>
                <LogOut size={16} aria-hidden="true" />
                <span className="app-logout-label">Sign out</span>
              </button>
            </div>
          )}
        </div>
      </header>
    </>
  );
};
