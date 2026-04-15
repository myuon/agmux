import { useState, useEffect } from "react";
import { useNavigate, useLoaderData } from "react-router-dom";
import { api } from "../api/client";
import type { AppConfig, RoleTemplate, PromptTemplate } from "../api/client";

type ConfigUpdater = (updater: (prev: AppConfig) => AppConfig) => void;
import { Section, Field } from "../components/ui/Section";
import { ToggleButton } from "../components/ui/ToggleButton";
import { AlertBanner } from "../components/ui/AlertBanner";
import { PermissionStatus } from "../components/ui/PermissionStatus";
import { SecondaryButton } from "../components/ui/SecondaryButton";

export function ConfigPage() {
  const navigate = useNavigate();
  const { config: initialConfig } = useLoaderData<{ config: AppConfig }>();
  const [config, setConfig] = useState<AppConfig>(initialConfig);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState<{ type: "success" | "error"; text: string } | null>(null);

  const handleSave = async () => {
    if (!config) return;
    setSaving(true);
    setMessage(null);
    try {
      await api.updateConfig(config);
      setMessage({ type: "success", text: "設定を保存しました" });
    } catch (err: unknown) {
      setMessage({ type: "error", text: err instanceof Error ? err.message : "保存に失敗しました" });
    } finally {
      setSaving(false);
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
        <h1 className="text-lg font-bold">Settings</h1>
      </header>

      <div className="max-w-2xl mx-auto p-6 space-y-6">
        <AlertBanner variant="warning">設定の変更は次回再起動時に反映されます</AlertBanner>

        {message && (
          <AlertBanner variant={message.type === "success" ? "success" : "error"}>
            {message.text}
          </AlertBanner>
        )}

        <Section title="Server">
          <Field label="Port">
            <input
              type="number"
              value={config.server.port}
              onChange={(e) =>
                setConfig({ ...config, server: { ...config.server, port: Number(e.target.value) } })
              }
              className="bg-white border border-gray-300 rounded px-3 py-1.5 text-sm w-32 focus:outline-none focus:border-blue-500"
            />
          </Field>
        </Section>

        <Section title="Daemon">
          <Field label="Interval">
            <input
              type="text"
              value={config.daemon.interval}
              onChange={(e) =>
                setConfig({ ...config, daemon: { ...config.daemon, interval: e.target.value } })
              }
              placeholder="30s"
              className="bg-white border border-gray-300 rounded px-3 py-1.5 text-sm w-32 focus:outline-none focus:border-blue-500"
            />
          </Field>
        </Section>

        <Section title="Session">
          <Field label="Claude Command">
            <input
              type="text"
              value={config.session.claudeCommand}
              onChange={(e) =>
                setConfig({ ...config, session: { ...config.session, claudeCommand: e.target.value } })
              }
              className="bg-white border border-gray-300 rounded px-3 py-1.5 text-sm w-full focus:outline-none focus:border-blue-500"
            />
          </Field>
        </Section>

        <NotificationStatus />

        {config.prompts && (
          <Section title="Prompts (read-only)">
            {config.prompts.systemPrompt && (
              <div>
                <label className="text-sm text-gray-600 block mb-1">System Prompt</label>
                <pre className="bg-gray-50 border border-gray-200 rounded px-3 py-2 text-xs text-gray-700 whitespace-pre-wrap">{config.prompts.systemPrompt}</pre>
              </div>
            )}
          </Section>
        )}

        <Section title="Developer">
          <Field label="開発者モード">
            <ToggleButton
              enabled={config.devMode}
              onClick={() => setConfig({ ...config, devMode: !config.devMode })}
            >
              {config.devMode ? "ON" : "OFF"}
            </ToggleButton>
          </Field>
          <p className="text-xs text-gray-500">
            有効にするとプレビュー・シナリオテストなどの開発者向け機能が表示されます
          </p>
        </Section>

        <TemplateManager templates={config.templates || []} onUpdate={(updater) => setConfig(updater)} />

        <PromptTemplateManager promptTemplates={config.promptTemplates || []} onUpdate={(updater) => setConfig(updater)} />

        {config.configPath && (
          <div className="text-xs text-gray-400">
            Config: {config.configPath}
          </div>
        )}

        <VersionInfo />

        <div className="pt-4">
          <button
            onClick={handleSave}
            disabled={saving}
            className="bg-blue-600 hover:bg-blue-700 disabled:opacity-50 text-white px-6 py-2 rounded text-sm font-medium transition-colors"
          >
            {saving ? "Saving..." : "Save"}
          </button>
        </div>
      </div>
    </div>
  );
}

function VersionInfo() {
  const [info, setInfo] = useState<{ version: string; commit: string; buildDate: string } | null>(null);

  useEffect(() => {
    api.getAgmuxVersion().then(setInfo).catch(() => {});
  }, []);

  if (!info) return null;

  return (
    <div className="text-xs text-gray-400 space-y-0.5">
      <div>Version: {info.version} ({info.commit})</div>
      <div>Build date: {info.buildDate}</div>
    </div>
  );
}

const NOTIFY_STATUSES = [
  { key: "working", label: "Working" },
  { key: "idle", label: "Idle" },
  { key: "waiting_input", label: "Waiting Input" },
  { key: "paused", label: "Paused" },
  { key: "exited", label: "Exited" },
] as const;

const DEFAULT_NOTIFY_STATUSES: Record<string, boolean> = {
  working: false,
  idle: true,
  waiting_input: true,
  paused: false,
  exited: false,
};

function GoalCompletionNotifySettings() {
  const [enabled, setEnabled] = useState(
    () => localStorage.getItem("agmux-notify-goal-completed") !== "false"
  );
  const [thresholdMin, setThresholdMin] = useState(
    () => Number(localStorage.getItem("agmux-notify-goal-threshold-min") || "10")
  );

  const toggleEnabled = () => {
    const next = !enabled;
    setEnabled(next);
    localStorage.setItem("agmux-notify-goal-completed", next ? "true" : "false");
  };

  const handleThresholdChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const val = Math.max(1, Number(e.target.value) || 1);
    setThresholdMin(val);
    localStorage.setItem("agmux-notify-goal-threshold-min", String(val));
  };

  return (
    <>
      <Field label="タスク完了通知">
        <ToggleButton enabled={enabled} onClick={toggleEnabled}>
          {enabled ? "ON" : "OFF"}
        </ToggleButton>
      </Field>
      {enabled && (
        <Field label="閾値（分）">
          <input
            type="number"
            min={1}
            value={thresholdMin}
            onChange={handleThresholdChange}
            className="w-20 px-2 py-1 text-sm border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </Field>
      )}
    </>
  );
}

function NotificationStatus() {
  const [permission, setPermission] = useState(() =>
    "Notification" in window ? Notification.permission : "unsupported"
  );
  const [notifyEnabled, setNotifyEnabled] = useState(
    () => localStorage.getItem("agmux-notify") === "true"
  );

  const toggleNotify = async () => {
    if (!notifyEnabled) {
      if ("Notification" in window) {
        const perm = Notification.permission === "granted"
          ? "granted"
          : await Notification.requestPermission();
        if (perm === "granted") {
          setNotifyEnabled(true);
          localStorage.setItem("agmux-notify", "true");
        }
      }
    } else {
      setNotifyEnabled(false);
      localStorage.setItem("agmux-notify", "false");
    }
  };
  const [statusFilters, setStatusFilters] = useState<Record<string, boolean>>(() => {
    const saved = localStorage.getItem("agmux-notify-statuses");
    return saved ? JSON.parse(saved) : { ...DEFAULT_NOTIFY_STATUSES };
  });

  const toggleStatus = (key: string) => {
    const current = statusFilters[key] ?? DEFAULT_NOTIFY_STATUSES[key] ?? true;
    const next = { ...statusFilters, [key]: !current };
    setStatusFilters(next);
    localStorage.setItem("agmux-notify-statuses", JSON.stringify(next));
  };

  const requestPermission = async () => {
    if ("Notification" in window) {
      const result = await Notification.requestPermission();
      setPermission(result);
    }
  };

  const sendTest = async () => {
    if (!("Notification" in window) || Notification.permission !== "granted") return;
    if ("serviceWorker" in navigator) {
      const reg = await navigator.serviceWorker.ready.catch(() => null);
      if (reg) {
        reg.showNotification("agmux", { body: "テスト通知です" });
        return;
      }
    }
    new Notification("agmux", { body: "テスト通知です" });
  };

  return (
    <Section title="Notifications">
      <Field label="Browser Permission">
        <PermissionStatus status={permission} />
      </Field>
      <Field label="通知">
        <ToggleButton enabled={notifyEnabled} onClick={toggleNotify}>
          {notifyEnabled ? "ON" : "OFF"}
        </ToggleButton>
      </Field>
      <div>
        <label className="text-sm text-gray-600 block mb-2">通知するステータス</label>
        <div className="flex flex-wrap gap-2">
          {NOTIFY_STATUSES.map(({ key, label }) => {
            const active = statusFilters[key] ?? DEFAULT_NOTIFY_STATUSES[key] ?? true;
            return (
              <ToggleButton
                key={key}
                enabled={active}
                onClick={() => toggleStatus(key)}
              >
                {label}
              </ToggleButton>
            );
          })}
        </div>
      </div>
      <GoalCompletionNotifySettings />
      <div className="flex gap-2 pt-2">
        {permission !== "granted" && (
          <SecondaryButton onClick={requestPermission} color="blue" className="px-3 py-1.5">
            通知を許可
          </SecondaryButton>
        )}
        {permission === "granted" && (
          <SecondaryButton onClick={sendTest} color="gray" className="px-3 py-1.5">
            テスト通知を送信
          </SecondaryButton>
        )}
      </div>
    </Section>
  );
}

function PromptTemplateManager({ promptTemplates, onUpdate }: { promptTemplates: PromptTemplate[]; onUpdate: ConfigUpdater }) {
  const [editingIndex, setEditingIndex] = useState<number | null>(null);
  const [isNew, setIsNew] = useState(false);
  const [form, setForm] = useState({ name: "", prompt: "" });

  const startNew = () => {
    setIsNew(true);
    setEditingIndex(null);
    setForm({ name: "", prompt: "" });
  };

  const startEdit = (index: number) => {
    const t = promptTemplates[index];
    setIsNew(false);
    setEditingIndex(index);
    setForm({ name: t.name, prompt: t.prompt });
  };

  const cancel = () => {
    setEditingIndex(null);
    setIsNew(false);
  };

  const save = () => {
    const newTemplate: PromptTemplate = {
      name: form.name,
      prompt: form.prompt,
    };
    if (isNew) {
      onUpdate((prev) => ({ ...prev, promptTemplates: [...(prev.promptTemplates || []), newTemplate] }));
    } else if (editingIndex !== null) {
      onUpdate((prev) => {
        const updated = [...(prev.promptTemplates || [])];
        updated[editingIndex] = newTemplate;
        return { ...prev, promptTemplates: updated };
      });
    }
    cancel();
  };

  const remove = (index: number) => {
    onUpdate((prev) => ({
      ...prev,
      promptTemplates: (prev.promptTemplates || []).filter((_, i) => i !== index),
    }));
  };

  const showForm = isNew || editingIndex !== null;

  return (
    <Section title="Prompt Templates">
      <p className="text-xs text-gray-500 mb-3">
        セッションにワンタップで送信できるプロンプトのテンプレートを登録します。config.toml の <code className="font-mono bg-gray-100 px-1 rounded">[[prompt_templates]]</code> で定義することもできます。
      </p>
      {promptTemplates.length === 0 && !showForm && (
        <p className="text-sm text-gray-500">No prompt templates yet.</p>
      )}
      {promptTemplates.map((t, index) => (
        <div key={`${t.name}-${index}`} className="flex items-start justify-between border-b border-gray-100 pb-3 last:border-0 last:pb-0">
          <div className="min-w-0 flex-1">
            <div className="font-medium text-sm">{t.name}</div>
            {t.prompt && (
              <pre className="text-xs text-gray-500 mt-1 whitespace-pre-wrap line-clamp-2">{t.prompt}</pre>
            )}
          </div>
          <div className="flex gap-1 ml-2 shrink-0">
            <SecondaryButton onClick={() => startEdit(index)} color="blue" className="px-2 py-1 text-xs">
              Edit
            </SecondaryButton>
            <SecondaryButton onClick={() => remove(index)} color="gray" className="px-2 py-1 text-xs">
              Delete
            </SecondaryButton>
          </div>
        </div>
      ))}
      {showForm && (
        <div className="space-y-3 border border-gray-200 rounded p-3 bg-gray-50">
          <div>
            <label className="text-xs text-gray-600 block mb-1">Name</label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              className="w-full bg-white border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:border-blue-500"
              placeholder="Template name"
            />
          </div>
          <div>
            <label className="text-xs text-gray-600 block mb-1">Prompt</label>
            <textarea
              value={form.prompt}
              onChange={(e) => setForm({ ...form, prompt: e.target.value })}
              rows={4}
              className="w-full bg-white border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:border-blue-500"
              placeholder="プロジェクトを初期化してください..."
            />
          </div>
          <div className="flex gap-2">
            <SecondaryButton onClick={save} color="blue" className="px-3 py-1.5 text-sm">
              {isNew ? "Create" : "Update"}
            </SecondaryButton>
            <SecondaryButton onClick={cancel} color="gray" className="px-3 py-1.5 text-sm">
              Cancel
            </SecondaryButton>
          </div>
        </div>
      )}
      {!showForm && (
        <div className="pt-2">
          <SecondaryButton onClick={startNew} color="blue" className="px-3 py-1.5 text-sm">
            + New Prompt Template
          </SecondaryButton>
        </div>
      )}
    </Section>
  );
}

function TemplateManager({ templates, onUpdate }: { templates: RoleTemplate[]; onUpdate: ConfigUpdater }) {
  const [editingIndex, setEditingIndex] = useState<number | null>(null);
  const [isNew, setIsNew] = useState(false);
  const [form, setForm] = useState({ name: "", systemPrompt: "", provider: "claude", model: "" });

  const startNew = () => {
    setIsNew(true);
    setEditingIndex(null);
    setForm({ name: "", systemPrompt: "", provider: "claude", model: "" });
  };

  const startEdit = (index: number) => {
    const t = templates[index];
    setIsNew(false);
    setEditingIndex(index);
    setForm({ name: t.name, systemPrompt: t.systemPrompt, provider: t.provider, model: t.model || "" });
  };

  const cancel = () => {
    setEditingIndex(null);
    setIsNew(false);
  };

  const save = () => {
    const newTemplate: RoleTemplate = {
      name: form.name,
      provider: form.provider,
      model: form.model || undefined,
      systemPrompt: form.systemPrompt,
    };
    if (isNew) {
      onUpdate((prev) => ({ ...prev, templates: [...(prev.templates || []), newTemplate] }));
    } else if (editingIndex !== null) {
      onUpdate((prev) => {
        const updated = [...(prev.templates || [])];
        updated[editingIndex] = newTemplate;
        return { ...prev, templates: updated };
      });
    }
    cancel();
  };

  const remove = (index: number) => {
    onUpdate((prev) => ({
      ...prev,
      templates: (prev.templates || []).filter((_, i) => i !== index),
    }));
  };

  const showForm = isNew || editingIndex !== null;

  return (
    <Section title="Role Templates">
      {templates.length === 0 && !showForm && (
        <p className="text-sm text-gray-500">No templates yet.</p>
      )}
      {templates.map((t, index) => (
        <div key={`${t.name}-${index}`} className="flex items-start justify-between border-b border-gray-100 pb-3 last:border-0 last:pb-0">
          <div className="min-w-0 flex-1">
            <div className="font-medium text-sm">{t.name}</div>
            <div className="text-xs text-gray-500 mt-0.5">
              {t.provider}{t.model ? ` / ${t.model}` : ""}
            </div>
            {t.systemPrompt && (
              <pre className="text-xs text-gray-500 mt-1 whitespace-pre-wrap line-clamp-2">{t.systemPrompt}</pre>
            )}
          </div>
          <div className="flex gap-1 ml-2 shrink-0">
            <SecondaryButton onClick={() => startEdit(index)} color="blue" className="px-2 py-1 text-xs">
              Edit
            </SecondaryButton>
            <SecondaryButton onClick={() => remove(index)} color="gray" className="px-2 py-1 text-xs">
              Delete
            </SecondaryButton>
          </div>
        </div>
      ))}
      {showForm && (
        <div className="space-y-3 border border-gray-200 rounded p-3 bg-gray-50">
          <div>
            <label className="text-xs text-gray-600 block mb-1">Name</label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              className="w-full bg-white border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:border-blue-500"
              placeholder="Template name"
            />
          </div>
          <div>
            <label className="text-xs text-gray-600 block mb-1">System Prompt</label>
            <textarea
              value={form.systemPrompt}
              onChange={(e) => setForm({ ...form, systemPrompt: e.target.value })}
              rows={3}
              className="w-full bg-white border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:border-blue-500"
              placeholder="You are a specialist in..."
            />
          </div>
          <div>
            <label className="text-xs text-gray-600 block mb-1">Provider</label>
            <select
              value={form.provider}
              onChange={(e) => setForm({ ...form, provider: e.target.value })}
              className="w-full bg-white border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:border-blue-500"
            >
              <option value="claude">Claude</option>
              <option value="codex">Codex</option>
            </select>
          </div>
          <div>
            <label className="text-xs text-gray-600 block mb-1">Model (optional)</label>
            <input
              type="text"
              value={form.model}
              onChange={(e) => setForm({ ...form, model: e.target.value })}
              className="w-full bg-white border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:border-blue-500"
              placeholder="e.g. claude-sonnet-4-5"
            />
          </div>
          <div className="flex gap-2">
            <SecondaryButton onClick={save} color="blue" className="px-3 py-1.5 text-sm">
              {isNew ? "Create" : "Update"}
            </SecondaryButton>
            <SecondaryButton onClick={cancel} color="gray" className="px-3 py-1.5 text-sm">
              Cancel
            </SecondaryButton>
          </div>
        </div>
      )}
      {!showForm && (
        <div className="pt-2">
          <SecondaryButton onClick={startNew} color="blue" className="px-3 py-1.5 text-sm">
            + New Template
          </SecondaryButton>
        </div>
      )}
    </Section>
  );
}

