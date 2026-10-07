import { request, expect } from '@playwright/test';

// Sign in with a bootstrap password and replace it; each user gets its own cookie jar.
async function replacePassword(baseURL, username, current, next) {
  const api = await request.newContext({ baseURL });
  try {
    await api.get('/');
    const login = await api.post('/api/auth/login', { data: { username, password: current } });
    expect(login.ok(), `${username}: ${login.status()} ${await login.text()}`).toBe(true);
    const state = await login.json();
    const cookies = (await api.storageState()).cookies;
    const csrf = cookies.find(cookie => cookie.name === 'ky_csrf')?.value;
    expect(csrf).toBeTruthy();
    expect(state.must_change_password ?? state.user?.must_change_password).toBe(true);
    const changed = await api.post('/api/auth/change-password', {
      headers: { 'X-CSRF-Token': csrf },
      data: { current_password: current, new_password: next },
    });
    expect(changed.ok(), JSON.stringify(state)).toBe(true);
    expect(changed.headers()['content-type']).toContain('application/json');
  } finally { await api.dispose(); }
}

export default async function setup(config) {
  const baseURL = config.projects[0].use.baseURL;
  await replacePassword(baseURL, 'admin', 'BrowserInitial123!', 'BrowserUpdated456!');
  await replacePassword(baseURL, 'walter', 'WalterInitial123!', 'WalterUpdated456!');
}
