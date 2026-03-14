import { type ReactNode, useState } from "react";

interface FileCodeViewerProps {
  files: { name: string; content: string }[];
  /** Format content before display (e.g. JSON pretty-print) */
  formatContent?: (content: string) => string;
  /** Custom content renderer per file (e.g. Markdown preview) */
  renderContent?: (content: string) => ReactNode;
  /** Enable line click to select and copy path:line */
  lineClickable?: boolean;
  /** No content message */
  emptyMessage?: string;
  /** Enable collapsible file list (expand/collapse per file) */
  collapsible?: boolean;
  /** Extra element rendered before the file name in the header (e.g. status badge) */
  fileHeaderExtra?: (file: { name: string; content: string }) => ReactNode;
  /** Custom line renderer for code display (e.g. diff coloring) */
  renderLine?: (line: string, index: number) => ReactNode;
}

export function FileCodeViewer({
  files,
  formatContent,
  renderContent,
  lineClickable = false,
  emptyMessage = "No content",
  collapsible = false,
  fileHeaderExtra,
  renderLine,
}: FileCodeViewerProps) {
  const [selectedFile, setSelectedFile] = useState<string | null>(null);
  const [selectedLine, setSelectedLine] = useState<number | null>(null);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  if (files.length === 0) {
    return <div className="text-gray-400 text-sm">{emptyMessage}</div>;
  }

  const toggle = (name: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  };

  const multiFile = files.length > 1;

  if (collapsible) {
    return (
      <div className="border border-gray-200 rounded-lg overflow-hidden">
        {files.map((file) => {
          const content = formatContent
            ? formatContent(file.content)
            : file.content;
          const isExpanded = expanded.has(file.name);

          return (
            <div key={file.name} className="border-b border-gray-100 last:border-b-0">
              <button
                onClick={() => toggle(file.name)}
                className="w-full flex items-center gap-2 px-3 py-1.5 text-xs hover:bg-gray-50 text-left min-w-0"
              >
                {fileHeaderExtra?.(file)}
                <span className="font-mono text-gray-700 truncate min-w-0">{file.name}</span>
                <span className="ml-auto text-gray-400 shrink-0">{isExpanded ? "\u25BC" : "\u25B6"}</span>
              </button>
              {isExpanded && (
                renderContent ? (
                  <div className="border-t border-gray-200 p-3">
                    {renderContent(file.content)}
                  </div>
                ) : (
                  <pre className="bg-gray-50 text-gray-800 text-xs p-3 overflow-x-auto whitespace-pre font-mono border-t border-gray-200">
                    {content.split("\n").map((line, i) => {
                      if (renderLine) return renderLine(line, i);

                      const lineNum = i + 1;
                      const isSelected =
                        lineClickable &&
                        selectedFile === file.name &&
                        selectedLine === lineNum;

                      return (
                        <div
                          key={i}
                          className={`flex ${lineClickable ? "cursor-pointer" : ""} ${isSelected ? "bg-yellow-100" : "hover:bg-gray-50"}`}
                          onClick={
                            lineClickable
                              ? () => {
                                  if (isSelected) {
                                    setSelectedFile(null);
                                    setSelectedLine(null);
                                  } else {
                                    setSelectedFile(file.name);
                                    setSelectedLine(lineNum);
                                    navigator.clipboard.writeText(
                                      `${file.name}:L${lineNum}`,
                                    );
                                  }
                                }
                              : undefined
                          }
                        >
                          <span
                            className={`select-none w-10 text-right pr-3 shrink-0 ${isSelected ? "text-yellow-600" : "text-gray-300"}`}
                          >
                            {lineNum}
                          </span>
                          <span className="flex-1">{line}</span>
                        </div>
                      );
                    })}
                  </pre>
                )
              )}
            </div>
          );
        })}
      </div>
    );
  }

  // Non-collapsible mode (original behavior)
  return (
    <div className="space-y-6">
      {files.map((file, fileIdx) => {
        const content = formatContent
          ? formatContent(file.content)
          : file.content;

        return (
          <div key={fileIdx}>
            {multiFile && (
              <div className="text-xs font-mono text-gray-500 bg-gray-100 px-2 py-1 rounded-t border border-b-0 border-gray-200">
                {file.name}
              </div>
            )}
            {renderContent ? (
              <div className={multiFile ? "border border-gray-200 rounded-b p-3" : ""}>
                {renderContent(file.content)}
              </div>
            ) : (
              <pre
                className={`text-xs leading-relaxed font-mono whitespace-pre-wrap ${multiFile ? "border border-gray-200 rounded-b" : ""}`}
              >
                {content.split("\n").map((line, i) => {
                  const lineNum = i + 1;
                  const isSelected =
                    lineClickable &&
                    selectedFile === file.name &&
                    selectedLine === lineNum;

                  return (
                    <div
                      key={i}
                      className={`flex ${lineClickable ? "cursor-pointer" : ""} ${isSelected ? "bg-yellow-100" : "hover:bg-gray-50"}`}
                      onClick={
                        lineClickable
                          ? () => {
                              if (isSelected) {
                                setSelectedFile(null);
                                setSelectedLine(null);
                              } else {
                                setSelectedFile(file.name);
                                setSelectedLine(lineNum);
                                navigator.clipboard.writeText(
                                  `${file.name}:L${lineNum}`,
                                );
                              }
                            }
                          : undefined
                      }
                    >
                      <span
                        className={`select-none w-10 text-right pr-3 shrink-0 ${isSelected ? "text-yellow-600" : "text-gray-300"}`}
                      >
                        {lineNum}
                      </span>
                      <span className="flex-1">{line}</span>
                    </div>
                  );
                })}
              </pre>
            )}
          </div>
        );
      })}
    </div>
  );
}
