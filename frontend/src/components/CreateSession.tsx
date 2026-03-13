import { useState, useEffect } from "react";
import { api, type CodexModel } from "../api/client";

interface Props {
  onClose: () => void;
  onCreate: (data: {
    name: string;
    projectPath: string;
    prompt?: string;
    outputMode?: "terminal" | "stream";
    provider?: string;
    model?: string;
    readOnly?: boolean;
  }) => void;
}

interface ModelOption {
  id: string;
  name: string;
  default?: boolean;
}

export function CreateSession({ onClose, onCreate }: Props) {
  const [name, setName] = useState("");
  const [projectPath, setProjectPath] = useState("");
  const [prompt, setPrompt] = useState("");
  const [outputMode, setOutputMode] = useState<"terminal" | "stream">("terminal");
  const [provider, setProvider] = useState("claude");
  const [model, setModel] = useState("");
  const [readOnly, setReadOnly] = useState(false);
  const [codexModels, setCodexModels] = useState<CodexModel[]>([]);
  const [claudeModels, setClaudeModels] = useState<ModelOption[]>([]);
  const [loadingModels, setLoadingModels] = useState(false);

  useEffect(() => {
    if (provider === "codex") {
      setLoadingModels(true);
      api.getCodexModels()
        .then((models) => {
          setCodexModels(models);
          const defaultModel = models.find((m) => m.isDefault);
          if (defaultModel && !model) {
            setModel(defaultModel.id);
          }
        })
        .catch(() => setCodexModels([]))
        .finally(() => setLoadingModels(false));
    } else if (provider === "claude") {
      api.getClaudeModels().then((models) => {
        setClaudeModels(models);
        const defaultModel = models.find((m) => m.default);
        if (defaultModel && !model) {
          setModel(defaultModel.id);
        }
      }).catch(() => {
        // Fallback if API is not available
      });
    } else {
      setModel("");
    }
  }, [provider]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    onCreate({
      name,
      projectPath,
      prompt: prompt || undefined,
      outputMode,
      provider,
      model: (provider === "codex" || provider === "claude") && model ? model : undefined,
      readOnly: readOnly || undefined,
    });
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-white rounded-lg p-6 w-full max-w-md">
        <h2 className="text-xl font-semibold mb-4">New Session</h2>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Session Name
            </label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              required
              className="w-full border border-gray-300 rounded px-3 py-2 text-sm"
              placeholder="my-project"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Project Path
            </label>
            <input
              type="text"
              value={projectPath}
              onChange={(e) => setProjectPath(e.target.value)}
              required
              className="w-full border border-gray-300 rounded px-3 py-2 text-sm"
              placeholder="/path/to/project"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Initial Prompt (optional)
            </label>
            <textarea
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              rows={3}
              className="w-full border border-gray-300 rounded px-3 py-2 text-sm"
              placeholder="Fix the bug in..."
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Output Mode
            </label>
            <div className="flex gap-4">
              <label className="flex items-center gap-1 text-sm">
                <input
                  type="radio"
                  name="outputMode"
                  value="terminal"
                  checked={outputMode === "terminal"}
                  onChange={() => setOutputMode("terminal")}
                />
                Terminal
              </label>
              <label className="flex items-center gap-1 text-sm">
                <input
                  type="radio"
                  name="outputMode"
                  value="stream"
                  checked={outputMode === "stream"}
                  onChange={() => setOutputMode("stream")}
                />
                Stream
              </label>
            </div>
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Provider
            </label>
            <select
              value={provider}
              onChange={(e) => setProvider(e.target.value)}
              className="w-full border border-gray-300 rounded px-3 py-2 text-sm"
            >
              <option value="claude">Claude</option>
              <option value="codex">Codex</option>
            </select>
          </div>
          {provider === "codex" && (
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Model
              </label>
              {loadingModels ? (
                <p className="text-sm text-gray-500">Loading models...</p>
              ) : (
                <select
                  value={model}
                  onChange={(e) => setModel(e.target.value)}
                  className="w-full border border-gray-300 rounded px-3 py-2 text-sm"
                >
                  <option value="">Default</option>
                  {codexModels.map((m) => (
                    <option key={m.id} value={m.id}>
                      {m.name || m.id}
                      {m.isDefault ? " (default)" : ""}
                    </option>
                  ))}
                </select>
              )}
            </div>
          )}
          {provider === "claude" && claudeModels.length > 0 && (
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Model
              </label>
              <select
                value={model}
                onChange={(e) => setModel(e.target.value)}
                className="w-full border border-gray-300 rounded px-3 py-2 text-sm"
              >
                {claudeModels.map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.name}
                  </option>
                ))}
              </select>
            </div>
          )}
          <div>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={readOnly}
                onChange={(e) => setReadOnly(e.target.checked)}
              />
              <span className="font-medium text-gray-700">Read Only</span>
              <span className="text-gray-400 text-xs">(blocks file modifications)</span>
            </label>
          </div>
          <div className="flex justify-end gap-2">
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 text-sm text-gray-600 hover:text-gray-800"
            >
              Cancel
            </button>
            <button
              type="submit"
              className="px-4 py-2 text-sm bg-blue-600 text-white rounded hover:bg-blue-700"
            >
              Create
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
