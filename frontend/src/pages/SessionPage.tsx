import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useParams, useNavigate, useLoaderData, Await } from "react-router-dom";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  Square, RefreshCw, Trash2, ArrowLeft, GitBranch, FolderOpen,
  Sparkles, Settings, Copy,
  RotateCcw, ImagePlus, SendHorizonal, Plus, Slash,
  Code, Eye, X, AlertTriangle,
} from "lucide-react";
import { Modal } from "../components/ui/Modal";
import { FileCodeViewer } from "../components/ui/FileCodeViewer";
import { Toast } from "../components/ui/Toast";
import { Chip } from "../components/ui/Chip";
import { IconButton } from "../components/ui/IconButton";
import { IconText } from "../components/ui/IconText";
import { ActionMenu, ActionMenuItem } from "../components/ui/ActionMenu";
import type { Session } from "../types/session";
import { api, type DiffFile } from "../api/client";
import { StatusDot } from "../components/StatusBadge";
import { setActiveSessionName } from "../activeSession";
import { useWebSocket } from "../hooks/useWebSocket";
import { StreamOutputView, ActiveTasksPanel } from "../components/session/StreamOutputView";
import { DiffDropdown } from "../components/session/DiffDropdown";
import { GoalPanel } from "../components/ui/GoalPanel";
import { ConnectionStatusIndicator } from "../components/ui/ConnectionStatus";
import { PullRequestBadge } from "../components/ui/PullRequestBadge";
import { AlertBanner } from "../components/ui/AlertBanner";
import type { StreamEntry } from "../models/stream";
import { extractActiveTasks } from "../models/stream";

type DeferredData = {
  streamOutput: { lines: unknown[]; total: number };
  diff: { files: DiffFile[] };
  providerVersion: string | null;
};

function SessionPageSkeleton() {
  return (
    <div className="h-dvh flex flex-col px-4 sm:px-8 pt-4 sm:pt-8 max-w-4xl mx-auto">
      <div className="flex items-center gap-3 mb-3">
        <div className="w-4 h-4 bg-gray-200 rounded animate-pulse" />
        <div className="w-3 h-3 rounded-full bg-gray-200 animate-pulse" />
        <div className="h-7 w-40 bg-gray-200 rounded animate-pulse" />
      </div>
      <div className="flex-1 bg-gray-50 rounded-lg animate-pulse" />
    </div>
  );
}

export function SessionPage() {
  const loaderData = useLoaderData<{
    session: Session;
    streamOutput: Promise<DeferredData["streamOutput"]>;
    diff: Promise<DeferredData["diff"]>;
    providerVersion: Promise<string | null>;
  }>();

  const deferredPromise = useMemo(
    () =>
      Promise.all([loaderData.streamOutput, loaderData.diff, loaderData.providerVersion]).then(
        ([streamOutput, diff, providerVersion]) => ({ streamOutput, diff, providerVersion })
      ),
    [loaderData.streamOutput, loaderData.diff, loaderData.providerVersion]
  );

  return (
    <Suspense fallback={<SessionPageSkeleton />}>
      <Await resolve={deferredPromise}>
        {(deferred: DeferredData) => (
          <SessionPageInner session={loaderData.session} deferred={deferred} />
        )}
      </Await>
    </Suspense>
  );
}

function SessionPageInner({ session: initialSession, deferred }: { session: Session; deferred: DeferredData }) {
  const { id: sessionId } = useParams<{ id: string }>();
  const navigate = useNavigate();

  const [session, setSession] = useState<Session | null>(initialSession);
  const [message, setMessage] = useState("");
  const [streamLines, setStreamLines] = useState<unknown[]>(deferred.streamOutput.lines);
  const [partialText, setPartialText] = useState("");
  const [diffFiles, setDiffFiles] = useState<DiffFile[]>(deferred.diff.files);
  const [pendingImages, setPendingImages] = useState<{ data: string; mediaType: string; preview: string }[]>([]);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [pendingEscalationId, setPendingEscalationId] = useState<string | null>(null);
  const [escalationMessage, setEscalationMessage] = useState<string | null>(null);
  const [escalationTimedOut, setEscalationTimedOut] = useState(false);
  const [escalationTimeoutSeconds, setEscalationTimeoutSeconds] = useState(300);
  const [pendingPermission, setPendingPermission] = useState<{ id: string; toolName: string; input: unknown; timedOut?: boolean; timeoutSeconds?: number } | null>(null);
  const [reconnectToast, setReconnectToast] = useState(false);
  const [disconnectToast, setDisconnectToast] = useState(false);
  const [clearToast, setClearToast] = useState<"success" | "error" | null>(null);
  const [showSlashMenu, setShowSlashMenu] = useState(false);
  const [showActionMenu, setShowActionMenu] = useState(false);
  const [slashFilter, setSlashFilter] = useState("");
  const [slashSelectedIndex, setSlashSelectedIndex] = useState(0);
  const [claudeMDOpen, setClaudeMDOpen] = useState(false);
  const [claudeMDFiles, setClaudeMDFiles] = useState<{ path: string; content: string }[] | null>(null);
  const [claudeMDLoading, setClaudeMDLoading] = useState(false);
  const [claudeMDViewMode, setClaudeMDViewMode] = useState<"preview" | "source">("preview");
  const [settingsJSONOpen, setSettingsJSONOpen] = useState(false);
  const [settingsJSONFiles, setSettingsJSONFiles] = useState<{ name: string; content: string }[]>([]);
  const [settingsJSONLoading, setSettingsJSONLoading] = useState(false);

  const providerVersion = deferred.providerVersion;
  const streamCursorRef = useRef<number | null>(null);

  // Compute active tasks for fixed display above input
  const activeTasks = useMemo(() => {
    const entries = streamLines
      .map((line) => line as StreamEntry)
      .filter((e: StreamEntry) => e.type === "user" || e.type === "assistant" || e.type === "system");
    return extractActiveTasks(entries);
  }, [streamLines]);

  // Extract slash_commands from system init messages
  const slashCommands = useMemo(() => {
    for (const line of streamLines) {
      const entry = line as Record<string, unknown>;
      if (entry.type === "system" && entry.subtype === "init") {
        const cmds = entry.slash_commands as string[] | undefined;
        return cmds && cmds.length > 0 ? cmds : [];
      }
    }
    return [];
  }, [streamLines]);

  // Track active session name for notification suppression
  useEffect(() => {
    return () => {
      setActiveSessionName(null);
    };
  }, []);

  // Listen for WebSocket messages (escalation + stream updates)
  const handleWsMessage = useCallback((msg: { type: string; data: unknown }) => {
    if (msg.type === "escalation") {
      const data = msg.data as { id: string; sessionId: string; message?: string; timeoutSeconds?: number };
      if (data.sessionId === sessionId) {
        setPendingEscalationId(data.id);
        setEscalationMessage(data.message ?? null);
        setEscalationTimedOut(false);
        setEscalationTimeoutSeconds(data.timeoutSeconds ?? 300);
      }
    }
    if (msg.type === "escalation_timeout") {
      const data = msg.data as { id: string; sessionId: string };
      if (data.sessionId === sessionId) {
        setEscalationTimedOut(true);
      }
    }
    if (msg.type === "permission_prompt") {
      const data = msg.data as { id: string; sessionId: string; toolName: string; input: unknown; timeoutSeconds?: number };
      if (data.sessionId === sessionId) {
        setPendingPermission({ id: data.id, toolName: data.toolName, input: data.input, timeoutSeconds: data.timeoutSeconds ?? 300 });
      }
    }
    if (msg.type === "permission_timeout") {
      const data = msg.data as { id: string; sessionId: string };
      if (data.sessionId === sessionId) {
        setPendingPermission((prev) => prev && prev.id === data.id ? { ...prev, timedOut: true } : prev);
      }
    }
    if (msg.type === "stream_update") {
      const data = msg.data as { sessionId: string; lines: unknown[]; total: number };
      if (data.sessionId === sessionId && data.lines.length > 0) {
        const regular: unknown[] = [];
        for (const line of data.lines) {
          const entry = line as Record<string, unknown>;
          if (entry.type === "stream_event") {
            // Extract text delta from content_block_delta events
            const evt = entry.event as { type?: string; delta?: { type?: string; text?: string } } | undefined;
            if (evt?.type === "content_block_delta" && evt.delta?.type === "text_delta" && evt.delta.text) {
              setPartialText((prev) => prev + evt.delta!.text!);
            }
          } else {
            // Clear partial text when a complete assistant message arrives
            if (entry.type === "assistant") {
              setPartialText("");
            }
            regular.push(line);
          }
        }
        if (regular.length > 0) {
          setStreamLines((prev) => [...prev, ...regular]);
        }
        streamCursorRef.current = data.total;
      }
    }
  }, [sessionId]);
  const { connectionState } = useWebSocket(handleWsMessage);
  const prevConnectionState = useRef<string>(connectionState);

  // On WebSocket reconnection, do a full sync to catch up on missed messages
  useEffect(() => {
    if (prevConnectionState.current === "disconnected" && connectionState === "connected" && sessionId) {
      // Re-fetch full stream data to ensure no gaps from disconnection
      api.getStreamOutput(sessionId).then((resp) => {
        setStreamLines(resp.lines);
        setPartialText("");
        streamCursorRef.current = resp.total;
      }).catch(() => {});
      api.getSession(sessionId).then(setSession).catch(() => {});
      api.getDiff(sessionId).then((r) => setDiffFiles(r.files)).catch(() => {});
    }
    prevConnectionState.current = connectionState;
  }, [connectionState, sessionId]);

  // Show toasts on WebSocket connection state changes
  useEffect(() => {
    if (connectionState === "disconnected") {
      setDisconnectToast(true);
    } else if (connectionState === "connected") {
      setDisconnectToast(false);
      setReconnectToast(true);
      const timer = setTimeout(() => setReconnectToast(false), 3000);
      return () => clearTimeout(timer);
    }
  }, [connectionState]);

  const MAX_IMAGE_SIZE = 5 * 1024 * 1024; // 5MB
  const ALLOWED_TYPES = ["image/jpeg", "image/png", "image/gif", "image/webp"];

  const addImageFiles = (files: FileList | File[]) => {
    Array.from(files).forEach((file) => {
      if (!ALLOWED_TYPES.includes(file.type)) {
        alert(`Unsupported format: ${file.type}. Supported: JPEG, PNG, GIF, WebP`);
        return;
      }
      if (file.size > MAX_IMAGE_SIZE) {
        alert(`File too large: ${(file.size / 1024 / 1024).toFixed(1)}MB. Max: 5MB`);
        return;
      }
      const reader = new FileReader();
      reader.onload = () => {
        const result = reader.result as string;
        const base64 = result.split(",")[1];
        setPendingImages((prev) => [
          ...prev,
          { data: base64, mediaType: file.type, preview: result },
        ]);
      };
      reader.readAsDataURL(file);
    });
  };

  const removeImage = (index: number) => {
    setPendingImages((prev) => prev.filter((_, i) => i !== index));
  };

  useEffect(() => {
    if (!sessionId) return;
    // Initialize cursor from loader data
    streamCursorRef.current = deferred.streamOutput.total || null;
    setActiveSessionName(initialSession.name);

    api.getPendingEscalation(sessionId).then((r) => {
      if (r.escalation) {
        setPendingEscalationId(r.escalation.id);
        setEscalationMessage(r.escalation.message ?? null);
        setEscalationTimedOut(r.escalation.timedOut ?? false);
        setEscalationTimeoutSeconds(r.escalation.timeoutSeconds ?? 300);
      }
    }).catch(() => {});
    api.getPendingPermission(sessionId).then((r) => {
      if (r.permission) {
        setPendingPermission({ id: r.permission.id, toolName: r.permission.toolName, input: r.permission.input, timedOut: r.permission.timedOut, timeoutSeconds: r.permission.timeoutSeconds ?? 300 });
      }
    }).catch(() => {});

    // Polling as fallback (stream updates are primarily via WebSocket)
    const interval = setInterval(() => {
      api.getSession(sessionId).then((s) => {
        setSession(s);
      });
      api.getDiff(sessionId).then((r) => setDiffFiles(r.files)).catch(() => {});
    }, 10000);
    return () => clearInterval(interval);
  }, [sessionId, initialSession.name, deferred.streamOutput.total]);

  const handleSend = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!sessionId || (!message.trim() && pendingImages.length === 0)) return;
    const images = pendingImages.length > 0
      ? pendingImages.map(({ data, mediaType }) => ({ data, mediaType }))
      : undefined;
    await api.sendToSession(sessionId, message, images);
    setMessage("");
    setPendingImages([]);
    // Stream mode updates arrive via WebSocket automatically
  };

  const sendForm = session ? (
    <form onSubmit={handleSend} className="shrink-0 sticky bottom-0 bg-white pt-2 pb-4 px-4 sm:px-8 -mx-4 sm:-mx-8">
      {pendingImages.length > 0 && (
        <div className="flex gap-2 mb-2 flex-wrap">
          {pendingImages.map((img, i) => (
            <div key={i} className="relative group">
              <img
                src={img.preview}
                alt={`preview ${i}`}
                className="w-16 h-16 object-cover rounded border border-gray-200"
              />
              <button
                type="button"
                onClick={() => removeImage(i)}
                className="absolute -top-1.5 -right-1.5 w-4 h-4 bg-red-500 text-white rounded-full flex items-center justify-center text-[10px]"
              >
                <X className="w-2.5 h-2.5" />
              </button>
            </div>
          ))}
        </div>
      )}
      <div className="flex gap-2 items-center">
        {/* Left action menu */}
        <div className="relative">
          <IconButton
            shape="circle"
            variant="secondary"
            onClick={() => setShowActionMenu((v) => !v)}
            title="Actions"
          >
            <Plus className="w-4 h-4" />
          </IconButton>
          {showActionMenu && (
            <ActionMenu className="absolute bottom-full left-0 mb-1 z-50">
              <ActionMenuItem
                icon={<ImagePlus className="w-4 h-4" />}
                label="Add image"
                onClick={() => {
                  fileInputRef.current?.click();
                  setShowActionMenu(false);
                }}
              />
              {slashCommands.length > 0 && (
                <ActionMenuItem
                  icon={<Slash className="w-4 h-4" />}
                  label="Slash commands"
                  onClick={() => {
                    setMessage("/");
                    setSlashFilter("");
                    setSlashSelectedIndex(0);
                    setShowSlashMenu(true);
                    setShowActionMenu(false);
                  }}
                />
              )}
              {session.type !== "controller" && (
                <ActionMenuItem
                  icon={<Copy className="w-4 h-4" />}
                  label="Duplicate"
                  onClick={async () => {
                    setShowActionMenu(false);
                    try {
                      const newSession = await api.duplicateSession(session.id);
                      navigate(`/sessions/${newSession.id}`);
                    } catch {
                      alert("Failed to duplicate session");
                    }
                  }}
                />
              )}
              <ActionMenuItem
                icon={<RotateCcw className="w-4 h-4" />}
                label="Clear context"
                onClick={async () => {
                  setShowActionMenu(false);
                  if (!confirm("Clear session context? This will start a fresh conversation.")) return;
                  try {
                    await api.clearSession(session.id);
                    setStreamLines([]);
                    setPartialText("");
                    streamCursorRef.current = 0;
                    api.getSession(session.id).then(setSession);
                    setClearToast("success");
                    setTimeout(() => setClearToast(null), 3000);
                  } catch {
                    setClearToast("error");
                    setTimeout(() => setClearToast(null), 3000);
                  }
                }}
              />
              <ActionMenuItem
                icon={<RefreshCw className="w-4 h-4" />}
                label="Reconnect"
                onClick={async () => {
                  setShowActionMenu(false);
                  if (!confirm("セッションを再接続しますか？")) return;
                  try {
                    await api.reconnectSession(session.id);
                    api.getSession(session.id).then(setSession);
                    setReconnectToast(true);
                    setTimeout(() => setReconnectToast(false), 3000);
                  } catch {
                    alert("再接続に失敗しました");
                  }
                }}
              />
              {session.status !== "stopped" && session.type !== "controller" && (
                <ActionMenuItem
                  icon={<Square className="w-4 h-4" />}
                  label="Stop session"
                  variant="danger"
                  onClick={async () => {
                    setShowActionMenu(false);
                    await api.stopSession(session.id);
                    api.getSession(session.id).then(setSession);
                  }}
                />
              )}
              {session.type !== "controller" && (
                <ActionMenuItem
                  icon={<Trash2 className="w-4 h-4" />}
                  label="Delete session"
                  variant="danger"
                  onClick={async () => {
                    setShowActionMenu(false);
                    if (!confirm("Delete this session?")) return;
                    await api.deleteSession(session.id);
                    navigate("/");
                  }}
                />
              )}
            </ActionMenu>
          )}
        </div>
        <input
          ref={fileInputRef}
          type="file"
          accept="image/jpeg,image/png,image/gif,image/webp"
          multiple
          className="hidden"
          onChange={(e) => {
            if (e.target.files) addImageFiles(e.target.files);
            e.target.value = "";
          }}
        />
        {/* Message input with slash command popup */}
        <div className="relative flex-1">
          {showSlashMenu && (() => {
            const filtered = slashCommands.filter((cmd) =>
              slashFilter === "" || cmd.toLowerCase().includes(slashFilter.toLowerCase())
            );
            if (filtered.length === 0) return null;
            return (
              <div className="absolute bottom-full left-0 right-0 mb-1 bg-white border border-gray-200 rounded-lg shadow-lg max-h-60 overflow-y-auto z-50">
                {filtered.map((cmd, i) => (
                  <button
                    key={cmd}
                    type="button"
                    className={`w-full text-left px-3 py-2 text-sm hover:bg-blue-50 ${
                      i === slashSelectedIndex ? "bg-blue-100 text-blue-800" : "text-gray-700"
                    }`}
                    onMouseDown={(e) => {
                      e.preventDefault();
                      setMessage(`/${cmd} `);
                      setShowSlashMenu(false);
                    }}
                  >
                    /{cmd}
                  </button>
                ))}
              </div>
            );
          })()}
          <textarea
            ref={(el) => {
              if (el) {
                el.style.height = "36px";
                el.style.height = Math.max(36, Math.min(el.scrollHeight, 120)) + "px";
              }
            }}
            value={message}
            rows={1}
            onChange={(e) => {
              const val = e.target.value;
              setMessage(val);
              if (val.startsWith("/") && slashCommands.length > 0) {
                const filter = val.slice(1).split(" ")[0];
                if (!val.includes(" ") || val === "/") {
                  setSlashFilter(filter);
                  setSlashSelectedIndex(0);
                  setShowSlashMenu(true);
                } else {
                  setShowSlashMenu(false);
                }
              } else {
                setShowSlashMenu(false);
              }
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter" && e.metaKey && !showSlashMenu) {
                e.preventDefault();
                e.currentTarget.form?.requestSubmit();
                return;
              }
              if (e.key === "Enter" && !showSlashMenu) {
                // Allow default newline behavior; do not submit
                return;
              }
              if (!showSlashMenu) return;
              const filtered = slashCommands.filter((cmd) =>
                slashFilter === "" || cmd.toLowerCase().includes(slashFilter.toLowerCase())
              );
              if (filtered.length === 0) return;
              if (e.key === "ArrowDown") {
                e.preventDefault();
                setSlashSelectedIndex((prev) => Math.min(prev + 1, filtered.length - 1));
              } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setSlashSelectedIndex((prev) => Math.max(prev - 1, 0));
              } else if (e.key === "Enter" || e.key === "Tab") {
                e.preventDefault();
                setMessage(`/${filtered[slashSelectedIndex]} `);
                setShowSlashMenu(false);
              } else if (e.key === "Escape") {
                setShowSlashMenu(false);
              }
            }}
            onBlur={() => { setShowSlashMenu(false); setShowActionMenu(false); }}
            onPaste={(e) => {
              const items = e.clipboardData?.items;
              if (!items) return;
              const imageFiles: File[] = [];
              for (const item of Array.from(items)) {
                if (item.type.startsWith("image/")) {
                  const file = item.getAsFile();
                  if (file) imageFiles.push(file);
                }
              }
              if (imageFiles.length > 0) {
                e.preventDefault();
                addImageFiles(imageFiles);
              }
            }}
            placeholder="Send a message..."
            className="block w-full border border-gray-300 rounded px-3 py-2 text-sm resize-none h-9 overflow-auto"
          />
        </div>
        {/* Send button */}
        <IconButton shape="circle" variant="primary" type="submit">
          <SendHorizonal className="w-4 h-4" />
        </IconButton>
      </div>
    </form>
  ) : null;

  return (
    <div className="h-dvh flex flex-col px-4 sm:px-8 pt-4 sm:pt-8 max-w-4xl mx-auto">
      {disconnectToast && <Toast message="WebSocket接続が切断されました。再接続を試みています..." variant="warning" />}
      {reconnectToast && <Toast message="再接続に成功しました" />}
      {clearToast === "success" && <Toast message="セッションをクリアしました" />}
      {clearToast === "error" && <Toast message="クリアに失敗しました" variant="error" />}
      <div className="flex flex-wrap items-center gap-2 sm:gap-3 mb-3 shrink-0">
        <IconButton onClick={() => navigate("/")} title="Back">
          <ArrowLeft className="w-4 h-4" />
        </IconButton>
        {session ? (
          <>
            <StatusDot status={session.status} />
            <h2 className="text-xl sm:text-2xl font-bold">{session.name}</h2>
            <span className="text-xs text-gray-400">{session.status}</span>
            {session.provider && (
              <Chip color={session.provider === "codex" ? "green" : session.provider === "claude" ? "blue" : "gray"}>
                {session.provider.charAt(0).toUpperCase() + session.provider.slice(1)}
                {providerVersion && ` ${providerVersion.match(/\d+\.\d+\.\d+/)?.[0] ?? ""}`}
              </Chip>
            )}
            {session.model && (
              <Chip color="purple">{session.model}</Chip>
            )}
          </>
        ) : (
          <>
            <div className="w-3 h-3 rounded-full bg-gray-200 animate-pulse" />
            <div className="h-7 w-40 bg-gray-200 rounded animate-pulse" />
          </>
        )}
      </div>
      {session ? (
        <div className="flex items-center gap-1.5 mb-2 shrink-0 text-xs sm:text-sm text-gray-500">
          <IconText icon={<FolderOpen className="w-3.5 h-3.5" />} className="text-xs sm:text-sm">
            <span className="truncate" title={session.projectPath}>{session.projectPath}</span>
          </IconText>
        {session.githubUrl && (
          <a
            href={session.githubUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="text-gray-400 hover:text-gray-700 shrink-0 ml-0.5"
            title="Open on GitHub"
          >
            <svg className="w-3.5 h-3.5" viewBox="0 0 16 16" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/></svg>
          </a>
        )}
        <button
          onClick={() => {
            setClaudeMDOpen(true);
            if (claudeMDFiles === null && !claudeMDLoading) {
              setClaudeMDLoading(true);
              api.getClaudeMD(session.id).then((res) => {
                setClaudeMDFiles(res.files);
              }).catch(() => {
                setClaudeMDFiles([{ path: "CLAUDE.md", content: "CLAUDE.md not found" }]);
              }).finally(() => {
                setClaudeMDLoading(false);
              });
            }
          }}
          className="text-gray-400 hover:text-purple-600 shrink-0 ml-0.5"
          title="Show CLAUDE.md"
        >
          <Sparkles className="w-3.5 h-3.5" />
        </button>
        <button
          onClick={() => {
            setSettingsJSONOpen(true);
            setSettingsJSONLoading(true);
            api.getSettingsJSON(session.id).then((res) => {
              setSettingsJSONFiles(res.files);
            }).catch(() => {
              setSettingsJSONFiles([]);
            }).finally(() => {
              setSettingsJSONLoading(false);
            });
          }}
          className="text-gray-400 hover:text-blue-600 shrink-0 ml-0.5"
          title="Show settings.json"
        >
          <Settings className="w-3.5 h-3.5" />
        </button>
        </div>
      ) : (
        <div className="flex items-center gap-1.5 mb-2 shrink-0 text-xs sm:text-sm">
          <FolderOpen className="w-3.5 h-3.5 text-gray-300 shrink-0" />
          <div className="h-4 w-60 bg-gray-200 rounded animate-pulse" />
        </div>
      )}
      {session && (session.branch || (session.pullRequests && session.pullRequests.length > 0) || diffFiles.length > 0) && (
        <div className="flex flex-wrap items-center gap-2 mb-2 shrink-0">
          {session.branch && (
            <>
              <GitBranch className="w-3.5 h-3.5 text-gray-400 shrink-0" />
              <span className="font-mono text-xs text-gray-700">{session.branch}</span>
            </>
          )}
          {session.pullRequests && session.pullRequests.map((pr) => (
            <PullRequestBadge key={pr.number} number={pr.number} state={pr.state} url={pr.url} />
          ))}
          <DiffDropdown files={diffFiles} />
        </div>
      )}

      {session && (session.currentTask || session.goal) && (
        <GoalPanel
          currentTask={session.currentTask}
          goal={session.goal}
          goals={session.goals}
        />
      )}

      {session && session.status === "stopped" && session.lastError && (
        <div className="mb-2 shrink-0">
          <AlertBanner variant="error">
            {session.lastError}
          </AlertBanner>
        </div>
      )}

      {session && connectionState !== "connected" && (
        <div className="mb-2 shrink-0">
          <ConnectionStatusIndicator state={connectionState} />
        </div>
      )}

      {!session ? (
        <div className="flex flex-col flex-1 min-h-0 items-center justify-center">
          <div className="space-y-3 w-full max-w-2xl px-4">
            <div className="h-4 bg-gray-200 rounded animate-pulse w-3/4" />
            <div className="h-4 bg-gray-200 rounded animate-pulse w-full" />
            <div className="h-4 bg-gray-200 rounded animate-pulse w-5/6" />
            <div className="h-4 bg-gray-200 rounded animate-pulse w-2/3" />
          </div>
        </div>
      ) : (
        <div className="flex flex-col flex-1 min-h-0">
          <StreamOutputView lines={streamLines} partialText={partialText} className="flex-1 min-h-0" sessionId={sessionId} pendingPermission={pendingPermission ?? undefined} onPermissionResponded={() => { setPendingPermission(null); }} provider={session?.provider ?? undefined} onAnswer={async (text) => {
            if (!sessionId) return;
            await api.sendToSession(sessionId, text);
          }} />
          {activeTasks.length > 0 && (
            <div className="shrink-0 pt-2 px-4 sm:px-8 -mx-4 sm:-mx-8">
              <ActiveTasksPanel tasks={activeTasks} />
            </div>
          )}
          {pendingPermission && sessionId ? (
            <PermissionPromptBanner
              permission={pendingPermission}
              sessionId={sessionId}
              onResponded={() => setPendingPermission(null)}
            />
          ) : pendingEscalationId && sessionId ? (
            <EscalationBanner
              escalationId={pendingEscalationId}
              message={escalationMessage}
              timedOut={escalationTimedOut}
              timeoutSeconds={escalationTimeoutSeconds}
              sessionId={sessionId}
              onResponded={() => { setPendingEscalationId(null); setEscalationMessage(null); setEscalationTimedOut(false); }}
            />
          ) : sendForm}
        </div>
      )}

      {/* CLAUDE.md Modal */}
      <Modal
        open={claudeMDOpen}
        onClose={() => setClaudeMDOpen(false)}
        title={
          <span className="flex items-center gap-2">
            <Sparkles className="w-4 h-4 text-purple-500" />
            CLAUDE.md
            <span className="flex items-center gap-0.5 ml-2">
              <button
                onClick={(e) => { e.stopPropagation(); setClaudeMDViewMode("preview"); }}
                className={`p-1 rounded ${claudeMDViewMode === "preview" ? "bg-purple-100 text-purple-700" : "text-gray-400 hover:text-gray-700"}`}
                title="Preview"
              >
                <Eye className="w-3.5 h-3.5" />
              </button>
              <button
                onClick={(e) => { e.stopPropagation(); setClaudeMDViewMode("source"); }}
                className={`p-1 rounded ${claudeMDViewMode === "source" ? "bg-purple-100 text-purple-700" : "text-gray-400 hover:text-gray-700"}`}
                title="Source"
              >
                <Code className="w-3.5 h-3.5" />
              </button>
            </span>
          </span>
        }
      >
        {claudeMDLoading ? (
          <div className="text-gray-400 text-sm">Loading...</div>
        ) : claudeMDFiles && claudeMDFiles.length > 0 ? (
          <FileCodeViewer
            files={claudeMDFiles.map((f) => ({ name: f.path, content: f.content }))}
            defaultExpanded
            lineClickable={claudeMDViewMode === "source"}
            renderContent={claudeMDViewMode === "preview" ? (content) => (
              <div className="prose prose-sm max-w-none">
                <Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown>
              </div>
            ) : undefined}
          />
        ) : (
          <div className="text-gray-400 text-sm">No content</div>
        )}
      </Modal>

      {/* settings.json Modal */}
      <Modal
        open={settingsJSONOpen}
        onClose={() => setSettingsJSONOpen(false)}
        title={
          <span className="flex items-center gap-2">
            <Settings className="w-4 h-4 text-blue-500" />
            settings.json
          </span>
        }
      >
        {settingsJSONLoading ? (
          <div className="text-gray-400 text-sm">Loading...</div>
        ) : (
          <FileCodeViewer
            files={settingsJSONFiles}
            formatContent={(content) => {
              try {
                return JSON.stringify(JSON.parse(content), null, 2);
              } catch {
                return content;
              }
            }}
            emptyMessage="No settings files found"
          />
        )}
      </Modal>
    </div>
  );
}

function formatTimeout(seconds: number): string {
  if (seconds >= 60) {
    const min = Math.floor(seconds / 60);
    return `${min}分`;
  }
  return `${seconds}秒`;
}

export function EscalationBanner({ escalationId, message, timedOut, timeoutSeconds, sessionId, onResponded }: {
  escalationId: string;
  message: string | null;
  timedOut: boolean;
  timeoutSeconds: number;
  sessionId: string;
  onResponded: () => void;
}) {
  const [response, setResponse] = useState("");
  const [sending, setSending] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const messageIsLong = message != null && message.split("\n").length > 6;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!response.trim() || sending) return;
    setSending(true);
    try {
      await api.respondEscalation(sessionId, escalationId, response.trim());
      setResponse("");
      onResponded();
    } catch {
      // ignore
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="shrink-0 sticky bottom-0 bg-white pt-2 pb-4 px-4 sm:px-8 -mx-4 sm:-mx-8 border-t border-gray-100">
      <div className="border border-red-300 rounded-lg bg-red-50 p-3 space-y-2">
        <div className="flex items-center gap-2">
          <AlertTriangle className="w-4 h-4 text-red-500 shrink-0" />
          <span className="font-medium text-sm text-red-800">Escalation</span>
          {timeoutSeconds && !timedOut && (
            <span className="text-xs text-gray-500">
              (タイムアウト: {formatTimeout(timeoutSeconds)})
            </span>
          )}
        </div>
        {message && (
          <div>
            <div className="relative">
              <div className={`text-sm text-gray-800 prose prose-sm max-w-none bg-white border border-gray-200 rounded px-2 py-1.5 ${!expanded && messageIsLong ? "max-h-24 overflow-hidden" : "max-h-80 overflow-y-auto"}`}>
                <Markdown remarkPlugins={[remarkGfm]}>{message}</Markdown>
              </div>
              {messageIsLong && !expanded && (
                <div className="absolute bottom-0 left-0 right-0 h-8 bg-gradient-to-t from-white to-transparent rounded-b pointer-events-none" />
              )}
            </div>
            {messageIsLong && (
              <button
                onClick={() => setExpanded(!expanded)}
                className="text-xs text-red-700 hover:text-red-900 hover:underline mt-1.5 relative z-10"
              >
                {expanded ? "折りたたむ" : "すべて表示"}
              </button>
            )}
          </div>
        )}
        {timedOut ? (
          <div className="text-xs text-amber-700">タイムアウト - エージェントは自動続行しました</div>
        ) : (
          <form onSubmit={handleSubmit} className="flex gap-2">
            <input
              type="text"
              value={response}
              onChange={(e) => setResponse(e.target.value)}
              placeholder="応答を入力..."
              className="flex-1 border border-gray-300 rounded-md px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-red-300 focus:border-red-300"
              disabled={sending}
              autoFocus
            />
            <button
              type="submit"
              disabled={sending || !response.trim()}
              className="px-4 py-1.5 text-sm bg-red-600 text-white rounded-md hover:bg-red-700 disabled:opacity-50 transition-colors"
            >
              {sending ? "..." : "応答"}
            </button>
          </form>
        )}
      </div>
    </div>
  );
}

export function PermissionPromptBanner({ permission, sessionId, onResponded }: {
  permission: { id: string; toolName: string; input: unknown; timedOut?: boolean; timeoutSeconds?: number };
  sessionId: string;
  onResponded: () => void;
}) {
  const [sending, setSending] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const inp = permission.input as Record<string, unknown> | undefined;
  const planText = typeof inp?.plan === "string" ? inp.plan : null;
  const planIsLong = planText != null && planText.split("\n").length > 6;

  const handleRespond = async (response: "allow" | "deny") => {
    if (sending) return;
    setSending(true);
    try {
      await api.respondPermission(sessionId, permission.id, response);
      onResponded();
    } catch {
      // ignore
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="shrink-0 sticky bottom-0 bg-white pt-2 pb-4 px-4 sm:px-8 -mx-4 sm:-mx-8">
      <div className="border border-amber-300 rounded-lg bg-amber-50 p-3 space-y-2">
        <div className="flex items-center gap-2">
          <AlertTriangle className="w-4 h-4 text-amber-500 shrink-0" />
          <span className="font-medium text-sm text-amber-800">Permission Request</span>
          <span className="text-xs text-gray-500 font-mono">{permission.toolName}</span>
        </div>
        {planText && (
          <div>
            <div className="relative">
              <pre className={`text-xs text-gray-600 bg-white border border-gray-200 rounded px-2 py-1.5 overflow-x-auto whitespace-pre-wrap ${!expanded && planIsLong ? "max-h-24 overflow-hidden" : "max-h-80 overflow-y-auto"}`}>
                {planText}
              </pre>
              {planIsLong && !expanded && (
                <div className="absolute bottom-0 left-0 right-0 h-8 bg-gradient-to-t from-white to-transparent rounded-b pointer-events-none" />
              )}
            </div>
            {planIsLong && (
              <button
                onClick={() => setExpanded(!expanded)}
                className="text-xs text-amber-700 hover:text-amber-900 hover:underline mt-1.5 relative z-10"
              >
                {expanded ? "折りたたむ" : "すべて表示"}
              </button>
            )}
          </div>
        )}
        {permission.timedOut ? (
          <div className="text-xs text-amber-700">タイムアウト - 自動承認しました</div>
        ) : (
          <div className="flex gap-2">
            <button
              onClick={() => handleRespond("allow")}
              disabled={sending}
              className="px-4 py-1.5 text-sm bg-green-600 text-white rounded-md hover:bg-green-700 disabled:opacity-50 transition-colors"
            >
              {sending ? "..." : "承認"}
            </button>
            <button
              onClick={() => handleRespond("deny")}
              disabled={sending}
              className="px-4 py-1.5 text-sm border border-gray-300 text-gray-700 rounded-md hover:bg-gray-100 disabled:opacity-50 transition-colors"
            >
              {sending ? "..." : "拒否"}
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
