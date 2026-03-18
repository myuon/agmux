import { useState } from "react";
import { Toast } from "../components/ui/Toast";
import { Modal } from "../components/ui/Modal";
import { Section, Field } from "../components/ui/Section";
import { SummaryCard } from "../components/ui/SummaryCard";
import { CollapsibleText } from "../components/ui/CollapsibleText";
import { FileCodeViewer } from "../components/ui/FileCodeViewer";
import { StatusBadge, StatusDot, statusDots } from "../components/StatusBadge";
import { ToolCallView } from "../components/session/ToolCallView";
import { ToolInputView } from "../components/session/ToolInputView";
import { DiffDropdown } from "../components/session/DiffDropdown";
import { Chip } from "../components/ui/Chip";
import { IconButton } from "../components/ui/IconButton";
import { IconText } from "../components/ui/IconText";
import { CircleButton } from "../components/ui/CircleButton";
import { ToggleButton } from "../components/ui/ToggleButton";
import { FilterButton } from "../components/ui/FilterButton";
import { ArrowLeft, FolderOpen, GitBranch, Plus, SendHorizonal, Settings, Sparkles } from "lucide-react";
import type { Session } from "../types/session";
import type { StreamDisplayItem, ActiveTask } from "../models/stream";
import type { DiffFile } from "../api/client";
import { GoalPanel } from "../components/ui/GoalPanel";
import { ActiveTasksPanel } from "../components/session/StreamOutputView";
import { SessionList } from "../components/SessionList";
import { AlertBanner } from "../components/ui/AlertBanner";
import { ConnectionStatusIndicator } from "../components/ui/ConnectionStatus";

function PreviewSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="space-y-3">
      <h2 className="text-lg font-bold text-gray-800 border-b border-gray-200 pb-2">{title}</h2>
      {children}
    </div>
  );
}

function ToastPreview() {
  const [visible, setVisible] = useState<Record<string, boolean>>({
    success: true,
    error: true,
    warning: true,
  });

  const resetAll = () => setVisible({ success: true, error: true, warning: true });
  const allVisible = Object.values(visible).every(Boolean);

  return (
    <PreviewSection title="Toast">
      <p className="text-xs text-gray-500 mb-2">Toastは固定位置で表示されるため、ここではインライン表示しています。閉じるボタンで非表示にできます。</p>
      {!allVisible && (
        <button onClick={resetAll} className="text-xs text-blue-600 hover:underline mb-2">すべて再表示</button>
      )}
      <div className="space-y-2">
        {(["success", "error", "warning"] as const).map((variant) => (
          <div key={variant} className="relative h-12 border border-dashed border-gray-200 rounded overflow-hidden">
            <div className="absolute inset-0">
              {visible[variant] ? (
                <Toast
                  message={`${variant}: サンプルメッセージです`}
                  variant={variant}
                  onClose={() => setVisible((prev) => ({ ...prev, [variant]: false }))}
                />
              ) : (
                <div className="flex items-center justify-center h-full text-xs text-gray-400">閉じられました</div>
              )}
            </div>
          </div>
        ))}
      </div>
    </PreviewSection>
  );
}

function ModalPreview() {
  const [open, setOpen] = useState(false);
  return (
    <PreviewSection title="Modal">
      <button
        onClick={() => setOpen(true)}
        className="px-3 py-1.5 text-sm bg-blue-600 text-white rounded hover:bg-blue-700"
      >
        モーダルを開く
      </button>
      <Modal open={open} onClose={() => setOpen(false)} title="サンプルモーダル">
        <p className="text-sm text-gray-600">
          これはモーダルのプレビューです。背景をクリックするか、右上の&times;ボタンで閉じられます。
        </p>
        <div className="mt-4 p-3 bg-gray-50 rounded text-xs text-gray-500 font-mono">
          モーダル内のコンテンツ領域
        </div>
      </Modal>
    </PreviewSection>
  );
}

function SectionPreview() {
  return (
    <PreviewSection title="Section / Field">
      <Section title="セクションタイトル">
        <Field label="フィールド1">
          <span className="text-sm text-gray-800">値1</span>
        </Field>
        <Field label="フィールド2">
          <span className="text-sm text-gray-800">値2</span>
        </Field>
        <Field label="トグル">
          <input type="checkbox" defaultChecked />
        </Field>
      </Section>
    </PreviewSection>
  );
}

function SummaryCardPreview() {
  return (
    <PreviewSection title="SummaryCard">
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        <SummaryCard label="セッション数" value="12" />
        <SummaryCard label="コスト" value="$3.42" />
        <SummaryCard label="トークン" value="125K" />
        <SummaryCard label="稼働時間" value="4h 32m" />
      </div>
    </PreviewSection>
  );
}

function StatusBadgePreview() {
  const statuses = Object.keys(statusDots) as Session["status"][];
  return (
    <PreviewSection title="StatusBadge / StatusDot">
      <div className="flex flex-wrap gap-4 items-center">
        {statuses.map((s) => (
          <StatusBadge key={s} status={s} />
        ))}
      </div>
      <div className="flex gap-3 items-center mt-2">
        {statuses.map((s) => (
          <StatusDot key={s} status={s} />
        ))}
      </div>
    </PreviewSection>
  );
}

function CollapsibleTextPreview() {
  const shortText = "短いテキストは折りたたまれません。\n2行目です。";
  const longText = Array.from({ length: 30 }, (_, i) => `行 ${i + 1}: これは長いテキストのサンプルです。`).join("\n");
  return (
    <PreviewSection title="CollapsibleText">
      <div className="space-y-4">
        <div>
          <p className="text-xs text-gray-500 mb-1">短いテキスト (折りたたみなし)</p>
          <CollapsibleText text={shortText} />
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">長いテキスト (折りたたみあり)</p>
          <CollapsibleText text={longText} />
        </div>
      </div>
    </PreviewSection>
  );
}

function FileCodeViewerPreview() {
  const sampleFiles = [
    {
      name: "main.go",
      content: `package main

import "fmt"

func main() {
    fmt.Println("Hello, World!")
}`,
    },
    {
      name: "utils.go",
      content: `package main

func add(a, b int) int {
    return a + b
}`,
    },
  ];

  const diffLines = [
    " package main",
    " ",
    "-func old() {}",
    "+func new() {}",
    " ",
    " func main() {}",
  ];

  return (
    <PreviewSection title="FileCodeViewer">
      <div className="space-y-6">
        <div>
          <p className="text-xs text-gray-500 mb-2">通常表示 (行クリック有効)</p>
          <FileCodeViewer files={sampleFiles} lineClickable />
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-2">折りたたみ表示</p>
          <FileCodeViewer files={sampleFiles} collapsible />
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-2">カスタム行レンダリング (diff風)</p>
          <FileCodeViewer
            files={[{ name: "diff.patch", content: diffLines.join("\n") }]}
            renderLine={(line, i) => (
              <div
                key={i}
                className={`flex ${line.startsWith("+") ? "bg-green-50 text-green-800" : line.startsWith("-") ? "bg-red-50 text-red-800" : ""}`}
              >
                <span className="select-none w-10 text-right pr-3 shrink-0 text-gray-300">{i + 1}</span>
                <span className="flex-1">{line}</span>
              </div>
            )}
            collapsible
          />
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-2">空の状態</p>
          <FileCodeViewer files={[]} emptyMessage="ファイルがありません" />
        </div>
      </div>
    </PreviewSection>
  );
}

function CircleButtonPreview() {
  return (
    <PreviewSection title="CircleButton">
      <div className="space-y-3">
        <div>
          <p className="text-xs text-gray-500 mb-1">Primary (送信ボタン)</p>
          <div className="flex items-center gap-2">
            <CircleButton title="Send">
              <SendHorizonal className="w-4 h-4" />
            </CircleButton>
          </div>
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">Secondary (メニューボタン)</p>
          <div className="flex items-center gap-2">
            <CircleButton variant="secondary" title="Actions">
              <Plus className="w-4 h-4" />
            </CircleButton>
          </div>
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">Disabled</p>
          <div className="flex items-center gap-2">
            <CircleButton disabled title="Disabled primary">
              <SendHorizonal className="w-4 h-4" />
            </CircleButton>
            <CircleButton variant="secondary" disabled title="Disabled secondary">
              <Plus className="w-4 h-4" />
            </CircleButton>
          </div>
        </div>
      </div>
    </PreviewSection>
  );
}

function ToolInputViewPreview() {
  const simpleInput = {
    command: "git status",
    timeout: 30000,
  };
  const nestedInput = {
    file_path: "/src/components/App.tsx",
    old_string: "const foo = 1;",
    new_string: "const foo = 2;",
  };
  const multilineInput = {
    content: `package main

import "fmt"

func main() {
    fmt.Println("Hello, World!")
}`,
  };

  return (
    <PreviewSection title="ToolInputView">
      <div className="space-y-4">
        <div>
          <p className="text-xs text-gray-500 mb-1">シンプルなキー・バリュー</p>
          <div className="border border-gray-200 rounded-lg p-3 bg-white">
            <ToolInputView input={simpleInput} />
          </div>
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">複数フィールド</p>
          <div className="border border-gray-200 rounded-lg p-3 bg-white">
            <ToolInputView input={nestedInput} />
          </div>
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">マルチラインコンテンツ</p>
          <div className="border border-gray-200 rounded-lg p-3 bg-white">
            <ToolInputView input={multilineInput} />
          </div>
        </div>
      </div>
    </PreviewSection>
  );
}

function ToolCallViewPreview() {
  const bashItem: Extract<StreamDisplayItem, { kind: "tool_call" }> = {
    kind: "tool_call",
    name: "Bash",
    input: { command: "ls -la /tmp" },
    result: "total 48\ndrwxrwxrwt  12 root  wheel  384 Mar 18 10:00 .\ndrwxr-xr-x   6 root  wheel  192 Mar 18 09:00 ..",
    toolUseId: "preview-bash-1",
  };
  const readItem: Extract<StreamDisplayItem, { kind: "tool_call" }> = {
    kind: "tool_call",
    name: "Read",
    input: { file_path: "/src/App.tsx", limit: 50 },
    toolUseId: "preview-read-1",
  };
  const todoItem: Extract<StreamDisplayItem, { kind: "tool_call" }> = {
    kind: "tool_call",
    name: "TodoWrite",
    input: {
      todos: [
        { id: "1", content: "コンポーネントを作成", status: "completed" },
        { id: "2", content: "テストを追加", status: "in_progress" },
        { id: "3", content: "ドキュメントを更新", status: "pending" },
      ],
    },
    result: "ok",
    toolUseId: "preview-todo-1",
  };
  const parentItem: Extract<StreamDisplayItem, { kind: "tool_call" }> = {
    kind: "tool_call",
    name: "Agent",
    input: { prompt: "Explore tool panel and textfield components", description: "Explore tool panel and textfield components", subagent_type: "Explore" },
    result: "Found 42 files matching the pattern.",
    toolUseId: "preview-agent-1",
    children: [
      { kind: "tool_call", name: "Glob", input: { pattern: "frontend/src/components/**/*.tsx" }, result: "src/components/ui/Toast.tsx\nsrc/components/ui/Modal.tsx", toolUseId: "child-1" },
      { kind: "tool_call", name: "Grep", input: { pattern: "export function", path: "frontend/src" }, result: "42 matches", toolUseId: "child-2" },
      { kind: "tool_call", name: "Read", input: { file_path: "frontend/src/api/client.ts" }, result: "export const api = { ... }", toolUseId: "child-3" },
      { kind: "tool_call", name: "Read", input: { file_path: "frontend/src/api/client.ts" }, result: "export interface DiffFile { ... }", toolUseId: "child-4" },
      { kind: "tool_call", name: "Read", input: { file_path: "frontend/src/components/ui/Modal.tsx" }, result: "export function Modal({ ... }) { ... }", toolUseId: "child-5" },
      { kind: "tool_call", name: "Read", input: { file_path: "frontend/src/components/ui/FileCodeViewer.tsx" }, result: "export function FileCodeViewer({ ... }) { ... }", toolUseId: "child-6" },
      { kind: "tool_call", name: "Read", input: { file_path: "frontend/src/components/ui/CollapsibleText.tsx" }, result: "export function CollapsibleText({ ... }) { ... }", toolUseId: "child-7" },
      { kind: "tool_call", name: "Read", input: { file_path: "frontend/src/components/session/ToolCallView.tsx" }, result: "export function ToolCallView({ ... }) { ... }", toolUseId: "child-8" },
      { kind: "tool_call", name: "Read", input: { file_path: "frontend/src/components/session/ToolInputView.tsx" }, result: "export function ToolInputView({ ... }) { ... }", toolUseId: "child-9" },
      { kind: "tool_call", name: "Read", input: { file_path: "frontend/src/components/session/DiffDropdown.tsx" }, result: "export function DiffDropdown({ ... }) { ... }", toolUseId: "child-10" },
      { kind: "tool_call", name: "Read", input: { file_path: "frontend/src/models/stream.ts" }, result: "export type StreamDisplayItem = ...", toolUseId: "child-11" },
      { kind: "tool_call", name: "Read", input: { file_path: "frontend/src/components/StatusBadge.tsx" }, result: "export function StatusBadge({ ... }) { ... }", toolUseId: "child-12" },
      { kind: "tool_call", name: "Read", input: { file_path: "frontend/src/components/CreateSession.tsx" }, result: "export function CreateSession({ ... }) { ... }", toolUseId: "child-13" },
    ],
  };
  const escalateItem: Extract<StreamDisplayItem, { kind: "tool_call" }> = {
    kind: "tool_call",
    name: "mcp__agmux__escalate",
    input: { message: "デプロイ先の環境を **staging** と **production** のどちらにしますか？" },
    toolUseId: "preview-escalate-1",
  };
  const escalateResolvedItem: Extract<StreamDisplayItem, { kind: "tool_call" }> = {
    kind: "tool_call",
    name: "mcp__agmux__escalate",
    input: { message: "この変更をマージしてよいですか？" },
    result: "はい、マージしてください",
    toolUseId: "preview-escalate-2",
  };

  return (
    <PreviewSection title="ToolCallView">
      <div className="space-y-3">
        <div>
          <p className="text-xs text-gray-500 mb-1">Bash (完了済み)</p>
          <ToolCallView item={bashItem} />
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">Read (実行中)</p>
          <ToolCallView item={readItem} />
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">TodoWrite</p>
          <ToolCallView item={todoItem} />
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">Agent (子ツールあり)</p>
          <ToolCallView item={parentItem} />
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">Escalate (未回答)</p>
          <ToolCallView item={escalateItem} />
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">Escalate (回答済み)</p>
          <ToolCallView item={escalateResolvedItem} />
        </div>
      </div>
    </PreviewSection>
  );
}

function DiffDropdownPreview() {
  const mockFiles: DiffFile[] = [
    {
      path: "src/App.tsx",
      status: "M",
      diff: `--- a/src/App.tsx
+++ b/src/App.tsx
@@ -1,5 +1,6 @@
 import React from "react";
+import { NewComponent } from "./NewComponent";

 function App() {
-  return <div>Hello</div>;
+  return <NewComponent />;
 }`,
    },
    {
      path: "src/NewComponent.tsx",
      status: "A",
      diff: `--- /dev/null
+++ b/src/NewComponent.tsx
@@ -0,0 +1,5 @@
+export function NewComponent() {
+  return <div>New Component</div>;
+}`,
    },
    {
      path: "src/OldComponent.tsx",
      status: "D",
      diff: `--- a/src/OldComponent.tsx
+++ /dev/null
@@ -1,3 +0,0 @@
-export function OldComponent() {
-  return <div>Old</div>;
-}`,
    },
  ];

  return (
    <PreviewSection title="DiffDropdown">
      <div className="space-y-3">
        <div>
          <p className="text-xs text-gray-500 mb-1">3ファイル変更 (Modified / Added / Deleted)</p>
          <div className="flex items-center gap-2">
            <span className="text-xs text-gray-600">クリックしてdiffを表示:</span>
            <DiffDropdown files={mockFiles} />
          </div>
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">1ファイル変更</p>
          <div className="flex items-center gap-2">
            <span className="text-xs text-gray-600">単一ファイル:</span>
            <DiffDropdown files={[mockFiles[0]]} />
          </div>
        </div>
      </div>
    </PreviewSection>
  );
}

function ChipPreview() {
  return (
    <PreviewSection title="Chip">
      <div className="space-y-3">
        <div>
          <p className="text-xs text-gray-500 mb-1">カラーバリエーション</p>
          <div className="flex flex-wrap gap-2">
            <Chip color="blue">Claude 2.1.76</Chip>
            <Chip color="green">Codex 0.1.2</Chip>
            <Chip color="purple">claude-opus-4-6[1m]</Chip>
            <Chip color="orange">warning</Chip>
            <Chip color="red">error</Chip>
            <Chip color="gray">default</Chip>
          </div>
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">セッションヘッダーでの使用例</p>
          <div className="flex flex-wrap items-center gap-2">
            <StatusDot status="working" />
            <span className="text-lg font-bold">my-session</span>
            <span className="text-xs text-gray-400">working</span>
            <Chip color="blue">Claude 2.1.76</Chip>
            <Chip color="purple">claude-opus-4-6[1m]</Chip>
          </div>
        </div>
      </div>
    </PreviewSection>
  );
}

function IconButtonPreview() {
  return (
    <PreviewSection title="IconButton">
      <div className="space-y-3">
        <div>
          <p className="text-xs text-gray-500 mb-1">標準アイコンボタン</p>
          <div className="flex items-center gap-2">
            <IconButton title="Back"><ArrowLeft className="w-4 h-4" /></IconButton>
            <IconButton title="Settings"><Settings className="w-4 h-4" /></IconButton>
            <IconButton title="CLAUDE.md"><Sparkles className="w-4 h-4" /></IconButton>
          </div>
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">セッションヘッダーでの使用例</p>
          <div className="flex items-center gap-1.5">
            <IconText icon={<FolderOpen className="w-3.5 h-3.5" />}>/Users/ioijoi/ghq/github.com/myuon/agmux</IconText>
            <IconButton title="GitHub" className="ml-0.5">
              <svg className="w-3.5 h-3.5" viewBox="0 0 16 16" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/></svg>
            </IconButton>
            <IconButton title="CLAUDE.md" className="ml-0.5"><Sparkles className="w-3.5 h-3.5" /></IconButton>
            <IconButton title="Settings" className="ml-0.5"><Settings className="w-3.5 h-3.5" /></IconButton>
          </div>
        </div>
      </div>
    </PreviewSection>
  );
}

function IconTextPreview() {
  return (
    <PreviewSection title="IconText">
      <div className="space-y-2">
        <IconText icon={<FolderOpen className="w-3.5 h-3.5" />}>/Users/ioijoi/ghq/github.com/myuon/agmux</IconText>
        <IconText icon={<GitBranch className="w-3.5 h-3.5" />}>feat/preview-domain-components</IconText>
      </div>
    </PreviewSection>
  );
}

function GoalPanelPreview() {
  return (
    <PreviewSection title="GoalPanel">
      <div className="space-y-4">
        <div>
          <p className="text-xs text-gray-500 mb-1">シンプル (タスク + ゴールのみ)</p>
          <GoalPanel
            currentTask="Add GoalPanel component to preview page"
            goal="Extract and componentize goal display from SessionPage"
          />
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">ネスト (親ゴールのブレッドクラムあり)</p>
          <GoalPanel
            currentTask="Write unit tests for GoalPanel"
            goal="Ensure GoalPanel renders correctly"
            goals={[
              { currentTask: "Refactor UI components", goal: "Improve code maintainability" },
              { currentTask: "Extract domain components", goal: "Create reusable component library" },
              { currentTask: "Write unit tests for GoalPanel", goal: "Ensure GoalPanel renders correctly" },
            ]}
          />
        </div>
      </div>
    </PreviewSection>
  );
}

function ActiveTasksPanelPreview() {
  const mockTasks: ActiveTask[] = [
    {
      taskId: "task-abc-123",
      taskType: "local_agent",
      agentId: "agent-def-456",
      description: "Explore codebase structure",
      lastToolName: "Grep",
      lastToolInput: { pattern: "export function", path: "src/" },
      usage: { inputTokens: 12500, outputTokens: 3200 },
    },
    {
      taskId: "task-ghi-789",
      taskType: "local_bash",
      description: "npm test --watch",
      lastToolName: "Bash",
      lastToolInput: { command: "npm test --watch" },
      output: "Tests: 42 passed, 1 failed",
      usage: { inputTokens: 800, outputTokens: 1500 },
    },
  ];

  return (
    <PreviewSection title="ActiveTasksPanel">
      <div className="space-y-4">
        <div>
          <p className="text-xs text-gray-500 mb-1">2つのアクティブタスク (agent + bash)</p>
          <ActiveTasksPanel tasks={mockTasks} />
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">空の状態</p>
          <ActiveTasksPanel tasks={[]} />
        </div>
      </div>
    </PreviewSection>
  );
}

function SessionListPreview() {
  const now = new Date().toISOString();
  const oneHourAgo = new Date(Date.now() - 3600000).toISOString();
  const threeHoursAgo = new Date(Date.now() - 10800000).toISOString();
  const oneDayAgo = new Date(Date.now() - 86400000).toISOString();

  const mockSessions: Session[] = [
    {
      id: "sess-001",
      name: "implement-auth",
      projectPath: "/Users/ioijoi/ghq/github.com/myuon/agmux",
      status: "working",
      type: "worker",
      provider: "claude",
      model: "claude-opus-4-6[1m]",
      currentTask: "Adding JWT authentication middleware",
      createdAt: oneHourAgo,
      updatedAt: now,
    },
    {
      id: "ext-48291",
      name: "refactor-api-client",
      projectPath: "/Users/ioijoi/ghq/github.com/myuon/agmux",
      status: "working",
      type: "external",
      provider: "claude",
      createdAt: threeHoursAgo,
      updatedAt: now,
    },
    {
      id: "ext-77120",
      name: "explore-deps",
      projectPath: "/Users/ioijoi/ghq/github.com/myuon/other-project",
      status: "working",
      type: "external",
      provider: "codex",
      createdAt: oneHourAgo,
      updatedAt: now,
    },
    {
      id: "sess-002",
      name: "fix-css-layout",
      projectPath: "/Users/ioijoi/ghq/github.com/myuon/agmux",
      status: "stopped",
      type: "worker",
      provider: "claude",
      model: "claude-opus-4-6[1m]",
      createdAt: oneDayAgo,
      updatedAt: threeHoursAgo,
    },
    {
      id: "sess-003",
      name: "update-docs",
      projectPath: "/Users/ioijoi/ghq/github.com/myuon/other-project",
      status: "idle",
      type: "worker",
      provider: "claude",
      createdAt: threeHoursAgo,
      updatedAt: oneHourAgo,
    },
  ];

  return (
    <PreviewSection title="SessionList">
      <div className="space-y-4">
        <div>
          <p className="text-xs text-gray-500 mb-2">通常セッション + external + stopped + idle (プロジェクト別グループ)</p>
          <div className="border border-gray-200 rounded-lg p-4 bg-gray-50">
            <SessionList sessions={mockSessions} onRestartController={() => {}} />
          </div>
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-2">空の状態</p>
          <div className="border border-gray-200 rounded-lg p-4 bg-gray-50">
            <SessionList sessions={[]} onRestartController={() => {}} />
          </div>
        </div>
      </div>
    </PreviewSection>
  );
}

function ToggleButtonPreview() {
  const [enabled, setEnabled] = useState(true);
  return (
    <PreviewSection title="ToggleButton">
      <div className="space-y-3">
        <div>
          <p className="text-xs text-gray-500 mb-1">ON / OFF 状態</p>
          <div className="flex items-center gap-3">
            <ToggleButton enabled={true} onClick={() => {}}>ON</ToggleButton>
            <ToggleButton enabled={false} onClick={() => {}}>OFF</ToggleButton>
          </div>
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">インタラクティブ</p>
          <ToggleButton enabled={enabled} onClick={() => setEnabled(!enabled)}>
            {enabled ? "ON" : "OFF"}
          </ToggleButton>
        </div>
      </div>
    </PreviewSection>
  );
}

function FilterButtonPreview() {
  const [selected, setSelected] = useState("1h");
  const options = ["1h", "6h", "24h", "7d", "all"];
  return (
    <PreviewSection title="FilterButton">
      <div className="space-y-3">
        <div>
          <p className="text-xs text-gray-500 mb-1">選択状態の切り替え</p>
          <div className="flex gap-1">
            {options.map((opt) => (
              <FilterButton
                key={opt}
                selected={selected === opt}
                onClick={() => setSelected(opt)}
              >
                {opt}
              </FilterButton>
            ))}
          </div>
        </div>
      </div>
    </PreviewSection>
  );
}

function AlertBannerPreview() {
  return (
    <PreviewSection title="AlertBanner">
      <div className="space-y-3">
        <div>
          <p className="text-xs text-gray-500 mb-1">Warning (デフォルト)</p>
          <AlertBanner variant="warning">設定の変更は次回再起動時に反映されます</AlertBanner>
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">Success</p>
          <AlertBanner variant="success">設定を保存しました</AlertBanner>
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">Error</p>
          <AlertBanner variant="error">保存に失敗しました</AlertBanner>
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">Info</p>
          <AlertBanner variant="info">新しいバージョンが利用可能です</AlertBanner>
        </div>
      </div>
    </PreviewSection>
  );
}

function ConnectionStatusPreview() {
  return (
    <PreviewSection title="ConnectionStatusIndicator">
      <div className="space-y-3">
        <div>
          <p className="text-xs text-gray-500 mb-1">Connected</p>
          <ConnectionStatusIndicator state="connected" />
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">Connecting</p>
          <ConnectionStatusIndicator state="connecting" />
        </div>
        <div>
          <p className="text-xs text-gray-500 mb-1">Disconnected</p>
          <ConnectionStatusIndicator state="disconnected" />
        </div>
      </div>
    </PreviewSection>
  );
}

export function PreviewPage() {
  return (
    <div className="p-6 space-y-8 pb-12">
      <div>
        <h1 className="text-2xl font-bold text-gray-900">UI Component Preview</h1>
        <p className="text-sm text-gray-500 mt-1">UIコンポーネントの一覧とバリエーション確認用ページ</p>
      </div>

      <ToastPreview />
      <ModalPreview />
      <StatusBadgePreview />
      <SummaryCardPreview />
      <SectionPreview />
      <CollapsibleTextPreview />
      <FileCodeViewerPreview />
      <ChipPreview />
      <IconButtonPreview />
      <IconTextPreview />
      <CircleButtonPreview />
      <ToolInputViewPreview />
      <ToolCallViewPreview />
      <DiffDropdownPreview />
      <GoalPanelPreview />
      <ActiveTasksPanelPreview />
      <SessionListPreview />
      <ToggleButtonPreview />
      <FilterButtonPreview />
      <AlertBannerPreview />
      <ConnectionStatusPreview />
    </div>
  );
}
