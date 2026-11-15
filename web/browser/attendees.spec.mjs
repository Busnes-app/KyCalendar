import { test, expect } from '@playwright/test';

// The event editor's attendee rows (Chat, contract H, and Remove) stay on one line at phone width.
// The suite side is stubbed in the page: no KyIdentity or KyMessages runs here.
const me = 'walter@example.test';
const long = 'bartholomew.featherstonehaugh-montgomery.the-third@engineering.example.test';

test('attendees: a long address ellipsizes, Chat and Remove stay on its line', async ({ browser }, testInfo) => {
  const { viewport, colorScheme } = testInfo.project.use;
  const phone = viewport.width <= 720;
  const context = await browser.newContext({ extraHTTPHeaders: { 'X-Forwarded-For': '203.0.113.41' }, viewport: phone ? { width: 360, height: 800 } : viewport, colorScheme });
  const page = await context.newPage();
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.locator('.fc')).toBeVisible();
  const calendars = await (await page.request.get('/api/calendars')).json();

  const patch = (url, edit) => page.route(url, async (route) => {
    const res = await route.fetch();
    const body = await res.json();
    edit(body);
    await route.fulfill({ response: res, json: body });
  });
  await patch('/api/auth/me', (b) => { b.user.email = me; });
  await patch('/api/settings', (b) => { b.scheduling = true; b.meetings = true; });
  await page.route('/api/suite/apps', (r) => r.fulfill({ json: { apps: [{ id: 'kymessage-chat', name: 'Chat', url: 'https://chat.example.test/' }] } }));
  await page.route(/\/api\/suite\/people\?/, (r) => r.fulfill({ json: { people: new URL(r.request().url()).searchParams.getAll('email').map((email) => ({ email, name: '' })) } }));
  await page.route(/\/api\/freebusy\?/, (r) => r.fulfill({ json: { people: [] } }));
  const start = new Date(); start.setHours(12, 0, 0, 0);
  const end = new Date(start.getTime() + 3600_000);
  const event = {
    calendar_id: calendars[0].id, uid: 'attendees-layout', etag: '"1"', title: `Attendees ${testInfo.project.name}`,
    start: start.toISOString(), end: end.toISOString(), series_start: start.toISOString(), series_end: end.toISOString(),
    all_day: false, recurring: false, repeat: { freq: '' }, editable: true, organizer: me,
    attendees: [{ email: long, status: 'NEEDS-ACTION' }, { email: 'ann@example.test', status: 'ACCEPTED' }],
  };
  await page.route(/\/api\/events\?/, (r) => r.fulfill({ json: [event] }));
  await page.reload();

  await page.locator('.fc-listWeek-button').click();
  await page.locator('.fc-list-event', { hasText: event.title }).click();
  const dialog = page.getByRole('dialog');
  const row = dialog.locator('.kc-people li', { hasText: long });
  const chat = row.getByRole('button', { name: `Chat with ${long}`, exact: true });
  const remove = row.getByRole('button', { name: `Remove ${long}`, exact: true });
  await expect(chat).toBeVisible();
  await expect(remove).toBeVisible();
  // Before/after captures for review; off in CI.
  if (process.env.KY_SHOTS) await dialog.screenshot({ path: `${process.env.KY_SHOTS}/attendees-${testInfo.project.name}.png` });

  // The full address stays in the text (read by screen readers) and the title; on screen it is cut.
  const name = row.locator('span').first();
  await expect(name).toHaveText(long);
  await expect(name).toHaveAttribute('title', long);
  const box = async (l) => (await l.boundingBox());
  const [n, c, r, li] = await Promise.all([name, chat, remove, row].map(box));
  // One line: every part shares the row's vertical band, and nothing spills out of the row.
  for (const part of [n, c, r]) {
    expect(part.y).toBeGreaterThanOrEqual(li.y - 1);
    expect(part.y + part.height).toBeLessThanOrEqual(li.y + li.height + 1);
  }
  expect(Math.abs(c.y - r.y)).toBeLessThan(1);
  expect(li.height).toBeLessThan(2 * c.height);
  // Nothing spills sideways: not out of the row, nor the row and its buttons out of the dialog.
  expect(await row.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  const d = await box(dialog);
  expect(r.x + r.width).toBeLessThanOrEqual(d.x + d.width);
  expect(li.x + li.width).toBeLessThanOrEqual(d.x + d.width);
  if (phone) {
    expect(await name.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true);
    // Touch targets: 44px on phones.
    expect(c.height).toBeGreaterThanOrEqual(44);
    expect(r.height).toBeGreaterThanOrEqual(44);
  }
  await context.close();
});
