import React, { useEffect, useId, useRef } from 'react';
import { Ellipsis, type LucideIcon } from 'lucide-react';

// A ⋯ button and a native popover under it (the suite's row menu, as in KyYard and KyMessages).
// Escape and an outside click close it; a scroll that moves the trigger closes it so it never
// detaches. Arrow keys move between items.
export function MoreMenu({ label, icon: Icon = Ellipsis, triggerRef, children }: {
  label: string;
  icon?: LucideIcon;
  /** Lets the owner return focus to the trigger, for example when a dialog the menu opened closes. */
  triggerRef?: React.RefObject<HTMLButtonElement | null>;
  children: (close: () => void) => React.ReactNode;
}) {
  const menu = useRef<HTMLDivElement>(null);
  const ownTrigger = useRef<HTMLButtonElement>(null);
  const trigger = triggerRef ?? ownTrigger;
  const id = `more-menu-${useId().replace(/:/g, '')}`;
  const close = () => menu.current?.hidePopover?.();
  useEffect(() => {
    const el = menu.current;
    if (!el) return;
    let at: DOMRect | null = null;
    // A scroll event can land after opening for a scroll that happened before it (focusing the
    // trigger scrolls it into view): close only when the trigger has actually moved.
    const onScroll = () => {
      const now = trigger.current?.getBoundingClientRect();
      if (!at || !now || now.top !== at.top || now.left !== at.left) close();
    };
    const toggled = (event: Event) => {
      const open = (event as ToggleEvent).newState === 'open';
      trigger.current?.setAttribute('aria-expanded', String(open));
      if (!open) { window.removeEventListener('scroll', onScroll, true); return; }
      at = trigger.current!.getBoundingClientRect();
      const below = at.bottom + 4 + el.offsetHeight <= window.innerHeight;
      el.style.top = `${below ? at.bottom + 4 : Math.max(8, at.top - 4 - el.offsetHeight)}px`;
      el.style.left = `${Math.max(8, Math.min(at.right - el.offsetWidth, window.innerWidth - 8 - el.offsetWidth))}px`;
      el.querySelector<HTMLElement>('.ky-menu-item:not(:disabled)')?.focus();
      window.addEventListener('scroll', onScroll, true);
    };
    el.addEventListener('toggle', toggled);
    return () => { el.removeEventListener('toggle', toggled); window.removeEventListener('scroll', onScroll, true); };
  }, [trigger]);
  return <>
    <button ref={trigger} type="button" className="ky-menu-trigger" popoverTarget={id} aria-haspopup="menu" aria-expanded="false" aria-label={label} title={label}>
      <Icon size={18} aria-hidden="true" />
    </button>
    <div ref={menu} id={id} popover="auto" className="ky-menu" role="menu" aria-label={label} onKeyDown={(event) => {
      if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
      event.preventDefault();
      const items = [...event.currentTarget.querySelectorAll<HTMLElement>('.ky-menu-item:not(:disabled)')];
      const at = items.indexOf(document.activeElement as HTMLElement);
      items[(at + (event.key === 'ArrowDown' ? 1 : items.length - 1)) % items.length]?.focus();
    }}>{children(close)}</div>
  </>;
}
