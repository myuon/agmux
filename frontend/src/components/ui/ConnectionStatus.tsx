const stateConfig = {
  connected: { dot: "bg-green-500", text: "text-green-600", label: "Connected" },
  connecting: { dot: "bg-yellow-500 animate-pulse", text: "text-yellow-600", label: "Connecting..." },
  disconnected: { dot: "bg-red-500", text: "text-red-600", label: "Disconnected" },
} as const;

export type ConnectionState = keyof typeof stateConfig;

export function ConnectionStatusIndicator({ state }: { state: ConnectionState }) {
  const config = stateConfig[state];

  return (
    <div className="flex items-center gap-1.5">
      <span className={`inline-block w-2 h-2 rounded-full ${config.dot}`} />
      <span className={`text-xs ${config.text}`}>{config.label}</span>
    </div>
  );
}
