import { useState, useMemo, useRef } from "react";
import { useNavigate } from "react-router-dom";
import { ArrowLeft, Send } from "lucide-react";
import { StreamOutputView, ActiveTasksPanel } from "../components/session/StreamOutputView";
import { PermissionPromptBanner, EscalationBanner } from "./SessionPage";
import { IconButton } from "../components/ui/IconButton";
import { SecondaryButton } from "../components/ui/SecondaryButton";
import { scenarioPresets } from "../fixtures/scenarios";
import type { ScenarioPreset } from "../fixtures/scenarios";
import type { StreamEntry } from "../models/stream";
import { extractActiveTasks } from "../models/stream";
import { SessionList } from "../components/SessionList";

function countEventTypes(lines: unknown[]): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const line of lines) {
    const entry = line as { type?: string };
    const t = entry.type || "unknown";
    counts[t] = (counts[t] || 0) + 1;
  }
  return counts;
}

const streamPresets = scenarioPresets.filter((p) => p.type === "stream");
const sessionListPresets = scenarioPresets.filter((p) => p.type === "session-list");

export function ScenarioTestPage() {
  const [selectedPresetId, setSelectedPresetId] = useState<string | null>(null);
  const [customJsonl, setCustomJsonl] = useState("");
  const [customLines, setCustomLines] = useState<unknown[] | null>(null);
  const [parseError, setParseError] = useState<string | null>(null);
  const [showSidebar, setShowSidebar] = useState(true);
  const navigate = useNavigate();

  const activePreset: ScenarioPreset | undefined = selectedPresetId
    ? scenarioPresets.find((p) => p.id === selectedPresetId)
    : undefined;

  const activeLines = useMemo(() => {
    if (customLines) return customLines;
    if (activePreset?.lines) return activePreset.lines;
    return [];
  }, [activePreset, customLines]);

  const stats = useMemo(() => countEventTypes(activeLines), [activeLines]);

  const activeTasks = useMemo(() => {
    const entries = activeLines
      .map((line) => line as StreamEntry)
      .filter((e) => e.type === "user" || e.type === "assistant" || e.type === "system");
    return extractActiveTasks(entries);
  }, [activeLines]);

  const handleLoadCustom = () => {
    setParseError(null);
    try {
      const lines = customJsonl
        .trim()
        .split("\n")
        .filter((line) => line.trim())
        .map((line) => JSON.parse(line));
      setCustomLines(lines);
      setSelectedPresetId(null);
      setShowSidebar(false);
    } catch (e) {
      setParseError(e instanceof Error ? e.message : "Invalid JSONL");
    }
  };

  const handleSelectPreset = (id: string) => {
    setSelectedPresetId(id);
    setCustomLines(null);
    setParseError(null);
    setShowSidebar(false);
  };

  // Send error simulation state
  const [sendMessage, setSendMessage] = useState("");
  const [sendError, setSendError] = useState<string | null>(null);
  const [isSending, setIsSending] = useState(false);
  const sendInputRef = useRef<HTMLTextAreaElement>(null);

  const handleSimulatedSend = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!sendMessage.trim()) return;
    setIsSending(true);
    setSendError(null);
    try {
      // Simulate API call that always fails with 500 error
      await new Promise((_, reject) =>
        setTimeout(() => reject(new Error("500 Internal Server Error: simulated send failure")), 500)
      );
      // This line should never execute
      setSendMessage("");
    } catch (err) {
      console.error("Failed to send message:", err);
      setSendError(err instanceof Error ? err.message : "Unknown error");
    } finally {
      setIsSending(false);
    }
  };

  const activeLabel = customLines
    ? "Custom JSONL"
    : activePreset?.label ?? null;

  const isSessionList = activePreset?.type === "session-list";
  const isSendError = activePreset?.simulateSendError === true && !customLines;

  const sidebar = (
    <div className="p-4 flex-1 min-h-0 overflow-y-auto">
      <h2 className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-3">
        Stream Output
      </h2>
      <div className="space-y-1">
        {streamPresets.map((preset) => (
          <button
            key={preset.id}
            onClick={() => handleSelectPreset(preset.id)}
            className={`w-full text-left px-3 py-2 rounded-lg text-sm transition-colors ${
              selectedPresetId === preset.id && !customLines
                ? "bg-blue-50 text-blue-700 border border-blue-200"
                : "hover:bg-gray-100 text-gray-700"
            }`}
          >
            <div className="font-medium">{preset.label}</div>
            <div className="text-xs text-gray-500 mt-0.5">
              {preset.description}
            </div>
          </button>
        ))}
      </div>

      <h2 className="text-xs font-semibold text-gray-500 uppercase tracking-wide mt-6 mb-3">
        Session List
      </h2>
      <div className="space-y-1">
        {sessionListPresets.map((preset) => (
          <button
            key={preset.id}
            onClick={() => handleSelectPreset(preset.id)}
            className={`w-full text-left px-3 py-2 rounded-lg text-sm transition-colors ${
              selectedPresetId === preset.id && !customLines
                ? "bg-blue-50 text-blue-700 border border-blue-200"
                : "hover:bg-gray-100 text-gray-700"
            }`}
          >
            <div className="font-medium">{preset.label}</div>
            <div className="text-xs text-gray-500 mt-0.5">
              {preset.description}
            </div>
          </button>
        ))}
      </div>

      <h2 className="text-xs font-semibold text-gray-500 uppercase tracking-wide mt-6 mb-3">
        Custom
      </h2>
      <textarea
        value={customJsonl}
        onChange={(e) => setCustomJsonl(e.target.value)}
        placeholder={'Paste JSONL here...\n{"type":"user","message":{"role":"user","content":"Hello"}}'}
        className="w-full h-32 border border-gray-300 rounded-lg px-3 py-2 text-xs font-mono resize-y focus:outline-none focus:ring-2 focus:ring-blue-400 focus:border-transparent"
      />
      {parseError && (
        <div className="text-xs text-red-600 mt-1">{parseError}</div>
      )}
      <button
        onClick={handleLoadCustom}
        disabled={!customJsonl.trim()}
        className="mt-2 w-full px-3 py-1.5 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
      >
        Load
      </button>
    </div>
  );

  return (
    <div className="h-screen flex flex-col bg-gray-50">
      {/* Header */}
      <header className="bg-white border-b border-gray-200 px-4 md:px-8 py-3 flex items-center gap-3 shrink-0">
        <IconButton onClick={() => navigate("/")} title="Back">
          <ArrowLeft className="w-4 h-4" />
        </IconButton>
        <h1 className="text-lg md:text-xl font-bold text-gray-900">
          Scenario Test
        </h1>

        {/* Mobile: toggle sidebar / show active scenario */}
        <SecondaryButton
          onClick={() => setShowSidebar(!showSidebar)}
          color="purple"
          className="md:hidden ml-auto"
        >
          {showSidebar ? "Hide scenarios" : activeLabel || "Select scenario"}
        </SecondaryButton>
      </header>

      {/* Main content */}
      <div className="flex-1 min-h-0 flex flex-col md:flex-row">
        {/* Sidebar: always visible on md+, toggleable on mobile */}
        <div
          className={`${
            showSidebar ? "flex" : "hidden"
          } md:flex w-full md:w-72 border-b md:border-b-0 md:border-r border-gray-200 bg-white flex-col shrink-0 ${
            showSidebar ? "max-h-[50vh] md:max-h-none" : ""
          } min-h-0`}
        >
          {sidebar}
        </div>

        {/* Right pane */}
        {isSessionList ? (
          <div className="flex-1 min-h-0 overflow-y-auto p-4 md:p-8">
            <div className="max-w-3xl mx-auto">
              <SessionList
                sessions={activePreset?.mockSessions ?? []}
                onRestartController={() => {}}
              />
            </div>
          </div>
        ) : (
          <div className="flex-1 flex flex-col min-h-0">
            <div className="flex-1 min-h-0 p-4">
              <StreamOutputView
                lines={activeLines as StreamEntry[]}
                className="h-full"
              />
            </div>

            {/* Active tasks panel */}
            {activeTasks.length > 0 && (
              <div className="px-4 sm:px-8 pb-2">
                <ActiveTasksPanel tasks={activeTasks} />
              </div>
            )}

            {/* Simulated escalation banner */}
            {activePreset?.simulatedEscalation && !customLines && (
              <div className="px-4 sm:px-8">
                <EscalationBanner
                  escalationId={activePreset.simulatedEscalation.id}
                  message={activePreset.simulatedEscalation.message}
                  timedOut={activePreset.simulatedEscalation.timedOut ?? false}
                  timeoutSeconds={activePreset.simulatedEscalation.timeoutSeconds ?? 300}
                  sessionId="scenario-test"
                  onResponded={() => {}}
                />
              </div>
            )}

            {/* Simulated permission banner */}
            {activePreset?.simulatedPermission && !customLines && (
              <div className="px-4 sm:px-8">
                <PermissionPromptBanner
                  permission={activePreset.simulatedPermission}
                  sessionId="scenario-test"
                  onResponded={() => {}}
                />
              </div>
            )}

            {/* Simulated send error form */}
            {isSendError && (
              <div className="px-4 sm:px-8 pb-2">
                {sendError && (
                  <div className="mb-2 px-3 py-2 bg-red-50 border border-red-200 rounded-lg text-sm text-red-700">
                    送信エラー: {sendError}
                    <span className="block text-xs text-red-500 mt-1">
                      メッセージ入力欄にテキストが保持されていることを確認してください
                    </span>
                  </div>
                )}
                <form onSubmit={handleSimulatedSend} className="flex gap-2 items-end">
                  <textarea
                    ref={sendInputRef}
                    value={sendMessage}
                    onChange={(e) => setSendMessage(e.target.value)}
                    placeholder="メッセージを入力して送信ボタンを押してください（必ずエラーになります）"
                    className="flex-1 border border-gray-300 rounded-lg px-3 py-2 text-sm resize-none focus:outline-none focus:ring-2 focus:ring-blue-400 focus:border-transparent"
                    rows={2}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" && !e.shiftKey) {
                        e.preventDefault();
                        handleSimulatedSend(e);
                      }
                    }}
                  />
                  <button
                    type="submit"
                    disabled={!sendMessage.trim() || isSending}
                    className="px-3 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors flex items-center gap-1.5"
                  >
                    <Send className="w-4 h-4" />
                    <span className="text-sm">{isSending ? "送信中..." : "送信"}</span>
                  </button>
                </form>
              </div>
            )}

            {/* Footer stats */}
            {activeLines.length > 0 && (
              <div className="border-t border-gray-200 bg-white px-4 py-2 shrink-0">
                <div className="flex flex-wrap items-center gap-2 md:gap-4 text-xs text-gray-500">
                  <span>
                    <span className="font-medium text-gray-700">
                      {activeLines.length}
                    </span>{" "}
                    lines
                  </span>
                  <span className="text-gray-300">|</span>
                  {Object.entries(stats).map(([type, count]) => (
                    <span key={type}>
                      <span className="font-medium text-gray-600">{type}</span>
                      {": "}
                      {count}
                    </span>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
