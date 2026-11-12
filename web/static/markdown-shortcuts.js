import { prepareChecklists } from './checklist.js?v=20261112-checklist-exit';
// Convert only the active paragraph; native commands preserve undo history.
export function installMarkdownShortcuts(editor) {
  let applying = false;
  const blocks = 'p, div, h1, h2, h3, h4, h5, h6, blockquote, pre, li';
  const escapeHTML = value => value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  function point(block, offset) {
    const walker = document.createTreeWalker(block, NodeFilter.SHOW_TEXT);
    let node;
    while ((node = walker.nextNode())) {
      if (offset <= node.length) return [node, offset];
      offset -= node.length;
    }
    return [block, block.childNodes.length];
  }
  function context() {
    const selection = getSelection();
    if (!selection?.isCollapsed || !selection.rangeCount || !editor.contains(selection.anchorNode)) return null;
    const range = selection.getRangeAt(0);
    const element = range.startContainer.nodeType === Node.ELEMENT_NODE ? range.startContainer : range.startContainer.parentElement;
    if (element.closest('pre, .diary-inline-image, .diary-image-gallery')) return null;
    let block = element.closest(blocks);
    if (!block || block === editor || !editor.contains(block)) block = editor;
    if (block === editor && editor.querySelector(blocks)) return null;
    const prefix = document.createRange();
    prefix.selectNodeContents(block);
    prefix.setEnd(range.startContainer, range.startOffset);
    if (prefix.cloneContents().querySelector('br, img, .diary-inline-image')) return null;
    return { block, inlineProtected: Boolean(element.closest('code, a')), prefix: prefix.toString().replace(/ /g, ' ') };
  }
  function replace(range, html) {
    const selection = getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
    document.execCommand('insertHTML', false, html);
  }
  function transform() {
    const current = context();
    if (!current) return;
    const { block, prefix } = current;
    const task = prefix.match(/^(?:[-*+] )?\[([ xX])\] $/);
    if (task && (block === editor || ['P', 'DIV', 'LI'].includes(block.tagName))
      && !block.querySelector('input, img, ul, ol')) {
      const clone = block.cloneNode(true);
      const removal = document.createRange();
      removal.setStart(...point(clone, 0));
      removal.setEnd(...point(clone, prefix.length));
      removal.deleteContents();
      const contents = '<input type=checkbox contenteditable=false' + (task[1].toLowerCase() === 'x' ? ' checked' : '') + '>' + (clone.innerHTML || '<br>');
      const range = document.createRange();
      if (block === editor || block.tagName === 'LI') range.selectNodeContents(block);
      else range.selectNode(block);
      replace(range, block.tagName === 'LI' ? contents : '<ul><li>' + contents + '</li></ul>');
      prepareChecklists(editor);
      const selection = getSelection();
      const anchor = selection.anchorNode;
      const element = anchor.nodeType === Node.ELEMENT_NODE ? anchor : anchor.parentElement;
      const checkbox = element.closest('li')?.querySelector('input');
      if (checkbox) {
        const caret = document.createRange(); caret.setStartAfter(checkbox); caret.collapse(true);
        selection.removeAllRanges(); selection.addRange(caret);
      }
      return;
    }
    const heading = prefix.match(/^(#{1,6}) $/);
    const unordered = /^[-*+] $/.test(prefix);
    const ordered = prefix.match(/^([0-9]{1,9})[.)] $/);
    const quote = prefix === '> ';
    const fence = /^```[a-zA-Z0-9_-]* $/.test(prefix);
    const ordinaryBlock = block === editor || ['P', 'DIV'].includes(block.tagName);
    const headingBlock = /^H[1-6]$/.test(block.tagName);
    if ((heading && (ordinaryBlock || headingBlock || block.tagName === 'LI')) || ((unordered || ordered || quote || fence) && ordinaryBlock)) {
      if (block.querySelector('img, .diary-inline-image, .diary-image-gallery')) return;
      const clone = block.cloneNode(true);
      const removal = document.createRange();
      removal.setStart(...point(clone, 0));
      removal.setEnd(...point(clone, prefix.length));
      removal.deleteContents();
      const remaining = clone.innerHTML || '<br>';
      const tag = heading ? 'h' + heading[1].length : unordered || ordered ? 'li' : quote ? 'blockquote' : 'pre';
      let html = heading ? '<' + tag + '>' + remaining + '</' + tag + '>'
        : unordered ? '<ul><li>' + remaining + '</li></ul>'
        : ordered ? '<ol start="' + Number(ordered[1]) + '"><li>' + remaining + '</li></ol>'
        : quote ? '<blockquote><p>' + remaining + '</p></blockquote>'
        : '<pre><code>' + remaining + '</code></pre>';
      const replacement = document.createRange();
      let headingIndex = null;
      if (heading && ['UL', 'OL'].includes(block.parentElement?.tagName)) {
        const selection = getSelection();
        const contents = document.createRange();
        contents.selectNodeContents(block);
        selection.removeAllRanges();
        selection.addRange(contents);
        document.execCommand('outdent', false, null);
        document.execCommand('formatBlock', false, tag);
        const selected = selection.getRangeAt(0);
        const element = selected.startContainer.nodeType === Node.ELEMENT_NODE ? selected.startContainer : selected.startContainer.parentElement;
        const nativeHeading = element.closest(tag);
        if (!nativeHeading || !editor.contains(nativeHeading)) return;
        replacement.selectNodeContents(nativeHeading);
        html = remaining;
        headingIndex = [...editor.querySelectorAll(tag)].indexOf(nativeHeading);
      } else if (block === editor) replacement.selectNodeContents(editor);
      else replacement.selectNode(block);
      replace(replacement, html);
      const selection = getSelection();
      const anchor = selection?.anchorNode;
      const element = anchor?.nodeType === Node.ELEMENT_NODE ? anchor : anchor?.parentElement;
      const transformed = headingIndex === null ? element?.closest(tag) : editor.querySelectorAll(tag)[headingIndex];
      if (transformed && editor.contains(transformed)) {
        const caret = document.createRange();
        caret.setStart(...point(transformed, 0));
        caret.collapse(true);
        selection.removeAllRanges();
        selection.addRange(caret);
      }
      return;
    }
    if (current.inlineProtected) return;
    const rules = [
      { pattern: /(?:^|[^\w*])[*][*]([^*]+)[*][*]$/, tag: 'strong', marker: '**' },
      { pattern: /(?:^|[^\w_])__([^_]+)__$/, tag: 'strong', marker: '__' },
      { pattern: /(?:^|[^\w~])~~([^~]+)~~$/, tag: 'del', marker: '~~' },
      { pattern: /(?:^|[^\w`])`([^`]+)`$/, tag: 'code', marker: '`' },
      { pattern: /(?:^|[^\w*])[*]([^*]+)[*]$/, tag: 'em', marker: '*' },
      { pattern: /(?:^|[^\w_])_([^_]+)_$/, tag: 'em', marker: '_' },
    ];
    for (const { pattern, tag, marker } of rules) {
      const match = prefix.match(pattern);
      if (!match) continue;
      const length = match[1].length + marker.length * 2;
      const range = document.createRange();
      range.setStart(...point(block, prefix.length - length));
      range.setEnd(...point(block, prefix.length));
      if (range.cloneContents().querySelector('img, br, code, .diary-inline-image')) return;
      replace(range, '<' + tag + '>' + escapeHTML(match[1]) + '</' + tag + '>' + (tag === 'code' ? '&nbsp;' : ''));
      const selection = getSelection();
      const anchor = selection?.anchorNode;
      const element = anchor?.nodeType === Node.ELEMENT_NODE ? anchor : anchor?.parentElement;
      const formatted = element?.closest(tag);
      if (formatted && editor.contains(formatted)) {
        const caret = document.createRange();
        caret.setStartAfter(formatted);
        caret.collapse(true);
        selection.removeAllRanges();
        selection.addRange(caret);
        const command = { strong: 'bold', em: 'italic', del: 'strikeThrough' }[tag];
        if (command && document.queryCommandState(command)) document.execCommand(command, false, null);
      }
      return;
    }
  }
  editor.addEventListener('beforeinput', event => {
    if (applying || event.isComposing || !['insertParagraph', 'deleteContentBackward'].includes(event.inputType)) return;
    const selection = getSelection();
    if (!selection?.rangeCount) return;
    const range = selection.getRangeAt(0);
    const element = range.startContainer.nodeType === Node.ELEMENT_NODE ? range.startContainer : range.startContainer.parentElement;
    const heading = element.closest('h1,h2,h3,h4,h5,h6');
    if (!heading || !editor.contains(heading) || !heading.contains(range.endContainer)) return;
    const exitHeading = event.inputType === 'deleteContentBackward';
    if (exitHeading) {
      if (!selection.isCollapsed) return;
      const beforeCaret = document.createRange();
      beforeCaret.selectNodeContents(heading);
      beforeCaret.setEnd(range.startContainer, range.startOffset);
      if (beforeCaret.toString().length || beforeCaret.cloneContents().querySelector('br, img, .diary-inline-image, .diary-image-gallery')) return;
    }
    event.preventDefault();
    applying = true;
    try {
      if (!exitHeading) document.execCommand('insertParagraph', false, null);
      document.execCommand('formatBlock', false, 'p');
    } finally { applying = false; }
    editor.dispatchEvent(new Event('input', { bubbles: true }));
  });
  editor.addEventListener('input', event => {
    if (applying || event.isComposing || !['insertText', 'insertCompositionText'].includes(event.inputType)) return;
    applying = true;
    try { transform(); } finally { applying = false; }
  });
  editor.addEventListener('compositionend', () => {
    queueMicrotask(() => {
      if (applying) return;
      applying = true;
      try { transform(); } finally { applying = false; }
      editor.dispatchEvent(new Event('input', { bubbles: true }));
    });
  });
}
