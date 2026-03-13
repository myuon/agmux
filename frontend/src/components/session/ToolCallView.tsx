import { useState } from "react";
import {
  CheckCircle2, ListTodo, Circle, AlertTriangle,
} from "lucide-react";
import { Modal } from "../ui/Modal";
import { CollapsibleText } from "../ui/CollapsibleText";
import type { StreamDisplayItem, AskUserQuestionItem } from "../../models/stream";
import { toolIcon, toolDescription, toolSubDetail, parseTodoInput } from "../../models/tool";
import { api } from "../../api/client";
import { ToolInputView } from "./ToolInputView";

function formatTimeout(seconds: number): string {
  if (seconds >= 60) {
    const min = Math.floor(seconds / 60);
    return `${min}分`;
  }
  return `${seconds}秒`;
}

function TodoCallView({ item }: { item: Extract<StreamDisplayItem, { kind: "tool_call" }> }) {
  const [open, setOpen] = useState(false);
  const todos = parseTodoInput(item.input);
  if (!todos || todos.length === 0) return null;

  const completed = todos.filter(t => t.status === "completed").length;

  return (
    <>
      <button
        onClick={() => setOpen(true)}
        className="w-full text-left border border-gray-200 rounded-lg overflow-hidden px-2.5 py-1.5 bg-gray-50 hover:bg-gray-100 transition-colors"
      >
        <div className="flex items-center gap-2 mb-1.5">
          <ListTodo className="w-3.5 h-3.5 text-gray-400" />
          <span className="font-medium text-xs text-gray-800">Tasks</span>
          <span className="text-[10px] text-gray-400 ml-auto">{completed}/{todos.length}</span>
        </div>
        <div className="space-y-0.5">
          {todos.map((todo, i) => (
            <div key={i} className="flex items-start gap-1.5 text-xs">
              {todo.status === "completed" ? (
                <CheckCircle2 className="w-3.5 h-3.5 text-green-500 shrink-0 mt-0.5" />
              ) : todo.status === "in_progress" ? (
                <Circle className="w-3.5 h-3.5 text-blue-500 fill-blue-500 shrink-0 mt-0.5" />
              ) : (
                <Circle className="w-3.5 h-3.5 text-gray-300 shrink-0 mt-0.5" />
              )}
              <span className={todo.status === "completed" ? "text-gray-400 line-through" : "text-gray-700"}>
                {todo.content}
              </span>
            </div>
          ))}
        </div>
      </button>
      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title={<span className="flex items-center gap-2"><ListTodo className="w-4 h-4 text-gray-500" />TodoWrite</span>}
      >
        <div className="space-y-3">
          <ToolInputView input={item.input} />
          {item.result !== undefined && (
            <div>
              <span className="text-gray-400 text-[10px] uppercase tracking-wide">Output</span>
              <pre className="text-gray-600 text-xs overflow-x-auto whitespace-pre-wrap mt-0.5">{item.result!.slice(0, 2000)}</pre>
            </div>
          )}
        </div>
      </Modal>
    </>
  );
}

function AskUserQuestionCallView({ item, onAnswer }: { item: Extract<StreamDisplayItem, { kind: "tool_call" }>; onAnswer?: (text: string) => void }) {
  const [expanded, setExpanded] = useState(true);
  const inp = item.input as { questions?: AskUserQuestionItem[] } | undefined;
  const Icon = toolIcon(item.name);
  const desc = toolDescription(item.name, item.input);

  return (
    <div className="border border-orange-200 rounded-lg overflow-hidden bg-orange-50">
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full text-left px-2.5 py-1.5 hover:bg-orange-100 transition-colors"
      >
        <div className="flex items-center gap-2">
          <Icon className="w-3.5 h-3.5 text-orange-500 shrink-0" />
          <span className="font-medium text-xs text-orange-800">{item.name}</span>
          {desc && (
            <span className="text-xs text-orange-600 truncate min-w-0">{desc}</span>
          )}
        </div>
      </button>
      {expanded && inp?.questions && (
        <div className="px-3 pb-3 space-y-3">
          {inp.questions.map((q, qi) => (
            <div key={qi}>
              {q.header && (
                <span className="inline-block px-1.5 py-0.5 text-[10px] font-medium bg-orange-200 text-orange-800 rounded mb-1">
                  {q.header}
                </span>
              )}
              <p className="text-sm text-gray-800 mb-2">{q.question}</p>
              <div className="flex flex-wrap gap-2">
                {q.options.map((opt, oi) => (
                  <button
                    key={oi}
                    onClick={() => onAnswer?.(opt.label)}
                    className="px-3 py-1.5 text-xs rounded-lg border bg-white border-gray-300 text-gray-700 hover:bg-blue-50 hover:border-blue-300 hover:text-blue-700 transition-colors"
                    title={opt.description}
                  >
                    {opt.label}
                  </button>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function EscalateCallView({ item, sessionId, escalationId, timedOut, timeoutSeconds, onResponded }: {
  item: Extract<StreamDisplayItem, { kind: "tool_call" }>;
  sessionId?: string;
  escalationId?: string;
  timedOut?: boolean;
  timeoutSeconds?: number;
  onResponded?: () => void;
}) {
  const [expanded, setExpanded] = useState(true);
  const [response, setResponse] = useState("");
  const [sending, setSending] = useState(false);
  const inp = item.input as { message?: string } | undefined;
  const isResolved = item.result !== undefined;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!sessionId || !response.trim() || sending) return;

    if (!escalationId) return;
    const escId = escalationId;
    setSending(true);
    try {
      await api.respondEscalation(sessionId, escId, response.trim());
      setResponse("");
      onResponded?.();
    } catch {
      // ignore errors
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="border border-red-200 rounded-lg overflow-hidden bg-red-50">
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full text-left px-2.5 py-1.5 hover:bg-red-100 transition-colors"
      >
        <div className="flex items-center gap-2">
          <AlertTriangle className="w-3.5 h-3.5 text-red-500 shrink-0" />
          <span className="font-medium text-xs text-red-800">Escalation</span>
          {timeoutSeconds && !isResolved && !timedOut && (
            <span className="text-xs text-gray-500">
              (タイムアウト: {formatTimeout(timeoutSeconds)})
            </span>
          )}
          {timedOut && !isResolved && (
            <span className="text-xs text-amber-600 bg-amber-50 border border-amber-200 rounded px-1.5 py-0.5">
              タイムアウト - エージェントは自動続行しました
            </span>
          )}
          {isResolved && <CheckCircle2 className="w-3 h-3 text-green-500 ml-auto shrink-0" />}
        </div>
      </button>
      {expanded && (
        <div className="px-3 pb-3 space-y-2">
          <p className="text-sm text-gray-800">{inp?.message}</p>
          {isResolved ? (
            <div className="text-xs text-gray-500 bg-white border border-gray-200 rounded px-2 py-1.5">
              <span className="text-gray-400">Response: </span>
              {item.result}
            </div>
          ) : timedOut ? (
            <div className="text-xs text-amber-700 bg-amber-50 border border-amber-200 rounded px-2 py-1.5">
              タイムアウト - エージェントは自動続行しました
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="flex gap-2">
              <input
                type="text"
                value={response}
                onChange={(e) => setResponse(e.target.value)}
                placeholder="Enter your response..."
                className="flex-1 border border-gray-300 rounded px-2 py-1.5 text-xs"
                disabled={sending}
              />
              <button
                type="submit"
                disabled={sending || !response.trim()}
                className="px-3 py-1.5 text-xs bg-red-600 text-white rounded hover:bg-red-700 disabled:opacity-50"
              >
                {sending ? "..." : "Respond"}
              </button>
            </form>
          )}
        </div>
      )}
    </div>
  );
}

export function ToolCallView({ item, onAnswer, sessionId, escalationId, escalationTimedOut, escalationTimeoutSeconds, onEscalationResponded }: {
  item: Extract<StreamDisplayItem, { kind: "tool_call" }>;
  onAnswer?: (text: string) => void;
  sessionId?: string;
  escalationId?: string;
  escalationTimedOut?: boolean;
  escalationTimeoutSeconds?: number;
  onEscalationResponded?: () => void;
}) {
  // Hooks must be called before any conditional returns (React Rules of Hooks)
  const [open, setOpen] = useState(false);
  const [childrenExpanded, setChildrenExpanded] = useState(false);

  if (item.name === "TodoWrite") {
    return <TodoCallView item={item} />;
  }
  if (item.name === "AskUserQuestion") {
    return <AskUserQuestionCallView item={item} onAnswer={onAnswer} />;
  }
  if (item.name === "mcp__agmux__escalate") {
    return <EscalateCallView item={item} sessionId={sessionId} escalationId={escalationId} timedOut={escalationTimedOut} timeoutSeconds={escalationTimeoutSeconds} onResponded={onEscalationResponded} />;
  }
  const Icon = toolIcon(item.name);
  const desc = toolDescription(item.name, item.input);
  const subDetail = toolSubDetail(item.name, item.input);
  const done = item.result !== undefined;
  const hasChildren = item.children && item.children.length > 0;
  const CHILD_DISPLAY_LIMIT = 5;

  return (
    <>
      <button
        onClick={() => setOpen(true)}
        className="w-full text-left border border-gray-200 rounded-lg overflow-hidden px-2.5 py-1.5 bg-gray-50 hover:bg-gray-100 transition-colors"
      >
        <div className="flex items-center gap-2">
          <Icon className="w-3.5 h-3.5 text-gray-500 shrink-0" />
          <span className="font-medium text-xs text-gray-800">{item.name}</span>
          {desc && (
            <span className="text-xs text-gray-500 truncate min-w-0">{desc}</span>
          )}
          {done && <CheckCircle2 className="w-3 h-3 text-green-500 ml-auto shrink-0" />}
        </div>
        {subDetail && (
          <div className="mt-0.5 ml-[22px] font-mono text-[11px] text-gray-400 truncate">{subDetail}</div>
        )}
      </button>
      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title={<span className="flex items-center gap-2"><Icon className="w-4 h-4 text-gray-500" />{item.name} {desc && <span className="text-gray-400 font-normal">{desc}</span>}</span>}
      >
        <div className="space-y-3">
          <ToolInputView input={item.input} />
          {item.resultImages && item.resultImages.length > 0 && (
            <div>
              <span className="text-gray-400 text-[10px] uppercase tracking-wide">Images</span>
              <div className="mt-1 flex flex-wrap gap-2">
                {item.resultImages.map((img, i) => (
                  <img
                    key={i}
                    src={`data:${img.mediaType};base64,${img.data}`}
                    alt={`result-${i}`}
                    className="max-w-full max-h-96 rounded border border-gray-200"
                  />
                ))}
              </div>
            </div>
          )}
          {done && item.result && (
            <div>
              <span className="text-gray-400 text-[10px] uppercase tracking-wide">Output</span>
              <pre className="text-gray-600 text-xs overflow-x-auto whitespace-pre-wrap mt-0.5">{item.result.slice(0, 2000)}</pre>
            </div>
          )}
        </div>
      </Modal>
      {hasChildren && (() => {
        const allChildren = item.children!;
        const totalCount = allChildren.length;
        const hasHidden = totalCount > CHILD_DISPLAY_LIMIT && !childrenExpanded;
        const hiddenCount = totalCount - CHILD_DISPLAY_LIMIT;
        const visibleChildren = hasHidden
          ? allChildren.slice(totalCount - CHILD_DISPLAY_LIMIT)
          : allChildren;
        return (
          <div className="ml-4 border-l-2 border-indigo-200 pl-3 space-y-1.5">
            {hasHidden && (
              <button
                onClick={() => setChildrenExpanded(true)}
                className="text-xs text-blue-500 hover:text-blue-700 py-0.5"
              >
                他 {hiddenCount} 件を表示
              </button>
            )}
            {childrenExpanded && totalCount > CHILD_DISPLAY_LIMIT && (
              <button
                onClick={() => setChildrenExpanded(false)}
                className="text-xs text-blue-500 hover:text-blue-700 py-0.5"
              >
                折りたたむ
              </button>
            )}
            {visibleChildren.map((child, i) => (
              <div key={hasHidden ? i + hiddenCount : i}>
                {child.kind === "tool_call" ? (
                  <ToolCallView item={child} />
                ) : child.kind === "text" ? (
                  <CollapsibleText text={child.text} />
                ) : null}
              </div>
            ))}
          </div>
        );
      })()}
    </>
  );
}
