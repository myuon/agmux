// Minimal service worker for push notifications
self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const sessionId = event.notification.data?.sessionId;
  const targetUrl = sessionId ? `/sessions/${sessionId}` : "/";
  event.waitUntil(clients.openWindow(targetUrl));
});
