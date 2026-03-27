import { useState, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { ArrowLeft } from "lucide-react";
import { StreamOutputView } from "../components/session/StreamOutputView";
import { PermissionPromptBanner } from "./SessionPage";
import { IconButton } from "../components/ui/IconButton";
import { SecondaryButton } from "../components/ui/SecondaryButton";
import { scenarioPresets } from "../fixtures/scenarios";
import type { ScenarioPreset } from "../fixtures/scenarios";
import type { StreamEntry } from "../models/stream";

function countEventTypes(lines: unknown[]): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const line of lines) {
    const entry = line as { type?: string };
    const t = entry.type || "unknown";
    counts[t] = (counts[t] || 0) + 1;
  }
  return counts;
}

export function ScenarioTestPage() {
  const [selectedPresetId, setSelectedPresetId] = useState<string | null>(null);
  const [customJsonl, setCustomJsonl] = useState("");
  const [customLines, setCustomLines] = useState<unknown[] | null>(null);
  const [parseError, setParseError] = useState<string | null>(null);
  const [showSidebar, setShowSidebar] = useState(true);
  const navigate = useNavigate();

  const activeLines = useMemo(() => {
    if (customLines) return customLines;
    if (selectedPresetId) {
      const preset = scenarioPresets.find((p) => p.id === selectedPresetId);
      return preset?.lines || [];
    }
    return [];
  }, [selectedPresetId, customLines]);

  const stats = useMemo(() => countEventTypes(activeLines), [activeLines]);

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

  const activePreset: ScenarioPreset | undefined = selectedPresetId
    ? scenarioPresets.find((p) => p.id === selectedPresetId)
    : undefined;

  const activeLabel = customLines
    ? "Custom JSONL"
    : activePreset?.label ?? null;

  const sidebar = (
    <div className="p-4 flex-1 min-h-0 overflow-y-auto">
      <h2 className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-3">
        Presets
      </h2>
      <div className="space-y-1">
        {scenarioPresets.map((preset) => (
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
          className="md:hidden"
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

        {/* Right pane: StreamOutputView + simulated banners */}
        <div className="flex-1 flex flex-col min-h-0">
          <div className="flex-1 min-h-0 p-4">
            <StreamOutputView
              lines={activeLines as StreamEntry[]}
              className="h-full"
            />
          </div>

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
      </div>
    </div>
  );
}
