import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Check, Copy } from 'lucide-react';

/** The suite page head (ky-ui shell): title, a one-sentence lede and the page's primary action (`ky-page-action`). */
export function PageHeader({ title, lede, action }: { title: string; lede?: ReactNode; action?: ReactNode }) {
  return (
    <header className="ky-page-head">
      <div>
        <h1 className="ky-page-title">{title}</h1>
        {lede && <p className="ky-page-lede">{lede}</p>}
      </div>
      {action}
    </header>
  );
}

/** What is missing and what to do next. */
export function EmptyState({ title, children, compact }: { title: string; children?: ReactNode; compact?: boolean }) {
  return (
    <div className={compact ? 'empty-state compact' : 'empty-state'}>
      <strong>{title}</strong>
      {children && <span>{children}</span>}
    </div>
  );
}

/**
 * A modal native dialog: title, body, then actions. Escape calls onClose.
 * The dialog's accessible name is its title.
 */
export function Modal({ title, description, onClose, children, wide }: { title: string; description?: ReactNode; onClose: () => void; children: ReactNode; wide?: boolean }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current!;
    d.showModal();
    return () => d.close();
  }, []);
  return (
    <dialog
      ref={ref}
      className={wide ? 'modal modal-wide' : 'modal'}
      aria-label={title}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
    >
      <h2 className="modal-title">{title}</h2>
      {description && <p className="modal-desc">{description}</p>}
      {children}
    </dialog>
  );
}

/** Copies text; says "Copied" for a moment. A failed copy leaves the text to select by hand. */
export function CopyButton({ text, label = 'Copy', className = 'btn-secondary btn-sm' }: { text: string; label?: string; className?: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 2000);
    return () => clearTimeout(t);
  }, [copied]);
  return (
    <button
      type="button"
      className={className}
      aria-label={copied ? 'Copied' : label}
      onClick={() => void navigator.clipboard?.writeText(text).then(() => setCopied(true), () => setCopied(false))}
    >
      {copied ? <Check size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}
      <span>{copied ? 'Copied' : 'Copy'}</span>
    </button>
  );
}

/** A preformatted block that scrolls sideways inside its own box, never the page. */
export function CodeBlock({ code, label }: { code: string; label: string }) {
  return (
    <div className="code-block">
      <pre tabIndex={0} aria-label={label}>
        <code>{code}</code>
      </pre>
      <CopyButton text={code} label={`Copy ${label}`} className="btn-secondary btn-sm code-copy" />
    </div>
  );
}
