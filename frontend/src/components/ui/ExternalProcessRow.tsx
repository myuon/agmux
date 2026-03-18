export function ExternalProcessRow({ provider, name, pid, timeAgo }: {
  provider: string;
  name: string;
  pid: string;
  timeAgo: string;
}) {
  const label = provider === "codex" ? "Codex" : provider === "claude" ? "Claude" : "External";

  return (
    <div className="flex items-center gap-2 px-3 py-1.5 rounded border border-dashed border-amber-300 bg-amber-50/50 text-xs text-gray-500">
      <span className="inline-block w-1.5 h-1.5 rounded-full bg-amber-400 shrink-0" />
      <span className="font-medium text-amber-700 shrink-0">{label}</span>
      <span className="text-gray-600 truncate">{name}</span>
      <span className="font-mono text-gray-400 shrink-0">PID {pid}</span>
      <span className="text-gray-400 ml-auto shrink-0">{timeAgo}</span>
    </div>
  );
}
