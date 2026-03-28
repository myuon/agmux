import { ListTodo, Target } from "lucide-react";

interface GoalPanelProps {
  currentTask?: string;
  goal?: string;
  goals?: { currentTask: string; goal: string }[];
}

export function GoalPanel({ currentTask, goal, goals }: GoalPanelProps) {
  return (
    <div className="border-l-2 border-gray-300 bg-gray-50 rounded-r-lg px-3 py-2 mb-3 space-y-1">
      {goals && goals.length > 1 && (
        <div className="text-[10px] text-gray-400 ml-5">
          {goals.slice(0, -1).map((g, i) => (
            <span key={i}>
              {i > 0 && " > "}
              {g.goal}
            </span>
          ))}
          {" > "}
        </div>
      )}
      {currentTask && (
        <div className="flex items-start gap-1.5 text-xs text-gray-700">
          <ListTodo className="w-3.5 h-3.5 shrink-0 mt-0.5 text-indigo-400" />
          <span className="min-w-0 break-words">{currentTask}</span>
        </div>
      )}
      {goal && (
        <div className="flex items-start gap-1.5 text-xs text-gray-500">
          <Target className="w-3.5 h-3.5 shrink-0 mt-0.5 text-emerald-400" />
          <span className="min-w-0 break-words">{goal}</span>
        </div>
      )}
    </div>
  );
}
