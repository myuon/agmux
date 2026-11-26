import { useEffect, useRef, useSyncExternalStore } from "react";
import { isPageHidden } from "./usePageVisible";

interface WSMessage {
  type: string;
  data: unknown;
}

// "paused" = the socket was closed on purpose because the tab is hidden. It is
// not an error: UIs must not show disconnect toasts for it.
export type WSConnectionState = "connecting" | "connected" | "disconnected" | "paused";

// The app shares ONE WebSocket connection. It is opened when the first
// subscriber mounts and closed when the last one unmounts (ref-counted).
type MessageListener = (msg: WSMessage) => void;
type StateListener = (state: WSConnectionState) => void;

const messageListeners = new Set<MessageListener>();
const stateListeners = new Set<StateListener>();
let ws: WebSocket | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
let backoff = 1000; // Start with 1s backoff
let currentState: WSConnectionState = "connecting";

// stream_update is only delivered for subscribed session IDs. Every
// useStreamSubscription caller registers its IDs here; the union is sent to
// the server whenever it changes and on every (re)connect.
const streamSubscriptions = new Set<string[]>();
let lastSentSubscription = "[]";

function subscriptionUnion(): string[] {
  const ids = new Set<string>();
  streamSubscriptions.forEach((list) => list.forEach((id) => ids.add(id)));
  return [...ids].sort();
}

function sendSubscriptions() {
  if (!ws || ws.readyState !== WebSocket.OPEN) return;
  const key = JSON.stringify(subscriptionUnion());
  if (key === lastSentSubscription) return;
  lastSentSubscription = key;
  ws.send(JSON.stringify({ type: "subscribe", sessionIds: JSON.parse(key) }));
}

function setState(state: WSConnectionState) {
  currentState = state;
  stateListeners.forEach((l) => l(state));
}

function connect() {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const socket = new WebSocket(`${protocol}//${window.location.host}/ws`);
  ws = socket;
  setState("connecting");

  socket.onopen = () => {
    console.log("WebSocket connected");
    backoff = 1000; // Reset backoff on successful connection
    lastSentSubscription = "[]"; // a new connection starts with no subscriptions
    sendSubscriptions();
    setState("connected");
  };

  socket.onmessage = (event) => {
    let msg: WSMessage;
    try {
      msg = JSON.parse(event.data) as WSMessage;
    } catch {
      console.error("Failed to parse WS message");
      return;
    }
    messageListeners.forEach((l) => {
      try {
        l(msg);
      } catch (e) {
        console.error("WS listener error", e);
      }
    });
  };

  socket.onclose = () => {
    // Only the socket we currently own may schedule a reconnect (see #709):
    // closing on last unsubscribe clears ws before onclose fires.
    if (ws !== socket) return;
    setState("disconnected");
    const delay = backoff;
    console.log(`WebSocket disconnected, reconnecting in ${delay}ms...`);
    reconnectTimer = setTimeout(connect, delay);
    // Exponential backoff: 1s -> 2s -> 4s -> 8s -> 16s -> 30s (max)
    backoff = Math.min(backoff * 2, 30000);
  };
}

function disconnect() {
  clearTimeout(reconnectTimer);
  reconnectTimer = undefined;
  const socket = ws;
  ws = null;
  backoff = 1000;
  currentState = "connecting";
  socket?.close();
}

// Close the socket while the tab is hidden and reopen it on return, so a
// backgrounded mobile tab neither receives pushes nor keeps reconnecting.
function pause() {
  clearTimeout(reconnectTimer);
  reconnectTimer = undefined;
  const socket = ws;
  ws = null; // onclose of this socket must not schedule a reconnect
  socket?.close();
  setState("paused");
}

function onVisibilityChange() {
  if (isPageHidden()) {
    if (currentState !== "paused") pause();
  } else if (currentState === "paused") {
    backoff = 1000;
    connect();
  }
}

function start() {
  document.addEventListener("visibilitychange", onVisibilityChange);
  if (isPageHidden()) setState("paused");
  else connect();
}

function stop() {
  document.removeEventListener("visibilitychange", onVisibilityChange);
  disconnect();
}

function subscribeState(listener: StateListener) {
  stateListeners.add(listener);
  return () => {
    stateListeners.delete(listener);
  };
}

function getState() {
  return currentState;
}

export function useWebSocket(onMessage: (msg: WSMessage) => void) {
  const handlerRef = useRef(onMessage);
  useEffect(() => {
    handlerRef.current = onMessage;
  });
  const connectionState = useSyncExternalStore(subscribeState, getState);

  useEffect(() => {
    const messageListener: MessageListener = (msg) => handlerRef.current(msg);
    messageListeners.add(messageListener);
    if (messageListeners.size === 1) start();
    return () => {
      messageListeners.delete(messageListener);
      if (messageListeners.size === 0) stop();
    };
  }, []);

  return { connectionState };
}

/**
 * Subscribe to stream_update for the given session IDs while mounted. The
 * server acks every subscription change with a "subscribed" message, which
 * consumers can use to catch up on lines missed before it took effect.
 */
export function useStreamSubscription(sessionIds: string[]) {
  const key = sessionIds.join("\n");
  useEffect(() => {
    const list = key ? key.split("\n") : [];
    streamSubscriptions.add(list);
    sendSubscriptions();
    return () => {
      streamSubscriptions.delete(list);
      sendSubscriptions();
    };
  }, [key]);
}
