// iOS pans the layout viewport to reveal the caret. Fixed controls need the
// same visual-viewport offset to stay at their resting position on screen.
export function installFixedWritingHeader(root = document.documentElement, viewport = window.visualViewport) {
  if (!root.classList.contains('is-ios-pwa') || !viewport) return;
  const update = () => {
    const offset = viewport.scale === 1 ? Math.max(0, viewport.offsetTop) : 0;
    root.style.setProperty('--diary-keyboard-pan', `${offset}px`);
  };
  viewport.addEventListener('resize', update);
  viewport.addEventListener('scroll', update);
  window.addEventListener('pageshow', update);
  update();
}
