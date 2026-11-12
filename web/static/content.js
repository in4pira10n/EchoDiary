import { marked } from './vendor/marked.js';
import DOMPurify from './vendor/dompurify.js';
import TurndownService from './vendor/turndown.js';
import { normalizeListHeadings, normalizeParagraphs } from './document-structure.js?v=20261111-editor-fixes';

const tags = ['p', 'div', 'br', 'strong', 'b', 'em', 'i', 'u', 's', 'strike', 'del', 'blockquote', 'pre', 'code', 'ul', 'ol', 'li', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'hr', 'a', 'img', 'table', 'thead', 'tbody', 'tr', 'th', 'td', 'input'];
const attrs = { a: ['href', 'title'], img: ['src', 'alt', 'title'], ol: ['start'], input: ['type', 'checked', 'disabled'] };
const alignedTags = new Set(['p', 'div', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'blockquote', 'li']);
const alignments = new Set(['left', 'center', 'right', 'justify']);
export const managedImageURL = value => /^\/media\/img-[a-f0-9]{32}\.(jpg|png|gif|webp|avif|heic|bmp)$/.test(value || '');

function safeURL(value, image = false) {
  if (managedImageURL(value)) return true;
  if (!image && /^#[^\s]*$/.test(value)) return true;
  try {
    const url = new URL(value);
    return ['https:', 'http:', ...(image ? [] : ['mailto:'])].includes(url.protocol);
  } catch { return false; }
}

// Failed conversions must never be coerced into empty or literal undefined text.
export function requireBodyString(value) {
  if (typeof value !== 'string') throw new TypeError('正文转换未返回有效文本');
  return value;
}

export function sanitizeHTML(value) {
  // Convert native browser style spans to semantic tags before removing styles.
  const fragment = DOMPurify.sanitize(requireBodyString(value), {
    ALLOWED_TAGS: [...tags, 'span'], ALLOWED_ATTR: ['href', 'title', 'src', 'alt', 'start', 'type', 'checked', 'disabled', 'style', 'align'],
    ALLOW_DATA_ATTR: false, ALLOW_ARIA_ATTR: false, RETURN_DOM_FRAGMENT: true,
  });
  for (const node of fragment.querySelectorAll('[style]')) {
    const style = node.style;
    if (alignedTags.has(node.localName) && alignments.has(style.textAlign)) node.setAttribute('align', style.textAlign);
    const formats = [];
    if (style.fontWeight === 'bold' || Number(style.fontWeight) >= 600) formats.push('strong');
    if (style.fontStyle === 'italic') formats.push('em');
    const decoration = `${style.textDecoration} ${style.textDecorationLine}`;
    if (decoration.includes('underline')) formats.push('u');
    if (decoration.includes('line-through')) formats.push('del');
    for (const tag of formats) {
      const wrapper = document.createElement(tag);
      wrapper.append(...node.childNodes);
      node.append(wrapper);
    }
  }
  for (const node of fragment.querySelectorAll('*')) {
    for (const attr of [...node.attributes]) {
      const alignment = attr.name === 'align' && alignedTags.has(node.localName) && alignments.has(attr.value);
      if (!alignment && !(attrs[node.localName] || []).includes(attr.name)) node.removeAttribute(attr.name);
    }
    if (node.localName === 'a') {
      if (!safeURL(node.getAttribute('href'))) node.removeAttribute('href');
      node.setAttribute('rel', 'nofollow noopener noreferrer');
    }
    if (node.localName === 'img' && !safeURL(node.getAttribute('src'), true)) { node.remove(); continue; }
    if (node.localName === 'input') { node.type = 'checkbox'; node.disabled = true; }
    if (node.localName === 'span') node.replaceWith(...node.childNodes);
  }
  const container = document.createElement('div');
  container.append(fragment);
  normalizeListHeadings(container);
  return container.innerHTML;
}

export function pastedHTML(html, text) {
  const container = document.createElement('div');
  container.innerHTML = sanitizeHTML(requireBodyString(html));
  if (!container.textContent.trim() && !container.querySelector('img,input') && text) {
    for (const line of requireBodyString(text).replace(/\r\n?/g, '\n').split('\n')) {
      const paragraph = document.createElement('p');
      if (line) paragraph.textContent = line;
      else paragraph.append(document.createElement('br'));
      container.append(paragraph);
    }
  } else normalizeParagraphs(container, true);
  return container.innerHTML;
}

export function markdownHTML(source) {
  const html = requireBodyString(marked.parse(requireBodyString(source), { gfm: true, breaks: true, async: false }));
  if (source.trim() && !html.trim()) throw new Error('Markdown 转换意外丢失正文');
  return sanitizeHTML(html);
}

export function editorHTML(editor) {
  const clone = editor.cloneNode(true);
  for (const checkbox of clone.querySelectorAll('input[type=checkbox]')) {
    checkbox.toggleAttribute('checked', checkbox.checked);
  }
  for (const wrapper of clone.querySelectorAll('.diary-inline-image')) {
    const image = document.createElement('img');
    image.setAttribute('src', wrapper.dataset.imageUrl);
    wrapper.replaceWith(image);
  }
  for (const gallery of clone.querySelectorAll('.diary-image-gallery')) gallery.replaceWith(...gallery.childNodes);
  return sanitizeHTML(clone.innerHTML);
}

export function htmlText(html) {
  const container = document.createElement('div');
  container.innerHTML = sanitizeHTML(requireBodyString(html));
  const blocks = new Set(['P', 'DIV', 'BLOCKQUOTE', 'PRE', 'UL', 'OL', 'LI', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6', 'TR', 'HR']);
  const read = node => {
    if (node.nodeType === Node.TEXT_NODE) return node.nodeValue;
    if (node.nodeType !== Node.ELEMENT_NODE) return '';
    if (node.tagName === 'BR') return '\n';
    if (node.tagName === 'IMG') return managedImageURL(node.getAttribute('src')) ? `[[image:${node.getAttribute('src')}]]` : '[图片]';
    return [...node.childNodes].map(read).join('') + (blocks.has(node.tagName) ? '\n\n' : ['TH', 'TD'].includes(node.tagName) ? '\t' : '');
  };
  return [...container.childNodes].map(read).join('').replace(/\u00a0/g, ' ').trim();
}

const turndown = new TurndownService({ headingStyle: 'atx', codeBlockStyle: 'fenced', bulletListMarker: '-', emDelimiter: '*', strongDelimiter: '**' });
turndown.addRule('strikethrough', { filter: ['del', 's', 'strike'], replacement: content => `~~${content}~~` });
// Keep formats without a lossless Markdown equivalent as safe inline HTML.
turndown.addRule('htmlFormats', { filter: ['u', 'table'], replacement: (_, node) => `\n\n${node.outerHTML}\n\n` });
turndown.addRule('task', { filter: 'input', replacement: (_, node) => node.checked ? '[x] ' : '[ ] ' });
turndown.addRule('alignment', {
  filter: node => (alignedTags.has(node.localName) && node.hasAttribute('align'))
    || (['ul', 'ol'].includes(node.localName) && Boolean(node.querySelector('[align]'))),
  replacement: (_, node) => '\n\n' + node.outerHTML + '\n\n',
});
// Delimiters beside Chinese punctuation or another formatted run can become
// literal text under CommonMark. Keep safe inline HTML when that happens.
for (const [tag, names, marker] of [
  ['strong', ['strong', 'b'], '**'],
  ['em', ['em', 'i'], '*'],
  ['del', ['del', 's', 'strike'], '~~'],
]) {
  turndown.addRule('roundtrip-' + tag, {
    filter: names,
    replacement(content, node) {
      const markdown = marker + content + marker;
      const before = node.previousSibling?.textContent?.slice(-1) || '';
      const after = node.nextSibling?.textContent?.slice(0, 1) || '';
      const probe = document.createElement('div');
      probe.innerHTML = marked.parseInline(before + markdown + after);
      const formatted = probe.querySelector(tag);
      return formatted?.textContent === node.textContent
        && probe.textContent === before + node.textContent + after
        ? markdown : '<' + tag + '>' + node.innerHTML + '</' + tag + '>';
    },
  });
}
export function htmlMarkdown(html) {
  const container = document.createElement('div');
  container.innerHTML = sanitizeHTML(html);
  // Native paragraph joins commonly leave adjacent bold nodes. Serialize them
  // as one run rather than emitting the ambiguous **one****two** sequence.
  for (const node of [...container.querySelectorAll('strong, b, em, i, del, s, strike')].reverse()) {
    const family = name => ({ b: 'strong', i: 'em', s: 'del', strike: 'del' }[name] || name);
    while (node.nextSibling?.nodeType === Node.ELEMENT_NODE
      && family(node.localName) === family(node.nextSibling.localName)) {
      const next = node.nextSibling;
      node.append(...next.childNodes);
      next.remove();
    }
  }
  const markdown = requireBodyString(turndown.turndown(container.innerHTML));
  if (container.textContent.trim() && !markdown.trim()) throw new Error('Markdown 转换意外丢失正文');
  return markdown;
}
