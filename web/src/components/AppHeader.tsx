import React, { useCallback, useEffect, useState } from 'react';
import { LicenseFooter } from './LicenseFooter';
import { AppRail, type SuiteApp } from './AppRail';
import { LogOut, Users, Settings as SettingsIcon, LayoutDashboard, Archive, CalendarDays, UsersRound, UserRound, KeyRound, Server } from 'lucide-react';

interface AppHeaderProps {
  appName: string;
  activeTab: string;
  onTabChange: (tab: string) => void;
  user: any;
  onLogout: () => void;
  /** Where a page renders its own sidebar content, below the navigation. */
  sidebarSlot?: (el: HTMLDivElement | null) => void;
  /** The server's version, from `/api/settings`. */
  version?: string;
  /** Suite apps for the rail, from `/api/suite/apps`. */
  apps?: SuiteApp[];
}

// useScrollCue marks which ends of a scroll box hide content (`data-cue="start end"`), so CSS can
// fade those edges and show there is more to scroll to.
export function useScrollCue() {
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

export const AppHeader: React.FC<AppHeaderProps> = ({ appName, activeTab, onTabChange, user, onLogout, sidebarSlot, version, apps = [] }) => {
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
        { id: 'server-settings', label: 'Server settings', icon: Server },
      ]
    : [{ id: 'calendar', label: 'Calendar', icon: CalendarDays }];
  const role = user?.role === 'admin' ? 'Administrator' : 'Everyday account';
  // Name over role, never the same word twice: a display name equal to the role yields to the username.
  const name = user?.display_name && user.display_name !== role ? user.display_name : user?.username;
  // Personal settings sit behind the gear, in the footer and the phone header, not in the navigation.
  const onSettings = activeTab.startsWith('settings/');
  const actions = (
    <>
      <a className="ky-icon-button" href="#/settings/account" aria-label="Settings" title="Settings" aria-current={onSettings ? 'page' : undefined}>
        <SettingsIcon aria-hidden="true" />
      </a>
      <button type="button" className="ky-icon-button" aria-label="Sign out" title="Sign out" onClick={onLogout}>
        <LogOut aria-hidden="true" />
      </button>
    </>
  );

  return (
    <header className="ky-sidebar">
      <div className="ky-mobile-head">
        <div className="ky-brand">
          <img className="brand-mark" src="/app-icon.png" width={28} height={28} alt="" />
          <span>{appName || 'KyCalendar'}</span>
        </div>
        {user && <div className="ky-mobile-actions">{actions}</div>}
      </div>
      {/* One flex row on phones: the tabs, then the page's sidebar content (the Calendars disclosure). */}
      <div className="app-nav-row">
        <nav className="ky-nav" aria-label="Primary">
          {navItems.map((item) => {
            const Icon = item.icon;
            const active = activeTab === item.id;
            return (
              <button
                key={item.id}
                type="button"
                onClick={() => onTabChange(item.id)}
                onFocus={(e) => e.currentTarget.scrollIntoView({ block: 'nearest', inline: 'nearest' })}
                className="ky-nav-item"
                aria-current={active ? 'page' : undefined}
              >
                <Icon aria-hidden="true" />
                <span>{item.label}</span>
              </button>
            );
          })}
        </nav>
        <div className="app-sidebar-slot" ref={slotRef} />
      </div>
      <div className="ky-sidebar-foot">
        <AppRail apps={apps} />
        {user && (
          <div className="ky-user">
            <div className="ky-user-copy">
              <p className="ky-user-name">{name}</p>
              <p className="ky-user-role">{role}</p>
            </div>
            <div className="ky-user-actions">{actions}</div>
          </div>
        )}
        <LicenseFooter version={version} />
      </div>
    </header>
  );
};
