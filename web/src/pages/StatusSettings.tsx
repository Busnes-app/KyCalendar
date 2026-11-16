import { useEffect, useState } from 'react';
import { ApiError, saveStatusSettings, statusSettings, type Manual, type StatusSettings as Settings } from '../calendarApi';
import { statusText } from '../components/StatusLabel';

const FOR = [
  { label: '1 hour', hours: 1 },
  { label: '4 hours', hours: 4 },
  { label: '1 day', hours: 24 },
  { label: '1 week', hours: 24 * 7 - 1 },
];

/** Settings → Status: the switch and manual status KyMessages keeps for this member. */
export function StatusSettings() {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [error, setError] = useState('');
  const [off, setOff] = useState(false);
  const [state, setState] = useState<Manual['state']>('busy');
  const [hours, setHours] = useState(1);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    statusSettings().then(setSettings, (e) => {
      if (e instanceof ApiError && e.code === 'status_off') setOff(true);
      else setError(e instanceof Error ? e.message : 'Could not load your status');
    });
  }, []);

  async function save(b: Parameters<typeof saveStatusSettings>[0]) {
    setSaving(true);
    setError('');
    try {
      setSettings(await saveStatusSettings(b));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not save');
    } finally {
      setSaving(false);
    }
  }

  if (off) return <div className="card"><p>Status needs KyMessages and a KyIdentity sign-in. Ask your administrator.</p></div>;
  if (!settings) return <div className="card">{error ? <p role="alert">{error}</p> : <p role="status">Loading…</p>}</div>;
  return (
    <>
      <div className="card">
        <h2 className="section-title">Show my status to colleagues</h2>
        <label className="kc-check">
          <input type="checkbox" role="switch" checked={settings.share} disabled={saving} onChange={(e) => void save({ share: e.target.checked })} /> Show my status to colleagues
        </label>
        <p className="kc-hint">Colleagues see Available, In a meeting or Out of office beside your name. Only start and end times of meetings you accepted are shared, never their titles or who attends. Off, you appear as “Status unknown” everywhere.</p>
      </div>
      {settings.share && (
        <div className="card">
          <h2 className="section-title">Set a status</h2>
          {settings.manual ? (
            <>
              <p>{statusText(settings.manual)} until {new Date(settings.manual.until).toLocaleString([], { weekday: 'short', hour: '2-digit', minute: '2-digit' })}. It wins over your calendar until then.</p>
              <button type="button" className="btn-secondary" disabled={saving} onClick={() => void save({ manual: null })}>Go back to automatic</button>
            </>
          ) : (
            <form className="kc-status-form" onSubmit={(e) => { e.preventDefault(); void save({ manual: { state, until: new Date(Date.now() + hours * 3600_000).toISOString().replace(/\.\d+Z$/, 'Z') } }); }}>
              <label htmlFor="kc-status-state">Status</label>
              <select id="kc-status-state" value={state} onChange={(e) => setState(e.target.value as Manual['state'])}>
                <option value="available">Available</option>
                <option value="busy">Busy</option>
                <option value="away">Away</option>
              </select>
              <label htmlFor="kc-status-for">For</label>
              <select id="kc-status-for" value={hours} onChange={(e) => setHours(Number(e.target.value))}>
                {FOR.map((f) => <option key={f.hours} value={f.hours}>{f.label}</option>)}
              </select>
              <button type="submit" className="btn-primary" disabled={saving}>Set status</button>
            </form>
          )}
        </div>
      )}
      {error && <p role="alert">{error}</p>}
    </>
  );
}
