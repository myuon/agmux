const variantStyles = {
  warning: "bg-yellow-50 border-yellow-300 text-yellow-800",
  success: "bg-green-50 border-green-300 text-green-800",
  error: "bg-red-50 border-red-300 text-red-800",
  info: "bg-blue-50 border-blue-300 text-blue-800",
} as const;

export type AlertBannerVariant = keyof typeof variantStyles;

export function AlertBanner({
  children,
  variant = "warning",
}: {
  children: React.ReactNode;
  variant?: AlertBannerVariant;
}) {
  return (
    <div className={`rounded-lg px-4 py-3 text-sm border ${variantStyles[variant]}`}>
      {children}
    </div>
  );
}
