import { useNavigate } from "react-router-dom";

export function IconLink({ children, to, title, className = "" }: {
  children: React.ReactNode;
  to: string;
  title?: string;
  className?: string;
}) {
  const navigate = useNavigate();

  return (
    <button
      onClick={() => navigate(to)}
      className={`p-1.5 text-gray-500 hover:text-gray-700 rounded-lg hover:bg-gray-100 ${className}`}
      title={title}
    >
      {children}
    </button>
  );
}
