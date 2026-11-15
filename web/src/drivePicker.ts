/** A file link from KyDrive's picker (contract I, `mode=link`). */
export interface DriveFile { name: string; url: string; type?: string }

/** The https origin of KyDrive from the user's suite app list, '' when absent or not https. */
export function driveOrigin(apps: { id: string; url: string }[]): string {
  const app = apps.find((a) => a.id === 'kydrive');
  try {
    const u = new URL(app?.url ?? '');
    return u.protocol === 'https:' ? u.origin : '';
  } catch {
    return '';
  }
}

// Every field is untrusted text: a name the server will accept, a link on KyDrive itself.
function picked(data: unknown, origin: string): DriveFile[] | null {
  const d = data as { type?: unknown; files?: unknown } | null;
  if (d?.type !== 'kydrive.picked' || !Array.isArray(d.files)) return null;
  const out: DriveFile[] = [];
  for (const f of d.files.slice(0, 20) as { name?: unknown; url?: unknown }[]) {
    const name = typeof f?.name === 'string' ? f.name.trim() : '';
    let url: URL | null = null;
    try {
      url = typeof f?.url === 'string' && f.url.length <= 2048 ? new URL(f.url) : null;
    } catch {
      url = null;
    }
    // eslint-disable-next-line no-control-regex
    if (!name || new TextEncoder().encode(name).length > 255 || /["\u0000-\u001f\u007f-\u009f]/.test(name)) continue;
    if (!url || url.origin !== origin || url.username || url.password || /\s/.test(String(f.url))) continue;
    out.push({ name, url: String(f.url) });
  }
  return out;
}

/**
 * Opens KyDrive's picker for links and resolves with what the user picked: [] when they close
 * it. Only `kydrive.picked` from that popup at exactly `origin` is read.
 */
export function pickFromDrive(origin: string): Promise<DriveFile[]> {
  const popup = window.open(`${origin}/pick?origin=${encodeURIComponent(window.location.origin)}&mode=link`, 'kydrive-pick', 'popup,width=720,height=640');
  if (!popup) return Promise.resolve([]);
  return new Promise((resolve) => {
    const done = (files: DriveFile[]) => {
      window.removeEventListener('message', onMessage);
      clearInterval(timer);
      resolve(files);
    };
    const onMessage = (e: MessageEvent) => {
      if (e.origin !== origin || e.source !== popup) return;
      const files = picked(e.data, origin);
      if (files) done(files);
    };
    window.addEventListener('message', onMessage);
    const timer = setInterval(() => {
      if (popup.closed) done([]);
    }, 500);
  });
}
