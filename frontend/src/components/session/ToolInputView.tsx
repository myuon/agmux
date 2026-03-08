export function ToolInputView({ input }: { input: unknown }) {
  if (input && typeof input === "object" && !Array.isArray(input)) {
    const entries = Object.entries(input as Record<string, unknown>);
    return (
      <div className="space-y-2">
        {entries.map(([key, value]) => {
          const str = typeof value === "string" ? value : JSON.stringify(value, null, 2);
          const isMultiline = str.includes("\n");
          return (
            <div key={key}>
              <span className="text-gray-400 text-[10px] uppercase tracking-wide">{key}</span>
              {isMultiline ? (
                <pre className="text-gray-700 text-xs mt-0.5 bg-gray-50 border border-gray-200 rounded p-2 overflow-x-auto whitespace-pre-wrap">{str}</pre>
              ) : (
                <div className="text-gray-700 text-xs mt-0.5 font-mono">{str}</div>
              )}
            </div>
          );
        })}
      </div>
    );
  }
  const str = typeof input === "string" ? input : JSON.stringify(input, null, 2);
  return <pre className="text-gray-600 text-xs overflow-x-auto whitespace-pre-wrap">{str}</pre>;
}
