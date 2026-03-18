import { useState } from "react";
import { FileDiff } from "lucide-react";
import { Modal } from "../ui/Modal";
import { FileCodeViewer } from "../ui/FileCodeViewer";
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

  if (files.length === 0) return null;

  const statusMap = new Map(files.map((f) => [f.path, f.status]));

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
        <FileCodeViewer
          files={files.map((f) => ({ name: f.path, content: f.diff || "" }))}
          fileHeaderExtra={(file) => {
            const status = statusMap.get(file.name) || "?";
            return (
              <span className={`px-1.5 py-0.5 rounded font-mono text-[10px] font-bold shrink-0 ${statusBadgeColor[status] || "bg-gray-100 text-gray-600"}`}>
                {status}
              </span>
            );
          }}
          renderLine={(line, i) => (
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
          )}
        />
      </Modal>
    </>
  );
}
