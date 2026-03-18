const variants = {
  primary: "bg-blue-600 text-white hover:bg-blue-700",
  secondary: "text-gray-500 bg-gray-50 hover:bg-gray-100",
} as const;

export function CircleButton({ children, onClick, title, disabled, type = "button", className = "", variant = "primary" }: {
  children: React.ReactNode;
  onClick?: () => void;
  title?: string;
  disabled?: boolean;
  type?: "button" | "submit" | "reset";
  className?: string;
  variant?: keyof typeof variants;
}) {
  return (
    <button
      type={type}
      onClick={onClick}
      title={title}
      disabled={disabled}
      className={`w-9 h-9 flex items-center justify-center rounded-full ${variants[variant]} ${className}`}
    >
      {children}
    </button>
  );
}
