import { useCallback, useEffect, useRef, useState } from "react";
import { Outlet, useLoaderData, useParams, useRevalidator } from "react-router-dom";
import { api, type ProjectDetail } from "../api/client";
import { useExperimental } from "../hooks/useExperimental";
import { useIsDesktop } from "../App";
import { useWebSocket } from "../hooks/useWebSocket";
import { ProjectPanel } from "../components/ProjectPanel";
import { ProjectSummaryLine } from "../components/ProjectSummaryLine";
import { SessionPage } from "./SessionPage";

/** Project detail, kept fresh by polling and session updates. */
function useProject(path: string) {
  const [project, setProject] = useState<ProjectDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(() => {
    api
      .getProject(path)
      .then((p) => {
        setProject(p);
        setError(null);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : "プロジェクトを取得できません"));
  }, [path]);
  useEffect(() => {
    load();
    const t = setInterval(load, 15_000);
    return () => clearInterval(t);
  }, [load]);
  useWebSocket(
    useCallback(
      (msg: { type: string }) => {
        if (msg.type === "session_update" || msg.type === "escalation") load();
      },
      [load],
    ),
  );
  return { project, error, reload: load };
}

/**
 * Project page (experimental "projects"): the coordinator's conversation in
 * the middle, the "配下" panel on the right (desktop) or as a top bar that
 * opens a sheet (mobile).
 */
export function ProjectLayout() {
  const { path = "" } = useParams<{ path: string }>();
  const isDesktop = useIsDesktop();
  const loopsOn = useExperimental("loops");
  const { project, error, reload } = useProject(path);
  const revalidator = useRevalidator();
  const [sheet, setSheet] = useState(false);

  // The loader resolves the coordinator, so reload the conversation when it changes
  // (reassigned from the panel, another tab, or the CLI / API).
  const coordinatorId = project?.coordinator?.id ?? "";
  const loadedCoordinator = useRef<string | null>(null);
  useEffect(() => {
    if (!project) return;
    if (loadedCoordinator.current !== null && loadedCoordinator.current !== coordinatorId) {
      revalidator.revalidate();
    }
    loadedCoordinator.current = coordinatorId;
  }, [project, coordinatorId, revalidator]);

  if (error && !project) {
    return <div className="p-6 text-sm text-red-600">{error}</div>;
  }

  const panel = project && (
    <ProjectPanel project={project} loopsOn={loopsOn} onChanged={reload} onNavigate={() => setSheet(false)} />
  );

  if (isDesktop) {
    return (
      <div className="h-full flex min-h-0">
        <div className="flex-1 min-w-0 min-h-0 flex flex-col">
          <Outlet />
        </div>
        <aside className="w-80 shrink-0 border-l border-gray-200 bg-gray-50 overflow-y-auto p-3" aria-label="配下パネル">
          {panel}
        </aside>
      </div>
    );
  }

  return (
    <div className="h-dvh flex flex-col">
      <button
        type="button"
        onClick={() => setSheet(true)}
        aria-label="配下を開く"
        className="shrink-0 w-full flex items-center gap-2 px-4 py-2.5 bg-white border-b border-gray-200 text-left text-xs text-gray-600"
      >
        <span className="font-semibold text-gray-900 truncate max-w-[40%]">{project?.name ?? path}</span>
        <span className="truncate min-w-0">{project && <ProjectSummaryLine project={project} loops={loopsOn} />}</span>
        <span className="ml-auto text-gray-400 shrink-0">▾</span>
      </button>
      <div className="flex-1 min-h-0 overflow-hidden [&>div]:h-full!">
        <Outlet />
      </div>
      {sheet && (
        <div className="fixed inset-0 z-50 flex flex-col justify-end" role="dialog" aria-label="配下">
          <div className="absolute inset-0 bg-black/30" onClick={() => setSheet(false)} />
          <div className="relative bg-gray-50 rounded-t-2xl max-h-[80dvh] overflow-y-auto p-4 pt-9 shadow-xl">
            <button
              type="button"
              onClick={() => setSheet(false)}
              className="absolute right-3 top-2 text-gray-400 text-sm px-2 py-1"
              aria-label="閉じる"
            >
              ✕
            </button>
            {panel}
          </div>
        </div>
      )}
    </div>
  );
}

/** Index route: the coordinator's SessionPage (its loader data is the session's). */
export function ProjectCoordinatorPage() {
  const data = useLoaderData() as { none?: true };
  if (data.none) {
    return (
      <div className="h-full flex items-center justify-center text-sm text-gray-400 p-6 text-center">
        このプロジェクトにはコーディネーターがいません。パネルの「設定」から指定できます。
      </div>
    );
  }
  return <SessionPage />;
}
