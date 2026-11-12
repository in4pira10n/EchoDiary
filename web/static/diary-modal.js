import { createEchoModal as createEchoKitModal } from '/kit/components/modal/index.js';

const tones = [
  { tone: 'warning', icons: ['exclamationmark', 'triangle', 'warning', 'bell'] },
  { tone: 'danger', icons: ['trash', 'octagon', 'hand.raised'] },
  { tone: 'success', icons: ['checkmark', 'check.circle', 'checkmark.circle'] },
  { tone: 'info', icons: ['info', 'questionmark', 'icloud', 'key'] },
];

function hasModalBodyContent(body) {
  if (body.textContent.trim()) return true;
  return Boolean(body.querySelector('input, button, textarea, select, img, svg, canvas, video, audio, iframe, [contenteditable="true"]'));
}

export function createEchoModal(options = {}) {
  const modal = createEchoKitModal(options);
  const body = modal.querySelector(':scope > .echo-modal > .echo-modal__body');
  if (body && !hasModalBodyContent(body)) body.remove();

  const mark = modal.querySelector(':scope > .echo-modal .echo-modal__mark');
  if (mark) {
    const icon = String(options.icon || '').toLowerCase();
    const tone = options.tone || tones.find(entry => entry.icons.some(fragment => icon.includes(fragment)))?.tone || 'neutral';
    mark.dataset.tone = tone;
  }
  return modal;
}
