import { useEffect, useRef, useSyncExternalStore } from "react";

interface WSMessage {
  type: string;
  data: unknown;
}

export type WSConnectionState = "connecting" | "connected" | "disconnected";

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
    if (messageListeners.size === 1) connect();
    return () => {
      messageListeners.delete(messageListener);
      if (messageListeners.size === 0) disconnect();
    };
  }, []);

  return { connectionState };
}
