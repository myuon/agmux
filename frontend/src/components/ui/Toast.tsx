import { CheckCircle2, AlertTriangle, X } from "lucide-react";

export type ToastVariant = "success" | "error" | "warning";

/** インライン表示可能なトーストバー（固定位置なし） */
export function ToastBar({ message, variant = "success", onClose }: { message: string; variant?: ToastVariant; onClose?: () => void }) {
  const styles = variant === "success"
    ? "bg-green-600"
    : variant === "warning"
      ? "bg-yellow-500"
      : "bg-red-600";
  const Icon = variant === "success" ? CheckCircle2 : AlertTriangle;

  return (
    <div className={`${styles} text-white px-4 py-2 rounded shadow-lg text-sm flex items-center gap-2 animate-fade-in w-fit max-w-full`}>
      <Icon className="w-4 h-4 shrink-0" />
      {message}
      {onClose && (
        <button onClick={onClose} className="ml-1 hover:opacity-80" aria-label="閉じる">
          <X className="w-4 h-4" />
        </button>
      )}
    </div>
  );
}

/** 画面上部固定のトーストコンテナ + バー */
export function Toast({ message, variant = "success", onClose }: { message: string; variant?: ToastVariant; onClose?: () => void }) {
  return (
    <div className="fixed top-4 left-4 right-4 sm:left-auto sm:right-4 z-50 flex justify-end">
      <ToastBar message={message} variant={variant} onClose={onClose} />
    </div>
  );
}
