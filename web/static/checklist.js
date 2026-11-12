import { normalizeEditingLists } from './document-structure.js?v=20261111-editor-fixes';
// Controls are runtime-only; content.js saves semantic, safe HTML.
export function prepareChecklists(container) {
  for (const checkbox of container.querySelectorAll('li input[type=checkbox]')) {
    checkbox.disabled = false;
    checkbox.contentEditable = 'false';
    checkbox.setAttribute('aria-label', '标记清单完成状态');
    const item = checkbox.closest('li');
    // Native insertion can inherit an inline B/EM wrapper. Keep the control
    // outside those runs so it always has the same layout and caret boundary.
    if (checkbox.parentElement !== item) item.prepend(checkbox);
    if (!item.textContent && !item.querySelector('br, img, ul, ol')) checkbox.after(document.createElement('br'));
  }
}

function currentItem(editor) {
  const selection = getSelection();
  if (!selection?.rangeCount) return null;
  const range = selection.getRangeAt(0);
  const node = range.startContainer;
  let element = node.nodeType === Node.ELEMENT_NODE ? node : node.parentElement;
  if (element === editor || element?.matches('ul,ol')) {
    const atEnd = range.startOffset >= node.childNodes.length;
    const child = node.childNodes[range.startOffset] || node.lastChild;
    const target = child?.nodeType === Node.ELEMENT_NODE ? child : child?.parentElement;
    const items = target?.querySelectorAll('li');
    element = target?.matches('li') ? target : items?.[atEnd ? items.length - 1 : 0] || element;
  }
  const item = element?.closest('li');
  return item && editor.contains(item) ? item : null;
}

function checkboxFor(item) {
  return [...item.querySelectorAll('input[type=checkbox]')].find(input => input.closest('li') === item);
}

function selectedItems(editor, range) {
  if (range.collapsed) {
    const item = currentItem(editor);
    return item ? [item] : [];
  }
  return [...editor.querySelectorAll('li')].filter(item => {
    const contents = document.createRange();
    contents.selectNodeContents(item);
    const nested = item.querySelector(':scope > ul, :scope > ol');
    if (nested) contents.setEndBefore(nested);
    return range.compareBoundaryPoints(Range.END_TO_START, contents) < 0
      && range.compareBoundaryPoints(Range.START_TO_END, contents) > 0;
  });
}

function includesParagraphs(editor, range) {
  if (range.collapsed) return false;
  return [...editor.querySelectorAll('p,div,h1,h2,h3,h4,h5,h6')].some(block => {
    if (block.closest('li') || block.querySelector('p,div,ul,ol')) return false;
    const contents = document.createRange(); contents.selectNodeContents(block);
    return range.compareBoundaryPoints(Range.END_TO_START, contents) < 0
      && range.compareBoundaryPoints(Range.START_TO_END, contents) > 0;
  });
}

export function toggleChecklist(editor, range) {
  const selection = getSelection();
  const collapsed = range.collapsed;
  editor.focus({ preventScroll: true });
  selection.removeAllRanges();
  selection.addRange(range);
  let items = selectedItems(editor, range);
  if (collapsed && items.length && !items[0].contains(range.startContainer)) {
    range.selectNodeContents(items[0]); range.collapse(false);
    selection.removeAllRanges(); selection.addRange(range);
  }
  if (!items.length || includesParagraphs(editor, range)) {
    // Empty contenteditables need an actual line for native list commands.
    if (!editor.childNodes.length) {
      const paragraph = document.createElement('p');
      paragraph.append(document.createElement('br'));
      editor.append(paragraph);
      range.selectNodeContents(paragraph); range.collapse(true);
      selection.removeAllRanges(); selection.addRange(range);
    }
    document.execCommand('insertUnorderedList', false, null);
    normalizeEditingLists(editor);
    range = selection.getRangeAt(0).cloneRange();
    items = selectedItems(editor, range);
  }
  if (!items.length) return;
  const remove = items.every(item => checkboxFor(item));
  for (const item of items) {
    const checkbox = checkboxFor(item);
    if (remove) { checkbox.remove(); continue; }
    if (checkbox) continue;
    const caret = document.createRange();
    caret.selectNodeContents(item);
    caret.collapse(true);
    selection.removeAllRanges(); selection.addRange(caret);
    document.execCommand('insertHTML', false, '<input type=checkbox contenteditable=false>');
    prepareChecklists(editor);
  }
  selection.removeAllRanges(); selection.addRange(range);
  prepareChecklists(editor);
  if (collapsed) {
    const checkbox = checkboxFor(items[0]);
    if (checkbox) {
      // Native list creation can leave the caret before the noneditable input.
      const caret = selection.getRangeAt(0);
      const prefix = document.createRange();
      prefix.selectNodeContents(items[0]);
      prefix.setEnd(caret.startContainer, caret.startOffset);
      if (!prefix.toString() && !prefix.cloneContents().querySelector('input')) {
        caret.setStartAfter(checkbox); caret.collapse(true);
        selection.removeAllRanges(); selection.addRange(caret);
      }
    }
  }
}

export function handleChecklistInput(event) {
  if (event.isComposing || event.defaultPrevented || event.inputType !== 'insertParagraph') return;
  const editor = event.currentTarget;
  const item = currentItem(editor);
  const checkbox = item && checkboxFor(item);
  if (!checkbox) return;
  event.preventDefault();
  if (!item.textContent.trim() && !item.querySelector('img, ul, ol')) {
    checkbox.remove();
    document.execCommand('outdent', false, null);
    document.execCommand('formatBlock', false, 'p');
    normalizeEditingLists(editor);
  } else {
    // Native splitting keeps inline formatting and nested lists intact.
    document.execCommand('insertParagraph', false, null);
    const next = currentItem(editor);
    if (next) {
      let input = checkboxFor(next);
      if (!input) { input = document.createElement('input'); input.type = 'checkbox'; next.prepend(input); }
      input.checked = false; input.removeAttribute('checked');
      const caret = document.createRange(); caret.setStartAfter(input); caret.collapse(true);
      const selection = getSelection(); selection.removeAllRanges(); selection.addRange(caret);
    }
  }
  prepareChecklists(editor);
  editor.dispatchEvent(new Event('input', { bubbles: true }));
}
