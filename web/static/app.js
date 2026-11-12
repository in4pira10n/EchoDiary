import { installHandoff } from './handoff.js?v=20261107-modal-tone';
import { createEchoModal } from './diary-modal.js?v=20261107-modal-tone';
import { installMarkdownShortcuts } from './markdown-shortcuts.js?v=20261111-editor-fixes';
import { prepareChecklists, toggleChecklist, handleChecklistInput } from './checklist.js?v=20261111-editor-fixes';
import { showContextMenu, closeContextMenu, installContextMenu, fitContextMenu } from './context-menu.js?v=20261112-top-status-bar';
import { sanitizeHTML, markdownHTML, editorHTML, htmlText, htmlMarkdown, requireBodyString, pastedHTML } from './content.js?v=20261111-editor-fixes';
import { normalizeParagraphs, normalizeEditingLists } from './document-structure.js?v=20261111-editor-fixes';
import { createEchoButton } from '/kit/components/button/index.js';
import { createEchoTabs } from '/kit/components/tab/index.js';
import { createEchoSegmented } from '/kit/components/segmented/index.js';
import { createEchoSettingRow } from '/kit/components/setting-row/index.js';
import { createEchoLibraryCard } from '/kit/components/library-card/index.js';
import { createEchoMetric } from '/kit/components/metric/index.js';
import { installEchoIcons, resolveEchoIcon } from '/kit/components/icon.js';

const legacyNotesCacheKey = 'echodiary.ui-notes';

function isManagedImageURL(value) {
  return typeof value === 'string' && /^\/media\/img-[a-f0-9]{32}\.(?:jpg|png|gif|webp|avif|heic|bmp)$/.test(value);
}

function normalizeNoteImages(images) {
  return Array.isArray(images) ? images.filter(isManagedImageURL) : [];
}

function isEmptyNote(note) {
  const title = typeof note?.title === 'string' ? note.title.trim() : '';
  const body = typeof note?.body === 'string' ? note.body.trim() : '';
  const hasImage = Array.isArray(note?.images) && note.images.some(isManagedImageURL);
  const hasMarkdown = note?.bodyFormat === 'markdown' && typeof note.bodyMarkdown === 'string' && note.bodyMarkdown.trim();
  const hasChecklist = typeof note?.bodyHtml === 'string' && note.bodyHtml.includes('<input');
  return !title && !body && !hasImage && !hasMarkdown && !hasChecklist;
}

function normalizeNoteBodyValue(value) {
  return typeof value === 'string' ? value : '';
}

function normalizeNoteBodyData(note) {
  const body = normalizeNoteBodyValue(note.body);
  const bodyMarkdown = normalizeNoteBodyValue(note.bodyMarkdown);
  const bodyHtml = normalizeNoteBodyValue(note.bodyHtml);
  return { ...note, body, bodyMarkdown, bodyHtml };
}

function addPanguSpacing(value) {
  return String(value || '')
    .replace(/(：)(?=[A-Za-z0-9])/g, '$1 ')
    .replace(/([\u3400-\u9fff])([A-Za-z0-9])/g, '$1 $2')
    .replace(/([A-Za-z0-9])([\u3400-\u9fff])/g, '$1 $2');
}

function plainNoteBody(value) {
  return String(value || '').replace(/\[\[image:\/media\/img-[a-f0-9]{32}\.(?:jpg|png|gif|webp|avif|heic|bmp)\]\]/g, '[图片]');
}

let notes = [];
let trashNotes = [];
let cachedNotesExist = false;
let cachedNotes = [];
let notesCacheKey = 'echodiary.ui-notes';
let legacyCacheNeedsCleanup = false;
let activeLibraryId = '';
let liveUpdates = null;
let liveSyncPending = false;
let liveSyncPendingLibrary = false;
let liveSyncTask = Promise.resolve();
let liveRevision = null;
let liveRevisionTimer = null;
let liveRevisionCheckRunning = false;
let pendingWritingRefresh = null;

let selectedId = notes[0]?.id;
let selectedNotes = new Set();
let selectionAnchorId = '';
let selecting = false;
let page = 'list';
let writingReturnPage = 'list';
const handoffFolderId = "handoff";
let folders = [];
let notesView = 'folders';
let activeFolderId = null;
try { notesView = localStorage.getItem('echodiary.notes-view') === 'all' ? 'all' : 'folders'; } catch {}
let selectionReturnPage = 'list';
let writingNoteId = '';
let writingDateMode = 'created';
let writingView = 0;
let writingEditView = 0;
let writingHTMLBaseline = '';
let writingMarkdownBaseline = '';
let writingRenderedNoteId = '';
let toastTimer;
let imageUploadInProgress = false;
let activeInsertMenu = null;
let releaseInsertMenuLayout = null;
let savedWritingRange = null;
let photoViewerScale = 1;
let photoPinchStartDistance = 0;
let photoPinchStartScale = 1;
let authMode = 'login';
let setupSystemAppearance = false;
let authSubmitButton = null;
let addLibraryModal = null;
let libraryEditorModal = null;
let kehuaImportModal = null;
let noteSortRule = 'newest';
try {
  const saved = localStorage.getItem('echodiary.note-sort');
  if (['newest', 'oldest', 'modified-newest', 'modified-oldest', 'title'].includes(saved)) noteSortRule = saved;
} catch {}
const persistedNotes = new Map();
const persistenceQueues = new Map();
const notePersistenceIntent = new Set();

const $ = selector => document.querySelector(selector);
const list = $('#note-list');

function notify(message) {
  const toast = $('#diary-toast');
  toast.textContent = message;
  toast.classList.add('is-visible');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => toast.classList.remove('is-visible'), 2400);
}

function pendingWritesKey() {
  return `echodiary.pending-writes:${activeLibraryId || 'unknown'}`;
}

function readPendingWrites() {
  try {
    const value = JSON.parse(localStorage.getItem(pendingWritesKey()) || '{}');
    return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
  } catch { return {}; }
}

function storePendingWrite(id, snapshot) {
  try {
    const pending = readPendingWrites();
    pending[id] = snapshot;
    localStorage.setItem(pendingWritesKey(), JSON.stringify(pending));
  } catch { /* The regular note cache remains a second recovery path. */ }
}

function clearPendingWrite(id) {
  try {
    const pending = readPendingWrites();
    delete pending[id];
    if (Object.keys(pending).length) localStorage.setItem(pendingWritesKey(), JSON.stringify(pending));
    else localStorage.removeItem(pendingWritesKey());
  } catch {}
}

async function describeSaveError(response) {
  const detail = (await response.text()).trim().slice(0, 180);
  return detail ? `HTTP ${response.status}：${detail}` : `HTTP ${response.status}`;
}

function saveNotes() {
  const nonEmptyNotes = notes.filter(note => !isEmptyNote(note));
  try { localStorage.setItem(notesCacheKey, JSON.stringify(nonEmptyNotes)); } catch { /* Cache is optional. */ }
  const pending = [];
  notes.forEach(note => {
    if (!isEmptyNote(note)) pending.push(persistNote(note.id));
  });
  return Promise.all(pending);
}

function persistNote(id) {
  const current = notes.find(item => item.id === id);
  if (!current || isEmptyNote(current)) return Promise.resolve();
  notePersistenceIntent.add(id);
  const previous = persistenceQueues.get(id) || Promise.resolve();
  let pendingSnapshot = '';
  const next = previous.catch(() => {}).then(async () => {
    const note = notes.find(item => item.id === id);
    if (!note || isEmptyNote(note)) {
      if (persistedNotes.has(id) || notePersistenceIntent.has(id)) {
        const response = await fetch(`/api/notes/${encodeURIComponent(id)}`, { method: 'DELETE' });
        if (!response.ok && response.status !== 404) throw new Error(`delete note: ${response.status}`);
        persistedNotes.delete(id);
        notePersistenceIntent.delete(id);
      }
      return;
    }
    const snapshot = JSON.stringify(note);
    pendingSnapshot = snapshot;
    if (snapshot === persistedNotes.get(id)) {
      clearPendingWrite(id);
      return;
    }
    const response = await fetch(`/api/notes/${encodeURIComponent(id)}`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: snapshot,
    });
    if (!response.ok) throw new Error(await describeSaveError(response));
    const saved = await response.json();
    const accepted = JSON.parse(snapshot);
    accepted.createdAt ||= saved.createdAt;
    accepted.updatedAt = saved.updatedAt || accepted.updatedAt;
    note.createdAt ||= accepted.createdAt;
    note.updatedAt = accepted.updatedAt;
    if (writingNoteId === id) renderWritingDate();
    const acceptedSnapshot = JSON.stringify(accepted);
    persistedNotes.set(id, acceptedSnapshot);
    const latestSnapshot = JSON.stringify(note);
    if (latestSnapshot === acceptedSnapshot) clearPendingWrite(id);
    else storePendingWrite(id, latestSnapshot);
    try { localStorage.setItem(notesCacheKey, JSON.stringify(notes)); } catch {}
    if (latestSnapshot !== acceptedSnapshot) queueMicrotask(() => { void persistNote(id); });
  }).catch(error => {
    if (pendingSnapshot) storePendingWrite(id, pendingSnapshot);
    const reason = error instanceof TypeError
      ? '无法连接服务端，草稿已暂存在此设备'
      : `保存失败（${error.message}），草稿已暂存在此设备`;
    notify(reason);
  });
  persistenceQueues.set(id, next);
  next.finally(() => { if (persistenceQueues.get(id) === next) persistenceQueues.delete(id); });
  return next;
}

function deletePersistedNote(id) {
  if (!persistedNotes.has(id) && !notePersistenceIntent.has(id)) return Promise.resolve();
  const previous = persistenceQueues.get(id) || Promise.resolve();
  const next = previous.catch(() => {}).then(async () => {
    const response = await fetch(`/api/notes/${encodeURIComponent(id)}`, { method: 'DELETE' });
    if (!response.ok && response.status !== 404) throw new Error(`delete note: ${response.status}`);
    persistedNotes.delete(id);
    notePersistenceIntent.delete(id);
    clearPendingWrite(id);
  }).catch(error => { notify(`删除失败（${error.message}）`); });
  persistenceQueues.set(id, next);
  next.finally(() => { if (persistenceQueues.get(id) === next) persistenceQueues.delete(id); });
  return next;
}

async function loadTrash() {
  const response = await fetch('/api/trash', { headers: { Accept: 'application/json' } });
  if (!response.ok) throw new Error(`trash: ${response.status}`);
  const payload = await response.json();
  trashNotes = Array.isArray(payload.notes) ? payload.notes.map(note => ({ ...note, images: normalizeNoteImages(note.images) })) : [];
}

async function trashAction(id, action) {
  const response = await fetch('/api/trash', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id, action }),
  });
  if (!response.ok) throw new Error(`trash action: ${response.status}`);
  await loadNotes();
  await loadTrash();
  renderNotes();
  renderTrash();
}

async function loadNotes({ strict = false } = {}) {
  try {
    const response = await fetch('/api/notes', { cache: 'no-store', headers: { Accept: 'application/json' } });
    if (!response.ok) throw new Error(`notes: ${response.status}`);
    const payload = await response.json();
    if (!payload || !Array.isArray(payload.notes)) throw new Error('invalid notes response');

    const loadedNotes = payload.notes.map(note => ({
      ...normalizeNoteBodyData(note),
      images: normalizeNoteImages(note.images),
    }));
    const remoteNotes = loadedNotes.filter(note => !isEmptyNote(note));
    persistedNotes.clear();
    notePersistenceIntent.clear();
    loadedNotes.forEach(note => {
      persistedNotes.set(note.id, JSON.stringify(note));
      notePersistenceIntent.add(note.id);
    });
    if (payload.bootstrap === true) {
      const seed = remoteNotes.length
        ? []
        : (cachedNotesExist ? cachedNotes : []).filter(note => !isEmptyNote(note)).map(note => ({
          ...note,
          images: [...note.images],
          createdAt: note.createdAt || `创建于：${new Intl.DateTimeFormat('zh-CN', { dateStyle: 'long', timeStyle: 'short' }).format(new Date())}`,
        }));
      notes = remoteNotes.length ? remoteNotes : seed;
      if (!selectedId || !notes.some(note => note.id === selectedId)) selectedId = notes[0]?.id || '';
      renderNotes();

      const initialized = await fetch('/api/notes/initialize', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(seed),
      });
      if (!initialized.ok) throw new Error(`initialize notes: ${initialized.status}`);
      if (legacyCacheNeedsCleanup) {
        try { localStorage.removeItem(legacyNotesCacheKey); } catch {}
        legacyCacheNeedsCleanup = false;
      }
      notes.forEach(note => {
        persistedNotes.set(note.id, JSON.stringify(note));
        notePersistenceIntent.add(note.id);
      });
    } else {
      notes = remoteNotes;
      persistedNotes.clear();
      notePersistenceIntent.clear();
      notes.forEach(note => {
        persistedNotes.set(note.id, JSON.stringify(note));
        notePersistenceIntent.add(note.id);
      });
      if (!selectedId || !notes.some(note => note.id === selectedId)) selectedId = notes[0]?.id || '';
      renderNotes();
    }
    const pendingWrites = readPendingWrites();
    const pendingNotes = Object.entries(pendingWrites).flatMap(([id, snapshot]) => {
      try {
        const note = JSON.parse(snapshot);
        if (!note || note.id !== id || typeof note.title !== 'string' || typeof note.body !== 'string') return [];
        return [{ ...normalizeNoteBodyData(note), images: normalizeNoteImages(note.images) }];
      } catch { return []; }
    });
    if (pendingNotes.length) {
      const pendingById = new Map(pendingNotes.map(note => [note.id, note]));
      notes = [...notes.filter(note => !pendingById.has(note.id)), ...pendingNotes];
      if (!selectedId || !notes.some(note => note.id === selectedId)) selectedId = notes[0]?.id || '';
      renderNotes();
      await Promise.all(pendingNotes.map(note => persistNote(note.id)));
    }
    try { localStorage.setItem(notesCacheKey, JSON.stringify(notes.filter(note => !isEmptyNote(note)))); } catch {}
  } catch (error) {
    if (strict) throw error;
    /* Keep the local browser cache if the server database is unavailable. */
  }
}

async function loadLibraryInfo() {
  try {
    const response = await fetch('/api/library', { headers: { Accept: 'application/json' } });
    if (!response.ok) return;
    const library = await response.json();
    activeLibraryId = library.id || '';
    notesCacheKey = `echodiary.ui-notes:${library.id}`;
    folders = [];
    activeFolderId = null;
    try { folders = (await requestJSON('/api/folders')).folders || []; } catch (error) { notify(`无法读取文件夹：${error.message}`); }
    const readNotesCache = key => {
      try {
        const saved = JSON.parse(localStorage.getItem(key));
        if (!Array.isArray(saved) || !saved.every(note => note && typeof note.id === 'string' && typeof note.title === 'string' && typeof note.body === 'string' && Array.isArray(note.images))) return null;
        return saved.filter(note => !isEmptyNote(note)).map(note => ({ ...normalizeNoteBodyData(note), images: normalizeNoteImages(note.images) }));
      } catch { return null; }
    };
    cachedNotes = readNotesCache(notesCacheKey);
    if (cachedNotes === null) {
      cachedNotes = readNotesCache(legacyNotesCacheKey);
      legacyCacheNeedsCleanup = cachedNotes !== null;
    }
    if (cachedNotes !== null) {
      notes = cachedNotes.map(note => ({ ...note, images: [...note.images] }));
      cachedNotesExist = true;
    }
  } catch { /* Library details are informational; notes remain usable offline. */ }
}

function iconImage(name, className = '') {
  const image = document.createElement('img');
  image.src = resolveEchoIcon(name) || '';
  image.alt = '';
  image.setAttribute('aria-hidden', 'true');
  if (className) image.className = className;
  return image;
}

async function requestJSON(url, options = {}) {
  const response = await fetch(url, {
    ...options,
    headers: { Accept: 'application/json', ...(options.body ? { 'Content-Type': 'application/json' } : {}), ...(options.headers || {}) },
  });
  if (response.status === 204) return null;
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(payload.error || `请求失败（${response.status}）`);
  return payload;
}

function formatBytes(value) {
  const bytes = Math.max(0, Number(value) || 0);
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let size = bytes / 1024;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) { size /= 1024; unit++; }
  return `${size.toFixed(size < 10 ? 2 : 1)} ${units[unit]}`;
}

function dateLabel(value) {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '—' : new Intl.DateTimeFormat('zh-CN', { month: 'numeric', day: 'numeric' }).format(date);
}

function relativeDateLabel(value) {
  if (!value) return '尚无编辑';
  const timestamp = new Date(value).getTime();
  if (!Number.isFinite(timestamp)) return '尚无编辑';
  const minutes = Math.max(0, Math.floor((Date.now() - timestamp) / 60000));
  if (minutes < 1) return '刚刚';
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  const days = Math.floor(hours / 24);
  return days < 30 ? `${days} 天前` : dateLabel(value);
}

function parseNoteDate(value) {
  const direct = new Date(value);
  if (!Number.isNaN(direct.getTime())) return direct;
  const match = String(value || '').match(/(\d{4})\s*年\s*(\d{1,2})\s*月\s*(\d{1,2})\s*日(?:\s+(\d{1,2}):(\d{2})(?::(\d{2}))?)?/);
  if (!match) return null;
  const date = new Date(
    Number(match[1]), Number(match[2]) - 1, Number(match[3]),
    Number(match[4] || 0), Number(match[5] || 0), Number(match[6] || 0),
  );
  return Number.isNaN(date.getTime()) ? null : date;
}

function renderWritingDate() {
  const note = notes.find(item => item.id === writingNoteId);
  if (!note) return;
  const modified = writingDateMode === 'updated';
  const value = modified ? (note.updatedAt || note.createdAt) : note.createdAt;
  const date = parseNoteDate(value);
  const formatted = date ? `${date.getFullYear()}年${date.getMonth() + 1}月${date.getDate()}日 ${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}` : String(value || '').replace(/^(创建于|修改于)[：:]\s*/, '');
  $('#writing-created').textContent = addPanguSpacing(`${modified ? '修改于' : '创建于'}：${formatted}`);
}

$('#writing-created').addEventListener('click', () => {
  writingDateMode = writingDateMode === 'created' ? 'updated' : 'created';
  renderWritingDate();
});

function noteDateParts(value) {
  const date = parseNoteDate(value);
  if (!date) return null;
  return {
    time: new Intl.DateTimeFormat('zh-CN', { hour: 'numeric', minute: '2-digit' }).format(date),
    date: new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: 'numeric', day: 'numeric' }).format(date),
  };
}

function makeLibraryCard(card) {
  const addition = card.available ? card.path : `${card.path} · ${card.error || '暂不可用'}`;
  const element = createEchoLibraryCard({
    icon: 'books.vertical.fill',
    logo: card.avatar || '',
    title: card.name || '未命名日记',
    addition,
    attachment: [],
    active: card.active,
    primary: card.favorite,
    available: card.available,
    content: [
      () => createEchoMetric({ icon: 'calendar', label: '创建日期', value: dateLabel(card.createdAt) }),
      () => createEchoMetric({ icon: 'internaldrive.fill', label: '占用空间', value: formatBytes(card.dataBytes) }),
      () => createEchoMetric({ icon: 'pencil', label: '最近编辑', value: relativeDateLabel(card.modifiedAt) }),
      () => createEchoMetric({ icon: 'book.fill', label: '日记', value: card.notes }),
      () => createEchoMetric({ icon: 'photo', label: '图片', value: card.images }),
      () => createEchoMetric({ icon: 'bookmark.fill', label: '收藏', value: card.favorites }),
    ],
    onClick: () => openLibraryEditor(card),
  });
  installContextMenu(element, event => {
    closeInsertMenu();
    showContextMenu(event, '资源库操作', [
      { label: card.active ? '正在使用此资源库' : '切换到此资源库', icon: card.active ? 'checkmark.circle' : 'arrow.right.circle', disabled: card.active || !card.available, action: () => activateLibrary(card) },
      { label: '编辑资源库', icon: 'pencil', action: () => openLibraryEditor(card) },
      { label: card.favorite ? '取消星标' : '星标资源库', icon: card.favorite ? 'star.slash' : 'star', action: async () => {
        await requestJSON(`/api/libraries/${encodeURIComponent(card.id)}/favorite`, { method: 'PATCH', body: JSON.stringify({ favorite: !card.favorite }) });
        await loadLibraryCards();
      } },
      '-',
      { label: '复制资源库路径', icon: 'folder', action: () => copyContextText(card.path, '资源库路径已复制') },
      { label: '刷新统计', icon: 'arrow.clockwise', action: () => loadLibraryCards(true) },
    ]);
  });
  return element;
}

async function loadLibraryCards(refresh = false) {
  const container = $('#library-grid');
  try {
    const response = await requestJSON(`/api/libraries${refresh ? '?refresh=1' : ''}`);
    container.replaceChildren(...(response.libraries || []).map(makeLibraryCard));
    if (!response.libraries?.length) {
      const empty = document.createElement('p');
      empty.className = 'diary-library-empty';
      empty.textContent = '还没有日记资源库，点击下方按钮添加。';
      container.append(empty);
    }
  } catch (error) {
    container.replaceChildren();
    const message = document.createElement('p');
    message.className = 'diary-library-empty is-error';
    message.textContent = error.message;
    container.append(message);
  }
}

async function activateLibrary(card) {
  if (card.active) return;
  if (!card.available) { notify(card.error || '此资源库当前不可用'); return; }
  try {
    await Promise.all([...persistenceQueues.values()]);
    await requestJSON(`/api/libraries/${encodeURIComponent(card.id)}/activate`, { method: 'POST' });
    handoff.clear();
    persistedNotes.clear();
    notePersistenceIntent.clear();
    selectedId = '';
    notes = [];
    renderNotes();
    await loadLibraryInfo();
    await loadNotes();
    await loadLibraryCards();
    if (page === 'account') await handoff.load();
    notify(`已切换到「${card.name}」`);
  } catch (error) { notify(error.message); }
}

function openLibraryAvatarCrop(file) {
  return new Promise((resolve, reject) => {
    const content = document.createElement('div'); content.className = 'diary-avatar-crop-content';
    const stage = document.createElement('div'); stage.className = 'diary-avatar-crop-stage';
    const image = document.createElement('img'); image.alt = '头像裁剪预览'; stage.append(image);
    const toolbar = document.createElement('div'); toolbar.className = 'diary-avatar-crop-toolbar'; toolbar.setAttribute('aria-label', '缩放');
    const zoomOut = document.createElement('button'); zoomOut.type = 'button'; zoomOut.className = 'diary-avatar-crop-tool'; zoomOut.textContent = '−'; zoomOut.setAttribute('aria-label', '缩小');
    const controls = document.createElement('label'); controls.className = 'diary-avatar-crop-control';
    const controlText = document.createElement('span'); controlText.textContent = '缩放';
    const zoom = document.createElement('input'); zoom.type = 'range'; zoom.min = '1'; zoom.max = '3'; zoom.step = '.01'; zoom.value = '1';
    const zoomValue = document.createElement('output'); zoomValue.className = 'diary-avatar-crop-zoom-value'; zoomValue.textContent = '100%'; zoomValue.htmlFor = zoom.id = 'diary-avatar-crop-zoom';
    controls.append(controlText, zoom, zoomValue);
    const zoomIn = document.createElement('button'); zoomIn.type = 'button'; zoomIn.className = 'diary-avatar-crop-tool'; zoomIn.textContent = '+'; zoomIn.setAttribute('aria-label', '放大');
    const reset = document.createElement('button'); reset.type = 'button'; reset.className = 'diary-avatar-crop-tool diary-avatar-crop-reset'; reset.setAttribute('aria-label', '重置'); reset.title = '重置'; reset.append(iconImage('arrow.counterclockwise'));
    const hint = document.createElement('p'); hint.className = 'diary-avatar-crop-hint'; hint.textContent = '可拖动图片；手机端也支持双指缩放。';
    toolbar.append(zoomOut, controls, zoomIn, reset); content.append(stage, toolbar, hint);
    const objectURL = URL.createObjectURL(file); let modal;
    const crop = { x: 0, y: 0, zoom: 1, sx: 0, sy: 0, ox: 0, oy: 0, width: 0, height: 0, base: 1, dragging: false, pointers: new Map(), pinchDistance: 0, pinchZoom: 1 };
    const clamp = (value, min, max) => Math.min(max, Math.max(min, value));
    const pointerDistance = () => { const points = [...crop.pointers.values()]; return points.length < 2 ? 0 : Math.hypot(points[0].x - points[1].x, points[0].y - points[1].y); };
    const apply = () => { if (!crop.width) return; const size = stage.clientWidth || 280; crop.base = Math.max(size / crop.width, size / crop.height); const width = crop.width * crop.base * crop.zoom; const height = crop.height * crop.base * crop.zoom; const cx = (size - width) / 2; const cy = (size - height) / 2; crop.x = clamp(crop.x, size - width - cx, -cx); crop.y = clamp(crop.y, size - height - cy, -cy); image.style.width = `${width}px`; image.style.height = `${height}px`; image.style.left = `${cx + crop.x}px`; image.style.top = `${cy + crop.y}px`; };
    const finish = (value, failed = false) => { URL.revokeObjectURL(objectURL); modal?.echoModal.close(); modal?.remove(); failed ? reject(value) : resolve(value); };
    const render = () => { const canvas = document.createElement('canvas'); canvas.width = 512; canvas.height = 512; const ctx = canvas.getContext('2d'); const size = stage.clientWidth || 280; const scale = crop.base * crop.zoom; const cropSize = Math.min(crop.width, crop.height, size / scale); const ox = (size - crop.width * scale) / 2 + crop.x; const oy = (size - crop.height * scale) / 2 + crop.y; const sx = clamp(-ox / scale, 0, crop.width - cropSize); const sy = clamp(-oy / scale, 0, crop.height - cropSize); ctx.drawImage(image, sx, sy, cropSize, cropSize, 0, 0, 512, 512); canvas.toBlob(blob => blob ? finish(new File([blob], 'library-avatar.png', { type: 'image/png' })) : finish(new Error('头像裁剪失败'), true), 'image/png'); };
    image.onload = () => { crop.width = image.naturalWidth; crop.height = image.naturalHeight; setZoom(crop.zoom); }; image.src = objectURL;
    const setZoom = value => { crop.zoom = clamp(Number(value) || 1, Number(zoom.min), Number(zoom.max)); zoom.value = String(crop.zoom); zoomValue.textContent = `${Math.round(crop.zoom * 100)}%`; const fraction = (crop.zoom - Number(zoom.min)) / (Number(zoom.max) - Number(zoom.min)); zoom.style.setProperty('--range-progress', `${fraction * 100}%`); apply(); };
    zoom.oninput = () => setZoom(zoom.value);
    zoomOut.onclick = () => setZoom(crop.zoom - .1);
    zoomIn.onclick = () => setZoom(crop.zoom + .1);
    reset.onclick = () => { crop.x = 0; crop.y = 0; setZoom(1); };
    stage.onwheel = event => { event.preventDefault(); setZoom(crop.zoom + (event.deltaY < 0 ? .08 : -.08)); };
    stage.onpointerdown = event => { event.preventDefault(); crop.pointers.set(event.pointerId, { x: event.clientX, y: event.clientY }); stage.setPointerCapture(event.pointerId); if (crop.pointers.size >= 2) { crop.dragging = false; crop.pinchDistance = pointerDistance(); crop.pinchZoom = crop.zoom; return; } crop.dragging = true; crop.sx = event.clientX; crop.sy = event.clientY; crop.ox = crop.x; crop.oy = crop.y; };
    stage.onpointermove = event => { if (!crop.pointers.has(event.pointerId)) return; crop.pointers.set(event.pointerId, { x: event.clientX, y: event.clientY }); if (crop.pointers.size >= 2) { const distance = pointerDistance(); if (crop.pinchDistance > 0 && distance > 0) setZoom(crop.pinchZoom * distance / crop.pinchDistance); return; } if (crop.dragging) { crop.x = crop.ox + event.clientX - crop.sx; crop.y = crop.oy + event.clientY - crop.sy; apply(); } };
    const releasePointer = event => { crop.pointers.delete(event.pointerId); crop.dragging = false; try { stage.releasePointerCapture(event.pointerId); } catch (_) {} if (crop.pointers.size === 1) { const remaining = [...crop.pointers.values()][0]; crop.dragging = true; crop.sx = remaining.x; crop.sy = remaining.y; crop.ox = crop.x; crop.oy = crop.y; } };
    stage.onpointerup = releasePointer; stage.onpointercancel = releasePointer;
    modal = createEchoModal({ id: 'diary-library-avatar-crop-modal', className: 'diary-avatar-crop-modal', icon: 'crop', title: '裁剪资源库头像', addition: '拖动图片调整构图，或使用缩放控制；裁剪框内的内容会作为 1:1 资源库头像。', content, actions: [{ label: '取消', icon: 'xmark', color: 'gray', close: false, onClick: () => finish(new Error('cancelled'), true) }, { label: '使用裁剪结果', icon: 'checkmark', color: 'green', close: false, onClick: render }] });
    document.body.append(modal); modal.echoModal.open($('#library-grid')); apply();
  });
}

function closeLibraryEditor() {
  if (!libraryEditorModal) return;
  const modal = libraryEditorModal;
  libraryEditorModal = null;
  modal.echoModal.close();
  modal.remove();
}

function openLibraryEditor(card) {
  closeLibraryEditor();

  let avatarValue = card.avatar || '';
  let avatarChanged = false;

  const fields = document.createElement('form');
  fields.className = 'diary-modal-fields diary-library-editor-fields';
  fields.addEventListener('submit', event => event.preventDefault());

  const avatarField = document.createElement('div'); avatarField.className = 'diary-library-editor-avatar';
  const avatarPreview = document.createElement('button'); avatarPreview.type = 'button'; avatarPreview.className = 'diary-library-avatar-preview'; avatarPreview.setAttribute('aria-label', '编辑资源库头像');
  const avatarWrap = document.createElement('div'); avatarWrap.className = 'diary-library-avatar-wrap';
  const removeAvatar = createEchoButton({ icon: 'xmark', size: 'icon', variant: 'switch', className: 'diary-attachment-remove diary-library-avatar-remove', ariaLabel: '移除头像' });
  const avatarInput = document.createElement('input'); avatarInput.type = 'file'; avatarInput.accept = 'image/png,image/jpeg,image/webp'; avatarInput.hidden = true;
  const renderAvatar = () => { const placeholder = iconImage('books.vertical.fill', 'diary-library-avatar-placeholder'); avatarPreview.replaceChildren(avatarValue ? Object.assign(document.createElement('img'), { src: avatarValue, alt: '资源库头像' }) : placeholder); removeAvatar.hidden = !avatarValue; };
  avatarPreview.onclick = () => avatarInput.click();
  removeAvatar.onclick = () => { avatarValue = ''; avatarChanged = true; renderAvatar(); };
  avatarInput.onchange = async () => { const file = avatarInput.files?.[0]; avatarInput.value = ''; if (!file) return; try { const cropped = await openLibraryAvatarCrop(file); const reader = new FileReader(); reader.onload = () => { avatarValue = String(reader.result || ''); avatarChanged = true; renderAvatar(); }; reader.readAsDataURL(cropped); } catch (_) {} };
  avatarWrap.append(avatarPreview, removeAvatar);
  avatarField.append(avatarWrap, avatarInput); renderAvatar();

  const nameField = createLibraryInput('资源库名称', 'diary-library-editor-name', card.name || '');
  nameField.input.maxLength = 120;
  nameField.input.autocomplete = 'off';
  const pathField = createLibraryInput('资源库文件夹路径（主机）', 'diary-library-editor-path', card.path || '');
  pathField.input.placeholder = '/Volumes/Diary 或 C:\\Users\\Name\\Diary';

  const favoriteRow = document.createElement('label');
  favoriteRow.className = 'diary-library-editor-toggle';
  const favoriteCopy = document.createElement('span');
  const favoriteTitle = document.createElement('span');
  favoriteTitle.className = 'diary-library-editor-toggle-title';
  favoriteTitle.textContent = '星标资源库';
  const favoriteDescription = document.createElement('span');
  favoriteDescription.className = 'diary-library-editor-toggle-description';
  favoriteDescription.textContent = '在资源库列表中优先识别此资源库';
  favoriteCopy.append(favoriteTitle, favoriteDescription);
  const favoriteSwitch = document.createElement('span');
  favoriteSwitch.className = 'diary-switch';
  const favoriteInput = document.createElement('input');
  favoriteInput.type = 'checkbox';
  favoriteInput.checked = Boolean(card.favorite);
  favoriteInput.setAttribute('aria-label', '星标资源库');
  const favoriteTrack = document.createElement('span');
  favoriteTrack.setAttribute('aria-hidden', 'true');
  favoriteSwitch.append(favoriteInput, favoriteTrack);
  favoriteRow.append(favoriteCopy, favoriteSwitch);

  const error = document.createElement('p');
  error.className = 'diary-library-modal-error';
  error.hidden = true;
  error.setAttribute('role', 'alert');
  fields.append(avatarField, nameField.label, pathField.label, favoriteRow, error);

  let modal;
  const setBusy = busy => {
    modal?.querySelectorAll('.echo-modal__footer button').forEach(button => { button.disabled = busy; });
    nameField.input.disabled = busy;
    pathField.input.disabled = busy;
    favoriteInput.disabled = busy;
    removeAvatar.disabled = busy; avatarPreview.disabled = busy;
  };
  const close = () => closeLibraryEditor();
  const save = async activate => {
    const name = nameField.input.value.trim();
    if (!name) {
      error.textContent = '资源库名称不能为空。';
      error.hidden = false;
      nameField.input.focus({ preventScroll: true });
      return;
    }
    error.textContent = '';
    setBusy(true);
    try {
      const path = pathField.input.value.trim();
      if (!path) {
        error.textContent = '资源库路径不能为空。';
        error.hidden = false;
        setBusy(false);
        pathField.input.focus({ preventScroll: true });
        return;
      }
      if (path !== card.path) {
        await requestJSON(`/api/libraries/${encodeURIComponent(card.id)}/relocate`, {
          method: 'PATCH', body: JSON.stringify({ path }),
        });
      }
      if (name !== card.name) {
        await requestJSON(`/api/libraries/${encodeURIComponent(card.id)}/rename`, {
          method: 'PATCH', body: JSON.stringify({ name }),
        });
      }
      if (favoriteInput.checked !== Boolean(card.favorite)) {
        await requestJSON(`/api/libraries/${encodeURIComponent(card.id)}/favorite`, {
          method: 'PATCH', body: JSON.stringify({ favorite: favoriteInput.checked }),
        });
      }
      if (avatarChanged) {
        await requestJSON(`/api/libraries/${encodeURIComponent(card.id)}/avatar`, {
          method: 'PATCH', body: JSON.stringify({ avatar: avatarValue }),
        });
      }
      if (activate && !card.active) {
        close();
        await activateLibrary({ ...card, name, path });
        return;
      }
      close();
      await refreshDiaryData();
      notify(activate && !card.active ? `已切换到「${name}」` : '资源库已保存');
    } catch (caught) {
      error.textContent = caught.message;
      error.hidden = false;
      setBusy(false);
    }
  };

  const actions = [
    { label: '取消', icon: 'xmark', color: 'gray', close: false, onClick: close },
  ];
  if (!card.active && card.available) {
    actions.push({ label: '切换', icon: 'arrow.right', color: 'gray', close: false, onClick: () => void save(true) });
  }
  actions.push({ label: '保存', icon: 'checkmark', color: 'green', close: false, onClick: () => void save(false) });
  modal = createEchoModal({
    id: 'edit-diary-library-modal',
    className: 'diary-library-editor-modal',
    icon: 'books.vertical.fill',
    title: '',
    addition: '',
    content: fields,
    actions,
  });
  libraryEditorModal = modal;
  document.body.append(modal);
  modal.echoModal.open($('#library-grid'));
}

function createLibraryInput(labelText, id, value = '') {
  const label = document.createElement('label');
  label.className = 'diary-modal-field';
  label.textContent = labelText;
  const input = document.createElement('input');
  input.id = id;
  input.required = true;
  input.value = value;
  input.autocomplete = 'off';
  label.append(input);
  return { label, input };
}

function openAddLibraryModal() {
  if (!addLibraryModal) {
    const content = document.createElement('div');
    content.className = 'diary-modal-fields';
    const nameField = createLibraryInput('资源库名称', 'diary-new-library-name');
    const pathField = createLibraryInput('资源库文件夹路径（主机）', 'diary-new-library-path');
    pathField.input.placeholder = '/Volumes/Diary 或 C:\\Users\\Name\\Diary';
    const note = document.createElement('p');
    note.className = 'diary-modal-note';
    note.textContent = '已有 .echodiary 目录会直接复用，不会清空或覆盖其中的日记和图片。路径不存在时会创建新文件夹。';
    const error = document.createElement('p');
    error.className = 'diary-library-modal-error';
    error.setAttribute('role', 'alert');
    content.append(nameField.label, pathField.label, note, error);
    const form = document.createElement('form');
    form.className = 'diary-modal-fields';
    form.append(content);
    addLibraryModal = createEchoModal({
      id: 'add-diary-library-modal', icon: 'books.vertical.fill', title: '添加日记资源库',
      addition: '添加到 EchoDiary 后即可在设备间切换。', content: form,
      actions: [
        { label: '取消', icon: 'xmark', color: 'gray' },
        { label: '添加', icon: 'plus', color: 'green', close: false, onClick: () => form.requestSubmit() },
      ],
    });
    form.addEventListener('submit', async event => {
      event.preventDefault();
      error.textContent = ''; error.hidden = true;
      if (!nameField.input.value.trim() || !pathField.input.value.trim()) {
        error.textContent = '请填写资源库名称和主机文件夹路径。';
        error.hidden = false;
        return;
      }
      const actions = addLibraryModal.querySelectorAll('.echo-modal__footer button');
      actions.forEach(button => { button.disabled = true; });
      try {
        await requestJSON('/api/libraries', {
          method: 'POST',
          body: JSON.stringify({ name: nameField.input.value.trim(), path: pathField.input.value.trim() }),
        });
        addLibraryModal.echoModal.close();
        await refreshDiaryData();
        notify('日记资源库已添加');
      } catch (err) {
        error.textContent = err.message;
        error.hidden = false;
      } finally {
        actions.forEach(button => { button.disabled = false; });
      }
    });
    document.body.append(addLibraryModal);
  }
  addLibraryModal.echoModal.open($('#settings-add-action'));
}

async function refreshDiaryData() {
  persistedNotes.clear();
  notePersistenceIntent.clear();
  await loadLibraryInfo();
  await loadNotes();
  await loadLibraryCards(true);
  handoff.clear();
  if (page === 'account') await handoff.load();
}

async function syncLiveData(event = {}) {
  if (document.querySelector('#diary-app')?.hidden) return;
  if (event.kind !== 'library' && activeLibraryId) {
    try {
      const current = await requestJSON('/api/library');
      if (current.id && current.id !== activeLibraryId) event = { ...event, kind: 'library' };
    } catch {}
  }
  if (page === 'writing' && event.kind === 'library') {
    liveSyncPending = true;
    liveSyncPendingLibrary = true;
    return;
  }
  liveSyncTask = liveSyncTask.catch(() => {}).then(async () => {
    await Promise.all([...persistenceQueues.values()]);
    const activeWritingNote = page === 'writing' ? notes.find(note => note.id === writingNoteId) : null;
    const activeWritingSnapshot = activeWritingNote ? JSON.stringify(activeWritingNote) : '';
    const activeWritingWasDirty = Boolean(activeWritingNote && activeWritingSnapshot !== persistedNotes.get(activeWritingNote.id));
    const writingEditorFocused = writingInteractionActive();
    if (event.kind === 'library') {
      await loadLibraryInfo();
      liveUpdates?.close();
      liveUpdates = null;
      await loadLibraryCards(true);
    }
    await loadNotes({ strict: true });
    if (activeWritingNote) {
      const remoteWritingNote = notes.find(note => note.id === activeWritingNote.id);
      if (activeWritingWasDirty || writingEditorFocused) {
        const latestRemoteWritingNote = remoteWritingNote
          ? { ...remoteWritingNote, images: [...(remoteWritingNote.images || [])] }
          : null;
        if (remoteWritingNote) Object.assign(remoteWritingNote, activeWritingNote);
        else notes.unshift(activeWritingNote);
        if (!activeWritingWasDirty && remoteWritingNote) {
          pendingWritingRefresh = {
            id: activeWritingNote.id,
            localSnapshot: activeWritingSnapshot,
            remoteNote: latestRemoteWritingNote,
          };
        } else if (activeWritingWasDirty) {
          liveSyncPending = true;
        }
        if (!remoteWritingNote) liveSyncPending = true;
      } else if (remoteWritingNote && activeWritingSnapshot !== JSON.stringify(remoteWritingNote)) {
        refreshWritingEditor(remoteWritingNote);
      }
    }
    folders = (await requestJSON('/api/folders')).folders || [];
    if (page === 'trash') {
      await loadTrash();
      renderTrash();
    }
    if (page === 'account') {
      await loadLibraryCards(true);
      await handoff.load().catch(() => {});
    }
    renderNotes();
    if (Number.isFinite(Number(event.revision)) && event.libraryId === activeLibraryId) {
      liveRevision = Number(event.revision);
    }
    if (!liveUpdates) startLiveUpdates();
  }).then(() => true).catch(() => false);
  return liveSyncTask;
}

function startLiveUpdates() {
  liveUpdates?.close();
  liveUpdates = new EventSource('/api/events');
  liveUpdates.addEventListener('ready', event => {
    try {
      const state = JSON.parse(event.data);
      if (state.libraryId === activeLibraryId) void syncLiveData(state);
    } catch {}
  });
  liveUpdates.addEventListener('change', event => {
    let change = {};
    try { change = JSON.parse(event.data); } catch {}
    if (change.kind === 'library' || !change.libraryId || change.libraryId === activeLibraryId) void syncLiveData(change);
  });
  if (!liveRevisionTimer) liveRevisionTimer = setInterval(() => { void checkLiveRevision(); }, 3000);
  void checkLiveRevision();
}

async function checkLiveRevision() {
  if (liveRevisionCheckRunning || document.hidden || $('#diary-app')?.hidden || !activeLibraryId) return;
  liveRevisionCheckRunning = true;
  try {
    const response = await fetch('/api/revision', { cache: 'no-store', headers: { Accept: 'application/json' } });
    if (!response.ok) throw new Error(`revision: ${response.status}`);
    const state = await response.json();
    const revision = Number(state.revision);
    if (state.libraryId !== activeLibraryId) {
      liveRevision = null;
      await syncLiveData({ kind: 'library', ...state });
    } else if (Number.isFinite(revision) && (liveRevision === null || revision !== liveRevision)) {
      await syncLiveData({ kind: 'notes', ...state });
    }
  } catch {
    // EventSource reconnects automatically; this lightweight check is its recovery path.
  } finally {
    liveRevisionCheckRunning = false;
  }
}

document.addEventListener('visibilitychange', () => {
  if (!document.hidden) void checkLiveRevision();
});

window.addEventListener('online', () => { void saveNotes(); });

function refreshWritingEditor(note) {
  if (!note || note.id !== writingNoteId || page !== 'writing') return;
  $('#writing-title').value = note.title || '';
  $('#writing-markdown').value = normalizeNoteBodyValue(note.bodyMarkdown);
  writingMarkdownBaseline = $('#writing-markdown').value;
  renderWritingEditor(note.body);
  renderWritingPreview(note.body);
  renderWritingImages();
  renderWritingDate();
  pendingWritingRefresh = null;
}

function writingInteractionActive() {
  return page === 'writing' && Boolean(
    $('#writing-view').contains(document.activeElement)
    || $('#writing-actions').contains(document.activeElement)
    || activeInsertMenu || document.querySelector('.diary-context-menu')
  );
}

function applyPendingWritingRefresh() {
  if (writingInteractionActive()) return;
  const pending = pendingWritingRefresh;
  if (!pending || page !== 'writing' || pending.id !== writingNoteId) return;
  const current = notes.find(note => note.id === pending.id);
  if (current && JSON.stringify(current) === pending.localSnapshot) {
    const index = notes.findIndex(note => note.id === pending.id);
    notes[index] = pending.remoteNote;
    refreshWritingEditor(pending.remoteNote);
  } else {
    pendingWritingRefresh = null;
  }
}

for (const editor of [$('#writing-title'), $('#writing-body'), $('#writing-markdown')]) {
  editor.addEventListener('blur', () => setTimeout(applyPendingWritingRefresh, 0));
}

async function openKehuaImportModal() {
  if (kehuaImportModal?.dataset.busy === 'true') {
    kehuaImportModal.echoModal.open($('#import-kehua'));
    return;
  }
  const trigger = $('#import-kehua');
  trigger.disabled = true;
  try {
    const target = await requestJSON('/api/library');
    kehuaImportModal?.remove();
    const form = document.createElement('form');
    form.className = 'diary-modal-fields';
    const source = createLibraryInput('可话导出文件夹路径（主机）', 'kehua-import-path');
    source.input.required = false;
    source.input.placeholder = '/Users/Name/Documents/可话-个人动态-昵称';
    const hint = document.createElement('p');
    hint.className = 'diary-modal-note';
    hint.textContent = '填写运行 EchoDiary 的电脑上的绝对路径，选择含「我的动态」的已解压文件夹。每条动态单独导入，标题留空，保留完整正文、段落、原日期和图片位置；不会修改源文件。重复条目会跳过，视频和缺失图片的引用会保留并报告。';
    const status = document.createElement('p');
    status.className = 'diary-modal-note';
    status.setAttribute('role', 'status');
    status.setAttribute('aria-live', 'polite');
    const error = document.createElement('p');
    error.className = 'diary-library-modal-error';
    error.setAttribute('role', 'alert');
    const details = document.createElement('details');
    details.className = 'diary-modal-note';
    details.hidden = true;
    const summary = document.createElement('summary');
    const warnings = document.createElement('ul');
    details.append(summary, warnings);
    form.append(source.label, hint, status, details, error);
    const modal = createEchoModal({
      id: 'kehua-import-modal', icon: 'square.and.arrow.down', title: '导入可话',
      addition: `导入到「${target.name || '当前资源库'}」`, content: form,
      actions: [
        { label: '关闭', icon: 'xmark', color: 'gray' },
        { label: '导入', icon: 'square.and.arrow.down', color: 'green', close: false, onClick: () => form.requestSubmit() },
      ],
    });
    kehuaImportModal = modal;
    form.addEventListener('submit', async event => {
      event.preventDefault();
      if (modal.dataset.busy === 'true') return;
      const path = source.input.value.trim();
      if (!path) { error.textContent = '请填写主机上的导出文件夹路径。'; return; }
      modal.dataset.busy = 'true';
      source.input.disabled = true;
      const actions = modal.querySelectorAll('.echo-modal__footer button');
      actions.forEach(button => { button.disabled = true; });
      error.textContent = '';
      details.hidden = true;
      warnings.replaceChildren();
      status.textContent = '正在读取动态、复制图片并保存条目，请稍候……';
      try {
        await Promise.all([...persistenceQueues.values()]);
        const result = await requestJSON('/api/imports/kehua', {
          method: 'POST', body: JSON.stringify({ path, libraryId: target.id }),
        });
        status.textContent = `共 ${result.total} 条 · 已导入 ${result.imported} 条 · 重复跳过 ${result.skipped} 条 · 空条目 ${result.empty} 条 · 图片 ${result.images} 张`;
        if (result.warnings?.length) {
          summary.textContent = `${result.warnings.length} 项附件提示（原引用已保留）`;
          warnings.replaceChildren(...result.warnings.map(message => {
            const item = document.createElement('li');
            item.textContent = message;
            return item;
          }));
          details.hidden = false;
        }
        notify(`可话导入完成：新增 ${result.imported} 条`);
        try { await refreshDiaryData(); }
        catch { error.textContent = '导入已保存，页面刷新失败，请手动刷新。'; }
      } catch (err) {
        status.textContent = '';
        error.textContent = err.message;
      } finally {
        modal.dataset.busy = 'false';
        source.input.disabled = false;
        actions.forEach(button => { button.disabled = false; });
      }
    });
    document.body.append(modal);
    modal.echoModal.open(trigger);
  } catch (error) { notify(error.message); }
  finally { trigger.disabled = false; }
}

function imageFor(url) {
  const image = document.createElement('img');
  image.src = url;
  image.alt = '';
  image.loading = 'lazy';
  image.decoding = 'async';
  image.draggable = false;
  return image;
}

function returnFromWriting() {
  if (!finishWritingDraft()) return;
  setPage(writingReturnPage);
  renderNotes();
  if (liveSyncPending || pendingWritingRefresh) {
    const libraryChange = liveSyncPendingLibrary;
    liveSyncPending = false;
    liveSyncPendingLibrary = false;
    pendingWritingRefresh = null;
    void syncLiveData({ kind: libraryChange ? 'library' : 'notes' });
  }
}

async function updateFolders(input) {
  const response = await requestJSON('/api/folders', { method: 'POST', body: JSON.stringify(input) });
  folders = response.folders || [];
  if (!folders.length || (activeFolderId && !folders.some(folder => folder.id === activeFolderId))) activeFolderId = null;
  renderNotes();
}

function editFolder(folder = null, afterCreate = null) {
  const field = createLibraryInput('文件夹名称', 'folder-name', folder?.name || '');
  field.input.maxLength = 120;
  field.input.placeholder = '为文件夹命名';
  const content = document.createElement('form'); content.className = 'diary-modal-fields';
  const error = document.createElement('p'); error.className = 'diary-library-modal-error'; error.hidden = true; error.setAttribute('role', 'alert');
  content.append(field.label, error);
  const modal = createEchoModal({
    id: 'folder-editor-modal', title: folder ? '重命名文件夹' : '新建文件夹', content,
    actions: [{ label: '取消', color: 'gray' }, { label: '保存', icon: 'checkmark', color: 'green', close: false, onClick: save }],
  });
  async function save() {
    const buttons = [...modal.querySelectorAll('button')]; buttons.forEach(button => button.disabled = true);
    try {
      const id = folder?.id || crypto.randomUUID();
      await updateFolders({ action: folder ? 'rename' : 'create', id, name: field.input.value.trim() });
      if (!folder) { notesView = 'folders'; activeFolderId = null; saveNotesView(); }
      modal.echoModal.close(); modal.remove(); renderNotes();
      if (afterCreate) await afterCreate(id).catch(error => notify(error.message));
    } catch (caught) { error.textContent = caught.message; error.hidden = false; buttons.forEach(button => button.disabled = false); }
  }
  content.onsubmit = event => { event.preventDefault(); void save(); };
  document.body.append(modal); modal.echoModal.open(); field.input.focus();
}

function deleteFolder(folder) {
  const modal = createEchoModal({
    id: 'folder-delete-modal', title: `删除“${folder.name}”？`, addition: '文件夹中的日记会保留在“所有笔记”中。',
    actions: [{ label: '取消', color: 'gray' }, { label: '删除文件夹', icon: 'trash', color: 'red', close: false, onClick: async () => {
      try { await updateFolders({ action: 'delete', id: folder.id }); modal.echoModal.close(); modal.remove(); }
      catch (error) { notify(error.message); }
    } }],
  });
  document.body.append(modal); modal.echoModal.open();
}

function openMoveToFolder(noteIds) {
  const content = document.createElement('div'); content.className = 'diary-folder-picker';
  const destinations = document.createElement('div'); destinations.className = 'diary-folder-destinations'; destinations.setAttribute('role', 'group'); destinations.setAttribute('aria-label', '选择目标文件夹');
  const error = document.createElement('p'); error.className = 'diary-library-modal-error'; error.hidden = true; error.setAttribute('role', 'alert');
  const modal = createEchoModal({ id: 'folder-picker-modal', className: 'diary-folder-picker-modal', title: '移到文件夹', content, actions: [{ label: '取消', color: 'gray' }] });
  const move = async id => {
    content.querySelectorAll('button').forEach(button => button.disabled = true);
    error.hidden = true; content.setAttribute('aria-busy', 'true');
    try {
      await updateFolders({ action: 'move', id, noteIds });
      modal.echoModal.close(); modal.remove();
      if (selecting) stopSelection();
      notify(id ? '已移到文件夹' : '已移出文件夹');
    } catch (caught) {
      if (!modal.isConnected) notify(caught.message);
      error.textContent = caught.message; error.hidden = false;
      content.querySelectorAll('button').forEach(button => button.disabled = button.dataset.current === 'true');
    } finally { content.removeAttribute('aria-busy'); }
  };
  for (const folder of [{ id: '', name: '根目录' }, ...folders]) {
    const current = noteIds.length > 0 && noteIds.every(id => folder.id ? folder.noteIds.includes(id) : !folders.some(item => item.noteIds.includes(id)));
    const row = document.createElement('button'); row.type = 'button'; row.className = 'diary-folder-destination'; row.disabled = current; row.dataset.current = String(current); row.setAttribute('aria-label', folder.name);
    const icon = document.createElement('span'); icon.className = 'diary-folder-destination-icon'; icon.append(iconImage('folder.fill'));
    const copy = document.createElement('span'); copy.className = 'diary-folder-destination-copy';
    const name = document.createElement('span'); name.className = 'diary-folder-destination-name'; name.textContent = folder.name;
    const description = document.createElement('span'); description.className = 'diary-folder-destination-description'; description.textContent = folder.id ? `${notes.filter(note => !isEmptyNote(note) && folder.noteIds.includes(note.id)).length} 篇日记` : '所有笔记 · 移出自定义文件夹'; copy.append(name, description);
    const state = document.createElement('span'); state.className = 'diary-folder-destination-state';
    if (current) { state.textContent = '当前位置'; state.append(iconImage('checkmark')); }
    else state.append(iconImage('chevron.right'));
    row.append(icon, copy, state); row.onclick = () => void move(folder.id); destinations.append(row);
  }
  const create = document.createElement('button'); create.type = 'button'; create.className = 'diary-folder-create-row'; create.append(iconImage('folder.badge.plus'), document.createTextNode('新建文件夹'));
  create.onclick = () => { modal.echoModal.close(); modal.remove(); editFolder(null, move); };
  content.append(destinations, create, error);
  document.body.append(modal); modal.echoModal.open();
}

function saveNotesView() {
  try { localStorage.setItem('echodiary.notes-view', notesView); } catch {}
}

function syncNotesViewIcons() {
  $('#notes-view-switch').querySelectorAll('.echo-segmented__option').forEach((button, index) => {
    const name = index === 0 ? 'folder' : 'all';
    const selected = button.getAttribute('aria-pressed') === 'true';
    button.querySelector('img').src = `/svg/bottom-tab-${name}${selected ? '-selected' : ''}.svg`;
  });
}

function renderFolderNavigation() {
  const isHome = page === 'list';
  const folder = folders.find(item => item.id === activeFolderId);
  const showGrid = isHome && notesView === 'folders' && activeFolderId === null && !selecting;
  $('#notes-view-switch').hidden = !isHome || selecting;
  const addButton = $('#add-action button');
  if (addButton) {
    addButton.setAttribute('aria-label', showGrid ? '新建文件夹' : '新建笔记');
    addButton.title = showGrid ? '新建文件夹' : '新建笔记';
  }
  $('#folder-back').hidden = !isHome || notesView !== 'folders' || activeFolderId === null || selecting;
  const viewControl = $('#notes-view-switch .echo-segmented')?.echoSegmented;
  const viewIndex = notesView === 'folders' ? 0 : 1;
  if (viewControl && viewControl.getOptions().activeId !== viewIndex) viewControl.setActive(viewIndex);
  syncNotesViewIcons();
  $('#folder-grid').hidden = !showGrid;
  list.hidden = Boolean(showGrid);
  $('#edit-action').hidden = !['list', 'favorites', 'handoff'].includes(page) || Boolean(showGrid);
  const backLabel = writingReturnPage === 'handoff' ? '接力' : writingReturnPage === 'favorites' ? '收藏日记' : notesView === 'folders' && folder ? folder.name : '所有笔记';
  $('#writing-back').replaceChildren(iconImage('chevron.left'), document.createTextNode(backLabel));
  $('#writing-back').setAttribute('aria-label', `返回${backLabel}`);
  if (!showGrid) return false;
  const grid = $('#folder-grid'); grid.replaceChildren();
  for (const item of [{ id: '', name: '根目录' }, ...folders.filter(folder => folder.id !== handoffFolderId)]) {
    const members = notes.filter(note => !isEmptyNote(note) && (!item.id || item.noteIds.includes(note.id)));
    const card = document.createElement('button'); card.type = 'button'; card.className = `diary-folder-card${item.id ? '' : ' diary-folder-card-root'}`; card.setAttribute('aria-label', `打开文件夹：${item.name}`);
    const cover = document.createElement('span'); cover.className = 'diary-folder-cover';
    const image = members.flatMap(note => normalizeNoteImages(note.images))[0];
    cover.append(image ? imageFor(image) : iconImage('folder.fill', 'diary-folder-placeholder'));
    const info = document.createElement('span'); info.className = 'diary-folder-info';
    const name = document.createElement('span'); name.className = 'diary-folder-name'; name.textContent = item.name;
    const count = document.createElement('span'); count.className = 'diary-folder-count'; count.textContent = item.id ? `${members.length} 篇日记` : '所有笔记';
    const badge = document.createElement('span'); badge.className = 'diary-folder-badge'; badge.append(iconImage('folder.fill'));
    info.append(name, count, badge);
    if (!item.id) {
      const rootLabel = document.createElement('span'); rootLabel.className = 'diary-folder-root-label'; rootLabel.textContent = '资源库根目录'; info.append(rootLabel);
    }
    card.append(cover, info);
    card.onclick = () => { activeFolderId = item.id; renderNotes(); $('#diary-content').scrollTop = 0; };
    if (item.id) installContextMenu(card, event => showContextMenu(event, '文件夹操作', [
      { label: '打开文件夹', icon: 'folder', action: card.onclick },
      { label: '重命名', icon: 'pencil', action: () => editFolder(item) },
      '-', { label: '删除文件夹', icon: 'trash', danger: true, action: () => deleteFolder(item) },
    ]));
    grid.append(card);
  }
  return true;
}

function renderNotes() {
  list.replaceChildren();
  const folderGridVisible = renderFolderNavigation();
  if (folderGridVisible) return;
  const folder = page === 'list' && notesView === 'folders' && activeFolderId ? folders.find(item => item.id === activeFolderId) : null;
  const handoffMembers = folders.find(folder => folder.id === handoffFolderId)?.noteIds || [];
  const visibleNotes = notes.filter(item => !isEmptyNote(item) && (page !== 'handoff' || handoffMembers.includes(item.id)) && (page !== 'favorites' || item.favorite) && (!folder || folder.noteIds.includes(item.id)));
  visibleNotes.sort((a, b) => {
    const dateKey = noteSortRule.startsWith('modified-') ? 'updatedAt' : 'createdAt';
    const difference = (parseNoteDate(b[dateKey] || b.createdAt)?.getTime() || 0) - (parseNoteDate(a[dateKey] || a.createdAt)?.getTime() || 0);
    if (noteSortRule === 'title') return (a.title || '').localeCompare(b.title || '', 'zh-CN', { numeric: true }) || difference;
    return ['oldest', 'modified-oldest'].includes(noteSortRule) ? -difference : difference;
  });
  for (const note of visibleNotes) {
    const card = document.createElement('button');
    card.type = 'button';
    card.className = `diary-note${selectedNotes.has(note.id) ? ' is-selected' : ''}`;
    card.dataset.noteId = note.id;
    card.setAttribute('aria-label', selecting ? `选择笔记：${note.title || '未命名笔记'}` : `打开笔记：${note.title || '未命名笔记'}`);
    card.setAttribute('aria-pressed', String(selectedNotes.has(note.id)));

    const copy = document.createElement('span');
    copy.className = 'diary-note-copy';
    const title = document.createElement('span');
    title.className = 'diary-note-title';
    title.textContent = note.title || '未命名笔记';
    const summary = document.createElement('span');
    summary.className = 'diary-note-summary';
    summary.textContent = note.body
      .replace(/\[\[image:\/media\/img-[a-f0-9]{32}\.(?:jpg|png|gif|webp|avif|heic|bmp)\]\]/g, '图片')
      .split('\n').find(line => line.trim()) || '暂无内容';
    copy.append(title, summary);
    if (showDatePreference) {
      const parts = noteDateParts(note.createdAt);
      if (parts) {
        const meta = document.createElement('span');
        meta.className = 'diary-note-meta';
        const time = document.createElement('span');
        time.className = 'diary-note-time';
        time.textContent = parts.time;
        const date = document.createElement('span');
        date.className = 'diary-note-date';
        date.textContent = parts.date;
        meta.append(time, date);
        copy.append(meta);
      }
    }
    card.append(copy);

    const images = normalizeNoteImages(note.images);
    if (images.length) {
      const photos = document.createElement('span');
      photos.className = 'diary-note-images';
      photos.setAttribute('aria-hidden', 'true');
      photos.append(...images.slice(0, 6).map(imageFor));
      card.append(photos);
    }

    if (selecting) {
      const check = document.createElement('span');
      check.className = 'diary-note-check';
      if (selectedNotes.has(note.id)) check.append(iconImage('checkmark'));
      card.append(check);
    }

    card.addEventListener('click', event => {
      if (selecting) {
        const anchor = visibleNotes.findIndex(item => item.id === selectionAnchorId);
        if (event.shiftKey && anchor >= 0) {
          const end = visibleNotes.findIndex(item => item.id === note.id);
          visibleNotes.slice(Math.min(anchor, end), Math.max(anchor, end) + 1).forEach(item => selectedNotes.add(item.id));
        } else {
          if (selectedNotes.has(note.id)) selectedNotes.delete(note.id);
          else selectedNotes.add(note.id);
          selectionAnchorId = note.id;
        }
        renderNotes();
        renderSelectionToolbar();
        return;
      }
      selectedId = note.id;
      openWritingPage(note.id);
    });
    installContextMenu(card, event => {
      closeInsertMenu();
      showContextMenu(event, '日记操作', [
        { label: '打开日记', icon: 'pencil', action: () => openWritingPage(note.id) },
        { label: note.favorite ? '取消收藏' : '收藏日记', icon: note.favorite ? 'bookmark.slash' : 'bookmark', action: async () => {
          const current = notes.find(item => item.id === note.id);
          if (!current) return;
          current.favorite = !current.favorite;
          await saveNotes();
          renderNotes();
        } },
        { label: '移到文件夹', icon: 'folder', action: () => openMoveToFolder([note.id]) },
        { label: '复制正文', icon: 'square.on.square', action: () => copyContextText(plainNoteBody(note.body), '日记正文已复制') },
        { label: '导出 Markdown', icon: 'arrow.down.document', action: () => exportMarkdownNote(note) },
        '-',
        { label: '移到回收站', icon: 'trash', danger: true, action: () => confirmDelete([note.id]) },
      ]);
    });
    list.append(card);
  }

  if (!visibleNotes.length) {
    const empty = document.createElement('div');
    empty.className = 'diary-empty';
    const icon = page === 'favorites' ? imageFor('/svg/favorites.svg') : iconImage('square.dashed');
    icon.alt = '';
    icon.setAttribute('aria-hidden', 'true');
    const message = document.createElement('p');
    message.className = 'diary-empty-message';
    message.textContent = page === 'favorites'
      ? '还没有收藏的日记，收藏的内容会显示在这里。'
      : page === 'handoff' ? '暂无接力笔记，可点击 + 新建，或将已有日记移到接力。'
      : '暂无笔记，点击 + 创建第一篇日记。';
    empty.append(icon, message);
    list.append(empty);
  }
}

function renderTrash() {
  const container = $('#trash-list');
  if (!container) return;
  container.replaceChildren();
  for (const note of trashNotes) {
    const card = document.createElement('article');
    card.className = 'diary-trash-card';
    const copy = document.createElement('div');
    copy.className = 'diary-trash-copy';
    const title = document.createElement('h2');
    title.textContent = note.title || '未命名笔记';
    const summary = document.createElement('p');
    summary.textContent = plainNoteBody(note.body).split('\n').find(line => line.trim()) || '暂无正文';
    const deleted = document.createElement('small');
    deleted.textContent = `删除于 ${note.DeletedAt || note.deletedAt || '刚刚'}`;
    copy.append(title, summary, deleted);
    const actions = document.createElement('div');
    actions.className = 'diary-trash-actions';
    const restore = document.createElement('button');
    restore.type = 'button'; restore.className = 'diary-plain-button'; restore.textContent = '恢复';
    restore.addEventListener('click', async () => { restore.disabled = true; try { await trashAction(note.id, 'restore'); notify('笔记已恢复'); } catch (error) { notify(error.message); restore.disabled = false; } });
    const remove = document.createElement('button');
    remove.type = 'button'; remove.className = 'diary-plain-button diary-plain-button--danger'; remove.textContent = '永久删除';
    remove.addEventListener('click', async () => { remove.disabled = true; try { await trashAction(note.id, 'delete'); notify('笔记已永久删除'); } catch (error) { notify(error.message); remove.disabled = false; } });
    actions.append(restore, remove);
    card.append(copy, actions);
    container.append(card);
  }
  if (!trashNotes.length) {
    const empty = document.createElement('div');
    empty.className = 'diary-empty';
    const icon = iconImage('trash'); icon.alt = ''; icon.setAttribute('aria-hidden', 'true');
    const message = document.createElement('p'); message.className = 'diary-empty-message'; message.textContent = '回收站为空';
    empty.append(icon, message); container.append(empty);
  }
}

const navigationVisibility = new WeakMap();

function showNavigation(element, visible) {
  const previous = navigationVisibility.get(element) ?? !element.hidden;
  navigationVisibility.set(element, visible);
  element.inert = !visible;
  element.setAttribute('aria-hidden', String(!visible));
  if (previous === visible) return;
  const opacity = element.hidden ? 0 : Number(getComputedStyle(element).opacity);
  element.getAnimations().forEach(animation => animation.cancel());
  element.hidden = false;
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
    element.hidden = !visible;
    return;
  }
  const animation = element.animate(
    [{ opacity }, { opacity: visible ? 1 : 0 }],
    { duration: 180, easing: 'cubic-bezier(0.23, 1, 0.32, 1)', fill: 'both' },
  );
  animation.finished.then(() => {
    if (navigationVisibility.get(element) === visible) element.hidden = !visible;
    animation.cancel();
  }).catch(() => {});
}

function setPage(next) {
  closeContextMenu();
  page = next;
  const isList = ['list', 'favorites', 'handoff'].includes(page);
  const isTrash = page === 'trash';
  const isWriting = page === 'writing';
  const isAccount = page === 'account';
  showNavigation($('#diary-header'), !isWriting && !(isList && selecting));
  showNavigation($('#selection-toolbar'), isList && selecting);
  $('#edit-action button')?.echoButton.update({
    icon: selecting ? 'pencil.slash' : 'pencil',
    ariaLabel: selecting ? '结束编辑' : '编辑列表',
    title: selecting ? '结束编辑' : '编辑列表',
    attributes: { 'aria-pressed': String(selecting) },
  });
  $('#writing-top').hidden = !isWriting;
  $('#notes-view').hidden = !isList;
  $('#trash-view').hidden = !isTrash;
  $('#writing-view').hidden = !isWriting;
  $('#account-view').hidden = !isAccount;
  $('#add-action').hidden = !['list', 'handoff'].includes(page);
  $('#edit-action').hidden = !isList;
  $('#diary-settings-actions').hidden = !isAccount;
  $('#writing-actions').hidden = !isWriting;
  if (!isWriting) {
    closeInsertMenu();
    $('#writing-top').classList.remove('is-created-hidden');
    document.body.classList.remove('is-writing-preview');
  }
  document.body.classList.toggle('is-selecting', isList && selecting);
  document.body.classList.toggle('is-writing', isWriting);
  updateThemeColor();
  $('#diary-content').scrollTop = 0;
  if (isTrash) renderTrash();
  if (isList) renderNotes();
}

function startSelection() {
  selectionAnchorId = '';
  selectionReturnPage = ['favorites', 'handoff'].includes(page) ? page : 'list';
  selecting = true;
  selectedNotes.clear();
  setPage(selectionReturnPage);
  renderNotes();
  renderSelectionToolbar();
}

function stopSelection() {
  selectionAnchorId = '';
  selecting = false;
  selectedNotes.clear();
  setPage(selectionReturnPage);
  renderNotes();
}

function removeNotes(ids) {
  const removing = new Set(ids);
  notes = notes.filter(note => !removing.has(note.id));
  if (removing.has(selectedId)) selectedId = notes[0]?.id || '';
  if (removing.has(writingNoteId)) writingNoteId = '';
  saveNotes();
  void Promise.all(ids.map(deletePersistedNote));
}

function confirmDelete(ids) {
  if (!ids.length) return;
  $('#delete-confirm')?.remove();
  const modal = createEchoModal({
    id: 'delete-confirm', title: '移到回收站？', icon: 'trash.fill',
    addition: `将把 ${ids.length} 篇笔记移到回收站，之后仍可恢复。`,
    actions: [
      { label: '取消', icon: 'xmark', color: 'gray' },
      { label: '移到回收站', icon: 'trash.fill', color: 'red', close: false, onClick: () => {
        removeNotes(ids);
        modal.echoModal.close();
        modal.remove();
        if (page === 'writing') setPage(writingReturnPage);
        if (selecting) stopSelection();
        renderNotes();
        notify('笔记已移到回收站');
      } },
    ],
  });
  document.body.append(modal);
  modal.echoModal.open();
}

function renderSelectionToolbar() {
  const toolbar = $('#selection-toolbar');
  toolbar.replaceChildren();
  const count = document.createElement('span');
  count.className = 'diary-selection-count';
  count.textContent = String(selectedNotes.size);
  count.setAttribute('aria-label', `${selectedNotes.size} 项已选`);
  toolbar.append(createEchoButton({
    icon: 'xmark', size: 'icon', variant: 'switch', className: 'diary-selection-action',
    ariaLabel: '取消选择', onClick: stopSelection,
  }), count);
  toolbar.append(createEchoButton({
    icon: '/svg/select-list-move-to-folder.svg', size: 'icon', variant: 'switch', className: 'diary-selection-action',
    ariaLabel: '移动所选笔记', disabled: selectedNotes.size === 0, onClick: () => openMoveToFolder([...selectedNotes]),
  }));
  toolbar.append(createEchoButton({
    icon: '/svg/select-list-download.svg', size: 'icon', variant: 'switch', className: 'diary-selection-action',
    ariaLabel: '导出所选笔记', disabled: selectedNotes.size === 0, onClick: () => {
      const selected = notes.filter(note => selectedNotes.has(note.id));
      const text = selected.map(note => `${note.title}\n\n${plainNoteBody(note.body)}`).join('\n\n---\n\n');
      const link = document.createElement('a');
      link.href = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }));
      link.download = 'EchoDiary-notes.txt';
      link.click();
      URL.revokeObjectURL(link.href);
      notify('已导出所选笔记');
    },
  }));
  toolbar.append(createEchoButton({
    icon: '/svg/select-list-bookmark.svg', size: 'icon', variant: 'switch', className: 'diary-selection-action',
    ariaLabel: '收藏所选笔记', disabled: selectedNotes.size === 0, onClick: () => {
      notes = notes.map(note => selectedNotes.has(note.id) ? { ...note, favorite: true } : note);
      saveNotes();
      notify('已收藏所选笔记');
    },
  }));
  toolbar.append(createEchoButton({
    icon: '/svg/select-list-trash.svg', size: 'icon', variant: 'switch', className: 'diary-selection-action',
    ariaLabel: '删除所选笔记', disabled: selectedNotes.size === 0, onClick: () => confirmDelete([...selectedNotes]),
  }));
}

function syncWritingPlaceholder() {
  const editor = $('#writing-body');
  const formatted = editor.querySelector('input,img,ul,ol,h1,h2,h3,h4,h5,h6,blockquote,pre,table,hr,.diary-inline-image');
  editor.classList.toggle('is-empty', !editor.textContent.trim() && !formatted);
}

function renderWritingEditor(text) {
  const editor = $('#writing-body');
  const note = notes.find(item => item.id === writingNoteId);
  // Stage and validate before replacing the live editor.
  const staged = document.createElement('div');
  if (note?.bodyHtml) renderFormattedContent(staged, note.bodyHtml, true);
  else renderWritingContent(staged, requireBodyString(text), true);
  if (!staged.childNodes.length) staged.innerHTML = '<p><br></p>';
  const baseline = editorHTML(staged);
  editor.replaceChildren(...staged.childNodes);
  writingHTMLBaseline = baseline;
  writingRenderedNoteId = note?.id || '';
  syncWritingPlaceholder();
}

function renderWritingContent(container, text, editing) {
  container.replaceChildren();
  // Consecutive attachments form a gallery without moving images past text.
  const marker = String.raw`\[\[image:\/media\/img-[a-f0-9]{32}\.(?:jpg|png|gif|webp|avif|heic|bmp)\]\]`;
  const galleries = new RegExp(`${marker}(?:\\s*${marker})+`, 'g');
  const appendParagraphs = value => {
    for (const paragraph of value.replace(/\r\n?/g, '\n').split('\n')) {
      if (!paragraph && !value) continue;
      const standaloneImage = paragraph.trim().match(/^\[\[image:(\/media\/img-[a-f0-9]{32}\.(?:jpg|png|gif|webp|avif|heic|bmp))\]\]$/);
      if (standaloneImage) {
        container.append(createInlineImage(standaloneImage[1], editing, true));
        continue;
      }
      const block = document.createElement('p');
      appendRichText(block, paragraph, editing);
      if (!block.childNodes.length) block.append(document.createElement('br'));
      container.append(block);
    }
  };
  const value = String(text || '');
  let cursor = 0;
  for (const match of value.matchAll(galleries)) {
    appendParagraphs(value.slice(cursor, match.index));
    const gallery = document.createElement('div');
    gallery.className = 'diary-image-gallery';
    for (const url of inlineImageURLs(match[0])) gallery.append(createInlineImage(url, editing));
    if (editing) installImageSorting(gallery, false);
    container.append(gallery);
    cursor = match.index + match[0].length;
  }
  appendParagraphs(value.slice(cursor));
}

function inlineImageURLs(text) {
  const pattern = /\[\[image:(\/media\/img-[a-f0-9]{32}\.(?:jpg|png|gif|webp|avif|heic|bmp))\]\]/g;
  return [...String(text || '').matchAll(pattern)].map(match => match[1]);
}

function appendTextWithBreaks(parent, text) {
  String(text).split('\n').forEach((line, index) => {
    if (index) parent.append(document.createElement('br'));
    if (line) parent.append(document.createTextNode(line));
  });
}

function createInlineImage(url, editing, standalone = false) {
  const note = notes.find(item => item.id === writingNoteId);
  const index = Math.max(0, normalizeNoteImages(note?.images).indexOf(url));
  const wrapper = document.createElement(standalone ? 'div' : 'span');
  wrapper.className = `diary-inline-image${standalone ? ' is-standalone' : ''}`;
  wrapper.contentEditable = 'false';
  wrapper.dataset.imageUrl = url;

  const attachment = document.createElement('span');
  attachment.className = 'diary-attachment';
  const preview = document.createElement('button');
  preview.type = 'button';
  preview.className = 'diary-attachment-preview';
  preview.setAttribute('aria-label', `查看图片 ${index + 1}`);
  const image = imageFor(url);
  image.alt = `笔记图片 ${index + 1}`;
  image.draggable = false;
  const markTallImage = () => {
    if (image.naturalHeight > image.naturalWidth * 1.35) wrapper.classList.add('is-tall');
  };
  if (image.complete) markTallImage();
  else image.addEventListener('load', markTallImage, { once: true });
  preview.append(image);
  preview.addEventListener('click', () => openImageViewer(url, index));
  installImageContextMenu(preview, url, index, editing ? () => removeInlineImage(wrapper) : null);
  attachment.append(preview);

  if (editing) {
    const remove = createEchoButton({
      icon: 'xmark', size: 'icon', variant: 'switch', className: 'diary-attachment-remove',
      ariaLabel: `移除图片 ${index + 1}`, onClick: () => { void removeInlineImage(wrapper); },
    });
    remove.contentEditable = 'false';
    attachment.append(remove);
  }
  wrapper.append(attachment);
  return wrapper;
}

function appendRichText(parent, text, editing) {
  const pattern = /\[\[image:(\/media\/img-[a-f0-9]{32}\.(?:jpg|png|gif|webp|avif|heic|bmp))\]\]/g;
  let cursor = 0;
  for (const match of String(text).matchAll(pattern)) {
    appendTextWithBreaks(parent, text.slice(cursor, match.index));
    parent.append(createInlineImage(match[1], editing));
    cursor = match.index + match[0].length;
  }
  appendTextWithBreaks(parent, text.slice(cursor));
}

function renderWritingPreview(text) {
  const note = notes.find(item => item.id === writingNoteId);
  if (note?.bodyHtml) renderFormattedContent($('#writing-preview'), note.bodyHtml, false);
  else renderWritingContent($('#writing-preview'), text, false);
}

function renderFormattedContent(container, html, editing) {
  const safeHTML = sanitizeHTML(html);
  container.innerHTML = safeHTML;
  normalizeParagraphs(container);
  prepareChecklists(container);
  // Markdown may put an image run in one paragraph with BR separators.
  // Lift images into document order before rebuilding gallery controls.
  for (const paragraph of [...container.querySelectorAll('p')]) {
    const children = [...paragraph.childNodes];
    if (!children.some(node => node.nodeType === Node.ELEMENT_NODE && node.tagName === 'IMG' && isManagedImageURL(node.getAttribute('src')))) continue;
    const output = document.createDocumentFragment();
    let text = document.createElement('p');
    const flushText = () => {
      if (text.textContent.trim() || text.querySelector('img')) output.append(text);
      text = document.createElement('p');
    };
    for (const child of children) {
      if (child.nodeType === Node.ELEMENT_NODE && child.tagName === 'IMG' && isManagedImageURL(child.getAttribute('src'))) {
        flushText();
        output.append(child);
      } else {
        text.append(child);
      }
    }
    flushText();
    paragraph.replaceWith(output);
  }
  for (const image of [...container.querySelectorAll('img')]) {
    const url = image.getAttribute('src');
    if (!isManagedImageURL(url)) { image.loading = 'lazy'; continue; }
    const parent = image.parentElement;
    const alone = parent !== container && parent.tagName === 'P' && parent.children.length === 1 && !parent.textContent.trim();
    const wrapper = createInlineImage(url, editing, alone || parent === container);
    if (alone) parent.replaceWith(wrapper);
    else image.replaceWith(wrapper);
  }
  // Rebuild runtime galleries; their controls never enter the saved HTML.
  let group = [];
  const flush = () => {
    if (group.length > 1) {
      const gallery = document.createElement('div');
      gallery.className = 'diary-image-gallery';
      group[0].before(gallery);
      for (const item of group) { item.classList.remove('is-standalone'); gallery.append(item); }
      if (editing) installImageSorting(gallery, false);
    }
    group = [];
  };
  for (const child of [...container.childNodes]) {
    if (child.nodeType === Node.TEXT_NODE && !child.textContent.trim()) continue;
    if (child.nodeType === Node.ELEMENT_NODE && (child.tagName === 'BR' || (['P', 'DIV'].includes(child.tagName) && !child.textContent.trim() && !child.querySelector('img, .diary-inline-image, hr, table')))) continue;
    if (child.nodeType === Node.ELEMENT_NODE && child.classList.contains('diary-inline-image')) group.push(child);
    else flush();
  }
  flush();
}

function resizeWritingTitle() {
  const title = $('#writing-title');
  const width = title.getBoundingClientRect().width;
  if (!width) return;
  const measure = title.cloneNode(false);
  measure.removeAttribute('id');
  measure.setAttribute('aria-hidden', 'true');
  measure.tabIndex = -1;
  measure.value = title.value;
  Object.assign(measure.style, {
    position: 'fixed', left: '-10000px', top: '0', visibility: 'hidden',
    width: `${width}px`, height: '0', minHeight: '0', pointerEvents: 'none',
  });
  // Keep the writing-view ancestor styles while measuring, including mobile sizing.
  title.after(measure);
  const height = Math.max(parseFloat(getComputedStyle(title).lineHeight), measure.scrollHeight);
  measure.remove();
  title.style.height = `${Math.ceil(height)}px`;
}

function resizeMarkdownEditor() {
  const editor = $('#writing-markdown');
  if (editor.hidden) return;
  // Measure offscreen: collapsing the live textarea clamps the page's scroll
  // position while typing in a long document. Keep its current height intact.
  const measure = editor.cloneNode(false);
  measure.removeAttribute('id');
  measure.removeAttribute('name');
  measure.setAttribute('aria-hidden', 'true');
  measure.tabIndex = -1;
  measure.value = editor.value;
  Object.assign(measure.style, {
    position: 'fixed', left: '-10000px', top: '0', visibility: 'hidden',
    width: `${editor.getBoundingClientRect().width}px`, height: '0',
    minHeight: '0', pointerEvents: 'none',
  });
  document.body.append(measure);
  const height = Math.max(240, measure.scrollHeight);
  measure.remove();
  editor.style.height = `${height}px`;
}

function markdownSourceFor(note) {
  if (note.bodyFormat === 'markdown' && typeof note.bodyMarkdown === 'string') return note.bodyMarkdown;
  if (typeof note.bodyHtml === 'string' && note.bodyHtml) return htmlMarkdown(note.bodyHtml);
  return typeof note.body === 'string' ? note.body : '';
}

function setWritingView(index) {
  if (!Number.isInteger(index) || index < 0 || index > 2) return;
  const note = notes.find(item => item.id === writingNoteId);
  if (!note) return;
  try {
    if (writingView !== 1 && !updateDraft()) throw new Error('正文尚未保存');
    if (index === 2) {
      const source = requireBodyString(markdownSourceFor(note));
      $('#writing-markdown').value = source;
      writingMarkdownBaseline = source;
      writingEditView = 2;
    } else if (index === 0) {
      renderWritingEditor(note.body);
      writingEditView = 0;
    }
  } catch {
    $('#writing-mode .echo-segmented').echoSegmented.setActive(writingView);
    notify('正文转换失败，已保留原内容');
    return;
  }
  writingView = index;
  $('#writing-body').hidden = index !== 0;
  $('#writing-markdown').hidden = index !== 2;
  $('#writing-preview').hidden = index !== 1;
  document.body.classList.toggle('is-writing-preview', index === 1);
  resizeMarkdownEditor();
  resizeWritingTitle();
}


function renderWritingImages() {
  const container = $('#writing-images');
  const note = notes.find(item => item.id === writingNoteId);
  container.replaceChildren();
  if (!note) return;
  const insertButton = $('#writing-add-action .echo-button');
  if (insertButton) insertButton.disabled = imageUploadInProgress;

  const inline = new Set(inlineImageURLs(note.body));
  normalizeNoteImages(note.images).forEach((url, index) => {
    if (inline.has(url)) return;
    const attachment = document.createElement('div');
    attachment.className = 'diary-attachment';
    const preview = document.createElement('button');
    preview.type = 'button';
    preview.className = 'diary-attachment-preview';
    preview.setAttribute('aria-label', `预览图片 ${index + 1}`);
    const image = imageFor(url);
    image.alt = `笔记图片 ${index + 1}`;
    preview.append(image);
    const markTallImage = () => {
      if (image.naturalHeight > image.naturalWidth * 1.35) attachment.classList.add('is-tall');
    };
    if (image.complete) markTallImage();
    else image.addEventListener('load', markTallImage, { once: true });
    preview.addEventListener('click', () => openImageViewer(url, index));
    installImageContextMenu(preview, url, index, () => {
      const current = notes.find(item => item.id === writingNoteId);
      const currentIndex = normalizeNoteImages(current?.images).indexOf(url);
      if (currentIndex >= 0) return removeWritingImage(currentIndex);
    });

    const remove = createEchoButton({
      icon: 'xmark', size: 'icon', variant: 'switch', className: 'diary-attachment-remove',
      ariaLabel: `移除图片 ${index + 1}`, onClick: () => { void removeWritingImage(index); },
    });
    attachment.append(preview, remove);
    container.append(attachment);
  });
  container.classList.toggle('diary-image-gallery', container.children.length > 1);
  if (!container.dataset.sortingInstalled) {
    installImageSorting(container, true);
    container.dataset.sortingInstalled = 'true';
  }

}

function installImageSorting(gallery, attachmentsOnly) {
  gallery.classList.add('is-sortable');
  let pending = null;
  let drag = null;
  let suppressClickUntil = 0;
  const finish = (cancelled = false) => {
    document.removeEventListener('pointermove', pointerMove);
    document.removeEventListener('pointerup', pointerEnd);
    document.removeEventListener('pointercancel', pointerEnd);
    clearTimeout(pending?.timer);
    const pointerId = drag?.pointerId ?? pending?.pointerId;
    pending = null;
    if (pointerId !== undefined && gallery.hasPointerCapture?.(pointerId)) gallery.releasePointerCapture(pointerId);
    if (!drag) return;
    const current = drag;
    drag = null;
    current.item.classList.remove('is-image-sorting');
    gallery.classList.remove('is-sorting');
    suppressClickUntil = Date.now() + 400;
    if (cancelled) {
      gallery.append(...current.original);
      return;
    }
    const note = notes.find(item => item.id === writingNoteId);
    if (!note || current.original.every((item, index) => gallery.children[index] === item)) return;
    if (attachmentsOnly) {
      const inline = new Set(inlineImageURLs(note.body));
      const originalImages = normalizeNoteImages(note.images);
      const unattached = originalImages.filter(url => !inline.has(url));
      const reordered = [...gallery.children].map(item => unattached[current.original.indexOf(item)]);
      let index = 0;
      note.images = originalImages.map(url => inline.has(url) ? url : reordered[index++]);
    } else {
      updateDraft();
      const ordered = inlineImageURLs(note.body);
      const inline = new Set(ordered);
      note.images = [...ordered, ...normalizeNoteImages(note.images).filter(url => !inline.has(url))];
    }
    void saveNotes();
    renderWritingImages();
    renderNotes();
  };
  gallery.addEventListener('pointerdown', event => {
    if (event.pointerType !== 'mouse' || event.button !== 0 || !event.isPrimary || document.body.classList.contains('is-writing-preview')) return;
    const preview = event.target.closest('.diary-attachment-preview');
    if (!preview || gallery.children.length < 2) return;
    const item = [...gallery.children].find(child => child.contains(preview));
    if (!item) return;
    if (pending || drag) finish(true);
    const start = { item, pointerId: event.pointerId, x: event.clientX, y: event.clientY, original: [...gallery.children] };
    const activate = () => {
      drag = start;
      item.classList.add('is-image-sorting');
      gallery.classList.add('is-sorting');
      gallery.setPointerCapture(event.pointerId);
    };
    pending = { ...start, timer: event.pointerType === 'mouse' ? null : setTimeout(activate, 350) };
    // Keep tracking outside the image and never capture on a node being reordered.
    document.addEventListener('pointermove', pointerMove, { passive: false });
    document.addEventListener('pointerup', pointerEnd);
    document.addEventListener('pointercancel', pointerEnd);
    if (event.pointerType === 'mouse') event.preventDefault();
  });
  const pointerMove = event => {
    if (!pending || pending.pointerId !== event.pointerId) return;
    const distance = Math.hypot(event.clientX - pending.x, event.clientY - pending.y);
    if (!drag && distance > 6) {
      if (event.pointerType !== 'mouse') { finish(true); return; }
      drag = pending;
      drag.item.classList.add('is-image-sorting');
      gallery.classList.add('is-sorting');
      gallery.setPointerCapture(event.pointerId);
    }
    if (!drag) return;
    event.preventDefault();
    const target = [...gallery.children].find(item => {
      const rect = item.getBoundingClientRect();
      return item !== drag.item && event.clientX >= rect.left && event.clientX <= rect.right && event.clientY >= rect.top && event.clientY <= rect.bottom;
    });
    if (!target) return;
    const items = [...gallery.children];
    if (items.indexOf(drag.item) < items.indexOf(target)) target.after(drag.item);
    else target.before(drag.item);
  };
  const pointerEnd = event => {
    if ((drag?.pointerId ?? pending?.pointerId) !== event.pointerId) return;
    finish(event.type === 'pointercancel');
  };
  gallery.addEventListener('touchmove', event => { if (drag) event.preventDefault(); }, { passive: false });
  gallery.addEventListener('contextmenu', event => { if (pending || drag) event.preventDefault(); });
  gallery.addEventListener('dragstart', event => event.preventDefault());
  gallery.addEventListener('click', event => {
    if (Date.now() < suppressClickUntil) { event.preventDefault(); event.stopImmediatePropagation(); }
  }, true);
}

async function removeInlineImage(wrapper) {
  const url = wrapper.dataset.imageUrl;
  const note = notes.find(item => item.id === writingNoteId);
  if (!url || !note) return;
  wrapper.remove();
  updateDraft();
  if (!inlineImageURLs(note.body).includes(url)) {
    note.images = normalizeNoteImages(note.images).filter(image => image !== url);
    renderWritingImages();
    renderNotes();
    await saveNotes();
    await deleteStoredImage(url);
  }
}

async function removeWritingImage(index) {
  const note = notes.find(item => item.id === writingNoteId);
  if (!note) return;
  const images = normalizeNoteImages(note.images);
  const [removed] = images.splice(index, 1);
  note.images = images;
  renderWritingImages();
  renderNotes();
  await saveNotes();
  if (removed) await deleteStoredImage(removed);
}

async function deleteStoredImage(url) {
  if (!isManagedImageURL(url)) return;
  const name = url.slice('/media/'.length);
  try {
    const response = await fetch(`/api/images/${encodeURIComponent(name)}`, { method: 'DELETE' });
    if (!response.ok && response.status !== 404 && response.status !== 409) throw new Error(`delete image: ${response.status}`);
  } catch {
    // A failed cleanup leaves the image available rather than breaking the note.
  }
}

function openImageViewer(url, index = 0) {
  const viewer = $('#photo-viewer');
  const image = $('#photo-viewer-image');
  setPhotoViewerScale(1);
  image.src = url;
  image.alt = `笔记图片 ${index + 1}`;
  viewer.hidden = false;
  $('#photo-viewer-close').focus();
}

function setPhotoViewerScale(scale) {
  photoViewerScale = Math.max(1, Math.min(5, scale));
  $('#photo-viewer-image').style.transform = `scale(${photoViewerScale})`;
  $('#photo-viewer-image').style.cursor = photoViewerScale > 1 ? 'grab' : 'zoom-in';
}

function writingRangeAtPoint(x, y) {
  const editor = $('#writing-body');
  const range = document.caretRangeFromPoint?.(x, y);
  if (range && editor.contains(range.startContainer)) return range;
  const position = document.caretPositionFromPoint?.(x, y);
  if (position && editor.contains(position.offsetNode)) {
    const caret = document.createRange();
    caret.setStart(position.offsetNode, position.offset);
    caret.collapse(true);
    return caret;
  }
  return null;
}

function currentWritingRange() {
  const editor = $('#writing-body');
  const selection = window.getSelection();
  if (selection?.rangeCount && editor.contains(selection.anchorNode)) return selection.getRangeAt(0).cloneRange();
  return null;
}

function insertInlineImage(url, range = savedWritingRange) {
  if (writingEditView === 2) {
    const editor = $('#writing-markdown');
    const position = editor.selectionStart;
    editor.setRangeText(`\n\n![图片](${url})\n\n`, position, editor.selectionEnd, 'end');
    updateDraft();
    resizeMarkdownEditor();
    renderWritingImages();
    return null;
  }
  const editor = $('#writing-body');
  let target = range?.cloneRange();
  if (!target || !editor.contains(target.commonAncestorContainer)) {
    let block = editor.lastElementChild;
    if (!block) {
      block = document.createElement('p');
      editor.append(block);
    }
    target = document.createRange();
    target.selectNodeContents(block);
    target.collapse(false);
  }

  let block = target.startContainer === editor ? null : target.startContainer.nodeType === Node.ELEMENT_NODE ? target.startContainer : target.startContainer.parentElement;
  while (block && block.parentElement !== editor) block = block.parentElement;
  if (!block || !['P', 'DIV'].includes(block.tagName)) {
    const paragraph = document.createElement('p');
    target.insertNode(paragraph);
    target.selectNodeContents(paragraph);
    target.collapse(true);
    block = paragraph;
  }

  target.deleteContents();
  const tailRange = target.cloneRange();
  tailRange.setEnd(block, block.childNodes.length);
  const tail = tailRange.extractContents();
  const image = createInlineImage(url, true, true);
  const hasBefore = block.childNodes.length > 0;
  if (hasBefore) block.after(image);
  else block.replaceWith(image);

  const nextParagraph = document.createElement('p');
  if (tail.childNodes.length) nextParagraph.append(tail);
  else nextParagraph.append(document.createElement('br'));
  image.after(nextParagraph);
  const caret = document.createRange();
  caret.selectNodeContents(nextParagraph);
  caret.collapse(true);
  const selection = window.getSelection();
  selection.removeAllRanges();
  selection.addRange(caret);
  savedWritingRange = caret.cloneRange();
  editor.focus({ preventScroll: true });
  updateDraft();
  renderWritingImages();
  return savedWritingRange;
}

function closeImageViewer() {
  const viewer = $('#photo-viewer');
  if (viewer.hidden) return;
  viewer.hidden = true;
  $('#photo-viewer-image').removeAttribute('src');
  setPhotoViewerScale(1);
}

async function addImages(files) {
  if (writingView === 1) {
    setWritingView(writingEditView);
    $('#writing-mode .echo-segmented').echoSegmented.setActive(writingEditView);
  }
  const noteId = writingNoteId;
  if (!noteId || !files.length || imageUploadInProgress) return;
  let insertionRange = savedWritingRange?.cloneRange() || currentWritingRange();
  imageUploadInProgress = true;
  renderWritingImages();
  let uploaded = 0;

  try {
    for (const file of files) {
      if (file.type && !file.type.startsWith('image/')) {
        notify('请选择图片文件');
        continue;
      }
      const form = new FormData();
      form.append('image', file, file.name);
      try {
        const response = await fetch('/api/images', { method: 'POST', body: form });
        if (!response.ok) throw new Error(await response.text());
        const payload = await response.json();
        if (!isManagedImageURL(payload.url)) throw new Error('服务器返回了无效图片地址');
        const note = notes.find(item => item.id === noteId);
        if (!note || writingNoteId !== noteId) {
          await deleteStoredImage(payload.url);
          continue;
        }
        note.images = [...normalizeNoteImages(note.images), payload.url];
        insertionRange = insertInlineImage(payload.url, insertionRange);
        uploaded += 1;
        renderNotes();
      } catch (error) {
        notify(error instanceof Error && error.message.includes('20 MB') ? '单张图片不能超过 20 MB' : '图片添加失败，请重试');
      }
    }
  } finally {
    imageUploadInProgress = false;
    renderWritingImages();
  }
  if (uploaded) notify(uploaded === 1 ? '图片已添加' : `已添加 ${uploaded} 张图片`);
}

// Validate all representations before changing the in-memory note or saving it.
function applyWritingDraft(note, draft) {
  const body = requireBodyString(draft.body);
  const bodyHtml = requireBodyString(draft.bodyHtml);
  const bodyMarkdown = requireBodyString(draft.bodyMarkdown);
  if (!['', 'markdown'].includes(draft.bodyFormat)) throw new TypeError('正文格式无效');
  const inline = inlineImageURLs(body);
  const baseline = writingEditView === 0 ? editorHTML($('#writing-body')) : $('#writing-markdown').value;
  const preview = document.createElement('div');
  if (bodyHtml) renderFormattedContent(preview, bodyHtml, false);
  else renderWritingContent(preview, body, false);
  Object.assign(note, { title: $('#writing-title').value, body, bodyHtml, bodyMarkdown, bodyFormat: draft.bodyFormat,
    images: [...new Set([...normalizeNoteImages(note.images), ...inline])],
  });
  if (writingEditView === 0) writingHTMLBaseline = baseline;
  else writingMarkdownBaseline = baseline;
  syncWritingPlaceholder();
  $('#writing-preview').replaceChildren(...preview.childNodes);
  saveNotes();
}

function updateDraft() {
  const note = notes.find(item => item.id === writingNoteId);
  if (!note || writingView === 1) return true;
  // An old or not-yet-rendered editor must never overwrite another note.
  if (writingRenderedNoteId !== note.id) return false;
  try {
    let draft;
    if (writingEditView === 2) {
      const source = requireBodyString($('#writing-markdown').value);
      if (source !== writingMarkdownBaseline) {
        draft = { bodyFormat: 'markdown', bodyMarkdown: source, bodyHtml: markdownHTML(source) };
      }
    } else {
      const html = editorHTML($('#writing-body'));
      if (html !== writingHTMLBaseline) draft = { bodyFormat: '', bodyMarkdown: '', bodyHtml: html };
    }
    // Title-only edits preserve the original body byte for byte.
    if (!draft) {
      if (note.title !== $('#writing-title').value) { note.title = $('#writing-title').value; saveNotes(); }
      return true;
    }
    draft.body = htmlText(draft.bodyHtml);
    applyWritingDraft(note, draft);
    return true;
  } catch (error) {
    console.error('正文保存已停止：', error);
    notify('正文转换失败，已保留原内容，请保留当前编辑后重试');
    return false;
  }
}

function finishWritingDraft() {
  const note = notes.find(item => item.id === writingNoteId);
  if (!updateDraft()) return false;
  if (note && isEmptyNote(note)) {
    notes = notes.filter(item => item.id !== note.id);
    if (selectedId === note.id) selectedId = notes[0]?.id || '';
    saveNotes();
  }
  writingNoteId = '';
  writingRenderedNoteId = '';
  return true;
}

function openWritingPage(id) {
  if (page === 'writing' && writingNoteId && !updateDraft()) return;
  const note = notes.find(item => item.id === id);
  if (!note) return;
  const editor = $('#writing-body');
  const restoreEditorFocus = document.activeElement === editor;
  selecting = false;
  selectedNotes.clear();
  if (page !== 'writing') writingReturnPage = ['favorites', 'handoff'].includes(page) ? page : 'list';
  writingNoteId = note.id;
  selectedId = note.id;
  if (!note.createdAt) note.createdAt = `创建于：${new Intl.DateTimeFormat('zh-CN', { dateStyle: 'long', timeStyle: 'short' }).format(new Date())}`;
  writingDateMode = 'created';
  writingView = 0;
  writingEditView = 0;
  renderWritingDate();
  $('#writing-top').classList.remove('is-created-hidden');
  savedWritingRange = null;
  $('#writing-title').value = note.title;
  renderWritingEditor(note.body);
  const selection = window.getSelection();
  const caret = document.createRange();
  caret.selectNodeContents(editor);
  caret.collapse(true);
  if (restoreEditorFocus) editor.focus({ preventScroll: true });
  selection?.removeAllRanges();
  selection?.addRange(caret);
  renderWritingPreview(note.body);
  renderWritingImages();
  $('#writing-mode .echo-segmented').echoSegmented.setActive(0);
  $('#writing-body').hidden = false;
  $('#writing-markdown').hidden = true;
  $('#writing-markdown').value = normalizeNoteBodyValue(note.bodyMarkdown);
  writingMarkdownBaseline = $('#writing-markdown').value;
  $('#writing-preview').hidden = true;
  document.body.classList.remove('is-writing-preview');
  setPage('writing');
  resizeWritingTitle();
  renderFolderNavigation();
}

function createNote() {
  if (page === 'writing' && !finishWritingDraft()) return;
  selecting = false;
  selectedNotes.clear();
  const note = {
    id: createUniqueNoteId(),
    title: '', body: '', images: [],
    createdAt: `创建于：${new Intl.DateTimeFormat('zh-CN', { dateStyle: 'long', timeStyle: 'short' }).format(new Date())}`,
  };
  notes.unshift(note);
  if (page === 'handoff' || (page === 'list' && notesView === 'folders' && activeFolderId)) {
    void updateFolders({ action: 'move', id: page === 'handoff' ? handoffFolderId : activeFolderId, noteIds: [note.id] }).catch(error => notify(error.message));
  }
  selectedId = note.id;
  openWritingPage(note.id);
}

function createUniqueNoteId() {
  const existingIds = new Set([...notes, ...trashNotes].map(note => note.id));
  const cryptoAPI = globalThis.crypto;
  for (let attempt = 0; attempt < 8; attempt += 1) {
    let id = cryptoAPI?.randomUUID?.();
    if (!id) {
      const entropy = new Uint32Array(4);
      if (cryptoAPI?.getRandomValues) cryptoAPI.getRandomValues(entropy);
      else for (let index = 0; index < entropy.length; index += 1) entropy[index] = Math.floor(Math.random() * 0x100000000);
      id = `note-${Date.now()}-${[...entropy].map(value => value.toString(16).padStart(8, '0')).join('')}`;
    }
    if (!existingIds.has(id)) return id;
  }
  throw new Error('无法生成唯一的笔记 ID，请重试');
}

function shareCurrentNote() {
  const note = notes.find(item => item.id === writingNoteId);
  if (!note) return;
  const payload = { title: note.title || 'EchoDiary', text: plainNoteBody(note.body) };
  if (navigator.share) navigator.share(payload).catch(() => {});
  else if (navigator.clipboard) navigator.clipboard.writeText(`${payload.title}\n\n${payload.text}`).then(() => notify('笔记内容已复制'));
  else notify('分享功能暂不可用');
}

async function copyContextText(text, message) {
  if (!navigator.clipboard?.writeText) throw new Error('当前浏览器无法访问剪贴板，请使用 HTTPS 或本机地址。');
  await navigator.clipboard.writeText(text);
  notify(message);
}

function downloadContextFile(url, name) {
  const link = document.createElement('a');
  link.href = url;
  link.download = name;
  document.body.append(link);
  link.click();
  link.remove();
}

function exportMarkdownNote(note) {
  const body = note.bodyFormat === 'markdown' ? note.bodyMarkdown : note.bodyHtml ? htmlMarkdown(note.bodyHtml) : note.body;
  const blob = new Blob([`# ${note.title || '未命名日记'}\n\n${body || ''}\n`], { type: 'text/markdown;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  downloadContextFile(url, `${(note.title || '未命名日记').replace(/[<>:"/\\|?*\x00-\x1f]/g, '_').slice(0, 100)}.md`);
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function installImageContextMenu(element, url, index, remove) {
  installContextMenu(element, event => {
    closeInsertMenu();
    const items = [
      { label: '预览图片', icon: 'arrow.up.left.and.arrow.down.right', action: () => openImageViewer(url, index) },
      { label: '下载原图', icon: 'arrow.down.to.line', action: () => downloadContextFile(url, url.split('/').pop()) },
      { label: '复制图片链接', icon: 'link', action: () => copyContextText(new URL(url, location.href).href, '图片链接已复制') },
    ];
    const attachment = element.closest('#writing-images > .diary-attachment, .diary-image-gallery > .diary-inline-image');
    const gallery = attachment?.parentElement;
    if (remove && writingView !== 1 && gallery?.children.length > 1) {
      const siblings = [...gallery.children];
      const position = siblings.indexOf(attachment);
      const move = async direction => {
        const target = siblings[position + direction];
        if (!target || !attachment.isConnected) return;
        if (direction < 0) target.before(attachment); else target.after(attachment);
        const note = notes.find(item => item.id === writingNoteId);
        if (!note) return;
        if (gallery.id === 'writing-images') {
          const original = normalizeNoteImages(note.images);
          const inline = new Set(inlineImageURLs(note.body));
          const attached = original.filter(image => !inline.has(image));
          const reordered = [...gallery.children].map(item => attached[siblings.indexOf(item)]);
          let next = 0;
          note.images = original.map(image => inline.has(image) ? image : reordered[next++]);
        } else {
          updateDraft();
          const ordered = inlineImageURLs(note.body);
          const inline = new Set(ordered);
          note.images = [...ordered, ...normalizeNoteImages(note.images).filter(image => !inline.has(image))];
        }
        await saveNotes();
        renderWritingImages();
        renderNotes();
      };
      items.push('-',
        { label: '向前移动', icon: 'arrow.left', disabled: position === 0, action: () => move(-1) },
        { label: '向后移动', icon: 'arrow.right', disabled: position === siblings.length - 1, action: () => move(1) });
    }
    if (remove && writingView !== 1) items.push('-',  { label: '移除图片', icon: 'trash', danger: true, action: () => {
      const modal = createEchoModal({
        title: '移除这张图片？', icon: 'trash', addition: '图片将从当前日记移除。此操作无法撤销。',
        actions: [
          { label: '取消', icon: 'xmark', color: 'gray' },
          { label: '移除图片', icon: 'trash', color: 'red', close: false, onClick: async () => {
            const button = modal.querySelector('.echo-modal__footer button:last-child');
            if (button) button.disabled = true;
            try { await remove(); modal.echoModal.close(); modal.remove(); }
            catch (error) { notify(error.message); if (button) button.disabled = false; }
          } },
        ],
      });
      document.body.append(modal);
      modal.echoModal.open();
    } });
    showContextMenu(event, '图片操作', items);
  });
}

document.addEventListener('diary-action-error', event => notify(event.detail?.message || '操作失败，请重试'));

function appendAction(container, options) {
  const button = createEchoButton(options);
  container.replaceChildren(button);
  return button;
}

function closeInsertMenu() {
  releaseInsertMenuLayout?.();
  releaseInsertMenuLayout = null;
  activeInsertMenu?.remove();
  activeInsertMenu = null;
  $('#writing-add-action .echo-button')?.setAttribute('aria-expanded', 'false');
}

function writingBlockForRange(range) {
  const editor = $('#writing-body');
  const node = range?.startContainer;
  const element = node?.nodeType === Node.ELEMENT_NODE ? node : node?.parentElement;
  const block = element?.closest('p, div, h1, h2, h3, h4, h5, h6, blockquote, pre, li');
  return block && editor.contains(block) ? block : editor;
}

function applyWritingFormat(format, targetRange) {
  if (writingView !== 0) return;
  const editor = $('#writing-body');
  let range = targetRange?.cloneRange() || currentWritingRange() || savedWritingRange?.cloneRange();
  if (!range || !editor.contains(range.commonAncestorContainer)) {
    // Formatting with no prior caret (e.g. a new note) starts in this editor.
    range = document.createRange();
    range.selectNodeContents(editor);
    range.collapse(false);
  }
  const block = writingBlockForRange(range);
  const selection = window.getSelection();
  if (!selection) return;
  if (['bold', 'italic', 'underline', 'strikeThrough'].includes(format) && range.collapsed && block !== editor) {
    range.selectNodeContents(block);
  }
  editor.focus({ preventScroll: true });
  selection.removeAllRanges();
  selection.addRange(range);
  if (format === 'checklist') {
    toggleChecklist(editor, range);
    prepareChecklists(editor);
    savedWritingRange = currentWritingRange() || savedWritingRange;
    editor.dispatchEvent(new Event('input', { bubbles: true }));
    return;
  }
  const listItem = block.closest('li');
  if (['paragraph', 'insertUnorderedList', 'insertOrderedList'].includes(format)) {
    const container = listItem || block;
    container.querySelectorAll('input[type=checkbox]').forEach(input => {
      if (!listItem || input.closest('li') === listItem) input.remove();
    });
  }
  if (format === 'paragraph' && listItem) {
    document.execCommand(listItem.parentElement.tagName === 'OL' ? 'insertOrderedList' : 'insertUnorderedList', false, null);
  }
  if (format === 'paragraph') document.execCommand('formatBlock', false, 'p');
  else if (/^h[1-6]$/.test(format) || format === 'blockquote') document.execCommand('formatBlock', false, format);
  else document.execCommand(format, false, null);
  normalizeEditingLists(editor);
  prepareChecklists(editor);
  savedWritingRange = currentWritingRange() || savedWritingRange;
  editor.dispatchEvent(new Event('input', { bubbles: true }));
}

const writingFormatGroups = [
  [['正文', 'text.alignleft', 'paragraph'], ['标题 1', 'textformat.size.larger', 'h1'], ['标题 2', 'textformat.size', 'h2']],
  [['项目符号列表', 'list.bullet', 'insertUnorderedList'], ['编号列表', 'list.number', 'insertOrderedList'], ['检查清单', 'checklist', 'checklist'], ['引用', 'quotelevel', 'blockquote']],
  [['加粗', 'bold', 'bold'], ['斜体', 'italic', 'italic'], ['下划线', 'underline', 'underline'], ['删除线', 'strikethrough', 'strikeThrough']],
  [['左对齐', 'text.alignleft', 'justifyLeft'], ['居中对齐', 'text.aligncenter', 'justifyCenter'], ['右对齐', 'text.alignright', 'justifyRight'], ['两端对齐', 'text.justify', 'justifyFull']],
];

function toggleInsertMenu(anchor) {
  closeContextMenu();
  if (activeInsertMenu) {
    closeInsertMenu();
    return;
  }
  savedWritingRange = currentWritingRange() || savedWritingRange;
  const formatRange = savedWritingRange?.cloneRange();

  const menu = document.createElement('div');
  menu.className = 'context-menu diary-insert-menu';
  menu.setAttribute('role', 'menu');
  menu.setAttribute('aria-label', '插入内容和文字格式');

  const addMenuItem = (label, symbol, action, disabled = false) => {
    const item = document.createElement('button');
    item.className = 'context-menu-item';
    item.type = 'button';
    item.setAttribute('role', 'menuitem');
    item.disabled = disabled;
    const icon = document.createElement('span');
    icon.className = 'context-menu-icon';
    icon.append(iconImage(symbol));
    const text = document.createElement('span');
    text.textContent = label;
    item.append(icon, text);
    item.addEventListener('click', () => {
      closeInsertMenu();
      action();
    });
    menu.append(item);
    return item;
  };
  const addDivider = () => {
    const divider = document.createElement('div');
    divider.className = 'context-menu-separator';
    divider.setAttribute('role', 'separator');
    menu.append(divider);
  };
  const photoItem = addMenuItem('插入照片', 'photo', () => $('#writing-image-picker').click());
  addDivider();
  writingFormatGroups.forEach((group, index) => {
    if (index) addDivider();
    group.forEach(([label, symbol, format]) => addMenuItem(label, symbol, () => applyWritingFormat(format, formatRange), writingView !== 0));
  });
  menu.addEventListener('keydown', event => {
    if (event.key !== 'Escape') return;
    event.preventDefault();
    closeInsertMenu();
    anchor.focus();
  });
  document.body.append(menu);
  activeInsertMenu = menu;

  releaseInsertMenuLayout = fitContextMenu(menu, { anchor });
  anchor.setAttribute('aria-expanded', 'true');
  photoItem.focus({ preventScroll: true });
}

const navigation = createEchoTabs({
  id: 'diary-nav', tabs: 5, activeId: 0, ariaLabel: '主导航',
  items: [
    { icon: '/svg/homepage.svg', iconActive: '/svg/homepage-selected.svg', ariaLabel: '日记' },
    { icon: '/svg/favorites.svg', iconActive: '/svg/favorites-selected.svg', ariaLabel: '收藏' },
    { icon: '/svg/handoff.svg', iconActive: '/svg/handoff-selected.svg', ariaLabel: '接力' },
    { icon: '/svg/trash.svg', iconActive: '/svg/trash-selected.svg', ariaLabel: '回收站' },
    { icon: '/svg/personal.svg', iconActive: '/svg/personal-selected.svg', ariaLabel: '设置' },
  ],
  onChange: index => {
    selecting = false;
    selectedNotes.clear();
    const target = ['list', 'favorites', 'handoff', 'trash', 'account'][index] || 'list';
    setPage(target);
    renderNotes();
    if (target === 'trash') void loadTrash().then(renderTrash).catch(error => notify(error.message));
    showNavigation($('#selection-toolbar'), false);
    if (target === 'handoff') void loadNotes().then(renderNotes);
    if (target === 'account') { void loadLibraryCards(); void handoff.load().catch(error => notify(error.message)); }
  },
});
$('#diary-header').append(navigation);
const handoff = installHandoff({
  requestJSON, notify, createEchoButton,
  getNotes: () => notes.filter(note => !isEmptyNote(note)),
  getHandoffNotes: () => {
    const memberIds = new Set(folders.find(folder => folder.id === handoffFolderId)?.noteIds || []);
    return notes.filter(note => memberIds.has(note.id) && !isEmptyNote(note));
  },
});
$('#notes-view-switch').append(createEchoSegmented({
  ariaLabel: '日记视图', activeId: notesView === 'folders' ? 0 : 1, className: 'diary-writing-mode',
  items: [{ icon: '/svg/bottom-tab-folder.svg', ariaLabel: '文件夹视图' }, { icon: '/svg/bottom-tab-all.svg', ariaLabel: '所有笔记' }],
  onChange: index => { notesView = index === 0 ? 'folders' : 'all'; activeFolderId = null; selecting = false; selectedNotes.clear(); saveNotesView(); setPage('list'); renderNotes(); },
}));
$('#folder-back').append(iconImage('chevron.left'));
$('#folder-back').onclick = () => { activeFolderId = null; renderNotes(); };


appendAction($('#add-action'), { icon: 'plus', size: 'icon', color: 'green', ariaLabel: '新建笔记', onClick: () => {
  if (page === 'list' && notesView === 'folders' && activeFolderId === null) editFolder();
  else createNote();
} });
appendAction($('#edit-action'), {
  icon: 'pencil', size: 'icon', variant: 'switch', ariaLabel: '编辑列表', title: '编辑列表',
  onClick: () => selecting ? stopSelection() : startSelection(),
});
appendAction($('#writing-share'), { icon: 'square.and.arrow.up', size: 'icon', color: 'teal', ariaLabel: '分享笔记', onClick: shareCurrentNote });
appendAction($('#writing-add-action'), {
  icon: 'ellipsis', size: 'icon', variant: 'switch', ariaLabel: '插入', title: '插入',
  className: 'diary-writing-insert-button',
  attributes: { 'aria-haspopup': 'menu', 'aria-expanded': 'false' },
  onClick: (_, button) => toggleInsertMenu(button),
});
appendAction($('#writing-delete-action'), { icon: 'trash.fill', size: 'icon', color: 'red', ariaLabel: '删除当前笔记', onClick: () => confirmDelete([writingNoteId]) });

const systemThemeQuery = window.matchMedia?.('(prefers-color-scheme: light)');

function updateThemeColor() {
  const dark = document.documentElement.dataset.theme === 'dark';
  $('meta[name="theme-color"]').content = dark ? '#000000' : '#f7f4ef';
}

function applyResolvedTheme(theme) {
  document.documentElement.dataset.theme = theme;
  updateThemeColor();
}

function applyThemePreference(preference) {
  const mode = ['system', 'light', 'dark'].includes(preference) ? preference : 'dark';
  const prefersLight = systemThemeQuery?.matches;
  const theme = mode === 'system' ? (prefersLight ? 'light' : 'dark') : mode;
  document.documentElement.dataset.themePreference = mode;
  applyResolvedTheme(theme);
  try { localStorage.setItem('echodiary.theme', mode); } catch {}
}

const handleSystemThemeChange = () => {
  if (setupSystemAppearance) {
    applyResolvedTheme(systemThemeQuery.matches ? 'light' : 'dark');
  } else if (document.documentElement.dataset.themePreference === 'system') {
    applyThemePreference('system');
  }
};
if (systemThemeQuery?.addEventListener) systemThemeQuery.addEventListener('change', handleSystemThemeChange);
else systemThemeQuery?.addListener?.(handleSystemThemeChange);

function restoreSavedThemePreference() {
  setupSystemAppearance = false;
  let preference = 'dark';
  try { preference = localStorage.getItem('echodiary.theme') || 'dark'; } catch {}
  applyThemePreference(preference);
}

$('#theme-control').append(createEchoSegmented({
  ariaLabel: '外观', activeId: ['system', 'light', 'dark'].indexOf(document.documentElement.dataset.themePreference || 'dark'),
  items: [{ label: '系统', ariaLabel: '跟随系统' }, { label: '浅色' }, { label: '深色' }],
  onChange: index => applyThemePreference(['system', 'light', 'dark'][index]),
}));
$('#theme-control .echo-segmented').classList.add('diary-theme-segmented');

const accentInput = $('#accent-color');
let savedAccent = '';
try { savedAccent = localStorage.getItem('echodiary.accent') || ''; } catch {}
const initialAccent = /^#[\da-f]{6}$/i.test(savedAccent) ? savedAccent : '#34c759';
document.documentElement.style.setProperty('--diary-accent', initialAccent);
document.documentElement.style.setProperty('--echo-color-green', initialAccent);
accentInput.value = initialAccent;
$('#accent-swatch').style.backgroundColor = initialAccent;
accentInput.addEventListener('input', () => {
  const color = accentInput.value;
  document.documentElement.style.setProperty('--diary-accent', color);
  document.documentElement.style.setProperty('--echo-color-green', color);
  $('#accent-swatch').style.backgroundColor = color;
  try { localStorage.setItem('echodiary.accent', color); } catch {}
});

const showDateInput = $('#show-date');
let showDatePreference = true;
try { showDatePreference = localStorage.getItem('echodiary.show-date') !== 'false'; } catch {}
showDateInput.checked = showDatePreference;
showDateInput.addEventListener('change', () => {
  try { localStorage.setItem('echodiary.show-date', String(showDateInput.checked)); } catch {}
  $('#writing-created').hidden = !showDateInput.checked;
  renderNotes();
});
$('#writing-created').hidden = !showDateInput.checked;
$('#note-sort').value = noteSortRule;
$('#note-sort').addEventListener('change', event => {
  noteSortRule = event.target.value;
  try { localStorage.setItem('echodiary.note-sort', noteSortRule); } catch {}
  renderNotes();
});

document.querySelectorAll('.diary-settings-group').forEach(group => {
  const heading = group.querySelector('.diary-settings-heading');
  const content = group.querySelector('.diary-settings-group-content');
  const toggle = group.querySelector('[data-settings-collapse]');
  if (!heading || !content || !toggle) return;

  const key = `echodiary.settings.collapsed.${toggle.dataset.settingsCollapse}`;
  let collapsed = toggle.dataset.settingsCollapse === 'handoff';
  try { const saved = localStorage.getItem(key); if (saved !== null) collapsed = saved === 'true'; } catch {}
  const row = createEchoSettingRow({
    icon: heading.querySelector('[data-echo-icon]')?.dataset.echoIcon || 'gear',
    title: heading.querySelector('h1, h2')?.textContent.trim() || '',
    description: heading.querySelector('p')?.textContent.trim() || '',
    // Keep the original content node so stable hooks such as #library-grid remain available.
    content,
    expanded: !collapsed,
    onToggle: expanded => {
      if (expanded && toggle.dataset.settingsCollapse === 'handoff') void handoff.load().catch(error => notify(error.message));
      try { localStorage.setItem(key, String(!expanded)); } catch {}
      row.classList.toggle('is-shadow-visible', expanded && matchMedia('(prefers-reduced-motion: reduce)').matches);
    },
  });
  row.classList.add(
    'diary-settings-kit-row',
    ...[...group.classList].filter(className => className.startsWith('diary-settings-group--')),
  );
  // Let card shadows extend beyond the library content after its fold animation.
  row.classList.toggle('is-shadow-visible', !collapsed);
  row.addEventListener('transitionend', event => {
    if (event.target === row.querySelector('.echo-setting-row__content') && event.propertyName === 'max-height') {
      row.classList.toggle('is-shadow-visible', row.classList.contains('is-expanded'));
    }
  });
  group.replaceWith(row);
});

appendAction($('#settings-refresh-action'), {
  icon: 'arrow.clockwise', size: 'icon', variant: 'switch', ariaLabel: '刷新资源库状态',
  onClick: () => void refreshDiaryData().then(() => notify('资源库状态已刷新')),
});
appendAction($('#settings-add-action'), {
  icon: 'plus', label: '添加日记资源库', color: 'green', ariaLabel: '添加日记资源库', onClick: openAddLibraryModal,
});
appendAction($('#settings-shutdown-action'), {
  icon: 'power', size: 'icon', color: 'red', ariaLabel: '关闭 EchoDiary', onClick: showShutdownModal,
});
$('#check-updates').addEventListener('click', () => notify('当前已是最新版本'));
$('#import-kehua').addEventListener('click', () => void openKehuaImportModal());

function showShutdownModal() {
  const modal = createEchoModal({
    title: '关闭 EchoDiary？', icon: 'power', addition: '关闭后当前页面会断开，已保存的日记不会受到影响。',
    actions: [
      { label: '取消', icon: 'xmark', color: 'gray' },
      { label: '关闭 EchoDiary', icon: 'power', color: 'red', close: false, onClick: async () => {
        try {
          await requestJSON('/api/settings/shutdown', { method: 'POST' });
          modal.echoModal.close();
          notify('EchoDiary 正在关闭');
        } catch (error) {
          notify(error.message);
        }
      } },
    ],
  });
  document.body.append(modal);
  modal.echoModal.open($('#settings-shutdown-action'));
  modal.addEventListener('transitionend', () => {
    if (!modal.classList.contains('is-open')) modal.remove();
  }, { once: true });
}

$('#writing-mode').append(createEchoSegmented({
  ariaLabel: '笔记显示方式', activeId: 0, className: 'diary-writing-mode',
  items: [{ icon: 'pencil', ariaLabel: '富文本编辑' }, { icon: 'eye.fill', ariaLabel: '预览笔记' }, { icon: '/svg/tab-markdown.svg', ariaLabel: 'Markdown 源文' }],
  onChange: setWritingView,
}));

$('#writing-back').append(iconImage('chevron.left'));
$('#writing-back').append(document.createTextNode('所有笔记'));
$('#writing-back').addEventListener('click', returnFromWriting);
$('#writing-title').addEventListener('input', () => { resizeWritingTitle(); updateDraft(); });
installMarkdownShortcuts($('#writing-body'));
document.execCommand('defaultParagraphSeparator', false, 'p');
$('#writing-body').addEventListener('beforeinput', handleChecklistInput);
for (const container of [$('#writing-body'), $('#writing-preview')]) {
  container.addEventListener('change', event => {
    const checkbox = event.target;
    if (!checkbox.matches('input[type=checkbox]') || !checkbox.closest('li')) return;
    const note = notes.find(item => item.id === writingNoteId);
    if (!note || writingRenderedNoteId !== note.id) return;
    checkbox.toggleAttribute('checked', checkbox.checked);
    try {
      const html = editorHTML(container);
      const draft = { bodyFormat: note.bodyFormat === 'markdown' ? 'markdown' : '',
        bodyMarkdown: note.bodyFormat === 'markdown' ? htmlMarkdown(html) : '', bodyHtml: html, body: htmlText(html) };
      applyWritingDraft(note, draft);
      if (container.id === 'writing-preview') renderWritingEditor(note.body);
      $('#writing-markdown').value = note.bodyMarkdown;
      writingMarkdownBaseline = note.bodyMarkdown;
    } catch {
      checkbox.checked = !checkbox.checked;
      checkbox.toggleAttribute('checked', checkbox.checked);
      notify('清单保存失败，已保留原内容');
    }
  });
}
$('#writing-body').addEventListener('input', updateDraft);
$('#writing-markdown').addEventListener('input', () => { updateDraft(); resizeMarkdownEditor(); });
$('#writing-body').addEventListener('paste', event => {
  const clipboard = event.clipboardData;
  if (!clipboard || clipboard.files.length) return;
  const html = clipboard.getData('text/html');
  const text = clipboard.getData('text/plain');
  if (!html && !text) return;
  event.preventDefault();
  try {
    const content = pastedHTML(html, text);
    // Unsupported/empty clipboard HTML must not delete selected note content.
    if (!content) return;
    document.execCommand('insertHTML', false, content);
    normalizeEditingLists(event.currentTarget);
    prepareChecklists(event.currentTarget);
    updateDraft();
  } catch {
    notify('粘贴内容转换失败，已保留原内容');
  }
});
$('#writing-body').addEventListener('contextmenu', event => {
  if (writingView !== 0 || event.target.closest('.diary-inline-image')) return;
  const selection = window.getSelection();
  const selected = currentWritingRange();
  const withinSelection = selected && !selected.collapsed && [...selected.getClientRects()].some(rect =>
    event.clientX >= rect.left && event.clientX <= rect.right && event.clientY >= rect.top && event.clientY <= rect.bottom);
  const range = (withinSelection ? selected : writingRangeAtPoint(event.clientX, event.clientY) || selected)?.cloneRange();
  if (range) {
    selection.removeAllRanges(); selection.addRange(range);
    savedWritingRange = range.cloneRange();
  }
  closeInsertMenu();
  const items = writingFormatGroups.flatMap((group, index) => [
    ...(index ? ['-'] : []),
    ...group.map(([label, icon, format]) => ({ label, icon, action: () => applyWritingFormat(format, range) })),
  ]);
  showContextMenu(event, '文字格式', items);
});
$('#writing-add-action').addEventListener('pointerdown', () => {
  savedWritingRange = currentWritingRange() || savedWritingRange;
});
document.addEventListener('selectionchange', () => {
  if (page !== 'writing') return;
  const range = currentWritingRange();
  if (range) savedWritingRange = range;
});
$('#diary-content').addEventListener('scroll', event => {
  if (page !== 'writing') return;
  const nextTop = event.currentTarget.scrollTop;
  const writingTop = $('#writing-top');
  writingTop.classList.toggle('is-created-hidden', nextTop >= 1);
});
document.addEventListener('pointerdown', event => {
  if (!activeInsertMenu) return;
  if (activeInsertMenu.contains(event.target) || $('#writing-add-action').contains(event.target)) return;
  closeInsertMenu();
});
window.addEventListener('resize', closeInsertMenu);
window.addEventListener('resize', () => { resizeMarkdownEditor(); resizeWritingTitle(); });

document.addEventListener('gesturestart', event => event.preventDefault(), { passive: false });
document.addEventListener('gesturechange', event => event.preventDefault(), { passive: false });
document.addEventListener('gestureend', event => event.preventDefault(), { passive: false });
document.addEventListener('touchmove', event => {
  if (event.touches.length > 1 && !event.target.closest('#photo-viewer')) event.preventDefault();
}, { passive: false });
document.addEventListener('dblclick', event => event.preventDefault(), { passive: false });

$('#writing-image-picker').addEventListener('change', event => {
  const files = [...event.currentTarget.files];
  event.currentTarget.value = '';
  void addImages(files);
});
$('#writing-view').addEventListener('dragover', event => {
  if ([...event.dataTransfer.types].includes('Files')) {
    event.preventDefault();
    $('#writing-view').classList.add('is-image-dragging');
  }
});
$('#writing-view').addEventListener('dragleave', event => {
  if (!event.currentTarget.contains(event.relatedTarget)) event.currentTarget.classList.remove('is-image-dragging');
});
$('#writing-view').addEventListener('drop', event => {
  event.preventDefault();
  $('#writing-view').classList.remove('is-image-dragging');
  savedWritingRange = writingRangeAtPoint(event.clientX, event.clientY) || currentWritingRange() || savedWritingRange;
  void addImages([...event.dataTransfer.files]);
});
document.addEventListener('paste', event => {
  if (page !== 'writing') return;
  const files = [...(event.clipboardData?.items || [])]
    .filter(item => item.kind === 'file' && item.type.startsWith('image/'))
    .map(item => item.getAsFile()).filter(Boolean);
  if (files.length) {
    event.preventDefault();
    void addImages(files);
  }
});
$('#photo-viewer').addEventListener('click', event => {
  if (event.target === event.currentTarget) closeImageViewer();
});
$('#photo-viewer-image').addEventListener('click', event => {
  event.stopPropagation();
  setPhotoViewerScale(photoViewerScale > 1 ? 1 : 2);
});
$('#photo-viewer-image').addEventListener('dblclick', event => {
  event.preventDefault();
  event.stopPropagation();
  setPhotoViewerScale(photoViewerScale > 1 ? 1 : 2);
});
$('#photo-viewer').addEventListener('wheel', event => {
  if (!$('#photo-viewer-image').getAttribute('src')) return;
  event.preventDefault();
  setPhotoViewerScale(photoViewerScale + (event.deltaY < 0 ? .25 : -.25));
}, { passive: false });
$('#photo-viewer').addEventListener('touchstart', event => {
  if (event.touches.length !== 2) return;
  const [a, b] = event.touches;
  photoPinchStartDistance = Math.hypot(a.clientX - b.clientX, a.clientY - b.clientY);
  photoPinchStartScale = photoViewerScale;
}, { passive: true });
$('#photo-viewer').addEventListener('touchmove', event => {
  if (event.touches.length !== 2 || !photoPinchStartDistance) return;
  event.preventDefault();
  const [a, b] = event.touches;
  const distance = Math.hypot(a.clientX - b.clientX, a.clientY - b.clientY);
  setPhotoViewerScale(photoPinchStartScale * distance / photoPinchStartDistance);
}, { passive: false });
$('#photo-viewer').addEventListener('touchend', event => {
  if (event.touches.length < 2) photoPinchStartDistance = 0;
}, { passive: true });
document.addEventListener('keydown', event => {
  if (selecting && ['list', 'favorites', 'handoff'].includes(page) && (event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'a'
      && !event.target.closest('input, textarea, [contenteditable="true"], [role="dialog"]')
      && !document.querySelector('.echo-modal-overlay.is-open, .echo-modal.is-open')) {
    event.preventDefault();
    const ids = [...list.querySelectorAll('[data-note-id]')].map(card => card.dataset.noteId);
    selectedNotes = new Set(ids);
    selectionAnchorId ||= ids[0] || '';
    renderNotes();
    renderSelectionToolbar();
    return;
  }
  if (event.key !== 'Escape') return;
  const viewer = $('#photo-viewer');
  if (!viewer.hidden) {
    event.preventDefault();
    event.stopImmediatePropagation();
    closeImageViewer();
    return;
  }
  if (document.querySelector('.echo-modal-overlay.is-open')) return;
  if (document.querySelector('.context-menu')) { event.preventDefault(); event.stopImmediatePropagation(); closeContextMenu(true); closeInsertMenu(); return; }
  if (activeInsertMenu) { closeInsertMenu(); return; }
  if (selecting) { event.preventDefault(); stopSelection(); return; }
  if (page === 'writing') { event.preventDefault(); returnFromWriting(); return; }
  if (page === 'list' && notesView === 'folders' && activeFolderId !== null) { event.preventDefault(); activeFolderId = null; renderNotes(); }
}, true);
appendAction($('#photo-viewer-close'), {
  icon: 'xmark', size: 'icon', variant: 'switch', ariaLabel: '关闭图片预览', onClick: closeImageViewer,
});

installEchoIcons();
renderNotes();

function configureAuthForm(state) {
  authMode = state.setupRequired ? 'setup' : 'login';
  const isSetup = authMode === 'setup';
  if (isSetup) {
    setupSystemAppearance = true;
    applyResolvedTheme(systemThemeQuery?.matches ? 'light' : 'dark');
  } else {
    restoreSavedThemePreference();
  }
  $('#diary-app').hidden = true;
  $('#auth-gate').hidden = false;
  $('#auth-gate').classList.toggle('is-setup', isSetup);
  $('#auth-title').textContent = isSetup ? '初始化设置' : 'EchoDiary';
  $('#auth-tagline').textContent = isSetup ? '创建管理员并选择日记资源库。' : '记录此刻，留住回声。';
  const localSetupBlocked = isSetup && state.localSetupRequired;
  $('#auth-local-warning').hidden = !localSetupBlocked;
  $('#auth-form').hidden = localSetupBlocked;
  if (isSetup && !authSubmitButton) {
    authSubmitButton = createEchoButton({
      id: 'auth-submit', label: '创建并开始使用', variant: 'primary', type: 'submit', className: 'diary-auth-submit',
    });
    $('#auth-submit').replaceWith(authSubmitButton);
  }
  if (authSubmitButton) authSubmitButton.echoButton.update({ label: isSetup ? '创建并开始使用' : '登录' });
  $('#auth-confirm-field').hidden = !isSetup;
  $('#auth-confirm').required = isSetup;
  $('#auth-confirm').autocomplete = isSetup ? 'new-password' : 'off';
  $('#auth-library-fields').hidden = !isSetup;
  $('#auth-library-name').required = isSetup;
  $('#auth-library-path').required = isSetup;
  if (isSetup) {
    $('#auth-library-name').value = state.suggestedLibraryName || '';
    $('#auth-library-path').value = state.suggestedLibraryPath || '';
    $('#auth-password').autocomplete = 'new-password';
    $('#auth-username').autocomplete = 'username';
  } else {
    $('#auth-password').autocomplete = 'current-password';
  }
  $('#auth-error').hidden = true;
}

async function bootAuthenticated(username) {
  restoreSavedThemePreference();
  $('#auth-gate').hidden = true;
  $('#diary-app').hidden = false;
  $('#diary-account-name').textContent = username || '已登录';
  await loadLibraryInfo();
  await loadNotes();
  await loadLibraryCards();
  renderNotes();
  startLiveUpdates();
}

$('#auth-form').addEventListener('submit', async event => {
  event.preventDefault();
  const error = $('#auth-error');
  error.hidden = true;
  const username = $('#auth-username').value.trim();
  const password = $('#auth-password').value;
  const submit = $('#auth-submit');
  submit.disabled = true;
  try {
    let response;
    if (authMode === 'setup') {
      if (password !== $('#auth-confirm').value) throw new Error('两次输入的密码不一致。');
      response = await requestJSON('/api/auth/setup', {
        method: 'POST',
        body: JSON.stringify({
          username, password,
          libraryName: $('#auth-library-name').value.trim(),
          libraryPath: $('#auth-library-path').value.trim(),
        }),
      });
    } else {
      response = await requestJSON('/api/auth/login', {
        method: 'POST', body: JSON.stringify({ username, password }),
      });
    }
    await bootAuthenticated(response.username);
  } catch (err) {
    error.textContent = err.message;
    error.hidden = false;
  } finally {
    submit.disabled = false;
  }
});

async function initializeDiary() {
  try {
    const state = await requestJSON('/api/auth/status');
    if (state.authenticated) await bootAuthenticated(state.username);
    else configureAuthForm(state);
  } catch (error) {
    configureAuthForm({ setupRequired: false });
    $('#auth-error').textContent = `无法连接 EchoDiary：${error.message}`;
    $('#auth-error').hidden = false;
  }
}

void initializeDiary();
