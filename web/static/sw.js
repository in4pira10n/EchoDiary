const VERSION = '{{ASSET_VERSION}}';
const CACHE_NAME = `echodiary-pwa-${VERSION}`;
const APP_SHELL = [
  '/',
  '/manifest.webmanifest?v=20261107-new-icon',
  '/pwa.js?v=20261103-pwa',
  '/app.css?v=20261111-menu-layout',
  '/app.js?v=20261111-menu-layout',
  '/diary-modal.js?v=20261107-modal-tone',
  '/context-menu.js?v=20261111-menu-layout',
  '/handoff.js?v=20261107-modal-tone',
  '/content.js?v=20261111-editor-fixes',
  '/checklist.js?v=20261111-editor-fixes',
  '/document-structure.js?v=20261111-editor-fixes',
  '/markdown-shortcuts.js?v=20261111-editor-fixes',
  '/svg/homepage.svg',
  '/svg/homepage-selected.svg',
  '/svg/favorites.svg',
  '/svg/favorites-selected.svg',
  '/svg/bottom-tab-all.svg',
  '/svg/bottom-tab-all-selected.svg',
  '/svg/bottom-tab-folder.svg',
  '/svg/bottom-tab-folder-selected.svg',
  '/svg/handoff.svg',
  '/svg/handoff-selected.svg',
  '/svg/trash.svg',
  '/svg/trash-selected.svg',
  '/svg/personal.svg',
  '/svg/personal-selected.svg',
  '/svg/tab-markdown.svg',
  '/vendor/dompurify.js?v=20261103-pwa',
  '/vendor/marked.js?v=20261103-pwa',
  '/vendor/turndown.js?v=20261103-pwa',
  '/assets/echodiary-icon.png?v=20261107-new-icon',
  '/assets/apple-touch-icon.png?v=20261107-new-icon',
  '/assets/favicon.ico?v=20261107-new-icon',
  '/assets/favicon-32.png?v=20261107-new-icon',
  '/assets/favicon-16.png?v=20261107-new-icon',
  '/assets/pwa-icon-192.png?v=20261107-new-icon',
  '/assets/pwa-icon-512.png?v=20261107-new-icon'
];

const APP_KIT_ASSETS = [
  '/kit/css/echokit.css',
  '/kit/css/base.css',
  '/kit/tokens/tokens.css?v=20261011-complete-blue',
  '/kit/tokens/ios.css?v=20261008-ios-roles',
  '/kit/components/button/index.js',
  '/kit/components/button/button.js',
  '/kit/components/tab/index.js',
  '/kit/components/tab/tab.js',
  '/kit/components/modal/index.js',
  '/kit/components/modal/modal.js',
  '/kit/components/segmented/index.js',
  '/kit/components/segmented/segmented.js',
  '/kit/components/setting-row/index.js',
  '/kit/components/setting-row/setting-row.js',
  '/kit/components/library-card/index.js',
  '/kit/components/library-card/library-card.js',
  '/kit/components/metric/index.js',
  '/kit/components/metric/metric.js',
  '/kit/components/icon.js',
  '/kit/components/button/button.css?v=20261011-complete-blue',
  '/kit/components/tab/tab.css',
  '/kit/components/modal/modal.css?v=20261010-modal-gradient-icon',
  '/kit/components/segmented/segmented.css?v=20261009-segmented-control',
  '/kit/components/setting-row/setting-row.css?v=20261010-setting-row-colors',
  '/kit/components/library-card/library-card.css?v=20261010-neutral-card-contrast',
  '/kit/components/metric/metric.css',
  '/kit/components/input/input.css?v=20261014-select-field',
  '/kit/components/surface/surface.css',
  '/kit/SFSymbols/chevron.down.svg',
  ...['checkmark.circle', 'arrow.right.circle', 'pencil', 'star', 'star.slash', 'folder', 'folder.fill', 'folder.badge.plus', 'list.bullet', 'arrow.clockwise', 'bookmark', 'bookmark.slash', 'square.on.square', 'arrow.down.document', 'trash', 'arrow.up.left.and.arrow.down.right', 'arrow.down.to.line', 'link'].map(name => `/kit/SFSymbols/${name}.svg?v=2026-10-08`)
];

self.addEventListener('install', event => {
  event.waitUntil(
    caches.open(CACHE_NAME).then(async cache => {
      await cache.addAll(APP_SHELL.map(url => new Request(url, { cache: 'reload' })));
      await Promise.all(APP_KIT_ASSETS.map(url => cache.add(new Request(url, { cache: 'reload' })).catch(() => undefined)));
    })
      .then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', event => {
  event.waitUntil(
    caches.keys()
      .then(keys => Promise.all(keys.filter(key => key.startsWith('echodiary-pwa-') && key !== CACHE_NAME).map(key => caches.delete(key))))
      .then(() => self.clients.claim())
  );
});

function shouldBypass(request) {
  const url = new URL(request.url);
  return request.method !== 'GET'
    || url.origin !== self.location.origin
    || url.pathname.startsWith('/api/')
    || url.pathname.startsWith('/media/');
}

self.addEventListener('fetch', event => {
  const request = event.request;
  if (shouldBypass(request)) return;
  if (request.mode === 'navigate') {
    event.respondWith(fetch(request).catch(() => caches.match('/')));
    return;
  }
  event.respondWith(
    fetch(request).then(response => {
      if (response.ok) caches.open(CACHE_NAME).then(cache => cache.put(request, response.clone())).catch(() => {});
      return response;
    }).catch(() => caches.match(request).then(cached => cached || Response.error()))
  );
});
