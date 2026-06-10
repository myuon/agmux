import { useState } from "react";
import { useNavigate, useLoaderData, Link } from "react-router-dom";
import { api } from "../api/client";
import type { Automation, AutomationRun } from "../api/client";
import { Section, Field } from "../components/ui/Section";
import { ToggleButton } from "../components/ui/ToggleButton";
import { AlertBanner } from "../components/ui/AlertBanner";
import { formatTrigger } from "../models/automation";

const RUN_STATUS_STYLES: Record<AutomationRun["status"], string> = {
  success: "bg-green-50 border-green-300 text-green-700",
  skipped: "bg-yellow-50 border-yellow-300 text-yellow-700",
  error: "bg-red-50 border-red-300 text-red-700",
};

function RunStatusBadge({ status }: { status: AutomationRun["status"] }) {
  return (
    <span
      className={`px-2 py-0.5 text-xs font-medium rounded-full border ${RUN_STATUS_STYLES[status]}`}
    >
      {status}
    </span>
  );
}

export function AutomationDetailPage() {
  const navigate = useNavigate();
  const { automation: initial, runs } = useLoaderData<{
    automation: Automation;
    runs: AutomationRun[];
  }>();
  const [automation, setAutomation] = useState<Automation>(initial);
  const [error, setError] = useState<string | null>(null);

  const handleToggle = async () => {
    setError(null);
    try {
      setAutomation(await api.setAutomationEnabled(automation.id, !automation.enabled));
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "切替に失敗しました");
    }
  };

  return (
    <div className="min-h-screen bg-gray-50 text-gray-900">
      <header className="bg-white border-b border-gray-200 px-6 py-3 flex items-center gap-4">
        <button
          onClick={() => navigate("/automations")}
          className="text-gray-500 hover:text-gray-700 text-sm"
        >
          ← Back
        </button>
        <h1 className="text-lg font-bold">{automation.name}</h1>
      </header>

      <div className="max-w-2xl mx-auto p-6 space-y-6">
        {error && <AlertBanner variant="error">{error}</AlertBanner>}

        <Section title="Automation">
          <Field label="Trigger">
            <span className="text-sm text-gray-800">{formatTrigger(automation)}</span>
          </Field>
          <Field label="Project">
            <span className="text-sm text-gray-800 font-mono break-all">
              {automation.projectPath || "(controller)"}
            </span>
          </Field>
          <Field label="有効">
            <ToggleButton enabled={automation.enabled} onClick={handleToggle}>
              {automation.enabled ? "ON" : "OFF"}
            </ToggleButton>
          </Field>
          <div>
            <label className="text-sm text-gray-600 block mb-1">Prompt</label>
            <pre className="bg-gray-50 border border-gray-200 rounded px-3 py-2 text-xs text-gray-700 whitespace-pre-wrap">
              {automation.prompt}
            </pre>
          </div>
        </Section>

        <Section title="実行履歴">
          {runs.length === 0 ? (
            <div className="text-sm text-gray-400">まだ実行されていません</div>
          ) : (
            <div className="divide-y divide-gray-100">
              {runs.map((run) => (
                <div key={run.id} className="py-2.5 flex items-start gap-3">
                  <RunStatusBadge status={run.status} />
                  <div className="min-w-0 flex-1">
                    <div className="text-sm text-gray-800">
                      {new Date(run.firedAt).toLocaleString()}
                    </div>
                    {run.message && (
                      <div className="text-xs text-gray-500 break-all">{run.message}</div>
                    )}
                  </div>
                  {run.sessionId && (
                    <Link
                      to={`/sessions/${run.sessionId}`}
                      className="text-xs text-blue-600 hover:underline shrink-0"
                    >
                      セッションを開く
                    </Link>
                  )}
                </div>
              ))}
            </div>
          )}
        </Section>
      </div>
    </div>
  );
}
