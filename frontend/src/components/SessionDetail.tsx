import { useEffect, useState } from "react";
import type { Session } from "../types/session";
import { api } from "../api/client";

interface Props {
  sessionId: string;
  onBack: () => void;
}

export function SessionDetail({ sessionId, onBack }: Props) {
  const [session, setSession] = useState<Session | null>(null);
  const [output, setOutput] = useState("");
  const [message, setMessage] = useState("");

  useEffect(() => {
    api.getSession(sessionId).then(setSession);
    api.getSessionOutput(sessionId).then((r) => setOutput(r.output));

    const interval = setInterval(() => {
      api.getSessionOutput(sessionId).then((r) => setOutput(r.output));
      api.getSession(sessionId).then(setSession);
    }, 3000);
    return () => clearInterval(interval);
  }, [sessionId]);

  const handleSend = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!message.trim()) return;
    await api.sendToSession(sessionId, message);
    setMessage("");
    setTimeout(
      () =>
        api.getSessionOutput(sessionId).then((r) => setOutput(r.output)),
      500
    );
  };

  if (!session) return <div className="p-8">Loading...</div>;

  return (
    <div className="p-8 max-w-4xl mx-auto">
      <button
        onClick={onBack}
        className="text-sm text-gray-500 hover:text-gray-800 mb-4"
      >
        &larr; Back
      </button>
      <div className="flex items-center gap-3 mb-4">
        <h2 className="text-2xl font-bold">{session.name}</h2>
        <span className="text-sm text-gray-500">{session.status}</span>
      </div>
      <p className="text-sm text-gray-500 mb-4">
        Project: {session.projectPath}
      </p>

      <div className="bg-gray-900 text-green-400 rounded-lg p-4 mb-4 font-mono text-xs h-96 overflow-y-auto whitespace-pre-wrap">
        {output || "No output yet."}
      </div>

      <form onSubmit={handleSend} className="flex gap-2">
        <input
          type="text"
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          placeholder="Send a message..."
          className="flex-1 border border-gray-300 rounded px-3 py-2 text-sm"
        />
        <button
          type="submit"
          className="px-4 py-2 text-sm bg-blue-600 text-white rounded hover:bg-blue-700"
        >
          Send
        </button>
      </form>
    </div>
  );
}
