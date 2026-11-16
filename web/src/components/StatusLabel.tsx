import type { Presence, PresenceState } from '../calendarApi';

const LABELS: Record<PresenceState, string> = {
  available: 'Available',
  busy: 'Busy',
  in_a_meeting: 'In a meeting',
  away: 'Away',
  out_of_office: 'Out of office',
  unknown: 'Status unknown',
};

/** "In a meeting until 15:00", "Out of office until 5 Dec": the label always carries the meaning. */
export function statusText(p: Pick<Presence, 'state' | 'until'>, now = new Date()): string {
  const label = LABELS[p.state] ?? LABELS.unknown;
  const until = p.until ? new Date(p.until) : null;
  if (!until || Number.isNaN(until.getTime())) return label;
  if (p.state === 'in_a_meeting') return `${label} until ${until.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`;
  if (p.state === 'out_of_office') {
    const sameYear = until.getFullYear() === now.getFullYear();
    return `${label} until ${until.toLocaleDateString([], { day: 'numeric', month: 'short', ...(sameYear ? {} : { year: 'numeric' }) })}`;
  }
  return label;
}

/** A status dot and its text; the dot is decoration only. */
export function StatusLabel({ presence }: { presence?: Pick<Presence, 'state' | 'until'> }) {
  if (!presence) return null;
  return (
    <span className="kc-status" data-state={presence.state} title={statusText(presence)}>
      <span className="kc-status-dot" aria-hidden="true" />
      <span className="kc-status-text">{statusText(presence)}</span>
    </span>
  );
}
