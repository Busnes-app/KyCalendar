import { useEffect, useRef } from 'react';
import { Settings as SettingsIcon } from 'lucide-react';
import { initMenuButton } from '../ky-ui/menu';
import { settingsSections } from '../pages/PersonalSettings';

/**
 * The Settings gear: ky-ui's menu button, listing this user's Settings sections in tab order.
 * `section` is the open section's id while Settings is open; the gear and that item are current.
 * `id` names the menu, so the footer and the phone header each need their own.
 */
export function SettingsMenu({ id, user, section }: { id: string; user: any; section?: string }) {
  const gear = useRef<HTMLButtonElement>(null);
  // ky-ui's script owns aria-expanded, hidden and the keys; it reads the items live, so React may re-render them.
  useEffect(() => initMenuButton(gear.current!), []);
  return (
    <div className="ky-menu">
      <button
        ref={gear}
        type="button"
        className="ky-icon-button"
        aria-label="Settings"
        title="Settings"
        aria-haspopup="menu"
        aria-expanded="false"
        aria-controls={id}
        aria-current={section ? 'page' : undefined}
      >
        <SettingsIcon aria-hidden="true" />
      </button>
      <div className="ky-menu-list" id={id} role="menu" aria-label="Settings" hidden>
        {settingsSections(user).map((s) => (
          <a key={s.id} role="menuitem" tabIndex={-1} href={`#/settings/${s.id}`} aria-current={s.id === section ? 'page' : undefined}>
            {s.label}
          </a>
        ))}
      </div>
    </div>
  );
}
