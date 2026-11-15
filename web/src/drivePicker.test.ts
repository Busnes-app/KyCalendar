import { afterEach, describe, expect, it, vi } from 'vitest';
import { driveOrigin, pickFromDrive } from './drivePicker';

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

const DRIVE = 'https://drive.example';

// A stand-in picker window: the test posts as it would.
function standIn() {
  const popup = { closed: false } as unknown as Window;
  const open = vi.spyOn(window, 'open').mockReturnValue(popup);
  const post = (data: unknown, origin = DRIVE, source: unknown = popup) =>
    window.dispatchEvent(new MessageEvent('message', { data, origin, source: source as Window }));
  return { popup, open, post };
}

describe('driveOrigin', () => {
  it('is the https origin of the kydrive app only', () => {
    expect(driveOrigin([{ id: 'kydrive', url: 'https://drive.example/app/' }])).toBe(DRIVE);
    expect(driveOrigin([{ id: 'kydrive', url: 'http://drive.example/' }])).toBe('');
    expect(driveOrigin([{ id: 'kydrive', url: 'javascript:alert(1)' }])).toBe('');
    expect(driveOrigin([{ id: 'kypost', url: 'https://mail.example/' }])).toBe('');
    expect(driveOrigin([])).toBe('');
  });
});

describe('pickFromDrive', () => {
  it('opens the link picker for this origin and takes only kydrive.picked from that popup at KyDrive', async () => {
    const { open, post } = standIn();
    const result = pickFromDrive(DRIVE);
    expect(open.mock.calls[0][0]).toBe(`${DRIVE}/pick?origin=${encodeURIComponent(window.location.origin)}&mode=link`);
    const good = { type: 'kydrive.picked', files: [{ name: 'Report.pdf', size: 3, url: `${DRIVE}/l/#tok`, expires: '' }] };
    post(good, 'https://evil.example'); // another origin
    post(good, DRIVE, window); // another window at the right origin
    post({ ...good, type: 'kydrive.other' }); // another message
    post({ type: 'kydrive.picked', files: 'x' });
    post({
      type: 'kydrive.picked',
      files: [
        { name: 'Report.pdf', size: 3, url: `${DRIVE}/l/#tok` },
        { name: 'Elsewhere', url: 'https://evil.example/l/#x' },
        { name: 'Script', url: 'javascript:alert(1)' },
        { name: 'Creds', url: 'https://u:p@drive.example/l/#x' },
        { name: 'a\nFile: evil https://evil.example', url: `${DRIVE}/l/#y` },
        { name: 'quote"d', url: `${DRIVE}/l/#z` },
        { name: '', url: `${DRIVE}/l/#e` },
        { name: { toString: () => 'obj' }, url: `${DRIVE}/l/#o` },
      ],
    });
    await expect(result).resolves.toEqual([{ name: 'Report.pdf', url: `${DRIVE}/l/#tok` }]);
  });

  it('keeps a plain mime as the type and drops any other', async () => {
    const { post } = standIn();
    const result = pickFromDrive(DRIVE);
    const f = (name: string, mime: unknown) => ({ name, size: 1, url: `${DRIVE}/l/#${name}`, mime });
    post({
      type: 'kydrive.picked',
      files: [
        f('pdf', 'application/pdf'),
        { name: 'none', size: 1, url: `${DRIVE}/l/#none` },
        f('quote', 'text/plain"x'),
        f('semi', 'text/html;charset=utf-8'),
        f('colon', 'text/x:evil'),
        f('crlf', 'text/plain\r\nATTACH:https://evil.example'),
        f('upper', 'Text/Plain'),
        f('long', 'application/' + 'x'.repeat(116)),
        f('obj', { toString: () => 'text/plain' }),
      ],
    });
    const got = await result;
    expect(got[0]).toEqual({ name: 'pdf', url: `${DRIVE}/l/#pdf`, type: 'application/pdf' });
    expect(got.slice(1).map((x) => x.type)).toEqual(Array(8).fill(undefined));
  });

  it('resolves empty when the user closes the picker, or it never opened', async () => {
    vi.useFakeTimers();
    const { popup } = standIn();
    const result = pickFromDrive(DRIVE);
    (popup as { closed: boolean }).closed = true;
    vi.advanceTimersByTime(600);
    await expect(result).resolves.toEqual([]);
    vi.spyOn(window, 'open').mockReturnValue(null);
    await expect(pickFromDrive(DRIVE)).resolves.toEqual([]);
  });
});
