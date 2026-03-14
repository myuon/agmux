import { useRouteError, isRouteErrorResponse, useNavigate } from "react-router-dom";

export function RouteErrorBoundary() {
  const error = useRouteError();
  const navigate = useNavigate();

  let title = "Something went wrong";
  let message = "An unexpected error occurred.";

  if (isRouteErrorResponse(error)) {
    title = `${error.status} ${error.statusText}`;
    message = error.data?.message ?? error.statusText;
  } else if (error instanceof Error) {
    message = error.message;
  }

  return (
    <div className="min-h-screen bg-gray-50 flex items-center justify-center">
      <div className="text-center space-y-4">
        <h1 className="text-2xl font-bold text-gray-900">{title}</h1>
        <p className="text-gray-600">{message}</p>
        <button
          onClick={() => navigate("/")}
          className="text-blue-600 hover:text-blue-800 text-sm font-medium"
        >
          Back to Dashboard
        </button>
      </div>
    </div>
  );
}
