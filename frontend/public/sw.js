// Minimal service worker for PWA installability and push notifications

// Required for PWA installability - network-first strategy with offline fallback
self.addEventListener("fetch", (event) => {
  event.respondWith(
    fetch(event.request).catch(
      () =>
        new Response("Offline - please check your network connection.", {
          status: 503,
          statusText: "Service Unavailable",
          headers: { "Content-Type": "text/plain" },
        }),
    ),
  );
});

// Handle push notification clicks
self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const sessionId = event.notification.data?.sessionId;
  // data.url is a same-origin path (e.g. the remote browser login page); ignore anything else.
  const url = event.notification.data?.url;
  const targetUrl =
    typeof url === "string" && url.startsWith("/") && !url.startsWith("//")
      ? url
      : sessionId
        ? `/sessions/${sessionId}`
        : "/";
  event.waitUntil(clients.openWindow(targetUrl));
});
