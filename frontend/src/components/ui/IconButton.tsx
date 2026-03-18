import { useNavigate } from "react-router-dom";

const shapeStyles = {
  square: "p-1 rounded",
  rounded: "p-1.5 rounded-lg",
  circle: "w-9 h-9 flex items-center justify-center rounded-full",
} as const;

const variantStyles = {
  ghost: "text-gray-400 hover:text-gray-700 hover:bg-gray-100",
  primary: "bg-blue-600 text-white hover:bg-blue-700",
  secondary: "text-gray-500 bg-gray-50 hover:bg-gray-100",
} as const;

// ghost + rounded uses slightly different base color to match old IconLink
const ghostRoundedOverride = "text-gray-500 hover:text-gray-700 hover:bg-gray-100";

export function IconButton({
  children,
  onClick,
  to,
  title,
  disabled,
  type = "button",
  className = "",
  shape = "square",
  variant = "ghost",
}: {
  children: React.ReactNode;
  onClick?: () => void;
  to?: string;
  title?: string;
  disabled?: boolean;
  type?: "button" | "submit" | "reset";
  className?: string;
  shape?: "square" | "rounded" | "circle";
  variant?: "ghost" | "primary" | "secondary";
}) {
  const navigate = useNavigate();

  const variantClass =
    variant === "ghost" && shape === "rounded"
      ? ghostRoundedOverride
      : variantStyles[variant];

  const handleClick = to
    ? () => navigate(to)
    : onClick;

  return (
    <button
      type={type}
      onClick={handleClick}
      title={title}
      disabled={disabled}
      className={`${shapeStyles[shape]} ${variantClass} shrink-0 ${className}`}
    >
      {children}
    </button>
  );
}
