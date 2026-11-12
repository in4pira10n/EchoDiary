import { resolveEchoIcon } from '/kit/components/icon.js';

let current = null;
let lastTouchAt = 0;

// Use the visible viewport so menus also fit above an on-screen keyboard.
export function fitContextMenu(menu, { x, y, anchor, gap = 16 }) {
  const viewport = window.visualViewport;
  const position = () => {
    const margin = 12;
    const left = (viewport?.offsetLeft || 0) + margin;
    const top = (viewport?.offsetTop || 0) + margin;
    const width = Math.max(0, (viewport?.width || window.innerWidth) - margin * 2);
    const height = Math.max(0, (viewport?.height || window.innerHeight) - margin * 2);
    menu.style.minWidth = `${Math.min(210, width)}px`;
    menu.style.maxWidth = `${width}px`;
    menu.style.maxHeight = `${height}px`;
    const rect = anchor?.getBoundingClientRect();
    const above = rect ? rect.top - menu.offsetHeight - gap : 0;
    const desiredX = rect ? rect.left : x;
    const desiredY = rect ? (above >= top ? above : rect.bottom + gap) : y;
    menu.style.left = `${Math.max(left, Math.min(desiredX, left + width - menu.offsetWidth))}px`;
    menu.style.top = `${Math.max(top, Math.min(desiredY, top + height - menu.offsetHeight))}px`;
  };
  position();
  viewport?.addEventListener('resize', position);
  viewport?.addEventListener('scroll', position);
  return () => {
    viewport?.removeEventListener('resize', position);
    viewport?.removeEventListener('scroll', position);
  };
}

// Share the same menu actions between desktop right-click and touch long-press.
export function installContextMenu(element, open) {
  element.classList.add('diary-context-target');
  let suppressClick = false;
  element.addEventListener('contextmenu', open);
  element.addEventListener('click', event => {
    if (!suppressClick) return;
    suppressClick = false;
    event.preventDefault();
    event.stopImmediatePropagation();
  }, true);
  element.addEventListener('pointerdown', event => {
    suppressClick = false;
    if (event.pointerType !== 'touch') return;
    lastTouchAt = Date.now();
    if (!event.isPrimary) return;
    const { pointerId, clientX, clientY } = event;
    let timer;
    const cancel = () => {
      clearTimeout(timer);
      document.removeEventListener('pointermove', move, true);
      document.removeEventListener('pointerup', end, true);
      document.removeEventListener('pointercancel', end, true);
      document.removeEventListener('pointerdown', cancel, true);
      document.removeEventListener('scroll', cancel, true);
      window.removeEventListener('blur', cancel);
    };
    const move = next => {
      if (next.pointerId === pointerId && Math.hypot(next.clientX - clientX, next.clientY - clientY) > 8) cancel();
    };
    const end = next => { if (next.pointerId === pointerId) cancel(); };
    timer = setTimeout(() => {
      cancel();
      if (!element.isConnected) return;
      suppressClick = true;
      open({ currentTarget: element, clientX, clientY, preventDefault() {}, stopPropagation() {} });
    }, 500);
    document.addEventListener('pointermove', move, true);
    document.addEventListener('pointerup', end, true);
    document.addEventListener('pointercancel', end, true);
    document.addEventListener('pointerdown', cancel, true);
    document.addEventListener('scroll', cancel, true);
    window.addEventListener('blur', cancel);
  });
}

export function closeContextMenu(restoreFocus = false) {
  if (!current) return;
  const { menu, trigger, releaseLayout } = current;
  current = null;
  releaseLayout();
  menu.remove();
  if (restoreFocus && trigger?.isConnected) trigger.focus({ preventScroll: true });
}

export function showContextMenu(event, label, items) {
  event.preventDefault();
  event.stopPropagation();
  closeContextMenu();
  const trigger = event.currentTarget;
  const menu = document.createElement('div');
  menu.className = 'context-menu diary-insert-menu diary-context-menu';
  menu.setAttribute('role', 'menu');
  menu.setAttribute('aria-label', label);
  menu.tabIndex = -1;
  for (const item of items) {
    if (item === '-') {
      const divider = document.createElement('div');
      divider.className = 'context-menu-separator';
      divider.setAttribute('role', 'separator');
      menu.append(divider);
      continue;
    }
    const button = document.createElement('button');
    button.type = 'button';
    button.className = `context-menu-item${item.danger ? ' danger' : ''}`;
    button.setAttribute('role', 'menuitem');
    button.tabIndex = -1;
    button.disabled = Boolean(item.disabled);
    const icon = document.createElement('span');
    icon.className = 'context-menu-icon';
    icon.setAttribute('aria-hidden', 'true');
    // A mask follows the text color, including destructive actions and themes.
    const image = document.createElement('span');
    image.className = 'diary-context-symbol';
    image.style.setProperty('--context-symbol', `url("${resolveEchoIcon(item.icon)}")`);
    icon.append(image);
    const text = document.createElement('span');
    text.textContent = item.label;
    button.append(icon, text);
    button.addEventListener('click', () => {
      closeContextMenu(true);
      Promise.resolve().then(item.action).catch(error => {
        document.dispatchEvent(new CustomEvent('diary-action-error', { detail: error }));
      });
    });
    menu.append(button);
  }
  document.body.append(menu);
  const rect = trigger.getBoundingClientRect();
  const keyboard = event.clientX === 0 && event.clientY === 0;
  const x = keyboard ? rect.left : event.clientX;
  const y = keyboard ? rect.bottom : event.clientY;
  const releaseLayout = fitContextMenu(menu, { x, y });
  current = { menu, trigger, releaseLayout };
  const buttons = [...menu.querySelectorAll('button:not(:disabled)')];
  (buttons[0] || menu).focus({ preventScroll: true });
  menu.addEventListener('keydown', key => {
    if (key.key === 'Escape' || key.key === 'Tab') {
      if (key.key === 'Escape') key.preventDefault();
      closeContextMenu(true);
    } else if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(key.key)) {
      key.preventDefault();
      if (!buttons.length) return;
      const index = buttons.indexOf(document.activeElement);
      const next = key.key === 'Home' ? 0 : key.key === 'End' ? buttons.length - 1
        : (index + (key.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length;
      buttons[next].focus();
    }
  });
}

document.addEventListener('pointerdown', event => {
  if (current && !current.menu.contains(event.target)) closeContextMenu();
});
document.addEventListener('contextmenu', event => {
  // iOS/Android may also emit a native contextmenu during the same hold.
  if (event.target.closest('.diary-context-target') &&
      (event.pointerType === 'touch' || (event.pointerType !== 'mouse' && Date.now() - lastTouchAt < 1500))) {
    event.preventDefault();
    event.stopImmediatePropagation();
    return;
  }
  if (current && !current.menu.contains(event.target)) closeContextMenu();
}, true);
document.addEventListener('scroll', event => {
  if (current && !current.menu.contains(event.target)) closeContextMenu();
}, true);
window.addEventListener('resize', () => closeContextMenu());
window.addEventListener('blur', () => closeContextMenu());
