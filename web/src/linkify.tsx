import type { ReactNode } from 'react';

const URL_PATTERN = /(https?:\/\/[^\s<>"']+)/g;
const TRAILING = /[.,;:!?)\]]+$/;

// linkify renders untrusted text: plain text, plus http(s) URLs as links that open safely.
export function linkify(text: string): ReactNode[] {
  return text.split(URL_PATTERN).map((part, i) => {
    if (i % 2 === 0) return part;
    const trail = part.match(TRAILING)?.[0] ?? '';
    const href = trail ? part.slice(0, -trail.length) : part;
    let ok = false;
    try {
      const u = new URL(href);
      ok = u.protocol === 'http:' || u.protocol === 'https:';
    } catch {
      ok = false;
    }
    if (!ok) return part;
    return (
      <span key={i}>
        <a href={href} target="_blank" rel="noopener noreferrer">{href}</a>
        {trail}
      </span>
    );
  });
}
