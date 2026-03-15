// Minimal service worker for PWA installability and push notifications

// Required for PWA installability - respond with network-first strategy
self.addEventListener("fetch", (event) => {
  event.respondWith(fetch(event.request));
});

// Handle push notification clicks
self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const sessionId = event.notification.data?.sessionId;
  const targetUrl = sessionId ? `/sessions/${sessionId}` : "/";
  event.waitUntil(clients.openWindow(targetUrl));
});
