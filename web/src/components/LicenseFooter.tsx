import { useRef, type ReactNode } from 'react';
import mitLicenseText from '../mit.txt?raw';

/** The suite's licence line and About dialog (ky-ui shell); `version` comes from `/api/settings`, which has it only for a session. `children` render above the line (the phone app rail). */
export function LicenseFooter({ version, className, children }: { version?: string; className?: string; children?: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null);
  const year = new Date().getFullYear();
  return (
    <footer className={className}>
      {children}
      <button type="button" className="ky-license-link" onClick={() => ref.current?.showModal()}>
        &copy; {year} &middot; MIT License
      </button>
      <dialog
        ref={ref}
        className="ky-about"
        aria-label="About KyCalendar"
        onClick={(event) => {
          if (event.target === ref.current) ref.current.close();
        }}
      >
        <div className="ky-about-head">
          <div>
            <h2 className="ky-about-name">KyCalendar {version ? <span className="ky-about-version">v{version}</span> : null}</h2>
            <p className="ky-about-meta">Developed by Busnes.app</p>
            <p className="ky-about-meta">&copy; {year} &middot; Licensed under the MIT License</p>
          </div>
          <button type="button" className="ky-about-close" onClick={() => ref.current?.close()}>
            Close
          </button>
        </div>
        <textarea className="ky-about-license" readOnly aria-label="MIT License" value={mitLicenseText} />
      </dialog>
    </footer>
  );
}
