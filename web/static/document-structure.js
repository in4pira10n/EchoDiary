// Lists can contain list items, not headings directly. Older native editing
// operations could leave headings between items; split that list without
// discarding its items, order, or heading formatting.
export function normalizeListHeadings(root) {
  for (const list of [...root.querySelectorAll('ul, ol')].reverse()) {
    if (![...list.children].some(child => /^H[1-6]$/.test(child.tagName))) continue;
    const output = document.createDocumentFragment();
    let segment = null;
    let number = Number(list.getAttribute('start') || 1);
    for (const child of [...list.childNodes]) {
      if (child.nodeType === Node.ELEMENT_NODE && /^H[1-6]$/.test(child.tagName)) {
        segment = null;
        output.append(child);
      } else {
        if (!segment) {
          segment = list.cloneNode(false);
          if (list.tagName === 'OL') segment.setAttribute('start', String(number));
          output.append(segment);
        }
        segment.append(child);
        if (child.nodeType === Node.ELEMENT_NODE && child.tagName === 'LI') number++;
      }
    }
    for (const node of [...output.children]) {
      if (['UL', 'OL'].includes(node.tagName) && !node.children.length && !node.textContent.trim()) node.remove();
    }
    list.replaceWith(output);
  }
}

// Clipboard HTML often uses DIVs, inline runs, and BRs for whole paragraphs.
// Rebuild those runs without flattening formatting, lists, tables, or code.
export function normalizeParagraphs(root, splitBreaks = false) {
  const blocks = 'p,div,h1,h2,h3,h4,h5,h6,ul,ol,li,blockquote,pre,table,hr';
  for (const div of [...root.querySelectorAll('div')].reverse()) {
    if (div.matches('.diary-inline-image, .diary-image-gallery')) continue;
    if (!div.querySelector(blocks)) {
      const paragraph = document.createElement('p');
      if (div.hasAttribute('align')) paragraph.setAttribute('align', div.getAttribute('align'));
      paragraph.append(...div.childNodes);
      div.replaceWith(paragraph);
    }
  }
  const wrapRuns = parent => {
    let paragraph = null;
    for (const child of [...parent.childNodes]) {
      if (child.nodeType === Node.ELEMENT_NODE && child.matches(blocks)) {
        paragraph = null;
        if (child.matches('div,blockquote')) wrapRuns(child);
        continue;
      }
      if (!paragraph && child.nodeType === Node.TEXT_NODE && !child.textContent.trim()) continue;
      if (!paragraph) { paragraph = document.createElement('p'); child.before(paragraph); }
      paragraph.append(child);
    }
  };
  wrapRuns(root);
  if (!splitBreaks) return;
  for (const paragraph of [...root.querySelectorAll('p')]) {
    if (paragraph.closest('li,pre,table')) continue;
    if (!paragraph.querySelector('br')) continue;
    const output = document.createDocumentFragment();
    for (const br of [...paragraph.querySelectorAll('br')]) {
      const range = document.createRange();
      range.selectNodeContents(paragraph);
      range.setEndBefore(br);
      const part = paragraph.cloneNode(false);
      part.append(range.extractContents());
      if (!part.textContent && !part.querySelector('img,input')) part.append(document.createElement('br'));
      output.append(part);
      br.remove();
    }
    if (!paragraph.textContent && !paragraph.querySelector('img,input')) paragraph.append(document.createElement('br'));
    paragraph.before(output);
  }
}

// Some native list commands put UL/OL inside P. Repair that live DOM while
// keeping selection endpoints on the same text/list item, not on moved parents.
export function normalizeEditingLists(editor) {
  const selection = getSelection();
  const range = selection?.rangeCount ? selection.getRangeAt(0) : null;
  const points = range && editor.contains(range.commonAncestorContainer)
    ? [range.startContainer, range.startOffset, range.endContainer, range.endOffset] : null;
  for (const paragraph of [...editor.querySelectorAll('p')]) {
    if (![...paragraph.children].some(child => child.matches('ul,ol,blockquote,pre,h1,h2,h3,h4,h5,h6'))) continue;
    const output = document.createDocumentFragment();
    let run = null;
    for (const child of [...paragraph.childNodes]) {
      if (child.nodeType === Node.ELEMENT_NODE && child.matches('ul,ol,blockquote,pre,h1,h2,h3,h4,h5,h6')) {
        run = null;
        output.append(child);
      } else {
        if (!run) { run = paragraph.cloneNode(false); output.append(run); }
        run.append(child);
      }
    }
    paragraph.replaceWith(output);
  }
  if (points && editor.contains(points[0]) && editor.contains(points[2])) {
    const restored = document.createRange();
    restored.setStart(points[0], points[1]); restored.setEnd(points[2], points[3]);
    selection.removeAllRanges(); selection.addRange(restored);
  }
}
