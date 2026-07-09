import { useState, useEffect, useCallback } from "react";
import { useNavigate } from "react-router-dom";
import { ArrowLeft, FolderOpen, SendHorizonal, X } from "lucide-react";
import { api, type CodexModel, type RoleTemplate } from "../api/client";
import { IconButton } from "../components/ui/IconButton";
import { IconText } from "../components/ui/IconText";
import { useDesktopPane } from "../App";

interface ModelOption {
  id: string;
  name: string;
  default?: boolean;
}

export function NewSessionPage() {
  const navigate = useNavigate();
  const isDesktopPane = useDesktopPane();

  const [projectPath, setProjectPath] = useState("");
  const [prompt, setPrompt] = useState("");
  const [systemPrompt, setSystemPrompt] = useState("");
  const [provider, setProvider] = useState("claude");
  const [model, setModel] = useState("");
  const [autoApprove, setAutoApprove] = useState(true);
  const [codexModels, setCodexModels] = useState<CodexModel[]>([]);
  const [claudeModels, setClaudeModels] = useState<ModelOption[]>([]);
  const [loadingModels, setLoadingModels] = useState(false);
  const [recentProjects, setRecentProjects] = useState<{ projectPath: string }[]>([]);
  const [templates, setTemplates] = useState<RoleTemplate[]>([]);
  const [selectedTemplate, setSelectedTemplate] = useState("");
  const [isSending, setIsSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [ephemeral, setEphemeral] = useState(false);
  const [ephemeralTimeout, setEphemeralTimeout] = useState("");

  useEffect(() => {
    api.getRecentProjects()
      .then(setRecentProjects)
      .catch(() => setRecentProjects([]));
    api.getConfig()
      .then((cfg) => {
        setTemplates(cfg.templates || []);
        if (cfg.session.defaultRole) {
          setSelectedTemplate(cfg.session.defaultRole);
          const tmpl = (cfg.templates || []).find((t) => t.name === cfg.session.defaultRole);
          if (tmpl) {
            if (tmpl.provider) setProvider(tmpl.provider);
            if (tmpl.model) setModel(tmpl.model);
            if (tmpl.systemPrompt) setSystemPrompt(tmpl.systemPrompt);
          }
        }
        if (cfg.session.defaultModel) {
          setModel(cfg.session.defaultModel);
        }
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
      }).catch(() => {});
    } else {
      setModel("");
    }
  }, [provider]);

  const textareaRef = useCallback((el: HTMLTextAreaElement | null) => {
    if (el) {
      el.style.height = "36px";
      el.style.height = el.scrollHeight + "px";
    }
  }, [prompt]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (isSending) return;

    setIsSending(true);
    setError(null);
    try {
      const timeoutSecs = ephemeralTimeout ? Math.max(1, Math.floor(parseInt(ephemeralTimeout, 10))) : undefined;
      const created = await api.createSession({
        name: projectPath ? (projectPath.split("/").pop() || projectPath) : "New Session",
        projectPath: projectPath || "",
        prompt: prompt.trim() || undefined,
        provider,
        model: (provider === "codex" || provider === "claude") && model ? model : undefined,
        autoApprove: provider === "codex" && autoApprove ? true : undefined,
        systemPrompt: systemPrompt || undefined,
        roleTemplate: selectedTemplate || undefined,
        ephemeral: ephemeral || undefined,
        ephemeralTimeoutSeconds: ephemeral && timeoutSecs && timeoutSecs > 0 ? timeoutSecs : undefined,
      });
      navigate(`/sessions/${created.id}`);
      setIsSending(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "セッション作成に失敗しました");
      setIsSending(false);
    }
  };

  return (
    <div className={`${isDesktopPane ? "h-full pt-2" : "h-dvh pt-4 sm:pt-8"} flex flex-col px-4 sm:px-8 ${isDesktopPane ? "" : "max-w-4xl mx-auto"}`}>
      {/* Header */}
      <div className="flex flex-wrap items-center gap-2 sm:gap-3 mb-3 shrink-0">
        {!isDesktopPane && (
          <IconButton onClick={() => navigate("/", { viewTransition: true } as never)} title="Back">
            <ArrowLeft className="w-4 h-4" />
          </IconButton>
        )}
        <h2 className="text-xl sm:text-2xl font-bold flex-1">新規セッション</h2>
        {isDesktopPane && (
          <IconButton onClick={() => navigate(-1)} title="閉じる">
            <X className="w-4 h-4" />
          </IconButton>
        )}
      </div>

      {/* Path row */}
      <div className="flex items-center gap-1.5 mb-2 shrink-0 text-xs sm:text-sm text-gray-500 min-w-0">
        <IconText icon={<FolderOpen className="w-3.5 h-3.5" />} className="text-xs sm:text-sm min-w-0 flex-1">
          <input
            type="text"
            value={projectPath}
            onChange={(e) => setProjectPath(e.target.value)}
            placeholder="プロジェクトパス（省略可）"
            className="bg-transparent border-none outline-none text-gray-700 placeholder-gray-400 w-full min-w-0 text-xs sm:text-sm"
          />
        </IconText>
      </div>

      {/* Recent projects */}
      {recentProjects.length > 0 && (
        <div className="flex flex-wrap gap-1.5 mb-3 shrink-0">
          {recentProjects.map((p) => {
            const dirName = p.projectPath.split("/").pop() || p.projectPath;
            return (
              <button
                key={p.projectPath}
                type="button"
                onClick={() => setProjectPath(p.projectPath)}
                className="inline-flex items-center px-2 py-0.5 rounded text-xs bg-gray-100 text-gray-700 hover:bg-blue-100 hover:text-blue-700 transition-colors"
                title={p.projectPath}
              >
                {dirName}
              </button>
            );
          })}
        </div>
      )}

      {/* Provider / Model / Template row */}
      <div className="flex flex-wrap items-center gap-2 mb-3 shrink-0 text-xs">
        <select
          value={provider}
          onChange={(e) => setProvider(e.target.value)}
          className="border border-gray-200 rounded px-2 py-1 text-xs text-gray-700 bg-white focus:outline-none focus:ring-1 focus:ring-blue-400"
        >
          <option value="claude">Claude</option>
          <option value="codex">Codex</option>
          <option value="cursor">Cursor</option>
        </select>

        {(provider === "codex" || (provider === "claude" && claudeModels.length > 0)) && (
          loadingModels ? (
            <span className="text-gray-400 text-xs">Loading models...</span>
          ) : (
            <select
              value={model}
              onChange={(e) => setModel(e.target.value)}
              className="border border-gray-200 rounded px-2 py-1 text-xs text-gray-700 bg-white focus:outline-none focus:ring-1 focus:ring-blue-400"
            >
              <option value="">Default model</option>
              {provider === "codex"
                ? codexModels.map((m) => (
                    <option key={m.id} value={m.id}>
                      {m.name || m.id}{m.isDefault ? " (default)" : ""}
                    </option>
                  ))
                : claudeModels.map((m) => (
                    <option key={m.id} value={m.id}>
                      {m.name}
                    </option>
                  ))
              }
            </select>
          )
        )}

        {templates.length > 0 && (
          <select
            value={selectedTemplate}
            onChange={(e) => handleTemplateChange(e.target.value)}
            className="border border-gray-200 rounded px-2 py-1 text-xs text-gray-700 bg-white focus:outline-none focus:ring-1 focus:ring-blue-400"
          >
            <option value="">テンプレートなし</option>
            {templates.map((t) => (
              <option key={t.name} value={t.name}>
                {t.name}
              </option>
            ))}
          </select>
        )}

        {provider === "codex" && (
          <label className="flex items-center gap-1.5 text-xs text-gray-600 cursor-pointer">
            <input
              type="checkbox"
              checked={autoApprove}
              onChange={(e) => setAutoApprove(e.target.checked)}
              className="rounded border-gray-300"
            />
            Full-auto
          </label>
        )}
      </div>

      {/* System prompt */}
      <div className="flex-1 min-h-0 overflow-y-auto">
        <div className="mb-3">
          <label className="block text-xs text-gray-400 mb-1">System Prompt（省略可）</label>
          <textarea
            value={systemPrompt}
            onChange={(e) => setSystemPrompt(e.target.value)}
            rows={3}
            placeholder="You are a specialist in..."
            className="w-full border border-gray-200 rounded px-3 py-2 text-sm resize-none focus:outline-none focus:ring-1 focus:ring-blue-400 text-gray-700 placeholder-gray-400"
          />
        </div>

        {/* Ephemeral settings */}
        <div className="mb-3">
          <label className="flex items-center gap-1.5 text-xs text-gray-600 cursor-pointer">
            <input
              type="checkbox"
              checked={ephemeral}
              onChange={(e) => setEphemeral(e.target.checked)}
              className="rounded border-gray-300"
            />
            Ephemeral（完了後に自動アーカイブ）
          </label>
        </div>
        {ephemeral && (
          <div className="mb-3">
            <label className="block text-xs text-gray-400 mb-1">タイムアウト（秒、省略可）</label>
            <input
              type="number"
              value={ephemeralTimeout}
              onChange={(e) => setEphemeralTimeout(e.target.value)}
              min="1"
              step="1"
              placeholder="例: 3600"
              className="w-full border border-gray-200 rounded px-3 py-2 text-sm focus:outline-none focus:ring-1 focus:ring-blue-400 text-gray-700 placeholder-gray-400"
            />
          </div>
        )}
      </div>

      {/* Error */}
      {error && (
        <p className="text-red-600 text-xs mb-2 shrink-0" role="alert">{error}</p>
      )}

      {/* Bottom send form (mirrors SessionPage style) */}
      <div className="shrink-0 sticky bottom-0 bg-white -mx-4 sm:-mx-8">
        <form onSubmit={handleSubmit} className="pt-2 pb-4 px-4 sm:px-8">
          <div className="flex gap-2 items-center">
            <div className="relative flex-1">
              <textarea
                ref={textareaRef}
                value={prompt}
                rows={1}
                onChange={(e) => setPrompt(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && e.metaKey) {
                    e.preventDefault();
                    e.currentTarget.form?.requestSubmit();
                  }
                }}
                disabled={isSending}
                placeholder="最初のプロンプトを入力（省略可）..."
                className="block w-full border border-gray-300 rounded px-3 py-2 text-sm resize-none h-9 overflow-auto disabled:bg-gray-50 disabled:text-gray-500 disabled:cursor-not-allowed"
                autoFocus
              />
            </div>
            <IconButton
              shape="circle"
              variant="primary"
              type="submit"
              disabled={isSending}
              title={isSending ? "作成中..." : "セッションを作成"}
              className={isSending ? "opacity-60 cursor-not-allowed" : ""}
            >
              <SendHorizonal className="w-4 h-4" />
            </IconButton>
          </div>
        </form>
      </div>
    </div>
  );
}
