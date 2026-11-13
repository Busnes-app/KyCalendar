import React, { useEffect, useState } from 'react';
import { PageHeader } from '../components/ui';
import { JSON_HEADERS, REAUTH, failure, send } from '../admin';

interface SettingsProps {
  settings: any;
}

interface ExemptStatus {
  networks: string;
  environment: string;
  exempted: number;
}

const NETWORKS_ROUTE = '/api/settings/rate-limit-networks';

const lines = (list: string) => list.split(',').filter(Boolean).join('\n');

// Office networks whose requests skip the per-address limits. One network per line.
const ExemptNetworks: React.FC = () => {
  const [status, setStatus] = useState<ExemptStatus | null>(null);
  const [text, setText] = useState('');
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  const show = (s: ExemptStatus) => {
    setStatus(s);
    setText(lines(s.networks));
  };

  useEffect(() => {
    void send(NETWORKS_ROUTE).then(async (res) =>
      res?.ok ? show(await res.json()) : setError(res ? `Could not load the networks (HTTP ${res.status}).` : 'Could not load the networks.'),
    );
  }, []);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setMessage('');
    setError('');
    const res = await send(NETWORKS_ROUTE, {
      method: 'PUT',
      headers: JSON_HEADERS,
      body: JSON.stringify({ networks: text.split(/[\s,]+/).filter(Boolean).join(',') }),
    });
    setSaving(false);
    if (!res?.ok) {
      const f = await failure(res);
      setError(f.code === 'reauth_required' ? REAUTH : (f.error ?? 'Could not save the networks.'));
      return;
    }
    const body: ExemptStatus = await res.json();
    show(body);
    setMessage(body.networks ? 'Saved. These networks now skip the per-address limit.' : 'Saved. No network skips the limit.');
  };

  return (
    <form className="card form-card" onSubmit={save} aria-labelledby="exempt-heading">
      <h2 className="section-title" id="exempt-heading">Office networks</h2>
      <p className="field-hint card-body">
        Sign-in and calendar apps are limited per network address. Staff in one office usually share one address and can trip that limit
        together. Networks listed here skip it; per-account limits and the security check still apply.
      </p>
      <div className="form form-wide">
        <div className="field">
          <label htmlFor="exempt-networks">Networks that skip the per-address limit, one per line</label>
          <textarea
            id="exempt-networks"
            className="font-mono"
            rows={4}
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder="203.0.113.0/24"
            aria-describedby="exempt-hint"
            disabled={!status}
          />
          <span className="field-hint" id="exempt-hint">
            List only networks you control: everyone behind a listed address loses the limit, so never add guest Wi-Fi, a VPN provider or
            your reverse proxy. The widest range accepted is /16 (IPv4) or /48 (IPv6).
          </span>
        </div>
        {error && <p role="alert">{error}</p>}
        {message && <p role="status">{message}</p>}
        {status && (
          <dl className="facts">
            {status.environment && (
              <>
                <dt>Also set by the server environment</dt>
                <dd className="font-mono">{status.environment.split(',').join(', ')}</dd>
              </>
            )}
            <dt>Requests that skipped the limit</dt>
            <dd>{status.exempted} since the last change or restart</dd>
          </dl>
        )}
      </div>
      <div className="form-actions card-footer">
        <button type="submit" disabled={saving || !status}>Save networks</button>
      </div>
    </form>
  );
};

const DRIVERS: Record<string, string> = { sqlite: 'SQLite', postgres: 'PostgreSQL', postgresql: 'PostgreSQL', pgx: 'PostgreSQL' };

export const Settings: React.FC<SettingsProps> = ({ settings }) => {
  const driver = String(settings?.db_driver || 'sqlite');
  const version = settings?.version ? String(settings.version) : '';
  return (
    <section>
      <PageHeader title="Server settings" lede="Where the server keeps its data, which version it runs, and which office networks skip the per-address limit." />

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

      <ExemptNetworks />
    </section>
  );
};
