/** A start and end as one short range: "Nov 17, 2026, 6:45 PM – 7:45 PM", the date once when both fall on it. */
export function timeRange(s: { start: string; end: string }): string {
  const a = new Date(s.start);
  const b = new Date(s.end);
  const same = a.toDateString() === b.toDateString();
  return `${a.toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })} – ${same ? b.toLocaleTimeString([], { timeStyle: 'short' }) : b.toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })}`;
}
