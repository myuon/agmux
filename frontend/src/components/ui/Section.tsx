export function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="bg-white rounded-lg border border-gray-200 shadow-sm">
      <h2 className="text-sm font-semibold text-gray-700 px-4 py-3 border-b border-gray-200">
        {title}
      </h2>
      <div className="p-4 space-y-4">{children}</div>
    </div>
  );
}

export function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between">
      <label className="text-sm text-gray-600">{label}</label>
      {children}
    </div>
  );
}
