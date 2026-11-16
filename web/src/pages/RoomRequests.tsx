import { useCallback, useEffect, useState } from 'react';
import { ApiError, decideRoomRequest, roomRequests, type RoomRequest } from '../calendarApi';
import { EmptyState, PageHeader } from '../components/ui';

const when = (s: { start: string; end: string }) => {
  const a = new Date(s.start);
  const b = new Date(s.end);
  const same = a.toDateString() === b.toDateString();
  return `${a.toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })} – ${same ? b.toLocaleTimeString([], { timeStyle: 'short' }) : b.toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })}`;
};

/** Pending bookings of the rooms and equipment the signed-in person approves. */
export default function RoomRequests() {
  const [requests, setRequests] = useState<RoomRequest[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setRequests(await roomRequests());
    } catch {
      setError('Could not load room requests. Reload the page to try again.');
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function decide(r: RoomRequest, decision: 'accept' | 'decline') {
    setError(null);
    setBusy(r.id);
    try {
      await decideRoomRequest(r.id, decision);
    } catch (e) {
      setError(e instanceof ApiError && e.code === 'not_pending' ? 'Someone already answered that request, or the event changed.' : 'Could not answer the request. Try again.');
    } finally {
      setBusy(null);
      await load();
    }
  }

  return (
    <section className="kc-requests">
      <PageHeader title="Room requests" lede="Requests for the rooms and equipment you approve. You see who asked and when, never what the event is." />
      {error && <p role="alert">{error}</p>}
      {requests === null ? (
        <p className="app-loading" role="status">Loading requests…</p>
      ) : requests.length === 0 ? (
        <EmptyState title="No requests waiting.">New requests appear here until you accept or decline them. Until then they hold the time.</EmptyState>
      ) : (
        requests.map((r) => (
          <article key={r.id} className="card" aria-labelledby={`req-${r.id}`}>
            <div className="card-header">
              <div>
                <h2 className="card-title" id={`req-${r.id}`}>{r.resource_name}</h2>
                <p className="card-meta">Requested by {r.organizer || 'someone no longer here'}</p>
              </div>
            </div>
            <dl className="facts card-body">
              <dt>When</dt>
              <dd>{r.occurrences > 0 ? when(r.first) : 'No upcoming time'}</dd>
              {r.occurrences > 1 && (
                <>
                  <dt>Repeats</dt>
                  <dd>{`${r.occurrences} times, the last ${when(r.last)}`}</dd>
                </>
              )}
            </dl>
            <div className="card-footer modal-actions">
              <button type="button" className="btn-secondary" disabled={busy === r.id} onClick={() => void decide(r, 'decline')}>Decline</button>
              <button type="button" disabled={busy === r.id} onClick={() => void decide(r, 'accept')}>Accept</button>
            </div>
          </article>
        ))
      )}
    </section>
  );
}
