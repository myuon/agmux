// Pure helpers for rendering tool inputs/outputs as code (no shiki imports here,
// so this stays in the main bundle cheaply).

export type CodeLang =
  | "typescript" | "tsx" | "javascript" | "jsx" | "json" | "go" | "rust" | "python"
  | "bash" | "yaml" | "toml" | "markdown" | "css" | "html" | "sql" | "c" | "cpp"
  | "java" | "kotlin" | "swift" | "ruby" | "lua" | "dockerfile" | "makefile";

const extLang: Record<string, CodeLang> = {
  ts: "typescript", mts: "typescript", cts: "typescript",
  tsx: "tsx",
  js: "javascript", mjs: "javascript", cjs: "javascript",
  jsx: "jsx",
  json: "json", jsonc: "json", jsonl: "json",
  go: "go",
  rs: "rust",
  py: "python", pyi: "python",
  sh: "bash", bash: "bash", zsh: "bash",
  yml: "yaml", yaml: "yaml",
  toml: "toml",
  md: "markdown", markdown: "markdown", mdx: "markdown",
  css: "css",
  html: "html", htm: "html",
  sql: "sql",
  c: "c", h: "c",
  cpp: "cpp", cc: "cpp", cxx: "cpp", hpp: "cpp", hh: "cpp", hxx: "cpp",
  java: "java",
  kt: "kotlin", kts: "kotlin",
  swift: "swift",
  rb: "ruby",
  lua: "lua",
  mk: "makefile",
};

/** Infer a highlight language from a file path. Returns null for unknown files (plain text). */
export function langFromPath(path: unknown): CodeLang | null {
  if (typeof path !== "string" || path === "") return null;
  const base = path.split(/[\\/]/).pop() ?? "";
  const lower = base.toLowerCase();
  if (lower === "dockerfile" || lower.startsWith("dockerfile.") || lower.endsWith(".dockerfile")) return "dockerfile";
  if (lower === "makefile" || lower === "gnumakefile") return "makefile";
  const dot = lower.lastIndexOf(".");
  if (dot <= 0 && !lower.startsWith(".")) return null;
  const ext = lower.slice(dot + 1);
  return extLang[ext] ?? null;
}

export interface NumberedSnippet {
  /** Plain text before the numbered block (e.g. "Here's the result of running `cat -n`...") */
  before: string;
  /** Code lines with their original line numbers */
  lines: { num: number; text: string }[];
  /** Plain text after the numbered block (e.g. trailing system reminders) */
  after: string;
}

// `cat -n` style as produced by Claude Code's Read / Edit results:
// "    42\tfoo" or "    42→foo"
const numberedLineRe = /^\s*(\d+)(?:\t|→)(.*)$/;

/**
 * Extract the contiguous `cat -n` style block from a tool result. Line numbers
 * come from the text itself, so results that start mid-file (Read with offset)
 * keep their original numbering. Returns null when no numbered block exists.
 */
export function parseNumberedLines(text: string): NumberedSnippet | null {
  const raw = text.split("\n");
  let start = -1;
  for (let i = 0; i < raw.length; i++) {
    if (numberedLineRe.test(raw[i])) { start = i; break; }
  }
  if (start < 0) return null;
  const lines: { num: number; text: string }[] = [];
  let end = start;
  let prev = -1;
  for (let i = start; i < raw.length; i++) {
    const m = numberedLineRe.exec(raw[i]);
    if (!m) break;
    const num = Number(m[1]);
    // Numbers must increase by one; otherwise this is not a cat -n block anymore
    if (prev >= 0 && num !== prev + 1) break;
    lines.push({ num, text: m[2] });
    prev = num;
    end = i + 1;
  }
  return {
    before: raw.slice(0, start).join("\n"),
    lines,
    after: raw.slice(end).join("\n"),
  };
}

export type DiffLine = { type: "same" | "del" | "add"; text: string; oldIdx?: number; newIdx?: number };

/**
 * Line-level diff (LCS). Inputs from Edit are small snippets; for very large
 * inputs we fall back to "all deleted, then all added" to stay cheap.
 */
export function diffLines(oldText: string, newText: string): DiffLine[] {
  const a = oldText.split("\n");
  const b = newText.split("\n");
  const n = a.length;
  const m = b.length;
  if (n * m > 1_000_000) {
    return [
      ...a.map((text, i) => ({ type: "del" as const, text, oldIdx: i })),
      ...b.map((text, i) => ({ type: "add" as const, text, newIdx: i })),
    ];
  }
  // dp[i][j] = LCS length of a[i..] and b[j..]
  const dp: Uint32Array[] = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      out.push({ type: "same", text: a[i], oldIdx: i, newIdx: j });
      i++; j++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      out.push({ type: "del", text: a[i], oldIdx: i });
      i++;
    } else {
      out.push({ type: "add", text: b[j], newIdx: j });
      j++;
    }
  }
  while (i < n) { out.push({ type: "del", text: a[i], oldIdx: i }); i++; }
  while (j < m) { out.push({ type: "add", text: b[j], newIdx: j }); j++; }
  return out;
}
