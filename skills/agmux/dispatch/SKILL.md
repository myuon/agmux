---
name: agmux:dispatch
description: agmux専用のdispatchスキル。GitHub Issuesの実装をagmuxセッションとして並列実行する。
allowed-tools: Bash, Read, Glob, Grep
---

# Dispatch (agmux版)

GitHub Issues と PR を triage → tasks → reviews の流れで処理する。
サブコマンドを指定すると該当フェーズだけ実行し、省略すると全フェーズを順に実行する。

グローバル版との違い: **Agent ツールの代わりに `agmux session create` で agmux セッションを作成**し、管理画面から進捗監視・制御を可能にする。

## dispatch triage

まだトリアージラベルがついていない Issue 一覧を確認し、以下のいずれかに振り分けてラベルを付与する:

- **リファイン必要** — 要件が曖昧でユーザーの判断待ちの分岐がある
- **調査必要** — 方針を決めるために調査が必要
- **実装着手可能** — 何を作る・変更するかが一意に解釈でき、完了条件が明確

## dispatch tasks

以下を並列で行う:

### 調査必要ラベルの Issue

各 Issue に対して agmux セッションを作成:

```bash
agmux session create "investigate-<issue番号>" \
  -p "$(pwd)" \
  -m "GitHub Issue #<N> を調査してください。

## 指示
1. Issue の内容を確認: gh issue view <N>
2. コードベースを調査し、実装方針を検討
3. 調査結果を Issue コメントとして投稿: gh issue comment <N> --body '...'
4. 調査完了後、ラベルを更新:
   gh issue edit <N> --remove-label '調査必要' --add-label '実装着手可能'
5. 完了したら complete_goal を呼んでください"
```

### 実装着手可能ラベルの Issue

各 Issue に対して worktree 付き agmux セッションを作成:

```bash
agmux session create "impl-<issue番号>" \
  -p "$(pwd)" \
  -w \
  -m "GitHub Issue #<N> を実装してください。

## 指示
1. Issue の内容と完了条件を確認: gh issue view <N>
2. 調査コメントがあれば参照: gh issue view <N> --comments
3. 実装を行う
4. make test でテスト通過を確認
5. make build でビルド通過を確認
6. conventional commits 形式でコミット
7. push して Closes #<N> を含む PR を作成
8. 完了したら complete_goal を呼んでください"
```

### リファイン必要ラベルの Issue

各 Issue に対して agmux セッションを作成し、要件の深掘りを行う:

```bash
agmux session create "refine-<issue番号>" \
  -p "$(pwd)" \
  -m "GitHub Issue #<N> の要件をリファインしてください。

## 指示
1. Issue の内容を確認: gh issue view <N>
2. 不明点・曖昧な点を洗い出す
3. コードベースを調査して技術的な制約を把握
4. 具体的な実装方針・完了条件を Issue コメントとして投稿
5. ラベルを更新:
   gh issue edit <N> --remove-label 'リファイン必要' --add-label '実装着手可能'
6. 完了したら complete_goal を呼んでください"
```

## dispatch reviews

### 1. レビュー

open な PR 一覧を確認し、各 PR に対して agmux セッションを作成:

```bash
agmux session create "review-pr-<PR番号>" \
  -p "$(pwd)" \
  -m "PR #<N> をレビューしてください。

## 指示
1. PR の内容を確認: gh pr view <N>
2. diff を確認: gh pr diff <N>
3. /review スキルを使ってレビュー
4. レビュー内容を PR コメントとして投稿
5. 完了したら complete_goal を呼んでください"
```

### 2. レビュー指摘の修正

レビューで修正が必要な PR に対して worktree 付き agmux セッションを作成:

```bash
agmux session create "fix-pr-<PR番号>" \
  -p "$(pwd)" \
  -w \
  -m "PR #<N> のレビュー指摘を修正してください。

## 指示
1. PR のレビューコメントを確認: gh pr view <N> --comments
2. 指摘事項を修正
3. make test && make build で確認
4. コミット & プッシュ
5. 完了したら complete_goal を呼んでください"
```

## 注意事項

- セッション作成は fire-and-forget。完了はagmux管理画面で監視する
- 実装タスクは必ず `-w` (worktree) 付きで作成する（並列で別ブランチを扱うため）
- 調査・レビュータスクは worktree 不要（コード変更なし）
- セッションのステータスは `idle` = ターン完了、`stopped` = プロセス終了
