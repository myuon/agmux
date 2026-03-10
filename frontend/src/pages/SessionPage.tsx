import { useCallback, useEffect, useRef, useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  Square, RefreshCw, Trash2, ArrowLeft, GitBranch, GitPullRequest, FolderOpen,
  Sparkles,
  ListTodo, Target, RotateCcw, ImagePlus, SendHorizonal, Plus, Slash,
  Code, Eye, X,
} from "lucide-react";
import { Modal } from "../components/ui/Modal";
import { Toast } from "../components/ui/Toast";
import { useAutoScroll } from "../hooks/useAutoScroll";
import type { Session } from "../types/session";
import { api, type DiffFile } from "../api/client";
import { StatusDot } from "../components/StatusBadge";
import { setActiveSessionName } from "../activeSession";
import { useWebSocket } from "../hooks/useWebSocket";
import { StreamOutputView } from "../components/session/StreamOutputView";
import { DiffDropdown } from "../components/session/DiffDropdown";

export function SessionPage() {
  const { id: sessionId } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [session, setSession] = useState<Session | null>(null);
  const [output, setOutput] = useState("");
  const [message, setMessage] = useState("");
  const [streamLines, setStreamLines] = useState<unknown[]>([]);
  const [diffFiles, setDiffFiles] = useState<DiffFile[]>([]);
  const [pendingImages, setPendingImages] = useState<{ data: string; mediaType: string; preview: string }[]>([]);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [pendingEscalationId, setPendingEscalationId] = useState<string | null>(null);
  const [escalationTimedOut, setEscalationTimedOut] = useState(false);
  const [escalationTimeoutSeconds, setEscalationTimeoutSeconds] = useState(300);
  const [reconnectToast, setReconnectToast] = useState(false);
  const [clearToast, setClearToast] = useState<"success" | "error" | null>(null);
  const [slashCommands, setSlashCommands] = useState<string[]>([]);
  const [showSlashMenu, setShowSlashMenu] = useState(false);
  const [showActionMenu, setShowActionMenu] = useState(false);
  const [slashFilter, setSlashFilter] = useState("");
  const [slashSelectedIndex, setSlashSelectedIndex] = useState(0);
  const [claudeMDOpen, setClaudeMDOpen] = useState(false);
  const [claudeMDFiles, setClaudeMDFiles] = useState<{ path: string; content: string }[] | null>(null);
  const [claudeMDLoading, setClaudeMDLoading] = useState(false);
  const [claudeMDViewMode, setClaudeMDViewMode] = useState<"preview" | "source">("preview");
  const [claudeMDSelectedLine, setClaudeMDSelectedLine] = useState<number | null>(null);
  const terminal = useAutoScroll(output);
  const streamCursorRef = useRef<number | null>(null);

  // Extract slash_commands from system init messages
  useEffect(() => {
    for (const line of streamLines) {
      const entry = line as Record<string, unknown>;
      if (entry.type === "system" && entry.subtype === "init") {
        const cmds = entry.slash_commands as string[] | undefined;
        if (cmds && cmds.length > 0) {
          setSlashCommands(cmds);
        }
        break;
      }
    }
  }, [streamLines]);

  // Track active session name for notification suppression
  useEffect(() => {
    return () => {
      setActiveSessionName(null);
    };
  }, []);

  // Listen for escalation WebSocket messages
  const handleWsMessage = useCallback((msg: { type: string; data: unknown }) => {
    if (msg.type === "escalation") {
      const data = msg.data as { id: string; sessionId: string; timeoutSeconds?: number };
      if (data.sessionId === sessionId) {
        setPendingEscalationId(data.id);
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
  }, [sessionId]);
  useWebSocket(handleWsMessage);

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
    // Reset cursor on session change
    streamCursorRef.current = null;

    api.getSession(sessionId).then((s) => {
      setSession(s);
      setActiveSessionName(s.name);
      if (s.outputMode === "stream") {
        api.getStreamOutput(sessionId).then((resp) => {
          setStreamLines(resp.lines);
          streamCursorRef.current = resp.total;
        }).catch(() => {});
      } else {
        api.getSessionOutput(sessionId).then((r) => setOutput(r.output));
      }
    });
    api.getDiff(sessionId).then((r) => setDiffFiles(r.files)).catch(() => {});
    api.getPendingEscalation(sessionId).then((r) => {
      if (r.escalation) {
        setPendingEscalationId(r.escalation.id);
        setEscalationTimedOut(r.escalation.timedOut ?? false);
        setEscalationTimeoutSeconds(r.escalation.timeoutSeconds ?? 300);
      }
    }).catch(() => {});

    const interval = setInterval(() => {
      api.getSession(sessionId).then((s) => {
        setSession(s);
        if (s.outputMode === "stream") {
          const cursor = streamCursorRef.current;
          if (cursor !== null) {
            // Delta fetch: only get new lines
            api.getStreamOutputDelta(sessionId, cursor).then((resp) => {
              if (resp.lines.length > 0) {
                setStreamLines((prev) => [...prev, ...resp.lines]);
              }
              streamCursorRef.current = resp.total;
            }).catch(() => {});
          } else {
            // Fallback to full fetch if cursor not initialized
            api.getStreamOutput(sessionId).then((resp) => {
              setStreamLines(resp.lines);
              streamCursorRef.current = resp.total;
            }).catch(() => {});
          }
        } else {
          api.getSessionOutput(sessionId).then((r) => setOutput(r.output));
        }
      });
      api.getDiff(sessionId).then((r) => setDiffFiles(r.files)).catch(() => {});
    }, 3000);
    return () => clearInterval(interval);
  }, [sessionId]);

  const handleSend = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!sessionId || (!message.trim() && pendingImages.length === 0)) return;
    const images = pendingImages.length > 0
      ? pendingImages.map(({ data, mediaType }) => ({ data, mediaType }))
      : undefined;
    await api.sendToSession(sessionId, message, images);
    setMessage("");
    setPendingImages([]);
    setTimeout(() => {
      if (session?.outputMode === "stream") {
        const cursor = streamCursorRef.current;
        if (cursor !== null) {
          api.getStreamOutputDelta(sessionId, cursor).then((resp) => {
            if (resp.lines.length > 0) {
              setStreamLines((prev) => [...prev, ...resp.lines]);
            }
            streamCursorRef.current = resp.total;
          }).catch(() => {});
        } else {
          api.getStreamOutput(sessionId).then((resp) => {
            setStreamLines(resp.lines);
            streamCursorRef.current = resp.total;
          }).catch(() => {});
        }
      } else {
        api.getSessionOutput(sessionId).then((r) => setOutput(r.output));
      }
    }, 500);
  };

  const isStream = session?.outputMode === "stream";

  const sendForm = session ? (
    <form onSubmit={handleSend} className="shrink-0 sticky bottom-0 bg-white pt-2 pb-4 px-4 sm:px-8 -mx-4 sm:-mx-8 border-t border-gray-100">
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
      <div className="flex gap-2 items-end">
        {/* Left action menu */}
        <div className="relative">
          <button
            type="button"
            onClick={() => setShowActionMenu((v) => !v)}
            className="w-9 h-9 flex items-center justify-center text-gray-500 bg-gray-50 rounded-full hover:bg-gray-100"
            title="Actions"
          >
            <Plus className="w-4 h-4" />
          </button>
          {showActionMenu && (
            <div className="absolute bottom-full left-0 mb-1 bg-white border border-gray-200 rounded-lg shadow-lg z-50 min-w-[160px]">
              <button
                type="button"
                className="w-full text-left px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 flex items-center gap-2"
                onMouseDown={(e) => {
                  e.preventDefault();
                  fileInputRef.current?.click();
                  setShowActionMenu(false);
                }}
              >
                <ImagePlus className="w-4 h-4" /> Add image
              </button>
              {slashCommands.length > 0 && (
                <button
                  type="button"
                  className="w-full text-left px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 flex items-center gap-2"
                  onMouseDown={(e) => {
                    e.preventDefault();
                    setMessage("/");
                    setSlashFilter("");
                    setSlashSelectedIndex(0);
                    setShowSlashMenu(true);
                    setShowActionMenu(false);
                  }}
                >
                  <Slash className="w-4 h-4" /> Slash commands
                </button>
              )}
              {session.status !== "stopped" && session.type !== "controller" && (
                <button
                  type="button"
                  className="w-full text-left px-3 py-2 text-sm text-red-600 hover:bg-red-50 flex items-center gap-2"
                  onMouseDown={async (e) => {
                    e.preventDefault();
                    setShowActionMenu(false);
                    await api.stopSession(session.id);
                    api.getSession(session.id).then(setSession);
                  }}
                >
                  <Square className="w-4 h-4" /> Stop session
                </button>
              )}
            </div>
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
            value={message}
            rows={3}
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
            className="w-full border border-gray-300 rounded px-3 py-2 text-sm resize-none"
          />
        </div>
        {/* Send button */}
        <button
          type="submit"
          className="w-9 h-9 flex items-center justify-center bg-blue-600 text-white rounded-full hover:bg-blue-700"
        >
          <SendHorizonal className="w-4 h-4" />
        </button>
      </div>
    </form>
  ) : null;

  return (
    <div className="h-dvh flex flex-col px-4 sm:px-8 pt-4 sm:pt-8 max-w-4xl mx-auto">
      {reconnectToast && <Toast message="再接続に成功しました" />}
      {clearToast === "success" && <Toast message="セッションをクリアしました" />}
      {clearToast === "error" && <Toast message="クリアに失敗しました" variant="error" />}
      <div className="flex flex-wrap items-center gap-2 sm:gap-3 mb-3 shrink-0">
        <button
          onClick={() => navigate("/")}
          className="p-1 text-gray-400 hover:text-gray-700 rounded hover:bg-gray-100 shrink-0"
          title="Back"
        >
          <ArrowLeft className="w-4 h-4" />
        </button>
        {session ? (
          <>
            <StatusDot status={session.status} />
            <h2 className="text-xl sm:text-2xl font-bold">{session.name}</h2>
            <span className="text-xs text-gray-400">{session.status}</span>
          </>
        ) : (
          <>
            <div className="w-3 h-3 rounded-full bg-gray-200 animate-pulse" />
            <div className="h-7 w-40 bg-gray-200 rounded animate-pulse" />
          </>
        )}
        {session && session.type !== "controller" && (
          <div className="flex gap-1.5 sm:ml-auto">
            <button
              onClick={async () => {
                if (!confirm("Clear session context? This will start a fresh conversation.")) return;
                try {
                  await api.clearSession(session.id);
                  // Optimistic UI update: immediately clear local state
                  setStreamLines([]);
                  setOutput("");
                  streamCursorRef.current = 0;
                  // Refresh session metadata
                  api.getSession(session.id).then(setSession);
                  setClearToast("success");
                  setTimeout(() => setClearToast(null), 3000);
                } catch {
                  setClearToast("error");
                  setTimeout(() => setClearToast(null), 3000);
                }
              }}
              className="p-1.5 text-orange-700 bg-orange-50 rounded hover:bg-orange-100"
              title="Clear"
            >
              <RotateCcw className="w-3.5 h-3.5" />
            </button>
            <button
              onClick={async () => {
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
              className="p-1.5 text-indigo-700 bg-indigo-50 rounded hover:bg-indigo-100"
              title="Reconnect"
            >
              <RefreshCw className="w-3.5 h-3.5" />
            </button>
            <button
              onClick={async () => {
                if (!confirm("Delete this session?")) return;
                await api.deleteSession(session.id);
                navigate("/");
              }}
              className="p-1.5 text-red-600 bg-red-50 rounded hover:bg-red-100"
              title="Delete"
            >
              <Trash2 className="w-3.5 h-3.5" />
            </button>
          </div>
        )}
      </div>
      {session ? (
        <div className="flex items-center gap-1.5 mb-2 shrink-0 text-xs sm:text-sm text-gray-500">
          <FolderOpen className="w-3.5 h-3.5 text-gray-400 shrink-0" />
          <span className="truncate" title={session.projectPath}>{session.projectPath}</span>
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
            <a
              key={pr.number}
              href={pr.url}
              target="_blank"
              rel="noopener noreferrer"
              className={`inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-full ${
                pr.state === "MERGED"
                  ? "bg-purple-100 text-purple-700"
                  : pr.state === "OPEN"
                    ? "bg-green-100 text-green-700"
                    : "bg-gray-100 text-gray-600"
              }`}
            >
              <GitPullRequest className="w-3 h-3" />
              #{pr.number}
            </a>
          ))}
          <DiffDropdown files={diffFiles} />
        </div>
      )}

      {session && (session.currentTask || session.goal) && (
        <div className="border-l-2 border-gray-300 bg-gray-50 rounded-r-lg px-3 py-2 mb-3 space-y-1">
          {session.goals && session.goals.length > 1 && (
            <div className="text-[10px] text-gray-400 ml-5">
              {session.goals.slice(0, -1).map((g, i) => (
                <span key={i}>
                  {i > 0 && " > "}
                  {g.goal}
                </span>
              ))}
              {" > "}
            </div>
          )}
          {session.currentTask && (
            <div className="flex items-start gap-1.5 text-xs text-gray-700">
              <ListTodo className="w-3.5 h-3.5 shrink-0 mt-0.5 text-indigo-400" />
              <span>{session.currentTask}</span>
            </div>
          )}
          {session.goal && (
            <div className="flex items-start gap-1.5 text-xs text-gray-500">
              <Target className="w-3.5 h-3.5 shrink-0 mt-0.5 text-emerald-400" />
              <span>{session.goal}</span>
            </div>
          )}
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
      ) : isStream ? (
        <div className="flex flex-col flex-1 min-h-0">
          <StreamOutputView lines={streamLines} className="flex-1 min-h-0" sessionId={sessionId} escalationId={pendingEscalationId ?? undefined} escalationTimedOut={escalationTimedOut} escalationTimeoutSeconds={escalationTimeoutSeconds} onEscalationResponded={() => { setPendingEscalationId(null); setEscalationTimedOut(false); }} onAnswer={async (text) => {
            if (!sessionId) return;
            await api.sendToSession(sessionId, text);
          }} />
          {sendForm}
        </div>
      ) : (
        <div className="flex flex-col flex-1 min-h-0">
          <div ref={terminal.ref} onScroll={terminal.onScroll} className="bg-gray-900 text-green-400 rounded-lg p-4 font-mono text-xs flex-1 min-h-0 overflow-y-auto whitespace-pre-wrap">
            {output || "No output yet."}
          </div>
          {sendForm}
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
          <div className="space-y-6">
            {claudeMDFiles.map((file, fileIdx) => (
              <div key={fileIdx}>
                {claudeMDFiles.length > 1 && (
                  <div className="text-xs font-mono text-gray-500 bg-gray-100 px-2 py-1 rounded-t border border-b-0 border-gray-200">
                    {file.path}
                  </div>
                )}
                {claudeMDViewMode === "preview" ? (
                  <div className={`prose prose-sm max-w-none ${claudeMDFiles.length > 1 ? "border border-gray-200 rounded-b p-3" : ""}`}>
                    <Markdown remarkPlugins={[remarkGfm]}>{file.content}</Markdown>
                  </div>
                ) : (
                  <pre className={`text-xs leading-relaxed font-mono whitespace-pre-wrap ${claudeMDFiles.length > 1 ? "border border-gray-200 rounded-b" : ""}`}>
                    {file.content.split("\n").map((line, i) => {
                      const lineNum = i + 1;
                      const isSelected = claudeMDSelectedLine === lineNum;
                      return (
                        <div
                          key={i}
                          className={`flex cursor-pointer ${isSelected ? "bg-yellow-100" : "hover:bg-gray-50"}`}
                          onClick={() => {
                            setClaudeMDSelectedLine(isSelected ? null : lineNum);
                            navigator.clipboard.writeText(`${file.path}:L${lineNum}`);
                          }}
                        >
                          <span
                            className={`select-none w-10 text-right pr-3 shrink-0 ${isSelected ? "text-yellow-600" : "text-gray-300"}`}
                          >
                            {lineNum}
                          </span>
                          <span className="flex-1">{line}</span>
                        </div>
                      );
                    })}
                  </pre>
                )}
              </div>
            ))}
          </div>
        ) : (
          <div className="text-gray-400 text-sm">No content</div>
        )}
      </Modal>
    </div>
  );
}
