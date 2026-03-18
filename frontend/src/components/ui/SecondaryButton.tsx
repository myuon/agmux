import React from "react";

type ColorScheme = "purple" | "blue" | "gray";

const colorStyles: Record<ColorScheme, string> = {
  purple: "bg-purple-50 text-purple-600 hover:bg-purple-100",
  blue: "bg-blue-600 text-white hover:bg-blue-700",
  gray: "bg-gray-600 text-white hover:bg-gray-700",
};

interface SecondaryButtonProps {
  children: React.ReactNode;
  onClick?: (e: React.MouseEvent<HTMLButtonElement>) => void;
  className?: string;
  color?: ColorScheme;
}

export function SecondaryButton({
  children,
  onClick,
  className = "",
  color = "purple",
}: SecondaryButtonProps) {
  return (
    <button
      onClick={onClick}
      className={`px-2 py-0.5 text-xs rounded ${colorStyles[color]} ${className}`}
    >
      {children}
    </button>
  );
}
