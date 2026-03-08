import { useState } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";

const COLLAPSE_LINE_THRESHOLD = 20;

export function CollapsibleText({ text }: { text: string }) {
  const [expanded, setExpanded] = useState(false);
  const lineCount = text.split("\n").length;
  const shouldCollapse = lineCount > COLLAPSE_LINE_THRESHOLD;

  if (!shouldCollapse || expanded) {
    return (
      <div>
        <div className="prose prose-xs max-w-none prose-pre:bg-gray-100 prose-pre:text-gray-800 prose-code:text-pink-600">
          <Markdown remarkPlugins={[remarkGfm]}>{text}</Markdown>
        </div>
        {shouldCollapse && (
          <button onClick={() => setExpanded(false)} className="text-xs text-blue-500 hover:text-blue-700 mt-1">
            折りたたむ
          </button>
        )}
      </div>
    );
  }

  const preview = text.split("\n").slice(0, 5).join("\n");
  return (
    <div>
      <div className="prose prose-xs max-w-none prose-pre:bg-gray-100 prose-pre:text-gray-800 prose-code:text-pink-600 relative overflow-hidden max-h-24">
        <Markdown remarkPlugins={[remarkGfm]}>{preview}</Markdown>
      </div>
      <button onClick={() => setExpanded(true)} className="text-xs text-blue-500 hover:text-blue-700 mt-1">
        続きを表示 ({lineCount} 行)
      </button>
    </div>
  );
}
