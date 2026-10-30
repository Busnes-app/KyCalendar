import { useEffect, useRef, useState } from 'react';
import mitLicenseText from '../mit.txt?raw';

/** The suite's licence line and About window; `version` comes from `/api/settings`, which has it only for a session. */
export function LicenseFooter({ version }: { version?: string }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDialogElement>(null);
  const year = new Date().getFullYear();

  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    if (open && !dialog.open) dialog.showModal();
    else if (!open && dialog.open) dialog.close();
  }, [open]);

  return (
    <>
      <div className="sidebar-footer">
        <p>
          <button type="button" className="license-link" onClick={() => setOpen(true)}>
            &copy; {year} &middot; MIT License
          </button>
        </p>
      </div>
      <dialog
        ref={ref}
        className="compose-backdrop"
        aria-label="About KyCalendar"
        onCancel={() => setOpen(false)}
        onClick={(event) => {
          if (event.target === ref.current) setOpen(false);
        }}
      >
        <div className="license-window">
          <div className="license-window-header">
            <div className="license-window-title">
              <div className="license-title-main">
                <span className="license-app-name">KyCalendar</span>
                {version ? <span className="license-version-badge">v{version}</span> : null}
              </div>
              <p className="license-title-sub">Developed by Busnes.app</p>
              <p className="license-title-sub">&copy; {year} &middot; Licensed under the MIT License</p>
            </div>
            <button type="button" className="btn-ghost" onClick={() => setOpen(false)}>
              Close
            </button>
          </div>
          <textarea className="license-text" readOnly aria-label="MIT License" value={mitLicenseText} />
        </div>
      </dialog>
    </>
  );
}
