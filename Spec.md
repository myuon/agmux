# Spec.md — Controller セッション

## 1. Goal
- agmux 起動時に自動で作成される「controllerセッション」を通じて、ユーザーが Claude Code と対話しながら他のセッションを操作できるようにする（copilot的な存在）

## 2. Non-Goals
- 既存 Daemon の置き換え・廃止（共存する）
- MCP Server の実装（CLIコマンド経由で操作する）
- controller セッションの複数起動
- controller セッション用の特別な初期プロンプト自動付与

## 3. Target Users
- agmux を使って複数の AI agent セッションを管理するローカル開発者
- Daemon のログやセッション状態を確認しながら、対話的にセッション操作を行いたいユーザー

## 4. Core User Flow
1. ユーザーが `agmux serve` を実行する
2. サーバー起動時に controller セッションが自動的に1つ作成される（tmux上のClaude Code）
3. Web UI にcontrollerセッションが他のセッションと区別されて表示される（「Controller」ラベル付き）
4. ユーザーがcontrollerセッションに対して自然言語で指示を送る（例:「新しいセッションを作って」「セッション一覧を見せて」）
5. controllerセッション内の Claude Code が `agmux session list` 等のCLIコマンドをbash経由で実行し、結果を返す
6. ユーザーはcontrollerセッションの出力を Web UI で確認する

## 5. Inputs & Outputs
- **入力**: ユーザーからの自然言語指示（Web UI の send 機能経由）
- **出力**: Claude Code の応答（agmux CLI の実行結果を含む）、Web UI 上でのセッション表示

## 6. Tech Stack
- 既存スタック（Go / React / SQLite）をそのまま使用
- 新規ライブラリ追加なし

## 7. Rules & Constraints
- controller セッションは常に1つだけ存在する（シングルトン）
- `agmux serve` 起動時に自動作成。既に存在する場合は再作成しない
- controller セッションは通常の ag-session と同じ仕組み（tmux + Claude Code）で動作する
- controller セッションの `type` フィールドで通常セッションと区別する
- Daemon は controller セッション自体もパトロール対象とする（通常セッションと同じ扱い）
- controller セッションの project_path は `~/.agmux/controller/`（自動作成）
- controller セッションは UI から手動削除できない（Stop は可能）
- controller セッションが停止した場合、UI から再起動できる

## 8. Open Questions
- なし（すべて確定済み）

## 9. Acceptance Criteria（最大10個）
1. `agmux serve` 起動時に controller セッションが自動作成される
2. controller セッションは DB 上で `type = "controller"` として保存されている
3. controller セッションは常に1つだけ存在する（重複作成されない）
4. Web UI で controller セッションに「Controller」ラベルが表示される
5. Web UI で controller セッションが通常セッションと視覚的に区別できる（ラベル・配置）
6. controller セッション内から `agmux session list` を実行して他セッションの一覧が取得できる
7. controller セッション内から `agmux session create` で新しいセッションを作成できる
8. controller セッションは UI から削除できない（削除ボタンが非表示 or 無効）
9. 停止した controller セッションを UI から再起動できる
10. Daemon は controller セッションに対しても通常通りパトロールを行う

## 10. Verification Strategy

- **進捗検証**: 各タスク完了後に `make build && make test` を実行し、ビルド・テストが通ることを確認
- **達成検証**: `agmux serve` を起動し、Web UI 上で controller セッションが表示されること、controller セッション経由で他セッションの操作ができることを手動確認
- **漏れ検出**: Acceptance Criteria のチェックリストを1つずつ確認。シングルトン制約はサーバー再起動時にも保たれることをテスト

## 11. Test Plan

### Test 1: Controller セッション自動作成
- **Given**: agmux サーバーが未起動で、controller セッションが存在しない
- **When**: `agmux serve` を実行する
- **Then**: DB に `type = "controller"` のセッションが1件作成され、tmux セッションが起動している

### Test 2: シングルトン制約
- **Given**: controller セッションが既に存在する状態
- **When**: `agmux serve` を再起動する
- **Then**: 新しい controller セッションは作成されず、既存のものが維持される（または停止済みなら新規作成）

### Test 3: UI 表示と操作制約
- **Given**: controller セッションと通常セッションが存在する
- **When**: Web UI のセッション一覧を表示する
- **Then**: controller セッションに「Controller」ラベルが付き、削除ボタンが非表示で、通常セッションには削除ボタンが表示される
