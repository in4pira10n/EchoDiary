// Evaluate this file in an EchoDiary page, then await runChecklistTests().
// Uses an isolated editor, never the note editor or any persistence API.
async function runChecklistTests(baseURL = location.origin) {
  const version = '20261112-checklist-exit';
  const [checklist, content, shortcuts] = await Promise.all([
    import(`${baseURL}/checklist.js?v=${version}`),
    import(`${baseURL}/content.js?v=${version}`),
    import(`${baseURL}/markdown-shortcuts.js?v=${version}`),
  ]);
  const selection = getSelection();
  const ranges = Array.from({ length: selection.rangeCount }, (_, index) => selection.getRangeAt(index).cloneRange());
  const focused = document.activeElement;
  const editor = document.createElement('div');
  editor.contentEditable = 'true';
  editor.style.cssText = 'position:fixed;left:-10000px;top:0;width:400px';
  document.body.append(editor);
  shortcuts.installMarkdownShortcuts(editor);
  editor.addEventListener('beforeinput', checklist.handleChecklistInput);
  let beforeinputs = 0;
  let saved = [];
  const errors = [];
  const onError = event => errors.push(String(event.error || event.message));
  window.addEventListener('error', onError);
  editor.addEventListener('beforeinput', () => beforeinputs++);
  editor.addEventListener('input', () => {
    if (checklist.isChecklistEditing(editor)) return;
    try { saved.push(content.htmlMarkdown(content.editorHTML(editor))); }
    catch (error) { errors.push(String(error)); }
  });
  const assert = (condition, message) => { if (!condition) throw new Error(message); };
  const select = (node, offset, end = node, endOffset = offset) => {
    const range = document.createRange();
    range.setStart(node, offset); range.setEnd(end, endOffset);
    selection.removeAllRanges(); selection.addRange(range);
    return range;
  };
  const caretText = block => {
    assert(block.contains(selection.anchorNode), 'Caret left its paragraph');
    const prefix = document.createRange(); prefix.selectNodeContents(block);
    prefix.setEnd(selection.anchorNode, selection.anchorOffset);
    return prefix.toString();
  };
  const setup = html => {
    editor.innerHTML = html;
    checklist.prepareChecklists(editor);
    editor.focus({ preventScroll: true });
    beforeinputs = 0; saved = []; errors.length = 0;
  };
  const toggle = range => {
    checklist.toggleChecklist(editor, range);
    editor.dispatchEvent(new Event('input', { bubbles: true }));
  };
  const enter = () => {
    const event = new InputEvent('beforeinput', { bubbles: true, cancelable: true, inputType: 'insertParagraph' });
    if (editor.dispatchEvent(event)) document.execCommand('insertParagraph', false, null);
  };
  const roundtrip = html => {
    const result = document.createElement('div');
    result.innerHTML = content.markdownHTML(content.htmlMarkdown(html));
    return result;
  };
  const results = [];
  const test = (name, run) => {
    try {
      run();
      assert(!errors.length, errors.join('; '));
      assert(!checklist.isChecklistEditing(editor), 'Checklist transaction did not finish');
      results.push({ name, passed: true });
    } catch (error) { results.push({ name, passed: false, error: String(error) }); }
    finally { errors.length = 0; }
  };
  try {
    test('Empty checked and unchecked tasks survive repeated Markdown round trips', () => {
      let html = '<p>前文</p><ul><li><input type=checkbox><br></li><li><input type=checkbox checked><br></li></ul><p>后文</p>';
      for (let index = 0; index < 3; index++) {
        const result = roundtrip(html);
        const inputs = result.querySelectorAll('input');
        assert(inputs.length === 2 && !inputs[0].checked && inputs[1].checked, 'Empty task state was lost');
        assert(result.textContent.replace(/\s/g, '') === '前文后文', 'Surrounding content changed');
        html = result.innerHTML;
      }
    });
    test('Formatted, nested, aligned and numbered tasks preserve content and state', () => {
      for (const html of [
        '<ul><li><input type=checkbox><strong>加粗</strong><em>强调</em><ul><li><input type=checkbox checked>子项</li></ul></li></ul>',
        '<ul><li align=center><input type=checkbox checked><u>对齐</u></li></ul>',
        '<ol start=3><li><input type=checkbox>编号</li></ol>',
      ]) {
        const before = document.createElement('div'); before.innerHTML = html;
        const result = roundtrip(html);
        assert(result.textContent.replace(/\s/g, '') === before.textContent.replace(/\s/g, ''), 'Task text changed');
        assert([...result.querySelectorAll('input')].map(input => input.checked).join() === [...before.querySelectorAll('input')].map(input => input.checked).join(), 'Task state changed');
        for (const tag of ['strong', 'em', 'u', '[align]', 'ol[start]']) {
          assert(result.querySelectorAll(tag).length === before.querySelectorAll(tag).length, `Lost ${tag}`);
        }
      }
    });
    test('Insert and cancel an empty checklist, then reinsert it', () => {
      setup(''); toggle(select(editor, 0));
      assert(editor.querySelectorAll('input').length === 1, 'Failed to insert checklist');
      const item = editor.querySelector('li');
      toggle(select(item, 1));
      assert(!editor.querySelector('li,input') && editor.querySelector('p br'), 'Cancel did not exit directly to a paragraph');
      toggle(selection.getRangeAt(0));
      assert(editor.querySelectorAll('input').length === 1, 'Failed to reinsert checklist');
      assert(selection.rangeCount && editor.contains(selection.anchorNode), 'Caret left the editor');
    });
    test('Selected list items toggle without changing adjacent text or nested items', () => {
      setup('<p>前文</p><ul><li>一<ul><li>子项</li></ul></li><li>二</li></ul><p>后文</p>');
      const items = editor.querySelectorAll(':scope > ul > li');
      const first = items[0].firstChild, last = items[1].firstChild;
      toggle(select(first, 0, last, 1));
      assert(editor.querySelectorAll('input').length === 3, 'Selected nested items were missed');
      toggle(select(first, 0, last, 1));
      assert(!editor.querySelector('input'), 'Failed to cancel selected checklists');
      assert(editor.textContent === '前文一子项二后文', 'Selected-list toggle changed content');
    });
    test('A mixed selection of paragraphs and lists becomes tasks without losing text', () => {
      setup('<p>前文</p><ul><li>一</li><li>二</li></ul><p>后文</p>');
      toggle(select(editor.firstChild.firstChild, 0, editor.lastChild.firstChild, 2));
      assert(editor.querySelectorAll('input').length === 4, 'Mixed selection skipped blocks');
      assert(editor.textContent === '前文一二后文', 'Mixed selection changed text');
      assert(saved.length === 1, 'Mixed selection saved an intermediate state');
    });
    test('A live checkbox state is preserved when serializing the editor', () => {
      setup('<ul><li><input type=checkbox>任务</li></ul>');
      editor.querySelector('input').checked = true;
      let result = roundtrip(content.editorHTML(editor));
      assert(result.querySelector('input').checked, 'Checked property was lost');
      editor.querySelector('input').checked = false;
      result = roundtrip(content.editorHTML(editor));
      assert(!result.querySelector('input').checked, 'Unchecked property was lost');
    });
    test('Changing format at a root-level caret does not clear other checklists', () => {
      setup('<ul><li><input type=checkbox checked>保留任务</li></ul><p>正文</p>');
      checklist.removeSelectedChecklists(editor, select(editor, 1));
      assert(editor.querySelector('input')?.checked, 'Unrelated checklist was cleared');
      checklist.removeSelectedChecklists(editor, select(editor.lastChild.firstChild, 1));
      assert(editor.querySelector('input')?.checked, 'Paragraph formatting cleared a checklist');
    });
    test('Removing a selected checklist preserves nested and adjacent task states', () => {
      setup('<ul><li><input type=checkbox>父项<ul><li><input type=checkbox checked>子项</li></ul></li><li><input type=checkbox checked>相邻任务</li></ul>');
      const item = editor.querySelector('li');
      checklist.removeSelectedChecklists(editor, select(item.childNodes[1], 1));
      assert(!item.querySelector(':scope > input'), 'Selected checklist was not removed');
      const remaining = editor.querySelectorAll('input');
      assert(remaining.length === 2 && [...remaining].every(input => input.checked), 'Unselected checklist state changed');
    });
    test('Enter at the end saves one completed operation without recursive events', () => {
      setup('<p>前文</p><ul><li><input type=checkbox checked>任务</li></ul><p>后文</p>');
      select(editor.querySelector('li').lastChild, 2); enter();
      const inputs = editor.querySelectorAll('input');
      assert(inputs.length === 2 && inputs[0].checked && !inputs[1].checked, 'New task state is incorrect');
      assert(beforeinputs <= 2, 'Enter recursively re-entered beforeinput');
      assert(saved.length === 1, 'Saved an intermediate checklist state');
      assert(roundtrip(content.editorHTML(editor)).querySelectorAll('input').length === 2, 'New empty task failed Markdown conversion');
      assert(editor.textContent === '前文任务后文', 'Enter changed surrounding text');
    });
    test('Consecutive Enter presses exit an empty task and continue as ordinary paragraphs', () => {
      setup('<p>前文</p><ul><li><input type=checkbox checked>任务</li></ul><p>后文</p>');
      select(editor.querySelector('li').lastChild, 2);
      enter(); enter(); enter();
      assert(editor.querySelectorAll('input').length === 1, 'Empty task did not exit');
      const node = selection.anchorNode;
      assert(editor.contains(node) && !(node.nodeType === Node.ELEMENT_NODE ? node : node.parentElement).closest('li'), 'Caret stayed inside the checklist');
      assert(editor.textContent === '前文任务后文', 'Consecutive Enter changed content');
      assert(beforeinputs < 10, 'Consecutive Enter recursively re-entered beforeinput');
    });
    test('Enter in the middle preserves both halves and formatting', () => {
      setup('<ul><li><input type=checkbox checked><strong>任务内容</strong></li></ul>');
      select(editor.querySelector('strong').firstChild, 2); enter();
      const items = editor.querySelectorAll('li');
      assert(items.length === 2 && items[0].textContent === '任务' && items[1].textContent === '内容', 'Task split lost text');
      assert(items[0].querySelector('strong') && items[1].querySelector('strong'), 'Task split lost formatting');
    });
    test('Enter at the beginning keeps completion with the existing task text', () => {
      setup('<ul><li><input type=checkbox checked>任务</li></ul>');
      select(editor.querySelector('li').lastChild, 0); enter();
      const items = editor.querySelectorAll('li');
      assert(items.length === 2 && !items[0].textContent && items[1].textContent === '任务', 'Task text moved incorrectly');
      assert(!items[0].querySelector('input').checked && items[1].querySelector('input').checked, 'Completion moved to the empty task');
    });
    test('Nested tasks split and exit without changing the parent', () => {
      setup('<ul><li><input type=checkbox>父项<ul><li><input type=checkbox checked>子项</li></ul></li></ul>');
      select(editor.querySelector('li li').lastChild, 2); enter(); enter();
      assert(editor.textContent === '父项子项', 'Nested operation changed text');
      assert(editor.querySelector('li li input').checked, 'Nested task completion changed');
      assert(beforeinputs < 8, 'Nested Enter recursively re-entered beforeinput');
    });
    test('Cancel a task directly to a paragraph and keep the caret in formatted text', () => {
      setup('<ul><li><input type=checkbox checked>前项</li><li><input type=checkbox><strong>任务内容</strong></li><li><input type=checkbox checked>后项</li></ul>');
      const text = editor.querySelector('strong').firstChild;
      toggle(select(text, 2));
      assert(editor.querySelectorAll('li').length === 2 && editor.querySelector('p strong').textContent === '任务内容', 'Cancel left a bullet or lost formatting');
      assert(caretText(editor.querySelector('p strong').parentElement) === '任务', 'Cancel moved the caret');
      assert([...editor.querySelectorAll('input')].every(input => input.checked), 'Cancel changed adjacent task state');
      assert(saved.length === 1, 'Cancel saved intermediate content');
    });
    const backspace = () => {
      const event = new InputEvent('beforeinput', { bubbles: true, cancelable: true, inputType: 'deleteContentBackward' });
      if (editor.dispatchEvent(event)) document.execCommand('delete', false, null);
    };
    test('Backspace at task start removes list formatting in one operation', () => {
      setup('<p>前文</p><ul><li><input type=checkbox><strong>任务</strong><ul><li><input type=checkbox checked>子项</li></ul></li><li><input type=checkbox checked>相邻</li></ul>');
      select(editor.querySelector('strong').firstChild, 0); backspace();
      assert(editor.querySelector('p strong').textContent === '任务', 'Task degraded to a bullet');
      assert(caretText(editor.querySelector('p strong').parentElement) === '', 'Caret left the converted paragraph');
      assert(editor.textContent === '前文任务子项相邻', 'Backspace lost text');
      assert(editor.querySelectorAll('input').length === 2 && [...editor.querySelectorAll('input')].every(input => input.checked), 'Nested or adjacent tasks changed');
      assert(saved.length === 1, 'Backspace saved an intermediate state');
    });
    test('Backspace exits an empty task without a bullet or caret jump', () => {
      setup('<p>前文</p><ul><li><input type=checkbox><br></li></ul>');
      select(editor.querySelector('li'), 1); backspace();
      assert(!editor.querySelector('li,input') && editor.lastChild.tagName === 'P', 'Empty task did not exit directly');
      assert(editor.lastChild.contains(selection.anchorNode), 'Caret jumped to the first paragraph');
    });
    test('Backspace within a task still deletes text normally', () => {
      setup('<ul><li><input type=checkbox>任务内容</li></ul>');
      select(editor.querySelector('li').lastChild, 2); backspace();
      assert(editor.querySelector('input') && editor.textContent === '任内容', 'Ordinary deletion changed list formatting');
    });
    test('Stale selection cannot edit outside the requested editor', () => {
      setup('<p>保留正文</p>');
      const other = document.createElement('div'); other.textContent = '其他内容';
      const range = document.createRange(); range.selectNodeContents(other);
      checklist.toggleChecklist(editor, range);
      assert(editor.innerHTML === '<p>保留正文</p>' && other.textContent === '其他内容', 'Stale selection edited content');
    });
  } finally {
    window.removeEventListener('error', onError);
    editor.remove();
    if (focused?.isConnected) focused.focus({ preventScroll: true });
    selection.removeAllRanges();
    for (const range of ranges) {
      if (range.startContainer.isConnected && range.endContainer.isConnected) selection.addRange(range);
    }
  }
  return { passed: results.filter(result => result.passed).length, failed: results.filter(result => !result.passed).length, results };
}
