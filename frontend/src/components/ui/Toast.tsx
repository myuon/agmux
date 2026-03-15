import { CheckCircle2, AlertTriangle } from "lucide-react";

export function Toast({ message, variant = "success" }: { message: string; variant?: "success" | "error" | "warning" }) {
  const styles = variant === "success"
    ? "bg-green-600"
    : variant === "warning"
      ? "bg-yellow-500"
      : "bg-red-600";
  const Icon = variant === "success" ? CheckCircle2 : AlertTriangle;

  return (
    <div className="fixed top-4 left-4 right-4 sm:left-auto sm:right-4 z-50 flex justify-end">
      <div className={`${styles} text-white px-4 py-2 rounded shadow-lg text-sm flex items-center gap-2 animate-fade-in w-fit max-w-full`}>
        <Icon className="w-4 h-4 shrink-0" />
        {message}
      </div>
    </div>
  );
}
