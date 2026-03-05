import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../api/client";
import type { AppConfig } from "../api/client";

export function ConfigPage() {
  const navigate = useNavigate();
  const [config, setConfig] = useState<AppConfig | null>(null);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState<{ type: "success" | "error"; text: string } | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api
      .getConfig()
      .then((cfg) => {
        setConfig(cfg);
        setLoading(false);
      })
      .catch((err) => {
        setMessage({ type: "error", text: err.message });
        setLoading(false);
      });
  }, []);

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

  if (loading) {
    return (
      <div className="min-h-screen bg-gray-50 flex items-center justify-center">
        <p className="text-gray-400">Loading...</p>
      </div>
    );
  }

  if (!config) {
    return (
      <div className="min-h-screen bg-gray-50 flex items-center justify-center">
        <p className="text-red-500">Failed to load config</p>
      </div>
    );
  }

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
        <div className="bg-yellow-50 border border-yellow-300 rounded-lg px-4 py-3 text-yellow-800 text-sm">
          設定の変更は次回再起動時に反映されます
        </div>

        {message && (
          <div
            className={`rounded-lg px-4 py-3 text-sm ${
              message.type === "success"
                ? "bg-green-50 border border-green-300 text-green-800"
                : "bg-red-50 border border-red-300 text-red-800"
            }`}
          >
            {message.text}
          </div>
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

        {config.prompts?.statusCheck && (
          <Section title="Prompts (read-only)">
            <div>
              <label className="text-sm text-gray-600 block mb-1">Status Check Prompt</label>
              <pre className="bg-gray-50 border border-gray-200 rounded px-3 py-2 text-xs text-gray-700 whitespace-pre-wrap">{config.prompts.statusCheck}</pre>
            </div>
          </Section>
        )}

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

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="bg-white rounded-lg border border-gray-200 shadow-sm">
      <h2 className="text-sm font-semibold text-gray-700 px-4 py-3 border-b border-gray-200">
        {title}
      </h2>
      <div className="p-4 space-y-4">{children}</div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between">
      <label className="text-sm text-gray-600">{label}</label>
      {children}
    </div>
  );
}
