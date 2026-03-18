interface GroupSectionHeaderProps {
  icon?: React.ReactNode;
  title: string;
  count?: number;
}

export function GroupSectionHeader({ icon, title, count }: GroupSectionHeaderProps) {
  return (
    <div className="flex items-center gap-2 mb-2 px-1">
      {icon}
      <span className="text-xs font-semibold text-gray-500 uppercase tracking-wide truncate">
        {title}
      </span>
      {count !== undefined && (
        <span className="text-xs text-gray-400">
          ({count})
        </span>
      )}
    </div>
  );
}
