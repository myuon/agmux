export function SystemEventRow({ label, detail }: { label: string; detail?: string }) {
  return (
    <div className="flex items-center gap-2 py-1 px-3 text-xs text-gray-500 border-y border-dashed border-gray-200">
      <span className="text-gray-400">{"\u27E1"}</span>
      <span>{label}</span>
      {detail && <span className="text-gray-400">{detail}</span>}
    </div>
  );
}
