const colorMap = {
  blue: "bg-blue-100 text-blue-700",
  green: "bg-green-100 text-green-700",
  purple: "bg-purple-100 text-purple-700",
  orange: "bg-orange-100 text-orange-700",
  red: "bg-red-100 text-red-700",
  gray: "bg-gray-100 text-gray-600",
} as const;

export type ChipColor = keyof typeof colorMap;

export function Chip({ children, color = "gray" }: { children: React.ReactNode; color?: ChipColor }) {
  return (
    <span className={`px-1.5 py-0.5 text-[10px] font-medium rounded ${colorMap[color]}`}>
      {children}
    </span>
  );
}
