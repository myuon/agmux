// Syntax highlighter backed by shiki's fine-grained core bundle.
// This module (and every grammar) is loaded lazily via dynamic import from
// HighlightedCode, so none of it ends up in the main page bundle.
import { createHighlighterCore, type HighlighterCore, type ThemedToken } from "@shikijs/core";
import { createJavaScriptRegexEngine } from "@shikijs/engine-javascript";
import type { CodeLang } from "../models/codeView";

const THEME = "github-light";

// Explicit loader map: Vite needs static import specifiers to code-split each
// grammar into its own chunk, which is then fetched only when first needed.
const langLoaders: Record<CodeLang, () => Promise<unknown>> = {
  typescript: () => import("@shikijs/langs/typescript"),
  tsx: () => import("@shikijs/langs/tsx"),
  javascript: () => import("@shikijs/langs/javascript"),
  jsx: () => import("@shikijs/langs/jsx"),
  json: () => import("@shikijs/langs/json"),
  go: () => import("@shikijs/langs/go"),
  rust: () => import("@shikijs/langs/rust"),
  python: () => import("@shikijs/langs/python"),
  bash: () => import("@shikijs/langs/bash"),
  yaml: () => import("@shikijs/langs/yaml"),
  toml: () => import("@shikijs/langs/toml"),
  markdown: () => import("@shikijs/langs/markdown"),
  css: () => import("@shikijs/langs/css"),
  html: () => import("@shikijs/langs/html"),
  sql: () => import("@shikijs/langs/sql"),
  c: () => import("@shikijs/langs/c"),
  cpp: () => import("@shikijs/langs/cpp"),
  java: () => import("@shikijs/langs/java"),
  kotlin: () => import("@shikijs/langs/kotlin"),
  swift: () => import("@shikijs/langs/swift"),
  ruby: () => import("@shikijs/langs/ruby"),
  lua: () => import("@shikijs/langs/lua"),
  dockerfile: () => import("@shikijs/langs/dockerfile"),
  makefile: () => import("@shikijs/langs/makefile"),
};

let highlighterPromise: Promise<HighlighterCore> | null = null;
const loadedLangs = new Map<CodeLang, Promise<void>>();

function getHighlighter(): Promise<HighlighterCore> {
  if (!highlighterPromise) {
    highlighterPromise = createHighlighterCore({
      themes: [import("@shikijs/themes/github-light")],
      langs: [],
      // forgiving: unsupported regex features degrade instead of throwing
      engine: createJavaScriptRegexEngine({ forgiving: true }),
    });
  }
  return highlighterPromise;
}

async function ensureLang(hl: HighlighterCore, lang: CodeLang): Promise<void> {
  let p = loadedLangs.get(lang);
  if (!p) {
    p = langLoaders[lang]().then((mod) =>
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      hl.loadLanguage((mod as any).default),
    );
    loadedLangs.set(lang, p);
  }
  return p;
}

export type { ThemedToken };

/**
 * Tokenize code into per-line token arrays. Never throws: on any failure the
 * caller gets null and should render plain text.
 */
export async function tokenizeLines(code: string, lang: CodeLang): Promise<ThemedToken[][] | null> {
  try {
    const hl = await getHighlighter();
    await ensureLang(hl, lang);
    return hl.codeToTokensBase(code, {
      lang,
      theme: THEME,
      // Very long lines (minified files) are left unhighlighted
      tokenizeMaxLineLength: 2000,
      tokenizeTimeLimit: 500,
    });
  } catch {
    return null;
  }
}
