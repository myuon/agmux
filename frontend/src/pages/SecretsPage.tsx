import { useCallback, useState } from "react";
import { Link, useLoaderData, useNavigate } from "react-router-dom";
import { ChevronDown, ChevronRight, KeyRound, Plus, Trash2 } from "lucide-react";
import { api, type SecretUsage } from "../api/client";
import type { SecretMeta } from "../lib/secret";
import type { Session } from "../types/session";
import { Chip } from "../components/ui/Chip";

// SecretsPage lists registered secrets (metadata only — values are never
// returned by the API) with their usage audit log.
export function SecretsPage() {
  const navigate = useNavigate();
  const { secrets: initial, sessions } = useLoaderData<{
    secrets: SecretMeta[];
    sessions: Session[];
  }>();
  const [secrets, setSecrets] = useState<SecretMeta[]>(initial);
  const [error, setError] = useState<string | null>(null);
  const sessionNames = new Map(sessions.map((s) => [s.id, s.name]));

  const reload = useCallback(() => {
    api.listSecrets().then(setSecrets).catch((e) => setError(String(e.message ?? e)));
  }, []);

  const handleDelete = async (name: string) => {
    if (!window.confirm(`シークレット「${name}」を削除しますか？保存されている値も削除されます。`)) return;
    try {
      await api.deleteSecret(name);
      setError(null);
      reload();
    } catch (e) {
      setError(`削除に失敗しました: ${(e as Error).message}`);
    }
  };

  return (
    <div className="min-h-screen bg-gray-50 text-gray-900">
      <header className="bg-white border-b border-gray-200 px-4 sm:px-6 py-3 flex items-center gap-4">
        <button onClick={() => navigate("/")} className="text-gray-500 hover:text-gray-700 text-sm">
          ← Back
        </button>
        <h1 className="text-lg font-bold">Secrets</h1>
        <div className="ml-auto flex items-center gap-2" data-testid="secrets-header-actions">
          <Link
            to="/secrets/new"
            className="inline-flex items-center gap-1 px-3 py-1.5 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-700"
          >
            <Plus size={16} />
            追加
          </Link>
        </div>
      </header>
      <div className="max-w-2xl mx-auto p-3 sm:p-6 space-y-2" data-testid="secrets-page">
        <p className="text-xs text-gray-500 px-1">
          値は表示されません。エージェントは <code className="font-mono">agmux secret exec</code> で名前を指定して利用します。
        </p>
        {error && (
          <div className="text-sm text-red-700 bg-red-50 border border-red-200 rounded p-2">{error}</div>
        )}
        {secrets.length === 0 ? (
          <div className="text-center text-sm text-gray-400 py-12">シークレットはまだありません</div>
        ) : (
          secrets.map((s) => (
            <SecretCard
              key={s.name}
              secret={s}
              sessionNames={sessionNames}
              onDelete={() => handleDelete(s.name)}
            />
          ))
        )}
      </div>
    </div>
  );
}

function formatTime(iso?: string | null): string {
  if (!iso) return "-";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "-" : d.toLocaleString();
}

function SecretCard({
  secret,
  sessionNames,
  onDelete,
}: {
  secret: SecretMeta;
  sessionNames: Map<string, string>;
  onDelete: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [usages, setUsages] = useState<SecretUsage[] | null>(null);
  const [usageError, setUsageError] = useState<string | null>(null);

  const toggle = () => {
    const next = !open;
    setOpen(next);
    if (next) {
      setUsageError(null);
      api
        .listSecretUsages(secret.name, 50)
        .then(setUsages)
        .catch((e) => setUsageError((e as Error).message));
    }
  };

  return (
    <div className="bg-white rounded-lg border border-gray-200 shadow-sm min-w-0" data-testid={`secret-${secret.name}`}>
      <div className="p-3 min-w-0">
        <div className="flex items-start gap-2 min-w-0">
          <KeyRound size={14} className="shrink-0 mt-0.5 text-gray-400" />
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2 min-w-0">
              <span className="font-mono text-sm font-semibold truncate">{secret.name}</span>
              <Chip color={secret.backend === "keychain" ? "blue" : secret.backend === "1password" ? "purple" : "gray"}>
                {secret.backend}
              </Chip>
            </div>
            {secret.description && (
              <div className="mt-0.5 text-xs text-gray-600 break-words">{secret.description}</div>
            )}
          </div>
          <button
            onClick={onDelete}
            className="shrink-0 p-1.5 -m-1 text-gray-400 hover:text-red-600 rounded hover:bg-red-50"
            title="削除"
            aria-label={`${secret.name} を削除`}
          >
            <Trash2 size={16} />
          </button>
        </div>
        <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs min-w-0">
          <dt className="text-gray-400">プロジェクト</dt>
          <dd className="min-w-0">
            {!secret.allowedProjects?.length ? (
              <span className="text-gray-500">すべて</span>
            ) : (
              secret.allowedProjects.map((p) => (
                <div key={p} className="font-mono text-gray-700 truncate" title={p}>
                  {p}
                </div>
              ))
            )}
          </dd>
          {secret.ref && (
            <>
              <dt className="text-gray-400">参照</dt>
              <dd className="font-mono text-gray-700 break-all" data-testid="secret-ref">
                {secret.ref}
              </dd>
            </>
          )}
          <dt className="text-gray-400">最終利用</dt>
          <dd className="text-gray-700">{formatTime(secret.lastUsedAt)}</dd>
        </dl>
      </div>
      <button
        onClick={toggle}
        className="w-full flex items-center gap-1 px-3 py-2 border-t border-gray-100 text-xs text-gray-600 hover:bg-gray-50"
        aria-expanded={open}
      >
        {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        利用履歴
      </button>
      {open && (
        <div className="border-t border-gray-100 px-3 py-2">
          {usageError ? (
            <div className="text-xs text-red-600">{usageError}</div>
          ) : usages === null ? (
            <div className="text-xs text-gray-400">読み込み中…</div>
          ) : usages.length === 0 ? (
            <div className="text-xs text-gray-400">利用履歴はありません</div>
          ) : (
            <ul className="divide-y divide-gray-100">
              {usages.map((u) => (
                <UsageRow key={u.id} usage={u} sessionNames={sessionNames} />
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}

function UsageRow({ usage: u, sessionNames }: { usage: SecretUsage; sessionNames: Map<string, string> }) {
  const sessionName = u.sessionId ? sessionNames.get(u.sessionId) : undefined;
  const command = u.command
    ? `${u.command}${u.argCount > 0 ? ` (+${u.argCount} args)` : ""}`
    : "-";
  return (
    <li className="py-2 min-w-0 text-xs" data-testid="secret-usage">
      <div className="flex items-center gap-2 min-w-0">
        <span className="text-gray-500 shrink-0">{formatTime(u.usedAt)}</span>
        <span className="ml-auto shrink-0">
          {u.status === "denied" ? (
            <Chip color="orange">denied</Chip>
          ) : u.exitCode === undefined ? (
            <Chip color="gray">exit ?</Chip>
          ) : (
            <Chip color={u.exitCode === 0 ? "green" : "red"}>exit {u.exitCode}</Chip>
          )}
        </span>
      </div>
      <div className="mt-0.5 font-mono text-gray-800 truncate" title={command}>
        {command}
      </div>
      <div className="mt-0.5 flex items-center gap-2 min-w-0 text-gray-500">
        {u.sessionId ? (
          sessionName !== undefined ? (
            <Link to={`/sessions/${u.sessionId}`} className="text-blue-700 hover:underline truncate shrink-0 max-w-[45%]">
              {sessionName || u.sessionId}
            </Link>
          ) : (
            <span className="truncate shrink-0 max-w-[45%]" title="セッションは存在しません">
              {u.sessionId.slice(0, 8)}
            </span>
          )
        ) : (
          <span className="shrink-0">セッション外</span>
        )}
        {u.projectPath && (
          <span className="font-mono truncate min-w-0" title={u.projectPath}>
            {u.projectPath}
          </span>
        )}
      </div>
    </li>
  );
}
