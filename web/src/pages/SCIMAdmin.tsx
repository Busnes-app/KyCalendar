import { useEffect, useState } from 'react';
import { send } from '../admin';
import { CopyButton, EmptyState, PageHeader } from '../components/ui';

interface SCIMUser {
  id: string;
  username: string;
  display_name: string;
  email: string;
  status: string;
}

interface SCIMStatus {
  enabled: boolean;
  token_configured: boolean;
  endpoint: string;
  users: SCIMUser[];
  total: number;
}

// The bearer token never reaches the browser: the server says only whether one is set.
export const SCIMAdmin: React.FC = () => {
  const [status, setStatus] = useState<SCIMStatus | null>(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    void (async () => {
      const res = await send('/api/admin/scim');
      const body = res?.ok ? await res.json().catch(() => null) : null;
      if (body) setStatus(body);
      else setError(true);
    })();
  }, []);

  return (
    <section className="page">
      <PageHeader
        title="Directory (SCIM)"
        lede="Your identity provider can create, update and disable people and groups here over SCIM 2.0. People it provisions are read-only in KyCalendar."
      />
      {error && <p role="alert">Could not load the SCIM status. Reload the page to try again.</p>}
      {status && (
        <>
          <div className="card">
            <div className="card-header">
              <h2 className="card-title">Provisioning</h2>
              <span className={status.enabled ? 'badge badge-success' : 'badge'}>{status.enabled ? 'Enabled' : 'Disabled'}</span>
            </div>
            <p className="card-body muted">
              {status.token_configured
                ? 'A bearer token is set in KY_SCIM_TOKEN. Give the same value to your identity provider.'
                : 'Set KY_SCIM_TOKEN (at least 32 characters) and restart to enable SCIM.'}
            </p>
            <div className="form form-wide card-body">
              <div className="field">
                <label htmlFor="scim-url">SCIM base URL</label>
                <div className="input-with-action">
                  <input id="scim-url" type="text" readOnly value={status.endpoint} className="font-mono" />
                  <CopyButton text={status.endpoint} label="Copy the SCIM base URL" />
                </div>
              </div>
            </div>
          </div>
          <section className="section" aria-labelledby="scim-accounts">
            <h2 className="section-title" id="scim-accounts">Provisioned accounts ({status.total})</h2>
            {status.users.length === 0 ? (
              <EmptyState title="No accounts provisioned through SCIM.">Accounts appear here once your identity provider pushes them.</EmptyState>
            ) : (
              <div className="table-card">
                <table className="data-table stacked">
                  <thead>
                    <tr>
                      <th>Username</th>
                      <th>Display name</th>
                      <th>Email</th>
                      <th>Status</th>
                    </tr>
                  </thead>
                  <tbody>
                    {status.users.map((u) => (
                      <tr key={u.id}>
                        <td className="cell-strong">{u.username}</td>
                        <td data-label="Display name">{u.display_name || '—'}</td>
                        <td data-label="Email">{u.email || '—'}</td>
                        <td data-label="Status">
                          <span className={`badge ${u.status === 'active' ? 'badge-success' : 'badge-danger'}`}>{u.status === 'active' ? 'Active' : 'Inactive'}</span>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {status.total > status.users.length && <p className="load-more">{`Showing ${status.users.length} of ${status.total}`}</p>}
          </section>
        </>
      )}
    </section>
  );
};
