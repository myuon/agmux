import type { ProjectSummary } from "../api/client";

/**
 * One-line summary of a project's members, e.g. 「ループ 2 稼働 · 1 停止 · 返事待ち 1」.
 * Stopped and waiting-for-reply parts are colored. `loops` switches the noun
 * between ループ (experimental "loops" on) and Automation.
 */
export function ProjectSummaryLine({ project, loops }: { project: ProjectSummary; loops: boolean }) {
  const a = project.automations;
  const parts: { text: string; className?: string }[] = [];
  const noun = loops ? "ループ" : "Automation";
  const autoParts: string[] = [];
  if (a.running > 0) autoParts.push(`${a.running} 稼働`);
  if (a.waiting > 0) autoParts.push(`${a.waiting} 待機`);
  if (autoParts.length > 0) parts.push({ text: `${noun} ${autoParts.join(" · ")}` });
  if (a.stopped > 0) {
    parts.push({
      text: autoParts.length > 0 ? `${a.stopped} 停止` : `${noun} ${a.stopped} 停止`,
      className: "text-red-600 font-medium",
    });
  }
  if (project.activeSessions > 0) parts.push({ text: `作業中 ${project.activeSessions}` });
  if (project.pendingEscalations > 0) {
    parts.push({ text: `返事待ち ${project.pendingEscalations}`, className: "text-orange-600 font-medium" });
  }
  if (parts.length === 0) return <span className="text-gray-400">配下なし</span>;
  return (
    <span>
      {parts.map((p, i) => (
        <span key={i}>
          {i > 0 && <span className="text-gray-400"> · </span>}
          <span className={p.className}>{p.text}</span>
        </span>
      ))}
    </span>
  );
}

export function projectUrl(path: string): string {
  return `/projects/${encodeURIComponent(path)}`;
}
