import { createBrowserRouter, type LoaderFunctionArgs } from "react-router-dom";
import App from "./App";
import { SessionPage } from "./pages/SessionPage";
import { ConfigPage } from "./pages/ConfigPage";
import { MetricsPage } from "./pages/MetricsPage";
import { api } from "./api/client";
import { RouteErrorBoundary } from "./components/RouteErrorBoundary";

export const router = createBrowserRouter([
  {
    path: "/",
    element: <App />,
    errorElement: <RouteErrorBoundary />,
    children: [
      {
        index: true,
        // Dashboard is rendered inline in App, no separate element needed
      },
      {
        path: "sessions/:id",
        element: <SessionPage />,
        loader: async ({ params }: LoaderFunctionArgs) => {
          const id = params.id!;
          const session = await api.getSession(id);
          return { session };
        },
        errorElement: <RouteErrorBoundary />,
      },
      {
        path: "config",
        element: <ConfigPage />,
        loader: async () => {
          const config = await api.getConfig();
          return { config };
        },
        errorElement: <RouteErrorBoundary />,
      },
      {
        path: "metrics",
        element: <MetricsPage />,
        loader: async () => {
          // Default range is "24h"
          const since = new Date(Date.now() - 86400_000).toISOString();
          const [summary, costTimeline, tokenTimeline, events] = await Promise.all([
            api.getMetricsSummary(since),
            api.getMetrics({ name: "claude_code.cost.usage", since }),
            api.getMetrics({ name: "claude_code.token.usage", since }),
            api.getMetricsEvents({ since }),
          ]);
          return { summary, costTimeline, tokenTimeline, events };
        },
        errorElement: <RouteErrorBoundary />,
      },
    ],
  },
]);
