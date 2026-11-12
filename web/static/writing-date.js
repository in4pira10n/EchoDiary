// Move the same date control, preserving its action and the navigation buttons.
export function installWritingDateLayout({ header, date, title, content, back, share, isPreview, isEnabled }) {
  let frame;
  const updateScroll = () => {
    header.classList.toggle('is-created-hidden', date.parentElement === header && !date.hidden && content.scrollTop >= 1);
  };
  const update = () => {
    date.hidden = isPreview() || !isEnabled();
    if (header.hidden) return;
    const bounds = header.getBoundingClientRect();
    if (!bounds.width) return;
    const probe = date.cloneNode(true);
    probe.removeAttribute('id'); probe.removeAttribute('hidden');
    probe.tabIndex = -1; probe.setAttribute('aria-hidden', 'true');
    Object.assign(probe.style, {
      position: 'absolute', visibility: 'hidden', display: 'block', width: 'max-content',
      maxWidth: 'none', fontSize: getComputedStyle(date).fontSize, padding: '0', border: '0', transform: 'none',
      transition: 'none', pointerEvents: 'none',
    });
    header.append(probe);
    const width = probe.getBoundingClientRect().width;
    probe.remove();
    // Keep the date centered in the header, with a gap on both sides.
    const center = bounds.left + bounds.width / 2;
    const available = Math.max(0, 2 * Math.min(center - back.getBoundingClientRect().right - 16, share.getBoundingClientRect().left - center - 16));
    const inline = width > available;
    if (inline && date.previousElementSibling !== title) title.after(date);
    else if (!inline && date.parentElement !== header) header.insertBefore(date, share);
    updateScroll();
  };
  const schedule = () => { cancelAnimationFrame(frame); frame = requestAnimationFrame(update); };
  const observer = new ResizeObserver(schedule);
  for (const element of [header, back, share]) observer.observe(element);
  window.addEventListener('resize', schedule);
  document.fonts?.ready.then(schedule);
  content.addEventListener('scroll', updateScroll, { passive: true });
  update();
  return { update, destroy() {
    cancelAnimationFrame(frame); observer.disconnect();
    window.removeEventListener('resize', schedule);
    content.removeEventListener('scroll', updateScroll);
  } };
}
