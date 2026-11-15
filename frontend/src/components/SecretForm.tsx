import { useState } from "react";
import { KeyRound, Loader2 } from "lucide-react";
import {
  buildSecretBody,
  validateSecretForm,
  type SecretMeta,
  type SecretRegisterBody,
  type SecretSourceMode,
  type SecretValueBackend,
} from "../lib/secret";

export interface SecretFormProps {
  /** Initial name. */
  initialName?: string;
  /** Name is fixed (requested by an agent). */
  nameReadOnly?: boolean;
  initialDescription?: string;
  /** Project path the scope defaults to (e.g. the requesting session's project). */
  defaultProject?: string;
  /** Sends the body. The value only travels here. */
  submit: (body: SecretRegisterBody) => Promise<SecretMeta>;
  onSaved: (secret: SecretMeta) => void;
  onCancel?: () => void;
  cancelLabel?: string;
}

const inputClass =
  "w-full border border-gray-300 rounded-lg px-3 py-2.5 text-base focus:outline-none focus:ring-2 focus:ring-blue-300 focus:border-blue-300 disabled:bg-gray-100 disabled:text-gray-600";

/**
 * Form to register a secret: either a value (stored in the server's backend)
 * or a 1Password reference. Reusable for agent requests and manual
 * registration. The value is kept in state only until submit.
 */
export function SecretForm({
  initialName = "",
  nameReadOnly = false,
  initialDescription = "",
  defaultProject = "",
  submit,
  onSaved,
  onCancel,
  cancelLabel = "キャンセル",
}: SecretFormProps) {
  const [name, setName] = useState(initialName);
  const [description, setDescription] = useState(initialDescription);
  const [mode, setMode] = useState<SecretSourceMode>("value");
  const [value, setValue] = useState("");
  const [ref, setRef] = useState("");
  const [backend, setBackend] = useState<SecretValueBackend>("");
  const [scoped, setScoped] = useState(defaultProject !== "");
  const [project, setProject] = useState(defaultProject);
  const [error, setError] = useState<string | null>(null);
  const [sending, setSending] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (sending) return;
    const input = { name, description, mode, value, ref, backend, project: scoped ? project : "" };
    const invalid = validateSecretForm(input);
    if (invalid) {
      setError(invalid);
      return;
    }
    const body = buildSecretBody(input);
    // Drop the value from React state right away; on failure it must be re-entered.
    setValue("");
    setError(null);
    setSending(true);
    try {
      const saved = await submit(body);
      onSaved(saved);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      body.value = undefined;
      setSending(false);
    }
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-4" data-testid="secret-form">
      <div className="space-y-1">
        <label htmlFor="secret-name" className="block text-sm font-medium text-gray-700">名前</label>
        <input
          id="secret-name"
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value.toUpperCase())}
          readOnly={nameReadOnly}
          placeholder="OPENAI_API_KEY"
          autoComplete="off"
          autoCorrect="off"
          autoCapitalize="characters"
          spellCheck={false}
          className={`${inputClass} font-mono ${nameReadOnly ? "bg-gray-100 text-gray-700" : ""}`}
          disabled={sending}
        />
        <p className="text-xs text-gray-500">環境変数名として使われます（大文字英数字と _）</p>
      </div>

      <div className="space-y-1">
        <label htmlFor="secret-description" className="block text-sm font-medium text-gray-700">説明</label>
        <input
          id="secret-description"
          type="text"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="用途のメモ（任意）"
          className={inputClass}
          disabled={sending}
        />
      </div>

      <fieldset className="space-y-2">
        <legend className="block text-sm font-medium text-gray-700 mb-1">登録方法</legend>
        <div className="grid grid-cols-2 gap-2" role="radiogroup">
          {([
            ["value", "値を入力"],
            ["1password", "1Password 参照を指定"],
          ] as const).map(([m, label]) => (
            <button
              key={m}
              type="button"
              role="radio"
              aria-checked={mode === m}
              onClick={() => { setMode(m); setError(null); }}
              disabled={sending}
              className={`px-2 py-2.5 text-sm rounded-lg border transition-colors ${
                mode === m ? "border-blue-500 bg-blue-50 text-blue-800 font-medium" : "border-gray-300 bg-white text-gray-700"
              }`}
            >
              {label}
            </button>
          ))}
        </div>

        {mode === "value" ? (
          <div className="space-y-3 pt-1">
            <div className="space-y-1">
              <label htmlFor="secret-value" className="block text-sm font-medium text-gray-700">値</label>
              <input
                id="secret-value"
                type="password"
                value={value}
                onChange={(e) => setValue(e.target.value)}
                autoComplete="off"
                autoCorrect="off"
                autoCapitalize="off"
                spellCheck={false}
                className={`${inputClass} font-mono`}
                disabled={sending}
              />
              <p className="text-xs text-gray-500">値はエージェントやログには表示されません</p>
            </div>
            <div className="space-y-1">
              <label htmlFor="secret-backend" className="block text-sm font-medium text-gray-700">保存先</label>
              <select
                id="secret-backend"
                value={backend}
                onChange={(e) => setBackend(e.target.value as SecretValueBackend)}
                className={`${inputClass} bg-white`}
                disabled={sending}
              >
                <option value="">既定（設定に従う）</option>
                <option value="keychain">Keychain</option>
                <option value="file">ファイル（~/.agmux/secrets）</option>
              </select>
            </div>
          </div>
        ) : (
          <div className="space-y-1 pt-1">
            <label htmlFor="secret-ref" className="block text-sm font-medium text-gray-700">1Password 参照</label>
            <input
              id="secret-ref"
              type="text"
              value={ref}
              onChange={(e) => setRef(e.target.value)}
              placeholder="op://vault/item/field"
              autoComplete="off"
              autoCorrect="off"
              autoCapitalize="off"
              spellCheck={false}
              className={`${inputClass} font-mono`}
              disabled={sending}
            />
            <p className="text-xs text-gray-500">実行時に 1Password CLI で解決されます。参照だけが保存されます</p>
          </div>
        )}
      </fieldset>

      <div className="space-y-2">
        <label className="flex items-center gap-2 text-sm text-gray-700">
          <input
            type="checkbox"
            checked={scoped}
            onChange={(e) => setScoped(e.target.checked)}
            disabled={sending}
            className="h-4 w-4"
          />
          プロジェクトに限定する
        </label>
        {scoped ? (
          <input
            type="text"
            value={project}
            onChange={(e) => setProject(e.target.value)}
            placeholder="/path/to/project"
            aria-label="限定するプロジェクトのパス"
            autoComplete="off"
            autoCorrect="off"
            autoCapitalize="off"
            spellCheck={false}
            className={`${inputClass} font-mono text-sm`}
            disabled={sending}
          />
        ) : (
          <p className="text-xs text-gray-500">すべてのプロジェクトから利用できます</p>
        )}
      </div>

      {error && (
        <div className="text-sm text-red-700 bg-red-50 border border-red-200 rounded-lg px-3 py-2" role="alert">
          {error}
        </div>
      )}

      <div className="flex gap-2 pt-1">
        {onCancel && (
          <button
            type="button"
            onClick={onCancel}
            disabled={sending}
            className="flex-1 whitespace-nowrap px-3 py-2.5 text-sm border border-gray-300 rounded-lg bg-white text-gray-700 hover:bg-gray-100 disabled:opacity-50"
          >
            {cancelLabel}
          </button>
        )}
        <button
          type="submit"
          disabled={sending}
          className="flex-1 inline-flex items-center justify-center gap-2 px-4 py-2.5 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50"
        >
          {sending ? <Loader2 className="w-4 h-4 animate-spin" /> : <KeyRound className="w-4 h-4" />}
          保存
        </button>
      </div>
    </form>
  );
}
