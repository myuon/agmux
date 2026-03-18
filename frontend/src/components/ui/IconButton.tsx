export function IconButton({ children, onClick, title, className = "" }: {
  children: React.ReactNode;
  onClick?: () => void;
  title?: string;
  className?: string;
}) {
  return (
    <button
      onClick={onClick}
      className={`p-1 text-gray-400 hover:text-gray-700 rounded hover:bg-gray-100 shrink-0 ${className}`}
      title={title}
    >
      {children}
    </button>
  );
}
