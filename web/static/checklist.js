import { normalizeEditingLists } from './document-structure.js?v=20261111-editor-fixes';
// WebKit dispatches beforeinput/input synchronously from execCommand. Keep a
// whole checklist operation atomic: do not re-enter it or save its intermediate DOM.
const editingChecklists = new WeakSet();
export const isChecklistEditing = editor => editingChecklists.has(editor);

function editChecklist(editor, change) {
  if (isChecklistEditing(editor)) return;
  editingChecklists.add(editor);
  try { return change(); }
  finally { editingChecklists.delete(editor); }
}
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

function removeCheckbox(item, checkbox) {
  checkbox.remove();
  if (!item.textContent && !item.querySelector('br, img, ul, ol')) item.append(document.createElement('br'));
}

// Keep selection markers inside the moved inline nodes. A live Range alone is
// relocated to the old parent when WebKit moves a list item out of its list.
function keepSelection(change) {
  const selection = getSelection();
  const range = selection.getRangeAt(0).cloneRange();
  const collapsed = range.collapsed;
  const end = document.createComment('selection-end');
  const start = document.createComment('selection-start');
  const tail = range.cloneRange(); tail.collapse(false); tail.insertNode(end);
  range.collapse(true); range.insertNode(start);
  change();
  const restored = document.createRange();
  restored.setStartAfter(start);
  if (collapsed) restored.collapse(true); else restored.setEndBefore(end);
  start.remove(); end.remove();
  selection.removeAllRanges(); selection.addRange(restored);
}

function unwrapItem(item) {
  const list = item.parentElement;
  const trailing = list.cloneNode(false);
  if (list.tagName === 'OL') {
    trailing.start = (Number(list.getAttribute('start')) || 1) + [...list.children].indexOf(item) + 1;
  }
  while (item.nextSibling) trailing.append(item.nextSibling);
  const paragraph = document.createElement('p');
  for (const attribute of ['align', 'style']) {
    if (item.hasAttribute(attribute)) paragraph.setAttribute(attribute, item.getAttribute(attribute));
  }
  checkboxFor(item)?.remove();
  const nested = [];
  while (item.firstChild) {
    const child = item.firstChild;
    if (child.nodeType === Node.ELEMENT_NODE && child.matches('ul,ol')) { nested.push(child); child.remove(); }
    else paragraph.append(child);
  }
  if (!paragraph.textContent && !paragraph.querySelector('br,img')) paragraph.append(document.createElement('br'));
  list.after(paragraph);
  let after = paragraph;
  for (const child of nested) { after.after(child); after = child; }
  if (trailing.childNodes.length) after.after(trailing);
  item.remove();
  if (!list.children.length) list.remove();
  return paragraph;
}

function atTaskStart(item, range) {
  if (!range.collapsed) return false;
  const prefix = range.cloneRange();
  prefix.selectNodeContents(item);
  prefix.setEnd(range.startContainer, range.startOffset);
  return !prefix.toString() && !prefix.cloneContents().querySelector('br,img,ul,ol');
}

export function removeSelectedChecklists(editor, range) {
  if (!range || !editor.contains(range.commonAncestorContainer)) return;
  for (const item of selectedItems(editor, range)) {
    const checkbox = checkboxFor(item);
    if (checkbox) removeCheckbox(item, checkbox);
  }
}

export function toggleChecklist(editor, range) {
  if (!range || !editor.contains(range.commonAncestorContainer)) return;
  return editChecklist(editor, () => toggleSelectedChecklist(editor, range));
}

function toggleSelectedChecklist(editor, range) {
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
  if (remove) {
    keepSelection(() => { for (const item of items) unwrapItem(item); });
    return;
  }
  for (const item of items) {
    const checkbox = checkboxFor(item);
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
  if (event.isComposing || event.defaultPrevented || !['insertParagraph', 'deleteContentBackward'].includes(event.inputType)) return;
  const editor = event.currentTarget;
  if (isChecklistEditing(editor)) return;
  const item = currentItem(editor);
  const checkbox = item && checkboxFor(item);
  if (!checkbox) return;
  const range = getSelection().getRangeAt(0);
  if (event.inputType === 'deleteContentBackward' && !atTaskStart(item, range)) return;
  event.preventDefault();
  editChecklist(editor, () => {
    if (event.inputType === 'deleteContentBackward') keepSelection(() => unwrapItem(item));
    else splitChecklistItem(editor, item, checkbox);
  });
  // Only the completed, normalized operation may update the saved note.
  editor.dispatchEvent(new Event('input', { bubbles: true }));
}

function splitChecklistItem(editor, item, checkbox) {
  if (!item.textContent.trim() && !item.querySelector('img, ul, ol')) {
    keepSelection(() => unwrapItem(item));
  } else {
    const range = getSelection().getRangeAt(0);
    const prefix = range.cloneRange();
    prefix.selectNodeContents(item);
    prefix.setEnd(range.startContainer, range.startOffset);
    const atStart = range.collapsed && !prefix.toString()
      && !prefix.cloneContents().querySelector('img, ul, ol');
    const checked = checkbox.checked;
    // Native splitting keeps inline formatting and nested lists intact.
    document.execCommand('insertParagraph', false, null);
    const next = currentItem(editor);
    if (next && next !== item) {
      let input = checkboxFor(next);
      if (!input) { input = document.createElement('input'); input.type = 'checkbox'; next.prepend(input); }
      input.checked = atStart && checked;
      input.toggleAttribute('checked', input.checked);
      if (atStart) { checkbox.checked = false; checkbox.removeAttribute('checked'); }
      const caret = document.createRange(); caret.setStartAfter(input); caret.collapse(true);
      const selection = getSelection(); selection.removeAllRanges(); selection.addRange(caret);
    }
  }
  prepareChecklists(editor);
}
