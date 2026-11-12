const storageKey = 'echodiary.typography';
export const typographyDefaults = { fontSize: 16, lineHeight: 2, fontWeight: 400, paragraphSpacing: 12 };
const limits = { fontSize: [14, 24, 1], lineHeight: [1.4, 2.6, .1], paragraphSpacing: [0, 28, 2] };
export function normalizeTypography(value) {
  const result = { ...typographyDefaults };
  for (const [key, [min, max, step]] of Object.entries(limits)) {
    const number = value?.[key];
    if (typeof number === 'number' && Number.isFinite(number)) {
      result[key] = Number((Math.round(Math.max(min, Math.min(max, number)) / step) * step).toFixed(1));
    }
  }
  if ([300, 400, 500, 600].includes(value?.fontWeight)) result.fontWeight = value.fontWeight;
  return result;
}

export function installTypographySettings({ onChange = () => {} } = {}) {
  let values = { ...typographyDefaults };
  try { values = normalizeTypography(JSON.parse(localStorage.getItem(storageKey))); } catch {}
  const apply = () => {
    const style = document.documentElement.style;
    style.setProperty('--diary-body-size', `${values.fontSize}px`);
    style.setProperty('--diary-body-leading', String(values.lineHeight));
    style.setProperty('--diary-body-weight', String(values.fontWeight));
    style.setProperty('--diary-paragraph-spacing', `${values.paragraphSpacing}px`);
    onChange();
  };
  const sync = () => {
    for (const key of Object.keys(typographyDefaults)) {
      const input = document.querySelector(`[data-typography="${key}"]`);
      input.value = String(values[key]);
      if (input.type === 'range') {
        input.style.setProperty('--range-progress', `${(values[key] - Number(input.min)) / (Number(input.max) - Number(input.min)) * 100}%`);
        const output = document.querySelector(`output[for="${input.id}"]`);
        output.value = key === 'lineHeight' ? `${values[key].toFixed(1)} 倍` : `${values[key]}px`;
      }
    }
    apply();
  };
  for (const input of document.querySelectorAll('[data-typography]')) {
    input.addEventListener(input.type === 'range' ? 'input' : 'change', () => {
      values = normalizeTypography({ ...values, [input.dataset.typography]: Number(input.value) });
      try { localStorage.setItem(storageKey, JSON.stringify(values)); } catch {}
      sync();
    });
  }
  document.querySelector('#typography-reset').addEventListener('click', () => {
    values = { ...typographyDefaults };
    try { localStorage.removeItem(storageKey); } catch {}
    sync();
  });
  sync();
}
