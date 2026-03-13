import { useState } from "react";

interface FileCodeViewerProps {
  files: { name: string; content: string }[];
  /** Format content before display (e.g. JSON pretty-print) */
  formatContent?: (content: string) => string;
  /** Enable line click to select and copy path:line */
  lineClickable?: boolean;
  /** No content message */
  emptyMessage?: string;
}

export function FileCodeViewer({
  files,
  formatContent,
  lineClickable = false,
  emptyMessage = "No content",
}: FileCodeViewerProps) {
  const [selectedFile, setSelectedFile] = useState<string | null>(null);
  const [selectedLine, setSelectedLine] = useState<number | null>(null);

  if (files.length === 0) {
    return <div className="text-gray-400 text-sm">{emptyMessage}</div>;
  }

  const multiFile = files.length > 1;

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
          </div>
        );
      })}
    </div>
  );
}
