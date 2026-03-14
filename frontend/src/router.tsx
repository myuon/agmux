import { createBrowserRouter, type LoaderFunctionArgs } from "react-router-dom";
import App, { Dashboard } from "./App";
import { SessionPage } from "./pages/SessionPage";
import { ConfigPage } from "./pages/ConfigPage";
import { MetricsPage } from "./pages/MetricsPage";
import { api } from "./api/client";
import { RouteErrorBoundary } from "./components/RouteErrorBoundary";

function sinceFromRange(range: string): string | undefined {
  if (range === "all") return undefined;
  const ms: Record<string, number> = {
    "1h": 3600_000,
    "6h": 21600_000,
    "24h": 86400_000,
    "7d": 604800_000,
  };
  return new Date(Date.now() - (ms[range] ?? 86400_000)).toISOString();
}

export const router = createBrowserRouter([
  {
    path: "/",
    element: <App />,
    errorElement: <RouteErrorBoundary />,
    children: [
      {
        index: true,
        element: <Dashboard />,
      },
      {
        path: "sessions/:id",
        element: <SessionPage />,
        loader: async ({ params }: LoaderFunctionArgs) => {
          const id = params.id!;
          const session = await api.getSession(id);
          const [streamOutput, diff, providerVersion] = await Promise.all([
            session.outputMode === "stream"
              ? api.getStreamOutput(id)
              : api.getSessionOutput(id).then((r) => ({ lines: [], total: 0, output: r.output })),
            api.getDiff(id).catch(() => ({ files: [] })),
            (session.provider === "codex" ? api.getCodexVersion : api.getClaudeVersion)()
              .then((r) => r.version)
              .catch(() => null),
          ]);
          return { session, streamOutput, diff, providerVersion };
        },
      },
      {
        path: "config",
        element: <ConfigPage />,
        loader: async () => {
          const config = await api.getConfig();
          return { config };
        },
      },
      {
        path: "metrics",
        element: <MetricsPage />,
        loader: async ({ request }: LoaderFunctionArgs) => {
          const url = new URL(request.url);
          const range = url.searchParams.get("range") ?? "24h";
          const since = sinceFromRange(range);
          const [summary, costTimeline, tokenTimeline, events] = await Promise.all([
            api.getMetricsSummary(since),
            api.getMetrics({ name: "claude_code.cost.usage", since }),
            api.getMetrics({ name: "claude_code.token.usage", since }),
            api.getMetricsEvents({ since }),
          ]);
          return { summary, costTimeline, tokenTimeline, events, range };
        },
      },
    ],
  },
]);
