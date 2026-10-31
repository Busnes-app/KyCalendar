// Calendar colours. A new calendar gets the first palette colour its neighbours don't use. The
// first calendar stored without one (the default calendar, CalDAV or agent-created) shows the
// theme accent; any further one shows the next unused palette colour, so no two share a colour by
// default. Every palette colour holds white text at 4.5:1; the accent carries `--button-text`.
export const ACCENT = 'var(--accent)';
export const PALETTE = ['#1f7a6d', '#bf3f18', '#3b63b8', '#7b4fc0', '#a0661a', '#b8336a', '#4f7a28', '#5d6b75'] as const;

const norm = (c: string) => c.slice(0, 7).toLowerCase();

/** The first palette colour not in `used`, starting at `from`; the palette repeats once all are taken. */
export function nextColor(used: Iterable<string>, from = 0): string {
  const taken = new Set([...used].filter(Boolean).map(norm));
  for (let i = 0; i < PALETTE.length; i++) {
    const c = PALETTE[(from + i) % PALETTE.length];
    if (!taken.has(c)) return c;
  }
  return PALETTE[(from + taken.size) % PALETTE.length];
}

/** Each calendar's display colour: its own, else the accent for the first without one, else the next unused. */
export function calendarColors(calendars: { id: string; color: string }[]): Map<string, string> {
  const used = calendars.map((c) => c.color).filter(Boolean);
  const out = new Map<string, string>();
  let accentTaken = false;
  for (const c of calendars) {
    if (c.color) {
      out.set(c.id, c.color);
      continue;
    }
    if (!accentTaken) {
      accentTaken = true;
      out.set(c.id, ACCENT);
      continue;
    }
    const pick = nextColor(used);
    used.push(pick);
    out.set(c.id, pick);
  }
  return out;
}

function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const v = parseInt(hex.slice(i, i + 2), 16) / 255;
    return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

/** Charcoal or white, whichever reads better on `hex` (#RRGGBB[AA]); undefined for anything else. */
export function textOn(hex: string): string | undefined {
  if (!/^#[0-9a-f]{6}([0-9a-f]{2})?$/i.test(hex)) return undefined;
  const l = luminance(hex);
  return (1.05 / (l + 0.05)) >= ((l + 0.05) / (0.0153 + 0.05)) ? '#ffffff' : '#182326';
}
