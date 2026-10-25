import { useEffect, useMemo, useState, type ReactNode } from "react";
import type { ThemedToken } from "@shikijs/core";
import { diffLines, type CodeLang } from "../../models/codeView";

// Skip highlighting for huge inputs; plain text is rendered instead.
const MAX_HIGHLIGHT_CHARS = 300_000;

/** Lazily tokenize code. Returns null while loading / for plain text / on failure. */
function useTokens(code: string, lang: CodeLang | null): ThemedToken[][] | null {
  const [state, setState] = useState<{ key: string; tokens: ThemedToken[][] | null } | null>(null);
  const key = `${lang}\u0000${code}`;
  const enabled = lang !== null && code.length <= MAX_HIGHLIGHT_CHARS;

  useEffect(() => {
    if (!enabled || !lang) return;
    let cancelled = false;
    import("../../lib/highlighter")
      .then((m) => m.tokenizeLines(code, lang))
      .then((tokens) => { if (!cancelled) setState({ key, tokens }); })
      .catch(() => { if (!cancelled) setState({ key, tokens: null }); });
    return () => { cancelled = true; };
  }, [enabled, lang, code, key]);

  return enabled && state?.key === key ? state.tokens : null;
}

function renderTokens(tokens: ThemedToken[] | undefined, fallback: string): ReactNode {
  // Keep empty lines at full height
  if (fallback === "") return " ";
  if (!tokens) return fallback;
  return tokens.map((t, i) => (
    <span
      key={i}
      style={{
        color: t.color,
        fontStyle: t.fontStyle && t.fontStyle & 1 ? "italic" : undefined,
        fontWeight: t.fontStyle && t.fontStyle & 2 ? 600 : undefined,
      }}
    >
      {t.content}
    </span>
  ));
}

const preClass = "bg-gray-50 border border-gray-200 rounded text-xs font-mono text-gray-800 overflow-x-auto py-1.5";
const gutterClass = "select-none sticky left-0 bg-gray-50 text-right pr-3 pl-2 shrink-0 text-gray-300 tabular-nums";

/**
 * Syntax highlighted code block with an optional line-number gutter.
 * `lineNumbers`: explicit number per line (e.g. original numbers from a `cat -n`
 * result), "fromOne" to number from 1, or omitted for no gutter.
 */
export function HighlightedCode({ code, lang, lineNumbers }: {
  code: string;
  lang: CodeLang | null;
  lineNumbers?: number[] | "fromOne";
}) {
  const tokens = useTokens(code, lang);
  const lines = useMemo(() => code.split("\n"), [code]);
  const nums = lineNumbers === "fromOne" ? lines.map((_, i) => i + 1) : lineNumbers;
  const gutterWidth = nums ? `${String(nums[nums.length - 1] ?? 1).length + 1.5}ch` : undefined;

  return (
    <pre className={preClass}>
      <div className="inline-block min-w-full">
        {lines.map((line, i) => (
          <div key={i} className="flex whitespace-pre">
            {nums && (
              <span className={gutterClass} style={{ width: gutterWidth, boxSizing: "content-box" }}>
                {nums[i] ?? ""}
              </span>
            )}
            <span className={`flex-1 ${nums ? "" : "pl-2"} pr-2`}>{renderTokens(tokens?.[i], line)}</span>
          </div>
        ))}
      </div>
    </pre>
  );
}

/**
 * Line diff between two snippets (Edit's old_string / new_string), each side
 * highlighted separately. No line numbers: snippets come from the middle of a
 * file and their absolute position is unknown.
 */
export function HighlightedDiff({ oldText, newText, lang }: {
  oldText: string;
  newText: string;
  lang: CodeLang | null;
}) {
  const oldTokens = useTokens(oldText, lang);
  const newTokens = useTokens(newText, lang);
  const diff = useMemo(() => diffLines(oldText, newText), [oldText, newText]);

  return (
    <pre className={preClass}>
      <div className="inline-block min-w-full">
        {diff.map((d, i) => {
          const tokens = d.type === "add" ? newTokens?.[d.newIdx!] : oldTokens?.[d.oldIdx!];
          const rowClass = d.type === "add" ? "bg-green-50" : d.type === "del" ? "bg-red-50" : "";
          const markClass = d.type === "add" ? "text-green-600 bg-green-100" : d.type === "del" ? "text-red-500 bg-red-100" : "text-gray-300 bg-gray-50";
          return (
            <div key={i} className={`flex whitespace-pre ${rowClass}`}>
              <span className={`select-none sticky left-0 w-5 text-center shrink-0 ${markClass}`}>
                {d.type === "add" ? "+" : d.type === "del" ? "-" : " "}
              </span>
              <span className="flex-1 pl-1 pr-2">{renderTokens(tokens, d.text)}</span>
            </div>
          );
        })}
      </div>
    </pre>
  );
}
