/** Measure from the viewport edges to the furthest control, plus the fade tail. */
export function edgeFadeHeights(canvas, upper = [], lower = [], extension = 50) {
  return {
    top: Math.max(0, ...upper.map(rect => rect.bottom - canvas.top)) + extension,
    bottom: Math.max(0, ...lower.map(rect => canvas.bottom - rect.top)) + extension,
  };
}

/** Reusable viewport fades. The host supplies controls; no page selectors here. */
export function createEdgeFades({ container = document.body, controls = [], extension = 50, className = '' } = {}) {
  const layer = container.ownerDocument.createElement('div');
  layer.className = ['edge-fades', className].filter(Boolean).join(' ');
  layer.setAttribute('aria-hidden', 'true');
  container.append(layer);
  const viewport = window.visualViewport;
  let frame;
  let destroyed = false;
  const tracked = new Set();
  const ancestors = new Set();
  const resolveControls = () => [...(typeof controls === 'function' ? controls() : controls)];
  const schedule = () => {
    if (destroyed) return;
    cancelAnimationFrame(frame);
    frame = requestAnimationFrame(update);
  };
  const resize = new ResizeObserver(schedule);
  const mutations = new MutationObserver(schedule);
  const track = elements => {
    for (const element of elements) {
      if (tracked.has(element)) continue;
      tracked.add(element);
      resize.observe(element);
      mutations.observe(element, { attributes: true, childList: true, subtree: true,
        attributeFilter: ['hidden', 'inert', 'aria-hidden', 'class', 'style', 'data-edge-fade-control'] });
      for (let parent = element.parentElement; parent; parent = parent.parentElement) {
        if (ancestors.has(parent) || tracked.has(parent)) continue;
        ancestors.add(parent);
        mutations.observe(parent, { attributes: true, attributeFilter: ['hidden', 'inert', 'aria-hidden', 'class'] });
      }
    }
  };
  function update() {
    if (destroyed) return;
    const elements = resolveControls();
    track(elements);
    const canvas = layer.getBoundingClientRect();
    if (!canvas.height) return;
    const upper = [], lower = [];
    for (const element of elements) {
      if (element.closest('[hidden], [inert], [aria-hidden="true"]') || !element.getClientRects().length) continue;
      const style = getComputedStyle(element);
      if (style.visibility === 'hidden' || style.visibility === 'collapse') continue;
      const rect = element.getBoundingClientRect();
      if (!rect.width || !rect.height) continue;
      const edge = element.dataset.edgeFadeControl;
      const top = edge === 'top' || (edge !== 'bottom' && (rect.top + rect.bottom) / 2 < (canvas.top + canvas.bottom) / 2);
      (top ? upper : lower).push(rect);
    }
    const heights = edgeFadeHeights(canvas, upper, lower, extension);
    for (const edge of ['top', 'bottom']) {
      const value = `${heights[edge]}px`;
      if (layer.style.getPropertyValue(`--edge-fade-${edge}`) !== value) layer.style.setProperty(`--edge-fade-${edge}`, value);
    }
  }
  resize.observe(layer);
  window.addEventListener('resize', schedule);
  viewport?.addEventListener('resize', schedule);
  viewport?.addEventListener('scroll', schedule);
  update();
  return { element: layer, update, destroy() {
    destroyed = true;
    cancelAnimationFrame(frame); resize.disconnect(); mutations.disconnect();
    window.removeEventListener('resize', schedule);
    viewport?.removeEventListener('resize', schedule);
    viewport?.removeEventListener('scroll', schedule);
    layer.remove();
  } };
}
