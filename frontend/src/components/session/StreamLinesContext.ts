import { createContext } from "react";

// Live stream lines of the parent session. Provided by StreamOutputView /
// ActiveTasksPanel so that an open SubagentThreadView can refresh when new
// lines arrive, and (when no sessionId is available, e.g. scenario/preview
// pages) can build the thread locally from these lines.
export const StreamLinesContext = createContext<unknown[] | null>(null);
