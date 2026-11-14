import './theme';
import React from 'react';
import ReactDOM from 'react-dom/client';
import { App } from './App';
import { watchForUpdate } from './ky-ui/update.js';
import { NEW_LINK_KEY } from './eventForm';

// Keep registration in the bundled script so script-src 'self' permits it.
if (import.meta.env.PROD && 'serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    void navigator.serviceWorker.register('/sw.js').catch(() => {});
  }, { once: true });
}

watchForUpdate({ name: 'KyCalendar' });

// /new is the "New meeting" deep link: keep its query for the calendar to open once signed in
// (sign-in, SSO included, returns to /), and leave the path. Nothing is stored server-side.
if (location.pathname === '/new') {
  sessionStorage.setItem(NEW_LINK_KEY, location.search);
  history.replaceState(null, '', '/#/calendar');
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
