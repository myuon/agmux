import { useCallback, useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { api, type LearningProposal } from "../api/client";

const targetLabel: Record<LearningProposal["target"], string> = {
  claude_md: "CLAUDE.md",
  skill: "スキル",
  memory: "メモリ",
  loop: "ループ",
};

function targetPath(p: LearningProposal): string {
  switch (p.target) {
    case "claude_md":
      return "CLAUDE.md";
    case "skill":
      return `.claude/skills/${p.name}/SKILL.md`;
    case "memory":
      return `memory/${p.name}.md（${p.memoryType ?? "project"}）`;
    case "loop":
      return `Automation（${p.triggerType} ${p.triggerValue}）`;
  }
}

function applyNote(p: LearningProposal): string {
  if (p.target === "loop") return "採用すると無効の Automation を作ります。中身を確認してから有効にしてください";
  return p.target === "memory"
    ? "採用するとメモリに直接書き込みます"
    : "採用すると反映用のセッションが起動し、PR を作ります";
}

const statusLabel: Record<LearningProposal["status"], string> = {
  pending: "未処理",
  applying: "PR 作成中",
  applied: "反映済み",
  rejected: "却下",
  failed: "失敗",
};

function Evidence({ p }: { p: LearningProposal }) {
  const items = p.evidence ?? [];
  if (!p.origin || items.length === 0) return null;
  const sessions = new Set(items.map((g) => g.sessionId)).size;
  const heading =
    p.origin === "repeated_task"
      ? `${sessions} セッションで同じ型の作業を手で依頼しています`
      : `${sessions} セッションで同じ趣旨の指摘がありました（${items.length} 回）`;
  return (
    <div className="bg-amber-50 border border-amber-200 rounded px-3 py-2 space-y-2" data-testid="evidence">
      <div className="text-xs font-medium text-amber-800">{heading}</div>
      <ul className="space-y-1.5">
        {items.map((g) => (
          <li key={g.id} className="text-xs text-gray-700">
            <Link to={`/sessions/${g.sessionId}`} className="text-blue-600 hover:underline">
              {g.sessionName || g.sessionId}
            </Link>
            <span className="ml-1">{g.summary}</span>
            {g.excerpt && <blockquote className="mt-0.5 border-l-2 border-amber-300 pl-2 text-gray-500">{g.excerpt}</blockquote>}
          </li>
        ))}
      </ul>
    </div>
  );
}

function projectName(path: string): string {
  return path.split("/").filter(Boolean).pop() ?? path;
}

function ProposalCard({ p, onChange }: { p: LearningProposal; onChange: (p: LearningProposal) => void }) {
  const [editing, setEditing] = useState(false);
  const [content, setContent] = useState(p.content);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const pending = p.status === "pending";

  const act = async (kind: "accept" | "reject") => {
    setBusy(true);
    setError(null);
    try {
      const r =
        kind === "accept"
          ? await api.acceptLearningProposal(p.id, content !== p.content ? content : undefined)
          : await api.rejectLearningProposal(p.id);
      onChange(r.proposal);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <article className="bg-white border border-gray-200 rounded-lg p-4 space-y-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2 text-xs text-gray-500">
            <span className="bg-indigo-50 text-indigo-700 rounded px-1.5 py-0.5 font-medium">{targetLabel[p.target]}</span>
            <span className="font-mono break-all">{targetPath(p)}</span>
          </div>
          <h2 className="mt-1.5 text-base font-semibold text-gray-900">{p.title}</h2>
        </div>
        {!pending && (
          <span className="shrink-0 text-xs text-gray-500 bg-gray-100 rounded px-2 py-0.5">{statusLabel[p.status]}</span>
        )}
      </div>

      {editing ? (
        <textarea
          value={content}
          onChange={(e) => setContent(e.target.value)}
          rows={Math.min(14, Math.max(4, content.split("\n").length + 1))}
          className="w-full font-mono text-sm border border-gray-300 rounded p-2 focus:outline-none focus:border-blue-500"
          aria-label="追記する内容"
        />
      ) : (
        <pre className="whitespace-pre-wrap break-words text-sm text-gray-800 bg-green-50 border-l-4 border-green-400 rounded-r px-3 py-2 font-sans">
          {content}
        </pre>
      )}

      {p.reason && <p className="text-sm text-gray-600">{p.reason}</p>}
      <Evidence p={p} />

      <div className="text-xs text-gray-400 flex flex-wrap gap-x-3">
        <span>{projectName(p.projectPath)}</span>
        <Link to={`/sessions/${p.sessionId}`} className="text-blue-600 hover:underline">
          {p.sessionName || p.sessionId} の会話から
        </Link>
        {p.automationId && (
          <Link to={`/automations/${p.automationId}`} className="text-blue-600 hover:underline">
            作成した Automation を開く（無効）
          </Link>
        )}
        {p.applySessionId && (
          <Link to={`/sessions/${p.applySessionId}`} className="text-blue-600 hover:underline">
            反映用セッションを開く
          </Link>
        )}
      </div>

      {p.error && <div className="text-sm text-red-600">{p.error}</div>}
      {error && <div className="text-sm text-red-600">{error}</div>}

      {pending && (
        <div className="space-y-2 pt-1">
          <div className="flex gap-2">
            <button
              onClick={() => act("accept")}
              disabled={busy}
              className="flex-1 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 text-white rounded px-3 py-2 text-sm font-medium"
            >
              採用
            </button>
            <button
              onClick={() => setEditing((v) => !v)}
              disabled={busy}
              className="bg-white border border-gray-300 hover:bg-gray-50 rounded px-3 py-2 text-sm"
            >
              {editing ? "編集を閉じる" : "編集"}
            </button>
            <button
              onClick={() => act("reject")}
              disabled={busy}
              className="bg-white border border-gray-300 hover:bg-gray-50 text-gray-600 rounded px-3 py-2 text-sm"
            >
              却下
            </button>
          </div>
          <p className="text-xs text-gray-400">{applyNote(p)}</p>
        </div>
      )}
    </article>
  );
}

export function LearningInboxPage() {
  const navigate = useNavigate();
  const [proposals, setProposals] = useState<LearningProposal[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showHistory, setShowHistory] = useState(false);

  const load = useCallback(() => {
    api
      .listLearningProposals()
      .then((r) => {
        setProposals(r.proposals);
        setError(null);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const replace = (p: LearningProposal) =>
    setProposals((prev) => prev?.map((x) => (x.id === p.id ? p : x)) ?? null);

  const pending = proposals?.filter((p) => p.status === "pending") ?? [];
  const decided = proposals?.filter((p) => p.status !== "pending") ?? [];

  return (
    <div className="min-h-screen bg-gray-50 text-gray-900">
      <header className="bg-white border-b border-gray-200 px-4 sm:px-6 py-3 flex items-center gap-4">
        <button onClick={() => navigate("/")} className="text-gray-500 hover:text-gray-700 text-sm">
          ← Back
        </button>
        <h1 className="text-lg font-bold">学びの受信箱</h1>
      </header>
      <main className="max-w-2xl mx-auto p-4 sm:p-6 space-y-4">
        <p className="text-sm text-gray-600">
          ゴールを完了したセッションの振り返りから、CLAUDE.md・スキル・メモリへの追記が提案されます。採用したものだけが反映されます。
        </p>
        {error && <div className="text-sm text-red-600">{error}</div>}
        {proposals === null && !error && <div className="text-sm text-gray-400">Loading...</div>}
        {proposals !== null && pending.length === 0 && (
          <div className="text-sm text-gray-500 bg-white border border-dashed border-gray-300 rounded-lg p-6 text-center">
            未処理の提案はありません。セッションのメニューの「振り返る」からも提案を作れます。
          </div>
        )}
        {pending.map((p) => (
          <ProposalCard key={p.id} p={p} onChange={replace} />
        ))}

        {decided.length > 0 && (
          <div className="pt-4">
            <button onClick={() => setShowHistory((v) => !v)} className="text-sm text-gray-600 hover:text-gray-900">
              {showHistory ? "▾" : "▸"} 処理済み（{decided.length}）
            </button>
            {showHistory && (
              <div className="mt-3 space-y-3">
                {decided.map((p) => (
                  <ProposalCard key={p.id} p={p} onChange={replace} />
                ))}
              </div>
            )}
          </div>
        )}
      </main>
    </div>
  );
}
