import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, type ProjectAutomation, type ProjectDetail } from "../api/client";
import { Chip } from "./ui/Chip";
import { lampClass } from "./StatusBadge";
import { formatNum, Sparkline } from "./LoopDashboard";

const stateLabel = { running: "実行中", waiting: "待機中", stopped: "停止" } as const;
const stateChip = { running: "blue", waiting: "gray", stopped: "red" } as const;

export function timeAgo(dateStr: string): string {
  const mins = Math.floor((Date.now() - new Date(dateStr).getTime()) / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  return hrs < 24 ? `${hrs}h ago` : `${Math.floor(hrs / 24)}d ago`;
}

/** Fetches the chronological values of one metric of a loop for the mini graph. */
function useMetricSeries(id: string, key: string | undefined, version: string | null) {
  const [values, setValues] = useState<number[]>([]);
  useEffect(() => {
    if (!key) return;
    let cancelled = false;
    api
      .listAutomationReports(id, 30)
      .then((rs) => {
        if (cancelled) return;
        setValues(rs.map((r) => r.report?.metrics?.[key]).filter((v): v is number => typeof v === "number"));
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [id, key, version]);
  return values;
}

function LoopItem({ a, loopsOn }: { a: ProjectAutomation; loopsOn: boolean }) {
  const metrics = loopsOn ? (a.latestReport?.metrics ?? {}) : {};
  const keys = Object.keys(metrics).sort();
  const series = useMetricSeries(a.id, keys[0], a.lastRunAt);
  return (
    <li className="border border-gray-200 rounded-lg px-2.5 py-2 bg-white">
      <div className="flex items-center gap-2">
        <Link to={`/automations/${a.id}`} className="font-medium text-sm truncate min-w-0 hover:underline">
          {loopsOn ? "↻ " : ""}
          {a.name}
        </Link>
        <span className="ml-auto shrink-0">
          <Chip color={stateChip[a.state]}>{stateLabel[a.state]}</Chip>
        </span>
      </div>
      <div className="mt-0.5 flex flex-wrap gap-x-2 text-xs text-gray-500">
        {loopsOn && a.pendingTasks !== undefined && <span>残タスク {a.pendingTasks}</span>}
        <span>{a.lastRunAt ? `最終 ${timeAgo(a.lastRunAt)}` : "未実行"}</span>
      </div>
      {a.state === "stopped" && a.stoppedReason && (
        <div className="mt-0.5 text-xs text-red-500 break-words">{a.stoppedReason.replace(/^disabled:\s*/, "")}</div>
      )}
      {loopsOn && keys.length > 0 && (
        <div className="mt-1.5">
          <div className="flex flex-wrap gap-x-3 text-xs text-gray-700">
            {keys.slice(0, 3).map((k) => (
              <span key={k}>
                <span className="text-gray-500">{k}</span> <span className="font-semibold">{formatNum(metrics[k])}</span>
              </span>
            ))}
          </div>
          {series.length >= 2 && <Sparkline points={series} className="w-full h-10" />}
        </div>
      )}
    </li>
  );
}

function CoordinatorSettings({ project, onChanged }: { project: ProjectDetail; onChanged: () => void }) {
  const [error, setError] = useState<string | null>(null);
  const candidates = project.sessions.filter(
    (s) => s.type === "worker" && s.status !== "archived" && s.projectPath === project.path,
  );
  const current = project.coordinator?.id ?? "";
  const options =
    project.coordinator && !candidates.some((s) => s.id === current)
      ? [{ id: current, name: project.coordinator.name }, ...candidates]
      : candidates;
  return (
    <div className="border border-gray-200 rounded-lg bg-white px-2.5 py-2 text-xs">
      <label className="block text-gray-500 mb-1" htmlFor="coordinator-select">
        コーディネーター
      </label>
      <select
        id="coordinator-select"
        value={current}
        onChange={async (e) => {
          if (!e.target.value || e.target.value === current) return;
          try {
            await api.setProjectCoordinator(project.path, e.target.value);
            setError(null);
            onChanged();
          } catch (err: unknown) {
            setError(err instanceof Error ? err.message : "付け替えに失敗しました");
          }
        }}
        className="w-full border border-gray-300 rounded px-2 py-1.5 text-sm bg-white"
      >
        {!current && <option value="">未設定</option>}
        {options.map((s) => (
          <option key={s.id} value={s.id}>
            {s.name || s.id}
          </option>
        ))}
      </select>
      {error && <div className="mt-1 text-red-600 break-words">{error}</div>}
    </div>
  );
}

/** The "配下" panel of the project page: loops, sessions, stop / waiting marks. */
export function ProjectPanel({
  project,
  loopsOn,
  onChanged,
  onNavigate,
}: {
  project: ProjectDetail;
  loopsOn: boolean;
  onChanged: () => void;
  onNavigate?: () => void;
}) {
  const [settings, setSettings] = useState(false);
  const coordId = project.coordinator?.id;
  const sessions = project.sessions
    .filter((s) => s.id !== coordId && s.status !== "archived")
    .sort(
      (a, b) =>
        Number(b.status === "working") - Number(a.status === "working") || b.updatedAt.localeCompare(a.updatedAt),
    );
  const heading = "text-xs font-semibold text-gray-500 uppercase tracking-wide mb-1.5";
  return (
    <div
      className="flex flex-col gap-4"
      onClick={(e) => {
        if ((e.target as HTMLElement).closest("a")) onNavigate?.();
      }}
    >
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-bold text-gray-900 truncate min-w-0">{project.name}</h2>
        <button
          type="button"
          onClick={() => setSettings((v) => !v)}
          aria-expanded={settings}
          className="text-xs text-gray-500 hover:text-gray-700 px-2 py-1 rounded hover:bg-gray-100 shrink-0"
        >
          設定
        </button>
      </div>
      {settings && <CoordinatorSettings project={project} onChanged={onChanged} />}

      {project.escalations.length > 0 && (
        <section>
          <h3 className={heading}>返事待ち</h3>
          <ul className="flex flex-col gap-1">
            {project.escalations.map((e) => (
              <li key={e.id}>
                <Link
                  to={`/sessions/${e.sessionId}`}
                  className="block border border-orange-300 bg-orange-50 rounded-lg px-2.5 py-1.5 text-xs text-orange-800 break-words hover:bg-orange-100"
                >
                  {e.message}
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}

      <section>
        <h3 className={heading}>
          {loopsOn ? "ループ" : "Automation"} ({project.automationList.length})
        </h3>
        {project.automationList.length === 0 ? (
          <p className="text-xs text-gray-400">ありません</p>
        ) : (
          <ul className="flex flex-col gap-1.5">
            {project.automationList.map((a) => (
              <LoopItem key={a.id} a={a} loopsOn={loopsOn} />
            ))}
          </ul>
        )}
      </section>

      <section>
        <h3 className={heading}>セッション ({sessions.length})</h3>
        {sessions.length === 0 ? (
          <p className="text-xs text-gray-400">ありません</p>
        ) : (
          <ul className="flex flex-col gap-0.5">
            {sessions.map((s) => (
              <li key={s.id}>
                <Link
                  to={`/sessions/${s.id}`}
                  className="flex items-center gap-2 text-sm text-gray-700 rounded px-1.5 py-1 hover:bg-gray-100"
                >
                  <span className={`w-2 h-2 rounded-full shrink-0 ${lampClass(s.status, s.unread)}`} />
                  <span className="truncate min-w-0">{s.name || s.id}</span>
                  <span className="ml-auto shrink-0 text-xs text-gray-400">{timeAgo(s.updatedAt)}</span>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
