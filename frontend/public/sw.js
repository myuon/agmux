// Minimal service worker for push notifications
self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const sessionId = event.notification.data?.sessionId;
  const targetUrl = sessionId ? `/sessions/${sessionId}` : "/";
  event.waitUntil(
    clients.matchAll({ type: "window" }).then((clientList) => {
      // If there's already an open window, navigate it to the target URL
      if (clientList.length > 0) {
        const client = clientList[0];
        client.navigate(targetUrl);
        return client.focus();
      }
      return clients.openWindow(targetUrl);
    })
  );
});
