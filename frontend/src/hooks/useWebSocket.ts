import { useEffect, useRef, useCallback, useState } from "react";

interface WSMessage {
  type: string;
  data: unknown;
}

export type WSConnectionState = "connecting" | "connected" | "disconnected";

export function useWebSocket(onMessage: (msg: WSMessage) => void) {
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const backoffRef = useRef(1000); // Start with 1s backoff
  const [connectionState, setConnectionState] = useState<WSConnectionState>("connecting");

  const connect = useCallback(() => {
    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(`${protocol}//${window.location.host}/ws`);
    wsRef.current = ws;

    setConnectionState("connecting");

    ws.onopen = () => {
      console.log("WebSocket connected");
      backoffRef.current = 1000; // Reset backoff on successful connection
      setConnectionState("connected");
    };

    ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data) as WSMessage;
        onMessage(msg);
      } catch {
        console.error("Failed to parse WS message");
      }
    };

    ws.onclose = () => {
      // Only the socket we currently own may schedule a reconnect. When the
      // effect cleanup closes a socket it clears the reconnect timer *before*
      // close() and onclose fires afterwards, so an unguarded reconnect here
      // would open an extra connection that nobody cleans up. That extra
      // socket stays live next to the one the current effect created and
      // delivers every message a second time to the same handler, which is
      // how live stream lines ended up rendered twice (issue #709).
      if (wsRef.current !== ws) return;
      setConnectionState("disconnected");
      const delay = backoffRef.current;
      console.log(`WebSocket disconnected, reconnecting in ${delay}ms...`);
      reconnectTimer.current = setTimeout(connect, delay);
      // Exponential backoff: 1s -> 2s -> 4s -> 8s -> 16s -> 30s (max)
      backoffRef.current = Math.min(backoffRef.current * 2, 30000);
    };
  }, [onMessage]);

  useEffect(() => {
    connect();
    return () => {
      clearTimeout(reconnectTimer.current);
      const ws = wsRef.current;
      wsRef.current = null;
      ws?.close();
    };
  }, [connect]);

  return { connectionState };
}
