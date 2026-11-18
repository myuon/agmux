import { useState } from "react";
import { useNavigate, useLoaderData, Link } from "react-router-dom";
import { api } from "../api/client";
import type { Automation, AutomationInput } from "../api/client";
import { ToggleButton } from "../components/ui/ToggleButton";
import { AlertBanner } from "../components/ui/AlertBanner";
import { formatTrigger } from "../models/automation";
import { useExperimental } from "../hooks/useExperimental";

// RFC3339 <-> <input type="datetime-local"> value (local time).
function toLocalInput(rfc: string | undefined): string {
  if (!rfc) return "";
  const d = new Date(rfc);
  if (isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function fromLocalInput(v: string): string {
  return v ? new Date(v).toISOString() : "";
}

const EMPTY_INPUT: AutomationInput = {
  name: "",
  prompt: "",
  triggerType: "interval",
  triggerValue: "",
  projectPath: "",
  model: "",
  enabled: true,
};

// AutomationForm is the create / edit form. API-side validation errors
// (e.g. invalid cron expressions) are surfaced via the error banner.
export function AutomationForm({
  initial,
  onSubmit,
  onCancel,
  submitLabel,
}: {
  initial: AutomationInput;
  onSubmit: (input: AutomationInput) => Promise<void>;
  onCancel: () => void;
  submitLabel: string;
}) {
  const loopsEnabled = useExperimental("loops");
  const [input, setInput] = useState<AutomationInput>(initial);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async () => {
    setSubmitting(true);
    setError(null);
    try {
      const payload = { ...input };
      if (!loopsEnabled) {
        // Loop settings are not shown (and not sent) while the feature is off.
        delete payload.maxRuns;
        delete payload.until;
        delete payload.watchMetric;
        delete payload.watchDirection;
        delete payload.watchTarget;
      } else {
        payload.maxRuns = payload.maxRuns || 0;
        payload.until = payload.until || "";
        payload.watchMetric = (payload.watchMetric || "").trim();
        payload.watchDirection = payload.watchMetric ? payload.watchDirection || "higher" : "";
        payload.watchTarget = payload.watchMetric ? (payload.watchTarget ?? null) : null;
      }
      await onSubmit(payload);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "保存に失敗しました");
    } finally {
      setSubmitting(false);
    }
  };

  const inputClass =
    "bg-white border border-gray-300 rounded px-3 py-1.5 text-sm w-full focus:outline-none focus:border-blue-500";

  return (
    <div className="space-y-3">
      {error && <AlertBanner variant="error">{error}</AlertBanner>}
      <div>
        <label className="text-sm text-gray-600 block mb-1">Name</label>
        <input
          type="text"
          value={input.name}
          onChange={(e) => setInput({ ...input, name: e.target.value })}
          placeholder="e.g. daily report"
          className={inputClass}
        />
      </div>
      <div>
        <label className="text-sm text-gray-600 block mb-1">Prompt</label>
        <textarea
          value={input.prompt}
          onChange={(e) => setInput({ ...input, prompt: e.target.value })}
          placeholder="発動時にエージェントへ渡す指示文"
          rows={4}
          className={inputClass}
        />
      </div>
      <div className="flex gap-3">
        <div>
          <label className="text-sm text-gray-600 block mb-1">Trigger</label>
          <select
            value={input.triggerType}
            onChange={(e) =>
              setInput({ ...input, triggerType: e.target.value as AutomationInput["triggerType"] })
            }
            className="bg-white border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:border-blue-500"
          >
            <option value="interval">Interval</option>
            <option value="cron">Cron</option>
            {(loopsEnabled || input.triggerType === "queue") && (
              <option value="queue">Queue（残タスクがある限り）</option>
            )}
          </select>
        </div>
        <div className="flex-1">
          <label className="text-sm text-gray-600 block mb-1">
            {input.triggerType === "interval"
              ? "Interval（Go duration）"
              : input.triggerType === "queue"
                ? "前回終了からの待ち時間（Go duration、空なら 0s）"
                : "Cron 式（5フィールド）"}
          </label>
          <input
            type="text"
            value={input.triggerValue}
            onChange={(e) => setInput({ ...input, triggerValue: e.target.value })}
            placeholder={
              input.triggerType === "interval" ? "30m" : input.triggerType === "queue" ? "0s" : "0 9 * * 1-5"
            }
            className={inputClass}
          />
        </div>
      </div>
      <div>
        <label className="text-sm text-gray-600 block mb-1">Project Path (optional)</label>
        <input
          type="text"
          value={input.projectPath || ""}
          onChange={(e) => setInput({ ...input, projectPath: e.target.value })}
          placeholder="未指定なら controller 領域で起動"
          className={inputClass}
        />
      </div>
      <div>
        <label className="text-sm text-gray-600 block mb-1">Model (optional)</label>
        <input
          type="text"
          value={input.model || ""}
          onChange={(e) => setInput({ ...input, model: e.target.value })}
          placeholder="未指定ならプロバイダの既定モデル（例: claude-haiku-5-5）"
          className={inputClass}
        />
      </div>
      {loopsEnabled && (
        <div className="flex gap-3">
          <div>
            <label className="text-sm text-gray-600 block mb-1">最大回数（0 = 無制限）</label>
            <input
              type="number"
              min={0}
              value={input.maxRuns ?? 0}
              onChange={(e) => setInput({ ...input, maxRuns: Math.max(0, Number(e.target.value) || 0) })}
              className={inputClass}
            />
          </div>
          <div className="flex-1">
            <label className="text-sm text-gray-600 block mb-1">期限（空なら無制限）</label>
            <input
              type="datetime-local"
              value={toLocalInput(input.until)}
              onChange={(e) => setInput({ ...input, until: fromLocalInput(e.target.value) })}
              className={inputClass}
            />
          </div>
        </div>
      )}
      {loopsEnabled && (
        <div className="flex gap-3">
          <div className="flex-1">
            <label className="text-sm text-gray-600 block mb-1">監視する指標の key（任意）</label>
            <input
              type="text"
              value={input.watchMetric || ""}
              onChange={(e) => setInput({ ...input, watchMetric: e.target.value })}
              placeholder="例: failing_tests（直近3回で改善しなければ通知）"
              className={inputClass}
            />
          </div>
          <div>
            <label className="text-sm text-gray-600 block mb-1">良い向き</label>
            <select
              value={input.watchDirection || "higher"}
              onChange={(e) => setInput({ ...input, watchDirection: e.target.value as "higher" | "lower" })}
              disabled={!input.watchMetric}
              className={inputClass}
            >
              <option value="higher">大きいほど良い</option>
              <option value="lower">小さいほど良い</option>
            </select>
          </div>
          <div>
            <label className="text-sm text-gray-600 block mb-1">目標値（任意）</label>
            <input
              type="number"
              value={input.watchTarget ?? ""}
              onChange={(e) =>
                setInput({ ...input, watchTarget: e.target.value === "" ? null : Number(e.target.value) })
              }
              disabled={!input.watchMetric}
              placeholder="例: 0"
              className={inputClass}
            />
          </div>
        </div>
      )}
      <div className="flex items-center gap-2">
        <label className="text-sm text-gray-600">有効</label>
        <ToggleButton
          enabled={input.enabled}
          onClick={() => setInput({ ...input, enabled: !input.enabled })}
        >
          {input.enabled ? "ON" : "OFF"}
        </ToggleButton>
      </div>
      <div className="flex gap-2 pt-1">
        <button
          onClick={handleSubmit}
          disabled={submitting}
          className="bg-blue-600 hover:bg-blue-700 disabled:opacity-50 text-white px-4 py-1.5 rounded text-sm font-medium transition-colors"
        >
          {submitLabel}
        </button>
        <button
          onClick={onCancel}
          className="bg-gray-100 hover:bg-gray-200 text-gray-700 px-4 py-1.5 rounded text-sm transition-colors"
        >
          キャンセル
        </button>
      </div>
    </div>
  );
}

export function AutomationsPage() {
  const navigate = useNavigate();
  const { automations: initial } = useLoaderData<{ automations: Automation[] }>();
  const [automations, setAutomations] = useState<Automation[]>(initial);
  const [creating, setCreating] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const reload = async () => {
    try {
      setAutomations(await api.listAutomations());
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Automation の取得に失敗しました");
    }
  };

  const handleCreate = async (input: AutomationInput) => {
    await api.createAutomation(input);
    setCreating(false);
    await reload();
  };

  const handleUpdate = async (id: string, input: AutomationInput) => {
    await api.updateAutomation(id, input);
    setEditingId(null);
    await reload();
  };

  const handleToggle = async (a: Automation) => {
    setError(null);
    try {
      const updated = await api.setAutomationEnabled(a.id, !a.enabled);
      setAutomations((prev) => prev.map((x) => (x.id === updated.id ? updated : x)));
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "切替に失敗しました");
    }
  };

  const handleDelete = async (a: Automation) => {
    if (!confirm(`Automation「${a.name}」を削除しますか？実行履歴も削除されます。`)) return;
    setError(null);
    try {
      await api.deleteAutomation(a.id);
      await reload();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "削除に失敗しました");
    }
  };

  return (
    <div className="min-h-screen bg-gray-50 text-gray-900">
      <header className="bg-white border-b border-gray-200 px-6 py-3 flex items-center gap-4">
        <button
          onClick={() => navigate("/")}
          className="text-gray-500 hover:text-gray-700 text-sm"
        >
          ← Back
        </button>
        <h1 className="text-lg font-bold">Automations</h1>
        <div className="ml-auto">
          {!creating && (
            <button
              onClick={() => {
                setCreating(true);
                setEditingId(null);
              }}
              className="bg-blue-600 hover:bg-blue-700 text-white px-4 py-1.5 rounded text-sm font-medium transition-colors"
            >
              + New Automation
            </button>
          )}
        </div>
      </header>

      <div className="max-w-2xl mx-auto p-6 space-y-4">
        {error && <AlertBanner variant="error">{error}</AlertBanner>}

        {creating && (
          <div className="bg-white rounded-lg border border-gray-200 shadow-sm p-4">
            <h2 className="text-sm font-semibold text-gray-700 mb-3">New Automation</h2>
            <AutomationForm
              initial={EMPTY_INPUT}
              onSubmit={handleCreate}
              onCancel={() => setCreating(false)}
              submitLabel="作成"
            />
          </div>
        )}

        {automations.length === 0 && !creating ? (
          <div className="text-center text-sm text-gray-400 py-12">
            Automation はまだありません
          </div>
        ) : (
          automations.map((a) => (
            <div key={a.id} className="bg-white rounded-lg border border-gray-200 shadow-sm p-4">
              {editingId === a.id ? (
                <>
                  <h2 className="text-sm font-semibold text-gray-700 mb-3">Edit Automation</h2>
                  <AutomationForm
                    initial={{
                      name: a.name,
                      prompt: a.prompt,
                      triggerType: a.triggerType,
                      triggerValue: a.triggerValue,
                      projectPath: a.projectPath || "",
                      model: a.model || "",
                      enabled: a.enabled,
                      maxRuns: a.maxRuns,
                      until: a.until,
                      watchMetric: a.watchMetric,
                      watchDirection: a.watchDirection,
                      watchTarget: a.watchTarget,
                    }}
                    onSubmit={(input) => handleUpdate(a.id, input)}
                    onCancel={() => setEditingId(null)}
                    submitLabel="保存"
                  />
                </>
              ) : (
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <Link
                      to={`/automations/${a.id}`}
                      className="text-sm font-semibold text-blue-600 hover:underline"
                    >
                      {a.name}
                    </Link>
                    <div className="text-xs text-gray-500 mt-1">{formatTrigger(a)}</div>
                    <div className="text-xs text-gray-500 font-mono truncate">
                      {a.projectPath || "(controller)"}
                    </div>
                    <div className="text-xs text-gray-400 mt-1 line-clamp-2 whitespace-pre-wrap">
                      {a.prompt}
                    </div>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    <ToggleButton enabled={a.enabled} onClick={() => handleToggle(a)}>
                      {a.enabled ? "ON" : "OFF"}
                    </ToggleButton>
                    <button
                      onClick={() => {
                        setEditingId(a.id);
                        setCreating(false);
                      }}
                      className="text-xs text-gray-500 hover:text-gray-700 px-2 py-1 rounded hover:bg-gray-100"
                    >
                      編集
                    </button>
                    <button
                      onClick={() => handleDelete(a)}
                      className="text-xs text-red-500 hover:text-red-700 px-2 py-1 rounded hover:bg-red-50"
                    >
                      削除
                    </button>
                  </div>
                </div>
              )}
            </div>
          ))
        )}
      </div>
    </div>
  );
}
