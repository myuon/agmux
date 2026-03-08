import { useState } from "react";
import { FileDiff } from "lucide-react";
import { Modal } from "../ui/Modal";
import type { DiffFile } from "../../api/client";

const statusBadgeColor: Record<string, string> = {
  M: "bg-yellow-100 text-yellow-800",
  A: "bg-green-100 text-green-800",
  D: "bg-red-100 text-red-800",
  R: "bg-blue-100 text-blue-800",
  "?": "bg-gray-100 text-gray-600",
};

export function DiffDropdown({ files }: { files: DiffFile[] }) {
  const [open, setOpen] = useState(false);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  if (files.length === 0) return null;

  const toggle = (path: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  };

  return (
    <>
      <button
        onClick={() => setOpen(true)}
        className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-full bg-orange-50 text-orange-700 hover:bg-orange-100"
        title="Changes"
      >
        <FileDiff className="w-3 h-3" />
        {files.length}
      </button>
      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title={<span className="flex items-center gap-2"><FileDiff className="w-4 h-4 text-orange-500" />Changes ({files.length} files)</span>}
      >
        <div className="border border-gray-200 rounded-lg overflow-hidden">
          {files.map((file) => (
            <div key={file.path} className="border-b border-gray-100 last:border-b-0">
              <button
                onClick={() => toggle(file.path)}
                className="w-full flex items-center gap-2 px-3 py-1.5 text-xs hover:bg-gray-50 text-left min-w-0"
              >
                <span className={`px-1.5 py-0.5 rounded font-mono text-[10px] font-bold shrink-0 ${statusBadgeColor[file.status] || "bg-gray-100 text-gray-600"}`}>
                  {file.status}
                </span>
                <span className="font-mono text-gray-700 truncate min-w-0">{file.path}</span>
                {file.diff && (
                  <span className="ml-auto text-gray-400 shrink-0">{expanded.has(file.path) ? "\u25BC" : "\u25B6"}</span>
                )}
              </button>
              {expanded.has(file.path) && file.diff && (
                <pre className="bg-gray-50 text-gray-800 text-xs p-3 overflow-x-auto whitespace-pre font-mono border-t border-gray-200">
                  {file.diff.split("\n").map((line, i) => (
                    <span
                      key={i}
                      className={
                        line.startsWith("+") && !line.startsWith("+++")
                          ? "text-green-700 bg-green-50"
                          : line.startsWith("-") && !line.startsWith("---")
                            ? "text-red-700 bg-red-50"
                            : line.startsWith("@@")
                              ? "text-blue-600"
                              : ""
                      }
                    >
                      {line}
                      {"\n"}
                    </span>
                  ))}
                </pre>
              )}
            </div>
          ))}
        </div>
      </Modal>
    </>
  );
}
