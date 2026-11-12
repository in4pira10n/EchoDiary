// Run in the current page. Fixtures never touch real notes or saved settings.
async function runNoteDisplayTests() {
  const { installWritingDateLayout } = await import('/writing-date.js?v=20261112-date-spacing');
  const { normalizeTypography } = await import('/typography.js?v=20261112-date-spacing');
  const results = [];
  const assert = (condition, message) => { if (!condition) throw Error(message); };
  const test = (name, run) => {
    try { run(); results.push({ name, passed: true }); }
    catch (error) { results.push({ name, passed: false, error: String(error) }); }
  };
  const fixture = document.createElement('div');
  fixture.style.cssText = 'position:fixed;left:-10000px;top:0;visibility:hidden';
  fixture.innerHTML = '<header class="diary-writing-top" style="position:relative;left:0;right:auto;top:0;width:900px;height:52px"><button style="position:absolute;top:0;left:0;width:120px;height:52px">返回</button><button class="diary-created" style="position:absolute">创建于：2026 年 11 月 12 日 19:46</button><div style="position:absolute;top:0;right:0;width:52px;height:52px"></div></header><section style="height:100px;overflow:auto"><h1>测试标题</h1><div style="height:500px"></div></section>';
  document.body.append(fixture);
  const header = fixture.querySelector('header'), date = fixture.querySelector('.diary-created');
  const title = fixture.querySelector('h1'), content = fixture.querySelector('section');
  const back = header.firstChild, share = header.lastChild;
  let preview = false, enabled = true;
  const layout = installWritingDateLayout({ header, date, title, content, back, share, isPreview: () => preview, isEnabled: () => enabled });
  try {
    test('Date fits the header when there is room', () => {
      layout.update(); assert(date.parentElement === header, 'Date did not return to header');
    });
    test('Insufficient space moves only the date below the title', () => {
      header.style.width = '335px';
      const before = [back, share].map(el => el.getBoundingClientRect());
      layout.update();
      assert(date.previousElementSibling === title, 'Date is not below title');
      [back, share].forEach((el, i) => {
        const after = el.getBoundingClientRect();
        assert(after.x === before[i].x && after.y === before[i].y && after.width === before[i].width, 'Navigation button moved');
      });
    });
    test('Inline date is not hidden by the header scroll handler', () => {
      content.scrollTop = 50; content.dispatchEvent(new Event('scroll'));
      assert(!header.classList.contains('is-created-hidden'), 'Inline date uses header hiding');
    });
    test('Wide header retains its scroll hiding behavior', () => {
      header.style.width = '900px'; layout.update();
      assert(date.parentElement === header && header.classList.contains('is-created-hidden'), 'Header date did not hide');
    });
    test('Actual button width can trigger fallback on a wide screen', () => {
      back.style.width = '400px'; layout.update();
      assert(date.previousElementSibling === title, 'Placement depends only on screen width');
      back.style.width = '120px';
    });
    test('Preview and show-date preference hide dates in both locations', () => {
      for (const width of ['335px', '900px']) {
        header.style.width = width; preview = true; layout.update(); assert(date.hidden, 'Preview date is visible');
        preview = false; enabled = false; layout.update(); assert(date.hidden, 'Disabled date is visible');
        enabled = true; layout.update(); assert(!date.hidden, 'Editing date did not return');
      }
    });
    test('Typography rejects invalid stored settings and clamps supported ranges', () => {
      assert(normalizeTypography(null).fontSize === 16, 'Default font size changed');
      const result = normalizeTypography({ fontSize: 999, lineHeight: -2, fontWeight: 'bold', paragraphSpacing: NaN });
      assert(result.fontSize === 24 && result.lineHeight === 1.4 && result.fontWeight === 400 && result.paragraphSpacing === 12, 'Invalid preference accepted');
    });
    test('Preview headings center without changing editor alignment', () => {
      const preview = document.createElement('div'); preview.className = 'diary-writing-preview';
      preview.innerHTML = '<h1 style="text-align:right">一级</h1><h2>二级</h2>';
      const editor = document.createElement('div'); editor.className = 'diary-writing-body'; editor.innerHTML = '<h1 style="text-align:right">编辑标题</h1>';
      fixture.append(preview, editor);
      assert([...preview.children].every(el => getComputedStyle(el).textAlign === 'center'), 'Preview headings are not centered');
      assert(getComputedStyle(editor.firstChild).textAlign === 'right', 'Editor alignment changed');
    });
  } finally { layout.destroy(); fixture.remove(); }
  return { passed: results.filter(r => r.passed).length, failed: results.filter(r => !r.passed).length, results };
}
