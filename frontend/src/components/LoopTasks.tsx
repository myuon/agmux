import { useCallback, useEffect, useState } from "react";
import { api } from "../api/client";
import type { AutomationTask } from "../api/client";
import { Section } from "./ui/Section";
import { AlertBanner } from "./ui/AlertBanner";
import { useExperimental } from "../hooks/useExperimental";

const STATUS_ORDER: AutomationTask["status"][] = ["in_progress", "pending", "failed", "done"];

const STATUS_LABEL: Record<AutomationTask["status"], string> = {
  in_progress: "実行中",
  pending: "待機中",
  failed: "失敗",
  done: "完了",
};

const STATUS_STYLES: Record<AutomationTask["status"], string> = {
  in_progress: "bg-blue-50 border-blue-300 text-blue-700",
  pending: "bg-gray-50 border-gray-300 text-gray-600",
  failed: "bg-red-50 border-red-300 text-red-700",
  done: "bg-green-50 border-green-300 text-green-700",
};

/**
 * Remaining-task list of a loop (experimental "loops"). Renders nothing while
 * the feature is off.
 */
export function LoopTasks({ automationId }: { automationId: string }) {
  const enabled = useExperimental("loops");
  const [tasks, setTasks] = useState<AutomationTask[]>([]);
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [error, setError] = useState<string | null>(null);

  const reload = useCallback(async () => {
    try {
      setTasks(await api.listAutomationTasks(automationId));
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "タスクの取得に失敗しました");
    }
  }, [automationId]);

  useEffect(() => {
    if (!enabled) return;
    reload();
    const t = setInterval(reload, 10000);
    return () => clearInterval(t);
  }, [enabled, reload]);

  if (!enabled) return null;

  const run = async (fn: () => Promise<unknown>) => {
    setError(null);
    try {
      await fn();
      await reload();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "操作に失敗しました");
    }
  };

  const handleAdd = (e: React.FormEvent) => {
    e.preventDefault();
    if (!title.trim()) return;
    run(async () => {
      await api.addAutomationTask(automationId, title.trim(), body.trim());
      setTitle("");
      setBody("");
    });
  };

  return (
    <Section title="残タスク">
      {error && <AlertBanner variant="error">{error}</AlertBanner>}
      <form onSubmit={handleAdd} className="space-y-2">
        <input
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="タスクの題名"
          className="w-full border border-gray-300 rounded px-3 py-2 text-sm"
        />
        <textarea
          value={body}
          onChange={(e) => setBody(e.target.value)}
          placeholder="詳細（任意）"
          rows={2}
          className="w-full border border-gray-300 rounded px-3 py-2 text-sm"
        />
        <button
          type="submit"
          disabled={!title.trim()}
          className="px-3 py-2 text-sm rounded bg-blue-600 text-white disabled:opacity-40"
        >
          追加
        </button>
      </form>
      {tasks.length === 0 ? (
        <div className="text-sm text-gray-400">残タスクはありません</div>
      ) : (
        STATUS_ORDER.map((status) => {
          const group = tasks.filter((t) => t.status === status);
          if (group.length === 0) return null;
          return (
            <div key={status}>
              <div className="text-xs font-medium text-gray-500 mb-1">
                {STATUS_LABEL[status]} ({group.length})
              </div>
              <div className="divide-y divide-gray-100">
                {group.map((t) => (
                  <div key={t.id} className="py-2 flex items-start gap-2">
                    <span
                      className={`px-2 py-0.5 text-xs rounded-full border shrink-0 ${STATUS_STYLES[t.status]}`}
                    >
                      {STATUS_LABEL[t.status]}
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="text-sm text-gray-800 break-words">
                        {t.title}
                        {t.createdBy === "agent" && (
                          <span className="ml-1 text-xs text-gray-400">(agent)</span>
                        )}
                      </div>
                      {t.body && (
                        <div className="text-xs text-gray-500 whitespace-pre-wrap break-words">
                          {t.body}
                        </div>
                      )}
                      {t.result && (
                        <div className="text-xs text-gray-600 whitespace-pre-wrap break-words">
                          結果: {t.result}
                        </div>
                      )}
                      {t.attempts > 0 && (
                        <div className="text-xs text-yellow-700">再挑戦 {t.attempts} 回</div>
                      )}
                    </div>
                    {t.status === "failed" && (
                      <button
                        onClick={() =>
                          run(() => api.updateAutomationTask(automationId, t.id, { status: "pending" }))
                        }
                        className="text-xs text-blue-600 hover:underline shrink-0 px-1 py-1"
                      >
                        再試行
                      </button>
                    )}
                    <button
                      onClick={() => run(() => api.deleteAutomationTask(automationId, t.id))}
                      className="text-xs text-red-600 hover:underline shrink-0 px-1 py-1"
                    >
                      削除
                    </button>
                  </div>
                ))}
              </div>
            </div>
          );
        })
      )}
    </Section>
  );
}
