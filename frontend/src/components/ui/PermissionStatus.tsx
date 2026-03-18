interface PermissionStatusProps {
  status: "granted" | "denied" | "default" | "prompt" | string;
}

export function PermissionStatus({ status }: PermissionStatusProps) {
  const colorClass =
    status === "granted"
      ? "text-green-600"
      : status === "denied"
        ? "text-red-600"
        : "text-yellow-600";

  return (
    <span className={`text-sm font-medium ${colorClass}`}>
      {status}
    </span>
  );
}
