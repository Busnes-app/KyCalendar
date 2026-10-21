import React from 'react';
import { ThemeSwitcher } from '../components/ThemeSwitcher';
import { PageHeader } from '../components/ui';

interface SettingsProps {
  settings: any;
}

const DRIVERS: Record<string, string> = { sqlite: 'SQLite', postgres: 'PostgreSQL', postgresql: 'PostgreSQL', pgx: 'PostgreSQL' };

export const Settings: React.FC<SettingsProps> = ({ settings }) => {
  const driver = String(settings?.db_driver || 'sqlite');
  const version = settings?.version ? String(settings.version) : '';
  return (
    <section className="page">
      <PageHeader title="Settings" lede="How this browser shows KyCalendar, where the server keeps its data, and which version it runs." />

      <div className="card">
        <h2 className="section-title">Appearance</h2>
        <div className="form">
          <div className="field">
            <span className="field-label">Colour theme</span>
            <ThemeSwitcher />
            <span className="field-hint">Saved in this browser only. System follows your device's light or dark setting.</span>
          </div>
        </div>
      </div>

      <div className="card">
        <h2 className="section-title">Database</h2>
        <dl className="facts">
          <dt>Engine</dt>
          <dd>
            <span className="badge">{DRIVERS[driver] ?? driver}</span>
          </dd>
        </dl>
        <p className="field-hint card-body">
          Set by <code>KY_DB_DRIVER</code> and <code>KY_DB_DSN</code> when the server starts. Backups capture SQLite only; see Backups.
        </p>
      </div>

      <div className="card">
        <h2 className="section-title">About</h2>
        <dl className="facts">
          <dt>Version</dt>
          <dd>{version || 'Unknown'}</dd>
        </dl>
        <p className="field-hint card-body">
          Stamped into the server when it was built. <code>dev</code> means it was built from source, not from a release.
        </p>
      </div>
    </section>
  );
};
