export function IconText({ icon, children, className = "" }: {
  icon: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={`flex items-center gap-1.5 text-xs text-gray-500 ${className}`}>
      <span className="text-gray-400 shrink-0">{icon}</span>
      <span className="truncate">{children}</span>
    </div>
  );
}
