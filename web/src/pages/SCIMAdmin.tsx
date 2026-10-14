import { useEffect, useState } from 'react';
import { Users } from 'lucide-react';
import { send } from '../admin';

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
    <div style={{ maxWidth: '1080px', margin: '0 auto', padding: '32px 20px' }}>
      <h1 style={{ fontSize: '24px', fontWeight: 'bold', display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '24px' }}>
        <Users size={24} style={{ color: 'var(--accent)' }} />
        <span>SCIM provisioning</span>
      </h1>
      {error && <p role="alert">Could not load the SCIM status. Reload the page to try again.</p>}
      {status && (
        <>
          <div className="panel" style={{ marginBottom: '24px' }}>
            <p>
              <span className={`badge badge-${status.enabled ? 'success' : 'neutral'}`}>{status.enabled ? 'Enabled' : 'Disabled'}</span>{' '}
              {status.token_configured
                ? 'A bearer token is set in KY_SCIM_TOKEN. Give the same value to your identity provider.'
                : 'Set KY_SCIM_TOKEN (at least 32 characters) and restart to enable SCIM.'}
            </p>
            <label style={{ display: 'block', fontSize: '12px', marginTop: '12px' }}>
              SCIM base URL
              <input type="text" readOnly value={status.endpoint} style={{ fontFamily: 'var(--font-mono)', fontSize: '13px' }} />
            </label>
          </div>
          <div className="panel">
            <h3 style={{ fontSize: '16px', marginBottom: '16px' }}>Provisioned accounts ({status.total})</h3>
            {status.users.length === 0 ? (
              <p style={{ fontSize: '13px' }}>No accounts provisioned through SCIM.</p>
            ) : (
              <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '13px' }}>
                <thead>
                  <tr style={{ borderBottom: '1px solid var(--line)', textAlign: 'left' }}>
                    <th style={{ padding: '8px 12px' }}>Username</th>
                    <th style={{ padding: '8px 12px' }}>Display name</th>
                    <th style={{ padding: '8px 12px' }}>Email</th>
                    <th style={{ padding: '8px 12px' }}>Status</th>
                  </tr>
                </thead>
                <tbody>
                  {status.users.map((u) => (
                    <tr key={u.id} style={{ borderBottom: '1px solid var(--line)' }}>
                      <td style={{ padding: '10px 12px', fontFamily: 'var(--font-mono)' }}>{u.username}</td>
                      <td style={{ padding: '10px 12px' }}>{u.display_name || '—'}</td>
                      <td style={{ padding: '10px 12px' }}>{u.email || '—'}</td>
                      <td style={{ padding: '10px 12px' }}>
                        <span className={`badge badge-${u.status === 'active' ? 'success' : 'danger'}`}>{u.status === 'active' ? 'Active' : 'Inactive'}</span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
            {status.total > status.users.length && <p>{`Showing ${status.users.length} of ${status.total}`}</p>}
          </div>
        </>
      )}
    </div>
  );
};
