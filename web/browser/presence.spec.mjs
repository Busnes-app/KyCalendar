import { test, expect } from '@playwright/test';

// Colleague status (contract P) on attendee rows and Settings → Status. KyMessages and
// KyIdentity are stubbed in the page: no suite server runs here.
const me = 'walter@example.test';
const long = 'bartholomew.featherstonehaugh-montgomery.the-third@engineering.example.test';

async function signIn(browser, testInfo) {
  const { viewport, colorScheme } = testInfo.project.use;
  const phone = viewport.width <= 720;
  const context = await browser.newContext({ extraHTTPHeaders: { 'X-Forwarded-For': '203.0.113.42' }, viewport: phone ? { width: 360, height: 800 } : viewport, colorScheme });
  const page = await context.newPage();
  await page.goto('/');
  await page.getByLabel('Username', { exact: true }).fill('walter');
  await page.locator('input[type=password]').fill('WalterUpdated456!');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.locator('.fc')).toBeVisible();
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
  return { context, page, phone };
}

test('status: each colleague row says their status in words, on one line', async ({ browser }, testInfo) => {
  const { context, page, phone } = await signIn(browser, testInfo);
  const calendars = await (await page.request.get('/api/calendars')).json();
  const start = new Date(); start.setHours(12, 0, 0, 0);
  const end = new Date(start.getTime() + 3600_000);
  const until = new Date(start.getTime() + 30 * 60_000).toISOString();
  const back = new Date(start.getTime() + 4 * 86400_000).toISOString();
  const event = {
    calendar_id: calendars[0].id, uid: 'presence-rows', etag: '"1"', title: `Status ${testInfo.project.name}`,
    start: start.toISOString(), end: end.toISOString(), series_start: start.toISOString(), series_end: end.toISOString(),
    all_day: false, recurring: false, repeat: { freq: '' }, editable: true, organizer: me,
    attendees: [{ email: long, status: 'ACCEPTED' }, { email: 'ann@example.test', status: 'ACCEPTED' }, { email: 'cy@example.test', status: 'NEEDS-ACTION' }],
  };
  const asked = [];
  await page.route('/api/presence', async (r) => {
    asked.push(...JSON.parse(r.request().postData()).emails);
    await r.fulfill({ json: { people: [
      { email: long, state: 'in_a_meeting', until },
      { email: 'ann@example.test', state: 'out_of_office', until: back },
      { email: 'cy@example.test', state: 'unknown' },
    ] } });
  });
  await page.route(/\/api\/events\?/, (r) => r.fulfill({ json: [event] }));
  await page.reload();
  await page.locator('.fc-listWeek-button').click();
  await page.locator('.fc-list-event', { hasText: event.title }).click();
  const dialog = page.getByRole('dialog');
  const row = dialog.locator('.kc-people li', { hasText: long });
  await expect(row.locator('.kc-status-text')).toContainText('In a meeting until');
  await expect(dialog.locator('.kc-people li', { hasText: 'ann@example.test' }).locator('.kc-status-text')).toContainText('Out of office until');
  await expect(dialog.locator('.kc-people li', { hasText: 'cy@example.test' }).locator('.kc-status-text')).toHaveText('Status unknown');
  expect(asked).not.toContain(me);
  // The dot is decoration: the words are what a screen reader gets.
  await expect(row.locator('.kc-status-dot')).toHaveAttribute('aria-hidden', 'true');
  if (process.env.KY_SHOTS) await dialog.screenshot({ path: `${process.env.KY_SHOTS}/after-attendees-${testInfo.project.name}.png` });
  // Desktop: one line. Phones: the status on its own line under the person. Nothing spills.
  const li = await row.boundingBox();
  const status = await row.locator('.kc-status').boundingBox();
  const chat = await row.getByRole('button', { name: `Chat with ${long}`, exact: true }).boundingBox();
  for (const part of await row.locator(':scope > *').all()) {
    const b = await part.boundingBox();
    if (!b || b.width === 0) continue;
    expect(b.y).toBeGreaterThanOrEqual(li.y - 1);
    expect(b.y + b.height).toBeLessThanOrEqual(li.y + li.height + 1);
    expect(b.x + b.width).toBeLessThanOrEqual(li.x + li.width + 1);
  }
  if (phone) expect(status.y).toBeGreaterThanOrEqual(chat.y + chat.height - 1);
  else expect(Math.abs((status.y + status.height / 2) - (chat.y + chat.height / 2))).toBeLessThan(2);
  await expect(row.locator('.kc-status-text')).toBeVisible();
  expect(await row.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  await context.close();
});

test('status: Settings → Status turns sharing off and sets a manual status', async ({ browser }, testInfo) => {
  const { context, page } = await signIn(browser, testInfo);
  let state = { share: true, manual: null };
  const sent = [];
  await page.route('/api/presence/me', async (r) => {
    if (r.request().method() === 'PUT') {
      const b = JSON.parse(r.request().postData());
      sent.push(b);
      if ('share' in b) state = { ...state, share: b.share, manual: b.share ? state.manual : null };
      if ('manual' in b) state = { ...state, manual: b.manual };
    }
    await r.fulfill({ json: state });
  });
  await page.goto('/#/settings/status');
  const toggle = page.getByRole('switch', { name: 'Show my status to colleagues' });
  await expect(toggle).toBeChecked();
  if (process.env.KY_SHOTS) await page.screenshot({ path: `${process.env.KY_SHOTS}/after-settings-${testInfo.project.name}.png`, fullPage: true });
  await page.locator('#kc-status-state').selectOption('away');
  await page.locator('#kc-status-for').selectOption('4');
  await page.getByRole('button', { name: 'Set status' }).click();
  await expect(page.getByText(/^Away until /)).toBeVisible();
  const until = Date.parse(sent[0].manual.until);
  expect(sent[0].manual.state).toBe('away');
  expect(Math.abs(until - Date.now() - 4 * 3600_000)).toBeLessThan(120_000);
  await page.getByRole('button', { name: 'Go back to automatic' }).click();
  await expect(page.getByRole('button', { name: 'Set status' })).toBeVisible();
  expect(sent[1]).toEqual({ manual: null });
  await toggle.click();
  await expect(toggle).not.toBeChecked();
  expect(sent[2]).toEqual({ share: false });
  await expect(page.getByRole('button', { name: 'Set status' })).toHaveCount(0);
  await context.close();
});
