import { useState } from "react";

interface Props {
  onClose: () => void;
  onCreate: (data: {
    name: string;
    projectPath: string;
    prompt?: string;
    outputMode?: "terminal" | "stream";
  }) => void;
}

export function CreateSession({ onClose, onCreate }: Props) {
  const [name, setName] = useState("");
  const [projectPath, setProjectPath] = useState("");
  const [prompt, setPrompt] = useState("");
  const [outputMode, setOutputMode] = useState<"terminal" | "stream">("terminal");

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    onCreate({ name, projectPath, prompt: prompt || undefined, outputMode });
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-white rounded-lg p-6 w-full max-w-md">
        <h2 className="text-xl font-semibold mb-4">New Session</h2>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Session Name
            </label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              required
              className="w-full border border-gray-300 rounded px-3 py-2 text-sm"
              placeholder="my-project"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Project Path
            </label>
            <input
              type="text"
              value={projectPath}
              onChange={(e) => setProjectPath(e.target.value)}
              required
              className="w-full border border-gray-300 rounded px-3 py-2 text-sm"
              placeholder="/path/to/project"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Initial Prompt (optional)
            </label>
            <textarea
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              rows={3}
              className="w-full border border-gray-300 rounded px-3 py-2 text-sm"
              placeholder="Fix the bug in..."
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Output Mode
            </label>
            <div className="flex gap-4">
              <label className="flex items-center gap-1 text-sm">
                <input
                  type="radio"
                  name="outputMode"
                  value="terminal"
                  checked={outputMode === "terminal"}
                  onChange={() => setOutputMode("terminal")}
                />
                Terminal
              </label>
              <label className="flex items-center gap-1 text-sm">
                <input
                  type="radio"
                  name="outputMode"
                  value="stream"
                  checked={outputMode === "stream"}
                  onChange={() => setOutputMode("stream")}
                />
                Stream
              </label>
            </div>
          </div>
          <div className="flex justify-end gap-2">
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 text-sm text-gray-600 hover:text-gray-800"
            >
              Cancel
            </button>
            <button
              type="submit"
              className="px-4 py-2 text-sm bg-blue-600 text-white rounded hover:bg-blue-700"
            >
              Create
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
