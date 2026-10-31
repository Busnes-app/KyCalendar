import React from 'react';
import { PageHeader } from '../components/ui';

interface SettingsProps {
  settings: any;
}

const DRIVERS: Record<string, string> = { sqlite: 'SQLite', postgres: 'PostgreSQL', postgresql: 'PostgreSQL', pgx: 'PostgreSQL' };

export const Settings: React.FC<SettingsProps> = ({ settings }) => {
  const driver = String(settings?.db_driver || 'sqlite');
  const version = settings?.version ? String(settings.version) : '';
  return (
    <section>
      <PageHeader title="Settings" lede="Where the server keeps its data, and which version it runs. Themes are under Appearance." />

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
