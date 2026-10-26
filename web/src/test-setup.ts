// jsdom has no modal <dialog>; emulate showModal/close.
HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
// jsdom has no layout: nothing to scroll or observe.
Element.prototype.scrollIntoView = function () {};
globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
