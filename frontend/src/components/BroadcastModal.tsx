import { useState } from "react";
import { Modal } from "./ui/Modal";
import { api } from "../api/client";
import type { Session } from "../types/session";

const ACTIVE_STATUSES = new Set(["working", "idle", "waiting_input"]);

interface Props {
  open: boolean;
  onClose: () => void;
  sessions: Session[];
}

export function BroadcastModal({ open, onClose, sessions }: Props) {
  const [text, setText] = useState("");
  const [mode, setMode] = useState<"active" | "select">("active");
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [sending, setSending] = useState(false);
  const [result, setResult] = useState<{
    results: { sessionId: string; status: string; error?: string }[];
  } | null>(null);

  const broadcastTargets = sessions.filter(
    (s) => s.type !== "external" && ACTIVE_STATUSES.has(s.status)
  );

  const toggleSession = (id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  };

  const handleSend = async () => {
    if (!text.trim()) return;
    setSending(true);
    setResult(null);
    try {
      const opts =
        mode === "select"
          ? { sessionIds: Array.from(selectedIds) }
          : { filter: "active" as const };
      const res = await api.broadcastToSessions(text.trim(), opts);
      setResult(res);
    } catch {
      setResult({ results: [{ sessionId: "", status: "error", error: "Failed to send" }] });
    } finally {
      setSending(false);
    }
  };

  const handleClose = () => {
    setText("");
    setMode("active");
    setSelectedIds(new Set());
    setResult(null);
    onClose();
  };

  const canSend =
    text.trim() !== "" &&
    !sending &&
    (mode === "active" ? broadcastTargets.length > 0 : selectedIds.size > 0);

  return (
    <Modal open={open} onClose={handleClose} title="Broadcast Message">
      <div className="flex flex-col gap-4">
        {/* Target mode selector */}
        <div>
          <label className="text-xs font-medium text-gray-600 mb-1 block">Target</label>
          <div className="flex gap-2">
            <button
              onClick={() => setMode("active")}
              className={`px-3 py-1.5 text-xs rounded-md border ${
                mode === "active"
                  ? "bg-blue-50 border-blue-300 text-blue-700"
                  : "bg-white border-gray-200 text-gray-600 hover:bg-gray-50"
              }`}
            >
              All Active ({broadcastTargets.length})
            </button>
            <button
              onClick={() => setMode("select")}
              className={`px-3 py-1.5 text-xs rounded-md border ${
                mode === "select"
                  ? "bg-blue-50 border-blue-300 text-blue-700"
                  : "bg-white border-gray-200 text-gray-600 hover:bg-gray-50"
              }`}
            >
              Select Sessions
            </button>
          </div>
        </div>

        {/* Session selection list */}
        {mode === "select" && (
          <div className="max-h-40 overflow-y-auto border border-gray-200 rounded-md divide-y divide-gray-100">
            {sessions
              .filter((s) => s.type !== "external")
              .map((s) => (
                <label
                  key={s.id}
                  className="flex items-center gap-2 px-3 py-2 hover:bg-gray-50 cursor-pointer"
                >
                  <input
                    type="checkbox"
                    checked={selectedIds.has(s.id)}
                    onChange={() => toggleSession(s.id)}
                    className="rounded border-gray-300"
                  />
                  <span className="text-sm text-gray-800 truncate">{s.name}</span>
                  <span
                    className={`ml-auto text-xs px-1.5 py-0.5 rounded ${
                      ACTIVE_STATUSES.has(s.status)
                        ? "bg-green-50 text-green-700"
                        : "bg-gray-100 text-gray-500"
                    }`}
                  >
                    {s.status}
                  </span>
                </label>
              ))}
          </div>
        )}

        {/* Message input */}
        <div>
          <label className="text-xs font-medium text-gray-600 mb-1 block">Message</label>
          <textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder="Enter message to broadcast..."
            rows={3}
            className="w-full px-3 py-2 border border-gray-200 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-300 resize-none"
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && canSend) {
                handleSend();
              }
            }}
          />
        </div>

        {/* Result display */}
        {result && (
          <div className="text-xs space-y-1">
            {result.results.map((r, i) => (
              <div
                key={i}
                className={`px-2 py-1 rounded ${
                  r.status === "sent"
                    ? "bg-green-50 text-green-700"
                    : "bg-red-50 text-red-700"
                }`}
              >
                {r.sessionId ? `${r.sessionId.slice(0, 8)}: ${r.status}` : r.error}
                {r.error && r.sessionId ? ` - ${r.error}` : ""}
              </div>
            ))}
          </div>
        )}

        {/* Actions */}
        <div className="flex justify-end gap-2">
          <button
            onClick={handleClose}
            className="px-3 py-1.5 text-xs text-gray-600 border border-gray-200 rounded-md hover:bg-gray-50"
          >
            Cancel
          </button>
          <button
            onClick={handleSend}
            disabled={!canSend}
            className="px-3 py-1.5 text-xs text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {sending ? "Sending..." : "Broadcast"}
          </button>
        </div>
      </div>
    </Modal>
  );
}
