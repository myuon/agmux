import type { ReactNode } from "react";
import { HighlightedCode, HighlightedDiff } from "../ui/HighlightedCode";
import { langFromPath, parseNumberedLines, type CodeLang } from "../../models/codeView";

const labelClass = "text-gray-400 text-[10px] uppercase tracking-wide";

function FieldLabel({ children }: { children: ReactNode }) {
  return <span className={labelClass}>{children}</span>;
}

function GenericField({ name, value }: { name: string; value: unknown }) {
  const str = typeof value === "string" ? value : JSON.stringify(value, null, 2);
  const isMultiline = str.includes("\n");
  return (
    <div>
      <FieldLabel>{name}</FieldLabel>
      {isMultiline ? (
        <pre className="text-gray-700 text-xs mt-0.5 bg-gray-50 border border-gray-200 rounded p-2 overflow-x-auto whitespace-pre-wrap">{str}</pre>
      ) : (
        <div className="text-gray-700 text-xs mt-0.5 font-mono">{str}</div>
      )}
    </div>
  );
}

function CodeField({ name, children }: { name: string; children: ReactNode }) {
  return (
    <div>
      <FieldLabel>{name}</FieldLabel>
      <div className="mt-0.5">{children}</div>
    </div>
  );
}

type EditLike = { old_string?: unknown; new_string?: unknown };

function isEditLike(v: unknown): v is EditLike {
  return !!v && typeof v === "object" && typeof (v as EditLike).old_string === "string" && typeof (v as EditLike).new_string === "string";
}

/**
 * Render a field specially for code-oriented tools (Bash / Write / Edit /
 * MultiEdit / NotebookEdit). Returns undefined to fall back to the generic view.
 */
function renderSpecialField(toolName: string, key: string, value: unknown, input: Record<string, unknown>): ReactNode | undefined {
  const lang = langFromPath(input.file_path ?? input.notebook_path);
  switch (toolName) {
    case "Bash":
      if (key === "command" && typeof value === "string") {
        return <CodeField key={key} name={key}><HighlightedCode code={value} lang="bash" /></CodeField>;
      }
      break;
    case "Write":
      if (key === "content" && typeof value === "string") {
        return <CodeField key={key} name={key}><HighlightedCode code={value} lang={lang} lineNumbers="fromOne" /></CodeField>;
      }
      break;
    case "Edit":
      if (key === "old_string" && isEditLike(input)) {
        return (
          <CodeField key="diff" name="diff">
            <HighlightedDiff oldText={input.old_string as string} newText={input.new_string as string} lang={lang} />
          </CodeField>
        );
      }
      if (key === "new_string" && isEditLike(input)) return null;
      break;
    case "MultiEdit":
      if (key === "edits" && Array.isArray(value) && value.every(isEditLike)) {
        return (
          <CodeField key={key} name={`${key} (${value.length})`}>
            <div className="space-y-1.5">
              {value.map((e, i) => (
                <HighlightedDiff key={i} oldText={e.old_string as string} newText={e.new_string as string} lang={lang} />
              ))}
            </div>
          </CodeField>
        );
      }
      break;
    case "NotebookEdit":
      if (key === "new_source" && typeof value === "string") {
        const cellLang: CodeLang = input.cell_type === "markdown" ? "markdown" : "python";
        return <CodeField key={key} name={key}><HighlightedCode code={value} lang={cellLang} /></CodeField>;
      }
      break;
  }
  return undefined;
}

export function ToolInputView({ input, toolName }: { input: unknown; toolName?: string }) {
  if (input && typeof input === "object" && !Array.isArray(input)) {
    const obj = input as Record<string, unknown>;
    const entries = Object.entries(obj);
    return (
      <div className="space-y-2">
        {entries.map(([key, value]) => {
          const special = toolName ? renderSpecialField(toolName, key, value, obj) : undefined;
          if (special !== undefined) return special;
          return <GenericField key={key} name={key} value={value} />;
        })}
      </div>
    );
  }
  const str = typeof input === "string" ? input : JSON.stringify(input, null, 2);
  return <pre className="text-gray-600 text-xs overflow-x-auto whitespace-pre-wrap">{str}</pre>;
}

const plainOutputClass = "text-gray-600 text-xs overflow-x-auto whitespace-pre-wrap";

/**
 * Tool result body. For Read (and Edit-family results that include a `cat -n`
 * snippet) the numbered block is highlighted using the original line numbers
 * from the result; everything else is plain text.
 */
export function ToolResultView({ toolName, input, result, isError }: {
  toolName: string;
  input: unknown;
  result: string;
  isError?: boolean;
}) {
  const filePath = input && typeof input === "object" ? (input as Record<string, unknown>).file_path : undefined;
  const numbered =
    !isError && (toolName === "Read" || toolName === "Edit" || toolName === "MultiEdit" || toolName === "Write")
      ? parseNumberedLines(result)
      : null;

  if (numbered && numbered.lines.length > 0) {
    const before = numbered.before.trim();
    const after = numbered.after.trim();
    return (
      <div className="space-y-1">
        {before && <pre className={plainOutputClass}>{before}</pre>}
        <HighlightedCode
          code={numbered.lines.map((l) => l.text).join("\n")}
          lang={langFromPath(filePath)}
          lineNumbers={numbered.lines.map((l) => l.num)}
        />
        {after && <pre className={plainOutputClass}>{after.slice(0, 2000)}</pre>}
      </div>
    );
  }
  return <pre className={plainOutputClass}>{result.slice(0, 2000)}</pre>;
}
