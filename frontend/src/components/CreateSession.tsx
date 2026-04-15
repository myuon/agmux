import { useState, useEffect } from "react";
import { api, type CodexModel, type RecentProject, type RoleTemplate } from "../api/client";

interface Props {
  onClose: () => void;
  onCreate: (data: {
    name: string;
    projectPath: string;
    prompt?: string;
    provider?: string;
    model?: string;
    autoApprove?: boolean;
    systemPrompt?: string;
    roleTemplate?: string;
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
  const [systemPrompt, setSystemPrompt] = useState("");
  const [provider, setProvider] = useState("claude");
  const [model, setModel] = useState("");
  const [codexModels, setCodexModels] = useState<CodexModel[]>([]);
  const [claudeModels, setClaudeModels] = useState<ModelOption[]>([]);
  const [autoApprove, setAutoApprove] = useState(true);
  const [loadingModels, setLoadingModels] = useState(false);
  const [recentProjects, setRecentProjects] = useState<RecentProject[]>([]);
  const [templates, setTemplates] = useState<RoleTemplate[]>([]);
  const [selectedTemplate, setSelectedTemplate] = useState("");

  useEffect(() => {
    api.getRecentProjects()
      .then(setRecentProjects)
      .catch(() => setRecentProjects([]));
    api.getConfig()
      .then((cfg) => {
        setTemplates(cfg.templates || []);
      })
      .catch(() => setTemplates([]));
  }, []);

  const handleTemplateChange = (templateName: string) => {
    setSelectedTemplate(templateName);
    if (!templateName) return;
    const tmpl = templates.find((t) => t.name === templateName);
    if (tmpl) {
      if (tmpl.provider) setProvider(tmpl.provider);
      if (tmpl.model) setModel(tmpl.model);
      if (tmpl.systemPrompt) setSystemPrompt(tmpl.systemPrompt);
    }
  };

  useEffect(() => {
    if (provider === "codex") {
      setLoadingModels(true);
      api.getCodexModels()
        .then((models) => {
          setCodexModels(models);
          setModel("");
        })
        .catch(() => setCodexModels([]))
        .finally(() => setLoadingModels(false));
    } else if (provider === "claude") {
      api.getClaudeModels().then((models) => {
        setClaudeModels(models);
        setModel("");
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
      provider,
      model: (provider === "codex" || provider === "claude") && model ? model : undefined,
      autoApprove: provider === "codex" && autoApprove ? true : undefined,
      systemPrompt: systemPrompt || undefined,
      roleTemplate: selectedTemplate || undefined,
    });
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-white rounded-lg p-6 w-full max-w-md">
        <h2 className="text-xl font-semibold mb-4">New Session</h2>
        <form onSubmit={handleSubmit} className="space-y-4">
          {templates.length > 0 && (
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Template
              </label>
              <select
                value={selectedTemplate}
                onChange={(e) => handleTemplateChange(e.target.value)}
                className="w-full border border-gray-300 rounded px-3 py-2 text-sm"
              >
                <option value="">None</option>
                {templates.map((t) => (
                  <option key={t.name} value={t.name}>
                    {t.name}
                  </option>
                ))}
              </select>
            </div>
          )}
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
            {recentProjects.length > 0 && (
              <div className="flex flex-wrap gap-1.5 mt-1.5">
                {recentProjects.map((p) => {
                  const dirName = p.projectPath.split("/").pop() || p.projectPath;
                  return (
                    <button
                      key={p.projectPath}
                      type="button"
                      onClick={() => {
                        setProjectPath(p.projectPath);
                        if (!name) {
                          setName(dirName);
                        }
                      }}
                      className="inline-flex items-center px-2 py-0.5 rounded text-xs bg-gray-100 text-gray-700 hover:bg-blue-100 hover:text-blue-700 transition-colors"
                      title={p.projectPath}
                    >
                      {dirName}
                    </button>
                  );
                })}
              </div>
            )}
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
              System Prompt (optional)
            </label>
            <textarea
              value={systemPrompt}
              onChange={(e) => setSystemPrompt(e.target.value)}
              rows={3}
              className="w-full border border-gray-300 rounded px-3 py-2 text-sm"
              placeholder="You are a specialist in..."
            />
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
          {provider === "codex" && (
            <div>
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={autoApprove}
                  onChange={(e) => setAutoApprove(e.target.checked)}
                />
                Full-auto mode (bypass permission prompts)
              </label>
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
                <option value="">Default</option>
                {claudeModels.map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.name}
                  </option>
                ))}
              </select>
            </div>
          )}
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
