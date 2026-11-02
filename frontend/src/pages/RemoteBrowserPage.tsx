import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ArrowLeft, ArrowRight, Keyboard, Loader2, Play, RotateCw } from "lucide-react";
import { mapClientToPage, normalizeUrl, screenDeltaToPage } from "../lib/remoteBrowser";

interface RemoteBrowserStatus {
  running: boolean;
  userTabReady?: boolean;
}

interface FrameMeta {
  deviceWidth: number;
  deviceHeight: number;
}

type ConnState = "connecting" | "connected" | "disconnected";

const TAP_SLOP_PX = 10;
const TAP_MAX_MS = 400;
const SCROLL_INTERVAL_MS = 30;

async function fetchStatus(): Promise<RemoteBrowserStatus> {
  const res = await fetch("/api/remote-browser/status");
  if (!res.ok) throw new Error(`status ${res.status}`);
  return res.json();
}

export function RemoteBrowserPage() {
  const [running, setRunning] = useState<boolean | null>(null);
  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState<string | null>(null);
  const [conn, setConn] = useState<ConnState>("connecting");
  const [frameSrc, setFrameSrc] = useState<string | null>(null);
  const [urlInput, setUrlInput] = useState("");
  const [keyboardOpen, setKeyboardOpen] = useState(false);

  const wsRef = useRef<WebSocket | null>(null);
  const metaRef = useRef<FrameMeta | null>(null);
  const imgRef = useRef<HTMLImageElement | null>(null);
  const kbRef = useRef<HTMLInputElement | null>(null);
  const urlFocusedRef = useRef(false);
  const composingRef = useRef(false);

  const send = useCallback((msg: Record<string, unknown>) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg));
  }, []);

  // Initial status
  useEffect(() => {
    let cancelled = false;
    fetchStatus()
      .then((s) => !cancelled && setRunning(s.running))
      .catch(() => !cancelled && setRunning(false));
    return () => {
      cancelled = true;
    };
  }, []);

  // WebSocket with automatic reconnect while Chrome is running
  useEffect(() => {
    if (!running) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let backoff = 1000;

    const connect = () => {
      setConn("connecting");
      const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
      const ws = new WebSocket(`${protocol}//${window.location.host}/api/remote-browser/ws`);
      wsRef.current = ws;
      ws.onopen = () => {
        backoff = 1000;
        setConn("connected");
      };
      ws.onmessage = (ev) => {
        let msg: { type?: string; data?: string; url?: string; metadata?: FrameMeta };
        try {
          msg = JSON.parse(ev.data);
        } catch {
          return;
        }
        if (msg.type === "frame" && msg.data && msg.metadata) {
          metaRef.current = msg.metadata;
          setFrameSrc(`data:image/jpeg;base64,${msg.data}`);
        } else if (msg.type === "url" && typeof msg.url === "string") {
          if (!urlFocusedRef.current) setUrlInput(msg.url);
        }
      };
      ws.onclose = () => {
        if (disposed || wsRef.current !== ws) return;
        wsRef.current = null;
        setConn("disconnected");
        // The server may have stopped Chrome: re-check before retrying.
        fetchStatus()
          .then((s) => {
            if (disposed) return;
            if (!s.running) setRunning(false);
            else timer = setTimeout(connect, backoff);
          })
          .catch(() => {
            if (!disposed) timer = setTimeout(connect, backoff);
          });
        backoff = Math.min(backoff * 2, 10000);
      };
    };
    connect();

    return () => {
      disposed = true;
      clearTimeout(timer);
      const ws = wsRef.current;
      wsRef.current = null;
      ws?.close();
    };
  }, [running]);

  const start = async () => {
    setStarting(true);
    setStartError(null);
    try {
      const res = await fetch("/api/remote-browser/start", { method: "POST" });
      if (!res.ok) {
        let message = `Failed to start (${res.status})`;
        try {
          const body = await res.json();
          if (body && typeof body.error === "string") message = body.error;
        } catch {
          /* ignore */
        }
        setStartError(message);
        return;
      }
      setRunning(true);
    } catch (e) {
      setStartError(e instanceof Error ? e.message : "Failed to start");
    } finally {
      setStarting(false);
    }
  };

  // ---- gestures ----
  const gesture = useRef<{
    id: number;
    startX: number;
    startY: number;
    lastX: number;
    lastY: number;
    startT: number;
    moved: boolean;
    lastSent: number;
    pendingDX: number;
    pendingDY: number;
  } | null>(null);

  const geometry = () => {
    const img = imgRef.current;
    const meta = metaRef.current;
    if (!img || !meta || !img.naturalWidth) return null;
    const r = img.getBoundingClientRect();
    return {
      rect: { left: r.left, top: r.top, width: r.width, height: r.height },
      iw: img.naturalWidth,
      ih: img.naturalHeight,
      dw: meta.deviceWidth,
      dh: meta.deviceHeight,
    };
  };

  const flushScroll = (g: NonNullable<typeof gesture.current>, clientX: number, clientY: number) => {
    const geo = geometry();
    if (!geo) return;
    const at = mapClientToPage(clientX, clientY, geo.rect, geo.iw, geo.ih, geo.dw, geo.dh);
    if (!at) return;
    // Finger moving up scrolls the page down: invert.
    const d = screenDeltaToPage(-g.pendingDX, -g.pendingDY, geo.rect, geo.iw, geo.ih, geo.dw, geo.dh);
    g.pendingDX = 0;
    g.pendingDY = 0;
    if (d.x === 0 && d.y === 0) return;
    send({ type: "scroll", x: at.x, y: at.y, deltaX: d.x, deltaY: d.y });
  };

  const onPointerDown = (e: React.PointerEvent) => {
    if (gesture.current) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    gesture.current = {
      id: e.pointerId,
      startX: e.clientX,
      startY: e.clientY,
      lastX: e.clientX,
      lastY: e.clientY,
      startT: Date.now(),
      moved: false,
      lastSent: 0,
      pendingDX: 0,
      pendingDY: 0,
    };
  };

  const onPointerMove = (e: React.PointerEvent) => {
    const g = gesture.current;
    if (!g || g.id !== e.pointerId) return;
    if (!g.moved && Math.hypot(e.clientX - g.startX, e.clientY - g.startY) > TAP_SLOP_PX) g.moved = true;
    if (!g.moved) return;
    g.pendingDX += e.clientX - g.lastX;
    g.pendingDY += e.clientY - g.lastY;
    g.lastX = e.clientX;
    g.lastY = e.clientY;
    const now = Date.now();
    if (now - g.lastSent >= SCROLL_INTERVAL_MS) {
      g.lastSent = now;
      flushScroll(g, e.clientX, e.clientY);
    }
  };

  const onPointerUp = (e: React.PointerEvent) => {
    const g = gesture.current;
    if (!g || g.id !== e.pointerId) return;
    gesture.current = null;
    if (g.moved) {
      g.pendingDX += e.clientX - g.lastX;
      g.pendingDY += e.clientY - g.lastY;
      flushScroll(g, e.clientX, e.clientY);
      return;
    }
    if (Date.now() - g.startT > TAP_MAX_MS * 3) return;
    const geo = geometry();
    if (!geo) return;
    const at = mapClientToPage(e.clientX, e.clientY, geo.rect, geo.iw, geo.ih, geo.dw, geo.dh);
    if (at) send({ type: "tap", x: at.x, y: at.y });
  };

  const onPointerCancel = () => {
    gesture.current = null;
  };

  const onWheel = (e: React.WheelEvent) => {
    const geo = geometry();
    if (!geo) return;
    const at = mapClientToPage(e.clientX, e.clientY, geo.rect, geo.iw, geo.ih, geo.dw, geo.dh);
    if (at) send({ type: "scroll", x: at.x, y: at.y, deltaX: e.deltaX, deltaY: e.deltaY });
  };

  // ---- keyboard ----
  const sendKey = (key: string, code: string, keyCode: number, text?: string) =>
    send({ type: "key", action: "press", key, code, keyCode, ...(text ? { text } : {}) });

  const specialKeys: Record<string, [string, number]> = {
    Enter: ["Enter", 13],
    Backspace: ["Backspace", 8],
    Tab: ["Tab", 9],
    Escape: ["Escape", 27],
    ArrowUp: ["ArrowUp", 38],
    ArrowDown: ["ArrowDown", 40],
    ArrowLeft: ["ArrowLeft", 37],
    ArrowRight: ["ArrowRight", 39],
  };

  // Forward typed text, then clear the field so nothing lingers in the DOM.
  const flushKeyboardText = (el: HTMLInputElement) => {
    const text = el.value;
    el.value = "";
    if (text) send({ type: "text", text });
  };

  const onKbKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.nativeEvent.isComposing || e.keyCode === 229) return;
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    const k = specialKeys[e.key];
    if (!k) return;
    e.preventDefault();
    sendKey(k[0], e.code || k[0], k[1]);
  };

  // Virtual keyboards (Android) may not emit usable keydown for these.
  useEffect(() => {
    const el = kbRef.current;
    if (!el) return;
    const onBeforeInput = (ev: InputEvent) => {
      if (ev.inputType === "deleteContentBackward" && el.value === "") {
        ev.preventDefault();
        sendKey("Backspace", "Backspace", 8);
      } else if (ev.inputType === "insertLineBreak" || ev.inputType === "insertParagraph") {
        ev.preventDefault();
        sendKey("Enter", "Enter", 13);
      }
    };
    el.addEventListener("beforeinput", onBeforeInput);
    return () => el.removeEventListener("beforeinput", onBeforeInput);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [running]);

  const toggleKeyboard = () => {
    const el = kbRef.current;
    if (!el) return;
    if (document.activeElement === el) el.blur();
    else el.focus(); // must happen inside the click handler for iOS
  };

  const submitUrl = (e: React.FormEvent) => {
    e.preventDefault();
    const url = normalizeUrl(urlInput);
    if (!url) return;
    send({ type: "navigate", url });
    (document.activeElement as HTMLElement | null)?.blur();
  };

  if (running === null) {
    return (
      <div className="h-dvh flex items-center justify-center text-gray-400">
        <Loader2 className="h-6 w-6 animate-spin" />
      </div>
    );
  }

  const connColor =
    conn === "connected" ? "bg-green-500" : conn === "connecting" ? "bg-yellow-400" : "bg-red-500";
  const connLabel =
    conn === "connected" ? "Connected" : conn === "connecting" ? "Connecting..." : "Disconnected. Reconnecting...";

  return (
    <div className="h-dvh flex flex-col bg-white overflow-hidden">
      <header className="flex items-center gap-2 px-2 py-1.5 border-b border-gray-200">
        <Link to="/" className="p-1.5 text-gray-500 hover:text-gray-700 rounded-lg hover:bg-gray-100" title="Back to sessions">
          <ArrowLeft className="h-5 w-5" />
        </Link>
        <h1 className="text-sm font-semibold text-gray-800">Remote Browser</h1>
        {running && (
          <span className="ml-auto flex items-center gap-1.5 text-xs text-gray-500" data-testid="rb-conn" data-state={conn}>
            <span className={`h-2 w-2 rounded-full ${connColor}`} />
            {connLabel}
          </span>
        )}
      </header>

      {!running ? (
        <div className="flex-1 flex flex-col items-center justify-center gap-4 p-6 text-center">
          <p className="text-sm text-gray-600">Remote browser is not running.</p>
          <button
            onClick={start}
            disabled={starting}
            className="flex items-center gap-2 px-4 py-2 rounded-lg bg-blue-600 text-white text-sm hover:bg-blue-700 disabled:opacity-50"
          >
            {starting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />}
            Start
          </button>
          {startError && (
            <p role="alert" className="text-sm text-red-600 max-w-sm break-words">
              {startError}
            </p>
          )}
        </div>
      ) : (
        <>
          <form onSubmit={submitUrl} className="flex gap-2 px-2 py-1.5 border-b border-gray-200">
            <input
              type="text"
              inputMode="url"
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
              value={urlInput}
              onChange={(e) => setUrlInput(e.target.value)}
              onFocus={(e) => {
                urlFocusedRef.current = true;
                e.currentTarget.select();
              }}
              onBlur={() => {
                urlFocusedRef.current = false;
              }}
              placeholder="URL"
              aria-label="URL"
              className="flex-1 min-w-0 px-3 py-1.5 text-base border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
            <button type="submit" className="px-3 text-sm rounded-lg bg-blue-600 text-white hover:bg-blue-700">
              Go
            </button>
          </form>

          <div className="relative flex-1 min-h-0 bg-gray-100">
            {frameSrc ? (
              <img
                ref={imgRef}
                src={frameSrc}
                alt="Remote browser"
                draggable={false}
                onMouseDown={(e) => e.preventDefault() /* keep the software keyboard open */}
                onPointerDown={onPointerDown}
                onPointerMove={onPointerMove}
                onPointerUp={onPointerUp}
                onPointerCancel={onPointerCancel}
                onWheel={onWheel}
                onContextMenu={(e) => e.preventDefault()}
                className="absolute inset-0 w-full h-full object-contain select-none"
                style={{ touchAction: "none", WebkitTouchCallout: "none", WebkitUserSelect: "none" }}
              />
            ) : (
              <div className="absolute inset-0 flex items-center justify-center text-gray-400 text-sm">
                <Loader2 className="h-5 w-5 animate-spin mr-2" />
                {conn === "connected" ? "Waiting for frames..." : connLabel}
              </div>
            )}
          </div>

          <div className="flex items-center justify-around px-2 py-1.5 border-t border-gray-200">
            <button
              onClick={() => send({ type: "back" })}
              className="p-2.5 text-gray-600 rounded-lg hover:bg-gray-100"
              title="Back"
              aria-label="Back"
            >
              <ArrowLeft className="h-5 w-5" />
            </button>
            <button
              onClick={() => send({ type: "reload" })}
              className="p-2.5 text-gray-600 rounded-lg hover:bg-gray-100"
              title="Reload"
              aria-label="Reload"
            >
              <RotateCw className="h-5 w-5" />
            </button>
            <button
              onClick={toggleKeyboard}
              className={`p-2.5 rounded-lg hover:bg-gray-100 ${keyboardOpen ? "text-blue-600 bg-blue-50" : "text-gray-600"}`}
              title="Keyboard"
              aria-label="Keyboard"
            >
              <Keyboard className="h-5 w-5" />
            </button>
            <button
              onClick={() => send({ type: "key", action: "press", key: "Enter", code: "Enter", keyCode: 13 })}
              className="p-2.5 text-gray-600 rounded-lg hover:bg-gray-100"
              title="Enter"
              aria-label="Enter"
            >
              <ArrowRight className="h-5 w-5" />
            </button>
          </div>

          {/* Text sink for the software keyboard. Visually hidden but focusable;
              16px font avoids iOS zoom. Content is forwarded and cleared immediately. */}
          <input
            ref={kbRef}
            type="text"
            aria-label="Remote keyboard input"
            autoCapitalize="off"
            autoCorrect="off"
            autoComplete="off"
            spellCheck={false}
            onFocus={() => setKeyboardOpen(true)}
            onBlur={() => setKeyboardOpen(false)}
            onKeyDown={onKbKeyDown}
            onCompositionStart={() => {
              composingRef.current = true;
            }}
            onCompositionEnd={(e) => {
              composingRef.current = false;
              flushKeyboardText(e.currentTarget);
            }}
            onInput={(e) => {
              if (composingRef.current) return;
              flushKeyboardText(e.currentTarget);
            }}
            className="fixed bottom-0 left-0 w-px h-px opacity-0 pointer-events-none"
            style={{ fontSize: 16 }}
          />
        </>
      )}
    </div>
  );
}
