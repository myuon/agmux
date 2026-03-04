package llm

import "fmt"

func BuildAnalysisPrompt(name, projectPath, lastStatus, capturedOutput string) string {
	return fmt.Sprintf(`あなたはAI agentセッションの監視デーモンです。
以下のセッションのターミナル出力を分析し、現在の状態と取るべきアクションを判断してください。

## セッション情報
- 名前: %s
- プロジェクト: %s
- 前回の状態: %s

## ターミナル出力（最新）
%s

## 判断基準
- agentが正常に動作中 → status: "running", action: "none"
- ユーザーの承認/確認を求めている（permission promptやY/Nの質問） → status: "waiting", action: "approve", send_textに送るべきテキストを記載（通常は "y" や "Y"）
- エラーが発生して停止している → status: "error", action: "retry", send_textにリトライ指示を記載
- 人間の判断が必要な重大な問題（設計判断、セキュリティ懸念など） → status: "error", action: "escalate", reasonに理由を記載
- タスクが完了した（シェルプロンプトが表示されている） → status: "done", action: "none"

以下のJSON形式のみで回答してください。説明は不要です:
{"status": "...", "action": "...", "reason": "...", "send_text": "..."}`, name, projectPath, lastStatus, capturedOutput)
}
