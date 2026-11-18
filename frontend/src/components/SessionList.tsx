import { useEffect, useMemo, useState } from "react";
import { useNavigate, useMatch } from "react-router-dom";
import { motion } from "motion/react";
import type { Session } from "../types/session";
import { SecondaryButton } from "./ui/SecondaryButton";
import { ExternalProcessRow } from "./ui/ExternalProcessRow";
import { SessionCard } from "./ui/SessionCard";
import { SessionSearch } from "./SessionSearch";
import { useExperimental } from "../hooks/useExperimental";
import { api, type AutomationSummary, type ProjectDetail } from "../api/client";
import { LoopRow } from "./ui/LoopRow";
import { ChoiceCardsBadge } from "./session/ChoiceCards";
import { useOpenChoiceSessions } from "../hooks/useChoiceCards";
import { ProjectRow } from "./ui/ProjectRow";
import { projectUrl } from "./ProjectSummaryLine";

function timeAgo(dateStr: string): string {
  const diff = Date.now() - new Date(dateStr).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

/** Build a map from parent session ID to its child sessions */
function buildChildrenMap(sessions: Session[]): Map<string, Session[]> {
  const map = new Map<string, Session[]>();
  for (const s of sessions) {
    if (s.parentSessionId) {
      const children = map.get(s.parentSessionId);
      if (children) {
        children.push(s);
      } else {
        map.set(s.parentSessionId, [s]);
      }
    }
  }
  return map;
}

/** The coordinator's last activity, else the project's. */
function projectUpdatedAt(p: ProjectDetail, sessions: Session[]): string | null {
  const coord = p.coordinator && sessions.find((s) => s.id === p.coordinator!.id);
  return coord?.updatedAt ?? p.lastUpdatedAt;
}

interface Props {
  sessions: Session[];
  onRestartController: () => void;
}

export function SessionList({ sessions, onRestartController }: Props) {
  const navigate = useNavigate();
  const sessionMatch = useMatch("/sessions/:id");
  const selectedSessionId = sessionMatch?.params.id ?? null;
  const projectMatch = useMatch("/projects/:path");
  const childrenMap = useMemo(() => buildChildrenMap(sessions), [sessions]);
  const searchEnabled = useExperimental("session_search");

  const openChoiceSessions = useOpenChoiceSessions(sessions);

  const loopsEnabled = useExperimental("loops");
  const [loops, setLoops] = useState<AutomationSummary[]>([]);
  useEffect(() => {
    if (!loopsEnabled) {
      setLoops([]);
      return;
    }
    let cancelled = false;
    const load = () =>
      api
        .listAutomationSummaries()
        .then((r) => {
          if (!cancelled) setLoops(r);
        })
        .catch(() => {});
    load();
    const timer = setInterval(load, 30_000);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
    // Session updates (websocket) also change loop state, so reload on them.
  }, [loopsEnabled, sessions]);

  // Experimental "projects": one row per project instead of the flat list.
  const projectsEnabled = useExperimental("projects");
  const [projects, setProjects] = useState<ProjectDetail[]>([]);
  useEffect(() => {
    if (!projectsEnabled) {
      setProjects([]);
      return;
    }
    let cancelled = false;
    const load = () =>
      api
        .listProjects()
        .then((list) => Promise.all(list.map((p) => api.getProject(p.path))))
        .then((r) => {
          if (!cancelled) setProjects(r);
        })
        .catch(() => {});
    // Session updates arrive often; debounce the (N+1) reload.
    const first = setTimeout(load, 300);
    const timer = setInterval(load, 30_000);
    return () => {
      cancelled = true;
      clearTimeout(first);
      clearInterval(timer);
    };
  }, [projectsEnabled, sessions]);
  const projectMembers = useMemo(() => {
    const sessionIds = new Set<string>();
    const automationIds = new Set<string>();
    for (const p of projects) {
      if (p.coordinator) sessionIds.add(p.coordinator.id);
      for (const x of p.sessions) sessionIds.add(x.id);
      for (const a of p.automationList) automationIds.add(a.id);
    }
    return { sessionIds, automationIds };
  }, [projects]);

  // Loops that feed another loop hang below it instead of getting their own row
  // (mutual feeders stay top-level so neither disappears).
  const { topLoops, feedersByTarget } = useMemo(() => {
    const byId = new Map(loops.map((l) => [l.id, l]));
    const feedersByTarget = new Map<string, AutomationSummary[]>();
    const top: AutomationSummary[] = [];
    for (const l of loops) {
      const targets = l.feeds.filter((t) => {
        const target = byId.get(t.id);
        return target && !target.feeds.some((b) => b.id === l.id);
      });
      if (targets.length === 0) {
        // Disabled loops that never ran (and were not stopped by a safety valve) are noise.
        if (l.enabled || l.lastRunAt || l.stoppedReason) top.push(l);
        continue;
      }
      for (const t of targets) {
        feedersByTarget.set(t.id, [...(feedersByTarget.get(t.id) ?? []), l]);
      }
    }
    return { topLoops: top, feedersByTarget };
  }, [loops]);

  const topLevelSessions = useMemo(() => {
    return sessions
      .filter((s) => !s.parentSessionId)
      .filter((s) => !projectMembers.sessionIds.has(s.id))
      .sort((a, b) => {
        // Controller pinned first, external processes last,
        // threads by most recent activity (last message) first
        if (a.type === "controller" && b.type !== "controller") return -1;
        if (a.type !== "controller" && b.type === "controller") return 1;
        if (a.type === "external" && b.type !== "external") return 1;
        if (a.type !== "external" && b.type === "external") return -1;
        return new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime();
      });
  }, [sessions, projectMembers]);

  if (sessions.length === 0 && topLoops.length === 0 && projects.length === 0) {
    return (
      <div className="text-center text-gray-400 py-8">
        <p className="text-sm">No sessions yet</p>
      </div>
    );
  }

  let sessionIndex = 0;
  const renderSession = (s: Session, depth: number) => {
    const children = childrenMap.get(s.id) || [];
    const idx = sessionIndex++;
    return (
      <motion.div
        key={s.id}
        initial={{ opacity: 0, y: 16 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ type: "spring", damping: 25, stiffness: 300, delay: idx * 0.04 }}
      >
        <div style={{ marginLeft: depth * 24 }}>
          {s.type === "external" ? (
            <ExternalProcessRow
              provider={s.provider}
              name={s.name}
              pid={s.id.replace("ext-", "")}
              timeAgo={timeAgo(s.createdAt)}
            />
          ) : (
            <SessionCard
              id={s.id}
              name={s.name}
              status={s.status}
              type={s.type}
              provider={s.provider}
              roleTemplate={s.roleTemplate}
              currentTask={s.currentTask}
              projectPath={s.projectPath}
              timeAgo={timeAgo(s.updatedAt)}
              isSubSession={depth > 0}
              isSelected={s.id === selectedSessionId}
              completionReport={s.completionReport}
              badge={openChoiceSessions.has(s.id) ? <ChoiceCardsBadge /> : undefined}
              onClick={() => navigate(`/sessions/${s.id}`, { viewTransition: true } as never)}
              actions={
                s.type === "controller" && (s.status === "paused" || s.status === "exited") ? (
                  <SecondaryButton
                    onClick={(e) => { e.stopPropagation(); onRestartController(); }}
                  >
                    Restart
                  </SecondaryButton>
                ) : undefined
              }
            />
          )}
        </div>
        {children.map((child) => renderSession(child, depth + 1))}
      </motion.div>
    );
  };

  // Loop rows are interleaved with sessions by latest activity; the controller
  // stays pinned first and external processes last.
  type Item =
    | { kind: "session"; s: Session }
    | { kind: "loop"; l: AutomationSummary }
    | { kind: "project"; p: ProjectDetail; at: number };
  const loopTime = (l: AutomationSummary) => new Date(l.lastRunAt ?? l.createdAt).getTime();
  const items: Item[] = [
    ...topLevelSessions.map((s): Item => ({ kind: "session", s })),
    ...topLoops.filter((l) => !projectMembers.automationIds.has(l.id)).map((l): Item => ({ kind: "loop", l })),
    ...projects.map((p): Item => ({ kind: "project", p, at: new Date(projectUpdatedAt(p, sessions) ?? 0).getTime() })),
  ];
  const rank = (i: Item) =>
    i.kind !== "session" ? 1 : i.s.type === "controller" ? 0 : i.s.type === "external" ? 2 : 1;
  const time = (i: Item) =>
    i.kind === "loop" ? loopTime(i.l) : i.kind === "project" ? i.at : new Date(i.s.updatedAt).getTime();
  items.sort((a, b) => rank(a) - rank(b) || time(b) - time(a));

  const list = (
    <div className="flex flex-col gap-1.5">
      {items.map((i) =>
        i.kind === "session" ? (
          renderSession(i.s, 0)
        ) : i.kind === "project" ? (
          <ProjectRow
            key={`project-${i.p.path}`}
            project={i.p}
            loops={loops.filter((l) => i.p.automationList.some((a) => a.id === l.id))}
            loopsOn={loopsEnabled}
            updatedAt={projectUpdatedAt(i.p, sessions)}
            isSelected={projectMatch ? decodeURIComponent(projectMatch.params.path ?? "") === i.p.path : false}
            timeAgo={timeAgo}
            onOpenProject={() => navigate(projectUrl(i.p.path))}
            onOpenLoop={(id) => navigate(`/automations/${id}`)}
            onOpenSession={(id) => navigate(`/sessions/${id}`)}
          />
        ) : (
          <LoopRow
            key={`loop-${i.l.id}`}
            loop={i.l}
            feeders={feedersByTarget.get(i.l.id)}
            timeAgo={timeAgo}
            onOpenLoop={(id) => navigate(`/automations/${id}`)}
            onOpenSession={(id) => navigate(`/sessions/${id}`)}
          />
        ),
      )}
    </div>
  );
  return searchEnabled ? <SessionSearch>{list}</SessionSearch> : list;
}
