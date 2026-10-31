import React from 'react';
import { Key, Archive, Users, ArrowRight, UsersRound, UserRound, CalendarDays } from 'lucide-react';
import { PageHeader } from '../components/ui';

interface DashboardProps {
  settings: any;
  user: any;
  onNavigate: (tab: string) => void;
}

interface Tile {
  tab: string;
  title: string;
  desc: string;
  action: string;
  icon: typeof Key;
  // Only facts the settings response states; nothing here claims a state it has not read.
  status?: { label: string; on: boolean };
}

export const Dashboard: React.FC<DashboardProps> = ({ settings, user, onNavigate }) => {
  const tiles: Tile[] = [
    {
      tab: 'people',
      title: 'People',
      desc: 'Add people who sign in with a password, reset passwords, make administrators and disable accounts.',
      action: 'Manage people',
      icon: UserRound,
    },
    {
      tab: 'groups',
      title: 'Groups',
      desc: 'Local groups, beside any your identity provider syncs. A group gives its members its group calendars.',
      action: 'Manage groups',
      icon: UsersRound,
    },
    {
      tab: 'group-calendars',
      title: 'Group calendars',
      desc: 'Calendars that belong to the organisation. Choose which groups read, edit or manage each one.',
      action: 'Manage group calendars',
      icon: CalendarDays,
    },
    {
      tab: 'signin',
      title: 'Sign-in',
      desc: 'Single Sign On or another OpenID Connect provider, set here or locked by environment variables.',
      action: 'Sign-in settings',
      icon: Key,
      status: settings?.signin_name ? { label: settings.signin_name, on: true } : { label: 'Passwords only', on: false },
    },
    {
      tab: 'scim',
      title: 'Directory (SCIM)',
      desc: 'Let your identity provider create, update and disable people and groups here automatically.',
      action: 'Directory settings',
      icon: Users,
      status: settings?.scim_enabled ? { label: 'On', on: true } : { label: 'Off', on: false },
    },
    {
      tab: 'backup',
      title: 'Backups',
      desc: 'Encrypted backups to KyRecovery or a local folder, on a schedule, with restore drills.',
      action: 'Backups',
      icon: Archive,
    },
  ];

  return (
    <section>
      <PageHeader
        title={`Welcome, ${user?.display_name || user?.username}`}
        lede={`Manage who can sign in to ${settings?.app_name || 'KyCalendar'}, who shares which calendars, and how it is backed up. Administrators never see calendars or events.`}
      />

      <div className="tiles">
        {tiles.map((t) => {
          const Icon = t.icon;
          return (
            <article key={t.tab} className="tile" aria-labelledby={`tile-${t.tab}`}>
              <div className="tile-head">
                <h2 className="tile-title" id={`tile-${t.tab}`}>
                  <Icon size={18} aria-hidden="true" />
                  {t.title}
                </h2>
                {t.status && <span className={t.status.on ? 'badge badge-success' : 'badge'}>{t.status.label}</span>}
              </div>
              <p className="tile-desc">{t.desc}</p>
              <button type="button" className="btn-secondary btn-sm tile-action" onClick={() => onNavigate(t.tab)}>
                {t.action}
                <ArrowRight size={14} aria-hidden="true" />
              </button>
            </article>
          );
        })}
      </div>
    </section>
  );
};
