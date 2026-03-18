interface ActionMenuProps {
  children: React.ReactNode;
  className?: string;
}

export function ActionMenu({ children, className = "" }: ActionMenuProps) {
  return (
    <div className={`bg-white border border-gray-200 rounded-lg shadow-lg min-w-[160px] ${className}`}>
      {children}
    </div>
  );
}

interface ActionMenuItemProps {
  icon: React.ReactNode;
  label: string;
  onClick?: () => void;
  variant?: "default" | "danger";
  disabled?: boolean;
}

export function ActionMenuItem({ icon, label, onClick, variant = "default", disabled = false }: ActionMenuItemProps) {
  const variantClasses =
    variant === "danger"
      ? "text-red-600 hover:bg-red-50"
      : "text-gray-700 hover:bg-gray-50";

  return (
    <button
      type="button"
      className={`w-full text-left px-3 py-2 text-sm flex items-center gap-2 ${variantClasses} ${disabled ? "opacity-40 pointer-events-none" : ""}`}
      onMouseDown={(e) => {
        e.preventDefault();
        onClick?.();
      }}
      disabled={disabled}
    >
      {icon} {label}
    </button>
  );
}
