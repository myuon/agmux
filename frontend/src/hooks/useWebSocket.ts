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
      setConnectionState("disconnected");
      const delay = backoffRef.current;
      console.log(`WebSocket disconnected, reconnecting in ${delay}ms...`);
      reconnectTimer.current = setTimeout(connect, delay);
      // Exponential backoff: 1s -> 2s -> 4s -> 8s -> 16s -> 30s (max)
      backoffRef.current = Math.min(backoffRef.current * 2, 30000);
    };

    wsRef.current = ws;
  }, [onMessage]);

  useEffect(() => {
    connect();
    return () => {
      clearTimeout(reconnectTimer.current);
      wsRef.current?.close();
    };
  }, [connect]);

  return { connectionState };
}
