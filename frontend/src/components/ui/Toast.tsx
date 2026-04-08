import { CheckCircle2, AlertTriangle, X } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";

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
    <div className={`${styles} text-white px-4 py-2 rounded shadow-lg text-sm flex items-center gap-2 w-fit max-w-full`}>
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

/** 画面上部固定のトーストコンテナ + バー（アニメーション付き） */
export function Toast({ message, variant = "success", onClose }: { message: string; variant?: ToastVariant; onClose?: () => void }) {
  return (
    <AnimatePresence>
      <motion.div
        className="fixed top-4 left-4 right-4 sm:left-auto sm:right-4 z-50 flex justify-end"
        initial={{ opacity: 0, y: -20, scale: 0.95 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        exit={{ opacity: 0, y: -20, scale: 0.95 }}
        transition={{ type: "spring", damping: 25, stiffness: 300 }}
      >
        <ToastBar message={message} variant={variant} onClose={onClose} />
      </motion.div>
    </AnimatePresence>
  );
}
