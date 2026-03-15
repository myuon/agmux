import { useState } from "react";
import { Toast } from "../components/ui/Toast";
import { Modal } from "../components/ui/Modal";
import { Section, Field } from "../components/ui/Section";
import { SummaryCard } from "../components/ui/SummaryCard";
import { CollapsibleText } from "../components/ui/CollapsibleText";
import { FileCodeViewer } from "../components/ui/FileCodeViewer";
import { StatusBadge, StatusDot, statusDots } from "../components/StatusBadge";
import type { Session } from "../types/session";

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

export function PreviewPage() {
  return (
    <div className="space-y-8 pb-12">
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
    </div>
  );
}
