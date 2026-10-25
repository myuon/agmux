import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";

export function MarkdownText({ text }: { text: string }) {
  return (
    <div className="prose prose-xs max-w-none prose-pre:bg-gray-100 prose-pre:text-gray-800 prose-code:text-pink-600">
      <Markdown remarkPlugins={[remarkGfm]}>{text}</Markdown>
    </div>
  );
}
