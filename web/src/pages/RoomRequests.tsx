import { useCallback, useEffect, useState } from 'react';
import { ApiError, decideRoomRequest, roomRequests, type RoomRequest } from '../calendarApi';
import { EmptyState, PageHeader } from '../components/ui';
import { timeRange as when } from '../timeRange';

/** Pending bookings of the rooms and equipment the signed-in person approves. */
export default function RoomRequests() {
  const [requests, setRequests] = useState<RoomRequest[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);

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
    setDone(null);
    setBusy(r.id);
    try {
      await decideRoomRequest(r.id, decision);
      setDone(`${decision === 'accept' ? 'Accepted' : 'Declined'}: ${r.resource_name}${r.occurrences > 0 ? `, ${when(r.first)}` : ''}.`);
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
      <p className="kc-request-done" role="status">{done}</p>
      {requests === null ? (
        <p className="app-loading" role="status">Loading requests…</p>
      ) : requests.length === 0 ? (
        <EmptyState title="No requests waiting.">Requests wait here until you accept or decline them. Until then they hold the time.</EmptyState>
      ) : (
        requests.map((r) => (
          <article key={r.id} className="card" aria-labelledby={`req-${r.id}`}>
            <div className="card-header">
              <div>
                <h2 className="card-title" id={`req-${r.id}`}>{r.resource_name}</h2>
                <p className="card-meta">Requested by {r.organizer || 'someone no longer here'}</p>
              </div>
            </div>
            <div className="kc-request-body">
            <dl className="facts">
              <dt>When</dt>
              <dd>{r.occurrences > 0 ? when(r.first) : 'No upcoming time'}</dd>
              {r.occurrences > 1 && (
                <>
                  <dt>Repeats</dt>
                  <dd>{`${r.occurrences} times, until ${new Date(r.last.start).toLocaleDateString([], { dateStyle: 'medium' })}`}</dd>
                </>
              )}
            </dl>
            <div className="kc-request-actions">
              <button type="button" className="btn-secondary" disabled={busy === r.id} onClick={() => void decide(r, 'decline')}>Decline</button>
              <button type="button" className="btn-secondary" disabled={busy === r.id} onClick={() => void decide(r, 'accept')}>Accept</button>
            </div>
            </div>
          </article>
        ))
      )}
    </section>
  );
}
