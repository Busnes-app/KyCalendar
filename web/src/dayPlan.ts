// Day planning, pure: busy blocks (events and placed tasks) between the working day's bounds,
// the free gaps between them, and where a task of some length fits. Times are epoch ms.

export interface Block { start: number; end: number }
export interface Gap extends Block { minutes: number }

const MIN = 60000;
export const SNAP = 15; // minutes

export function merge(blocks: Block[]): Block[] {
  const sorted = blocks.filter((b) => b.end > b.start).sort((a, b) => a.start - b.start);
  const out: Block[] = [];
  for (const b of sorted) {
    const last = out[out.length - 1];
    if (last && b.start <= last.end) last.end = Math.max(last.end, b.end);
    else out.push({ ...b });
  }
  return out;
}

// gaps are the free stretches in [from, to) outside busy, at least SNAP minutes long.
export function gaps(from: number, to: number, busy: Block[]): Gap[] {
  const out: Gap[] = [];
  let at = from;
  for (const b of merge(busy)) {
    if (b.end <= from) continue;
    if (b.start >= to) break;
    if (b.start > at) out.push({ start: at, end: b.start, minutes: Math.round((b.start - at) / MIN) });
    at = Math.max(at, b.end);
  }
  if (to > at) out.push({ start: at, end: to, minutes: Math.round((to - at) / MIN) });
  return out.filter((g) => g.minutes >= SNAP);
}

// snap rounds t up to the next SNAP minutes.
export function snapUp(t: number): number {
  const step = SNAP * MIN;
  return Math.ceil(t / step) * step;
}

// fit is the earliest start at or after after where minutes fit in a gap, or null.
export function fit(free: Gap[], minutes: number, after = -Infinity): number | null {
  for (const g of free) {
    const s = Math.max(g.start, after === -Infinity ? g.start : snapUp(after));
    if (s + minutes * MIN <= g.end) return s;
  }
  return null;
}

export interface Candidate { id: string; minutes: number }

// suggestions pair each gap (in order) with the first candidate that fits it whole.
export function suggestions(free: Gap[], candidates: Candidate[], after: number): { start: number; gap: number; id: string }[] {
  const out: { start: number; gap: number; id: string }[] = [];
  const used = new Set<string>();
  for (const g of free) {
    const s = Math.max(g.start, snapUp(after));
    const room = Math.round((g.end - s) / MIN);
    const c = candidates.find((x) => !used.has(x.id) && x.minutes <= room);
    if (c) { used.add(c.id); out.push({ start: s, gap: room, id: c.id }); }
  }
  return out;
}

export const hm = (minutes: number) => (minutes < 60 ? `${minutes}m` : minutes % 60 ? `${Math.floor(minutes / 60)}h ${minutes % 60}m` : `${minutes / 60}h`);
