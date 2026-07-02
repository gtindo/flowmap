const fallbackCache = "flowmap-offline-v1";
const fallbackAssets = ["/offline.html"];

self.addEventListener("install", event => {
  event.waitUntil(caches.open(fallbackCache).then(cache => cache.addAll(fallbackAssets)));
  self.skipWaiting();
});

self.addEventListener("activate", event => {
  event.waitUntil(
    caches.keys()
      .then(keys => Promise.all(keys
        .filter(key => key.startsWith("flowmap-offline-") && key !== fallbackCache)
        .map(key => caches.delete(key))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener("fetch", event => {
  if (event.request.mode !== "navigate") return;
  event.respondWith(fetch(event.request).catch(() => caches.match("/offline.html")));
});
