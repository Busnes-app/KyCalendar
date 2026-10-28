// Capture tools contact only an explicitly numbered, literal IPv4 loopback fixture.
export function fixtureOrigin(value) {
  const match = typeof value === 'string' && /^http:\/\/127\.0\.0\.1:([0-9]+)$/.exec(value);
  if (!match) throw new Error('Expected a literal HTTP loopback fixture origin');
  const port = Number.parseInt(match[1], 10);
  if (port < 1024 || port > 65535 || String(port) !== match[1]) throw new Error('Expected an unprivileged canonical fixture port');
  return `http://127.0.0.1:${port}`;
}

export function fixtureURL(origin, path) {
  const base = fixtureOrigin(origin);
  if (typeof path !== 'string' || !/^\/[A-Za-z0-9_./-]*$/.test(path) || path.startsWith('//') || path.split('/').some(part => part === '.' || part === '..')) {
    throw new Error('Expected a local fixture path without traversal, query or fragment');
  }
  return base + path;
}

export function fetchFixture(origin, path) {
  return fetch(fixtureURL(origin, path), { redirect: 'error', signal: AbortSignal.timeout(5000) });
}
