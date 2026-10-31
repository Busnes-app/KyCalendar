// jsdom has no modal <dialog>; emulate showModal/close.
HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
// jsdom has no layout: nothing to scroll or observe.
Element.prototype.scrollIntoView = function () {};
globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
// jsdom hides popovers but has no popover API and does not invoke `popovertarget`: emulate both.
const popover = (el: HTMLElement, open: boolean) => {
  el.style.display = open ? 'grid' : 'none';
  el.dispatchEvent(Object.assign(new Event('toggle'), { newState: open ? 'open' : 'closed' }));
};
HTMLElement.prototype.showPopover = function () { popover(this, true); };
HTMLElement.prototype.hidePopover = function () { popover(this, false); };
HTMLElement.prototype.togglePopover = function () { popover(this, this.style.display !== 'grid'); return this.style.display === 'grid'; };
document.addEventListener('click', (event) => {
  const trigger = (event.target as Element | null)?.closest?.('[popovertarget]');
  if (trigger) document.getElementById(trigger.getAttribute('popovertarget')!)?.togglePopover();
});
