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
      <div className="min-h-screen bg-gray-950 text-gray-100 flex items-center justify-center">
        <p className="text-gray-400">Loading...</p>
      </div>
    );
  }

  if (!config) {
    return (
      <div className="min-h-screen bg-gray-950 text-gray-100 flex items-center justify-center">
        <p className="text-red-400">Failed to load config</p>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-gray-950 text-gray-100">
      <header className="bg-gray-900 border-b border-gray-700 px-6 py-3 flex items-center gap-4">
        <button
          onClick={() => navigate("/")}
          className="text-gray-400 hover:text-gray-200 text-sm"
        >
          ← Back
        </button>
        <h1 className="text-lg font-bold">Settings</h1>
      </header>

      <div className="max-w-2xl mx-auto p-6 space-y-6">
        <div className="bg-yellow-900/30 border border-yellow-700 rounded-lg px-4 py-3 text-yellow-300 text-sm">
          設定の変更は次回再起動時に反映されます
        </div>

        {message && (
          <div
            className={`rounded-lg px-4 py-3 text-sm ${
              message.type === "success"
                ? "bg-green-900/30 border border-green-700 text-green-300"
                : "bg-red-900/30 border border-red-700 text-red-300"
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
              className="bg-gray-800 border border-gray-600 rounded px-3 py-1.5 text-sm w-32 focus:outline-none focus:border-blue-500"
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
              className="bg-gray-800 border border-gray-600 rounded px-3 py-1.5 text-sm w-32 focus:outline-none focus:border-blue-500"
            />
          </Field>
          <Field label="Auto Approve">
            <button
              onClick={() =>
                setConfig({ ...config, daemon: { ...config.daemon, autoApprove: !config.daemon.autoApprove } })
              }
              className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                config.daemon.autoApprove ? "bg-blue-600" : "bg-gray-600"
              }`}
            >
              <span
                className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                  config.daemon.autoApprove ? "translate-x-6" : "translate-x-1"
                }`}
              />
            </button>
          </Field>
        </Section>

        <Section title="LLM">
          <Field label="Model">
            <input
              type="text"
              value={config.llm.model}
              onChange={(e) =>
                setConfig({ ...config, llm: { ...config.llm, model: e.target.value } })
              }
              className="bg-gray-800 border border-gray-600 rounded px-3 py-1.5 text-sm w-full focus:outline-none focus:border-blue-500"
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
              className="bg-gray-800 border border-gray-600 rounded px-3 py-1.5 text-sm w-full focus:outline-none focus:border-blue-500"
            />
          </Field>
        </Section>

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
    <div className="bg-gray-900 rounded-lg border border-gray-700">
      <h2 className="text-sm font-semibold text-gray-300 px-4 py-3 border-b border-gray-700">
        {title}
      </h2>
      <div className="p-4 space-y-4">{children}</div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between">
      <label className="text-sm text-gray-400">{label}</label>
      {children}
    </div>
  );
}
