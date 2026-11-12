import { createEchoModal } from './diary-modal.js?v=20261107-modal-tone';
import { createEchoSegmented } from '/kit/components/segmented/index.js';
import { resolveEchoIcon } from '/kit/components/icon.js';

export function installHandoff({ requestJSON, notify, createEchoButton, getNotes, getHandoffNotes }) {
  const root = document.querySelector('#handoff-settings');
  const keyList = root.querySelector('#handoff-keys');
  let libraryId = '';
  let libraryName = '';
  let cachedKeys = [];
  let createModal;
  let tokenModal;

  const button = (container, label, handler, options = {}) => {
    const element = document.createElement('button');
    element.type = 'button';
    element.className = `diary-plain-button diary-handoff-plain-action${options.className ? ` ${options.className}` : ''}`;
    element.setAttribute('aria-label', options.ariaLabel || label);
    const text = document.createElement('span'); text.textContent = label;
    element.append(text);
    element.addEventListener('click', () => void Promise.resolve().then(handler).catch(error => notify(error.message)));
    container.append(element);
    return element;
  };
  const createEmptyAction = () => {
    const element = document.createElement('button');
    element.type = 'button';
    element.className = 'diary-handoff-empty-action';
    element.setAttribute('aria-label', '创建新密钥');
    const image = document.createElement('img');
    image.src = resolveEchoIcon('plus') || '';
    image.alt = '';
    image.setAttribute('aria-hidden', 'true');
    const text = document.createElement('span'); text.textContent = '创建新密钥';
    element.append(image, text);
    element.addEventListener('click', openCreate);
    return element;
  };
  const copy = async value => {
    if (!navigator.clipboard?.writeText) throw new Error('请通过 HTTPS 访问后复制，或手动选择文本。');
    await navigator.clipboard.writeText(value);
    notify('已复制');
  };
  const shortcutConfig = (baseURL, token = '创建密钥时显示的一次性密钥') => JSON.stringify({
    notesURL: `${baseURL}/api/handoff/notes`,
    appendURL: `${baseURL}/api/handoff/append`,
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: { noteId: '选择的日记 ID', text: '剪贴板变量', format: 'text', requestId: '每次运行生成的 UUID' },
  }, null, 2);

  const closeModal = modal => {
    if (!modal) return;
    modal.echoModal.close();
    modal.remove();
  };
  const addModal = options => {
    const modal = createEchoModal(options);
    document.body.append(modal);
    modal.echoModal.open(root);
    return modal;
  };
  const showToken = (keyName, token) => {
    const content = document.createElement('div');
    content.className = 'diary-handoff-token-content';
    const key = document.createElement('label');
    key.className = 'diary-modal-field';
    key.textContent = '密钥 · 仅显示这一次';
    const tokenInput = document.createElement('input');
    tokenInput.value = token;
    tokenInput.readOnly = true;
    tokenInput.spellcheck = false;
    key.append(tokenInput);
    const address = document.createElement('label');
    address.className = 'diary-modal-field';
    address.textContent = '服务地址';
    const addressInput = document.createElement('input');
    addressInput.type = 'url';
    addressInput.value = location.origin;
    address.append(addressInput);
    const note = document.createElement('p');
    note.className = 'diary-handoff-muted';
    note.textContent = '请立即复制并保存。手机需使用可访问的局域网地址或 HTTPS 域名。';
    const actions = document.createElement('div');
    actions.className = 'diary-handoff-token-actions';
    content.append(key, address, note, actions);
    const modal = addModal({
      className: 'diary-handoff-token-modal', icon: 'key.icloud.fill', title: `「${keyName}」已创建`,
      addition: '密钥原文只在创建时提供，请立即复制保存。', content,
      actions: [
        { label: '复制密钥', icon: 'doc.on.doc', color: 'gray', close: false, onClick: () => copy(tokenInput.value) },
        { label: '复制快捷指令配置', icon: 'square.and.arrow.down', color: 'green', close: false, onClick: () => {
          try {
            const url = new URL(addressInput.value.trim());
            if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) throw new Error('请输入有效的 HTTP 或 HTTPS 地址');
            void copy(shortcutConfig(url.href.replace(/\/$/, ''), tokenInput.value));
          } catch (error) { notify(error.message); }
        } },
      ],
    });
    tokenModal = modal;
    modal.addEventListener('click', event => { if (event.target.matches('.echo-modal-overlay')) tokenModal = null; });
  };

  const openCreate = () => {
    if (createModal) closeModal(createModal);
    const content = document.createElement('form');
    content.className = 'diary-handoff-create-content';
    content.addEventListener('submit', event => event.preventDefault());
    const mark = document.createElement('div');
    mark.className = 'diary-handoff-create-mark';
    const markImage = document.createElement('img');
    markImage.src = resolveEchoIcon('key.icloud.fill') || '';
    markImage.alt = '';
    mark.append(markImage);
    const title = document.createElement('h2'); title.textContent = '创建新密钥';
    const description = document.createElement('p'); description.textContent = '输入信息完成密钥创建。';
    const intro = document.createElement('div'); intro.className = 'diary-handoff-create-intro'; intro.append(mark, title, description);
    const scopeLabel = document.createElement('span'); scopeLabel.className = 'diary-handoff-field-label'; scopeLabel.textContent = '密钥权限';
    const scope = createEchoSegmented({
      className: 'diary-handoff-scope', ariaLabel: '密钥权限', activeId: 1,
      items: [
        { label: '全部接力笔记', ariaLabel: '全部接力笔记' },
        { label: '一或多条接力笔记', ariaLabel: '一或多条接力笔记' },
      ],
    });
    const notesLabel = document.createElement('span'); notesLabel.className = 'diary-handoff-field-label'; notesLabel.textContent = '接力笔记';
    const noteList = document.createElement('div'); noteList.className = 'diary-handoff-create-notes';
    const handoffNotes = getHandoffNotes();
    const selectedNotes = new Set(handoffNotes.map(note => note.id));
    const scopeOptions = [...scope.querySelectorAll('button')];
    let selectedScope = 1;
    const renderNoteList = () => {
      noteList.replaceChildren();
      noteList.hidden = selectedScope === 0;
      for (const note of handoffNotes) {
        const row = document.createElement('button'); row.type = 'button'; row.className = 'diary-handoff-create-note';
        const copy = document.createElement('span'); copy.className = 'diary-handoff-create-note-copy';
        const noteTitle = document.createElement('strong'); noteTitle.textContent = note.title?.trim() || '无标题日记';
        const preview = document.createElement('span'); preview.textContent = String(note.body || '').replace(/\s+/g, ' ').slice(0, 88) || '暂无内容';
        const check = document.createElement('span'); check.className = 'diary-handoff-note-check';
        const checkIcon = document.createElement('img'); checkIcon.src = resolveEchoIcon('checkmark') || ''; checkIcon.alt = '';
        check.append(checkIcon); copy.append(noteTitle, preview); row.append(copy, check);
        row.setAttribute('aria-checked', String(selectedNotes.has(note.id))); row.setAttribute('role', 'checkbox');
        row.addEventListener('click', () => { if (selectedNotes.has(note.id)) selectedNotes.delete(note.id); else selectedNotes.add(note.id); renderNoteList(); });
        noteList.append(row);
      }
      if (!handoffNotes.length) {
        const empty = document.createElement('p'); empty.className = 'diary-handoff-empty-notes'; empty.textContent = '目前没有可选择的接力笔记。'; noteList.append(empty);
      }
    };
    scope.addEventListener('click', event => {
      const index = scopeOptions.indexOf(event.target.closest('button'));
      if (index >= 0) { selectedScope = index; renderNoteList(); }
    });
    renderNoteList();
    const error = document.createElement('p'); error.className = 'diary-handoff-form-error'; error.hidden = true; error.setAttribute('role', 'alert');
    content.append(intro, scopeLabel, scope, notesLabel, noteList, error);
    const submit = createEchoButton({ label: '添加新密钥', icon: 'person.badge.key.fill', color: 'green', type: 'button' });
    const cancel = createEchoButton({ label: '取消', icon: 'xmark', color: 'gray', type: 'button' });
    const modal = createEchoModal({
      id: 'handoff-create-key-modal', className: 'diary-handoff-create-modal', content,
      actions: [cancel, submit],
    });
    cancel.addEventListener('click', () => { if (createModal === modal) createModal = null; closeModal(modal); });
    submit.addEventListener('click', async () => {
      error.hidden = true;
      try {
        const keyName = `快捷指令 ${cachedKeys.length + 1}`;
        const noteIds = selectedScope === 0 ? [] : [...selectedNotes];
        if (selectedScope === 1 && !noteIds.length) { error.textContent = '至少选择一条接力笔记。'; error.hidden = false; return; }
        submit.disabled = true; cancel.disabled = true;
        const data = await requestJSON('/api/handoff/keys', { method: 'POST', body: JSON.stringify({ name: keyName, noteIds }) });
        createModal = null; closeModal(modal);
        showToken(keyName, data.token);
        await load();
      } catch (caught) { error.textContent = caught.message; error.hidden = false; submit.disabled = false; cancel.disabled = false; }
    });
    document.body.append(modal); modal.echoModal.open(root); createModal = modal;
  };

  const ask = (title, message, confirmLabel, action) => {
    const content = document.createElement('p'); content.className = 'diary-handoff-confirm-copy'; content.textContent = message;
    const modal = createEchoModal({
      className: 'diary-handoff-confirm-modal', title, icon: 'exclamationmark.triangle', content,
      actions: [
        { label: '取消', icon: 'xmark', color: 'gray' },
        { label: confirmLabel, icon: 'trash', color: 'red', close: false, onClick: async () => { const buttons = modal.querySelectorAll('.echo-modal__footer button'); buttons.forEach(item => { item.disabled = true; }); try { await action(); closeModal(modal); } catch (error) { notify(error.message); buttons.forEach(item => { item.disabled = false; }); } } },
      ],
    });
    document.body.append(modal); modal.echoModal.open(root);
  };

  const actionIcon = (container, icon, label, handler, color = '') => {
    const item = document.createElement('button');
    item.type = 'button';
    item.className = `diary-handoff-icon-action${color ? ` diary-handoff-icon-action--${color}` : ''}`;
    item.setAttribute('aria-label', label);
    item.title = label;
    const image = document.createElement('img');
    image.className = 'diary-handoff-icon-glyph';
    image.src = resolveEchoIcon(icon) || '';
    image.alt = '';
    image.setAttribute('aria-hidden', 'true');
    item.append(image);
    item.addEventListener('click', () => void Promise.resolve().then(handler).catch(error => notify(error.message)));
    container.append(item);
  };

  async function load() {
    const data = await requestJSON('/api/handoff/keys');
    if (libraryId && libraryId !== data.libraryId) closeModal(tokenModal);
    libraryId = data.libraryId; libraryName = data.libraryName; cachedKeys = data.keys || [];
    root.dataset.libraryId = libraryId;
    const summary = root.querySelector('#handoff-summary');
    summary.textContent = cachedKeys.length ? `已生成 ${cachedKeys.length} 个密钥 · ${libraryName}` : `创建密钥，与 iOS 设备共享接力笔记 · ${libraryName}`;
    keyList.replaceChildren();
    keyList.classList.toggle('is-empty', !cachedKeys.length);
    if (!cachedKeys.length) {
      keyList.append(createEmptyAction());
    } else {
      for (const key of cachedKeys) {
        const row = document.createElement('article'); row.className = 'diary-handoff-key';
        const icon = document.createElement('span'); icon.className = 'diary-handoff-key-icon';
        const image = document.createElement('img'); image.src = resolveEchoIcon('key.fill') || ''; image.alt = '';
        icon.append(image);
        const copy = document.createElement('div'); copy.className = 'diary-handoff-key-copy';
        const name = document.createElement('strong'); name.textContent = key.name;
        const description = document.createElement('span');
        const scope = key.noteId ? `${getNotes().find(note => note.id === key.noteId)?.title || '指定日记'} · 旧密钥` : key.noteIds?.length ? `${key.noteIds.length} 篇接力笔记` : '全部接力笔记';
        description.textContent = `${scope} · ${new Date(key.createdAt).toLocaleDateString()}`;
        copy.append(name, description);
        const actions = document.createElement('div'); actions.className = 'diary-handoff-key-actions';
        actionIcon(actions, 'pencil', `重命名密钥：${key.name}`, async () => {
          const content = document.createElement('label'); content.className = 'diary-modal-field'; content.textContent = '密钥名称';
          const input = document.createElement('input'); input.maxLength = 60; input.value = key.name; input.required = true; content.append(input);
          const error = document.createElement('p'); error.className = 'diary-handoff-form-error'; error.hidden = true; content.append(error);
          const modal = createEchoModal({ className: 'diary-handoff-rename-modal', icon: 'pencil', title: '重命名密钥', content,
            actions: [
              { label: '取消', icon: 'xmark', color: 'gray' },
              { label: '保存', icon: 'checkmark', color: 'green', close: false, onClick: async () => {
                if (!input.value.trim()) { error.textContent = '请输入密钥名称。'; error.hidden = false; input.focus(); return; }
                await requestJSON('/api/handoff/keys', { method: 'PATCH', body: JSON.stringify({ id: key.id, name: input.value.trim() }) });
                closeModal(modal); await load(); notify('密钥名称已更新');
              } },
            ],
          });
          document.body.append(modal); modal.echoModal.open(root);
        });
        actionIcon(actions, 'xmark', `撤销密钥：${key.name}`, () => ask('撤销此密钥', `撤销后「${key.name}」对应的快捷指令将无法再访问此资源库。`, '撤销密钥', async () => {
          await requestJSON(`/api/handoff/keys?id=${encodeURIComponent(key.id)}`, { method: 'DELETE' });
          await load(); notify('密钥已撤销');
        }));
        row.append(icon, copy, actions); keyList.append(row);
      }
    }
  }

  button(root.querySelector('#handoff-add'), '添加密钥', openCreate);
  button(root.querySelector('#handoff-get-shortcut'), '获取快捷指令', () => {
    const content = document.createElement('div'); content.className = 'diary-handoff-config-content';
    const intro = document.createElement('p'); intro.textContent = '将以下接口配置粘贴到「快捷指令」的“获取 URL 内容”操作中。创建密钥后，使用创建时显示的一次性密钥替换占位文本。';
    const config = document.createElement('pre'); config.textContent = shortcutConfig(location.origin);
    content.append(intro, config);
    const modal = createEchoModal({ className: 'diary-handoff-config-modal', icon: 'square.and.arrow.down', title: '快捷指令接口配置', content,
      actions: [
        { label: '关闭', icon: 'xmark', color: 'gray' },
        { label: '复制配置', icon: 'doc.on.doc', color: 'green', close: false, onClick: () => copy(config.textContent) },
      ],
    });
    document.body.append(modal); modal.echoModal.open(root);
  });
  button(root.querySelector('#handoff-delete-all'), '全部删除', () => ask('删除全部密钥', '所有接力密钥都会立即撤销，关联的快捷指令将无法继续使用。', '全部删除', async () => {
    for (const key of cachedKeys) await requestJSON(`/api/handoff/keys?id=${encodeURIComponent(key.id)}`, { method: 'DELETE' });
    await load(); notify('全部密钥已撤销');
  }), { className: 'diary-handoff-delete-all' });

  return {
    load,
    clear() { keyList.replaceChildren(); cachedKeys = []; closeModal(tokenModal); tokenModal = null; closeModal(createModal); createModal = null; },
  };
}
