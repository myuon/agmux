package learning

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Accept applies a pending proposal. content overrides the proposal text when
// non-empty (the person may edit it before accepting).
//   - memory: written directly under ~/.claude/projects/<project>/memory
//   - claude_md / skill: a session is started that commits the change on a
//     branch and opens a PR; nothing is left uncommitted in the checkout.
func (r *Reviewer) Accept(id int64, content string) (*Proposal, error) {
	p, err := r.store.Get(id)
	if err != nil {
		return nil, err
	}
	if p.Status != StatusPending {
		return nil, fmt.Errorf("proposal %d is not pending", id)
	}
	if strings.TrimSpace(content) != "" {
		p.Content = strings.TrimSpace(content)
	}

	switch p.Target {
	case TargetMemory:
		if err := r.writeMemory(p); err != nil {
			_ = r.store.Decide(id, StatusFailed, p.Content, "", err.Error())
			return nil, err
		}
		if err := r.store.Decide(id, StatusApplied, p.Content, "", ""); err != nil {
			return nil, err
		}
	case TargetClaudeMD, TargetSkill:
		if r.StartSession == nil {
			return nil, fmt.Errorf("cannot start apply session")
		}
		sid, err := r.StartSession(fmt.Sprintf("学びの反映: %s", p.Title), p.ProjectPath, applyPrompt(p))
		if err != nil {
			_ = r.store.Decide(id, StatusFailed, p.Content, "", err.Error())
			return nil, err
		}
		if err := r.store.Decide(id, StatusApplying, p.Content, sid, ""); err != nil {
			return nil, err
		}
	case TargetLoop:
		if r.CreateAutomation == nil {
			return nil, fmt.Errorf("cannot create automation")
		}
		// Always created disabled: the person reviews it before enabling.
		aid, err := r.CreateAutomation(p.Title, p.Content, p.ProjectPath, p.TriggerType, p.TriggerValue)
		if err != nil {
			return nil, err
		}
		if err := r.store.Decide(id, StatusApplied, p.Content, "", ""); err != nil {
			return nil, err
		}
		if err := r.store.SetAutomation(id, aid); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown target: %s", p.Target)
	}
	return r.store.Get(id)
}

// Reject records that the proposal was declined. Rejected proposals are fed
// back to later reviews so they are not suggested again.
func (r *Reviewer) Reject(id int64) (*Proposal, error) {
	p, err := r.store.Get(id)
	if err != nil {
		return nil, err
	}
	if err := r.store.Decide(id, StatusRejected, p.Content, "", ""); err != nil {
		return nil, err
	}
	if p.ClusterID > 0 {
		// Never suggest this repeated pattern again.
		if err := r.store.setClusterState(p.ClusterID, clusterDismissed); err != nil {
			return nil, err
		}
	}
	if p.ClusterID > 0 {
		// Never suggest this repeated pattern again.
		if err := r.store.setClusterState(p.ClusterID, clusterDismissed); err != nil {
			return nil, err
		}
	}
	return r.store.Get(id)
}

func (r *Reviewer) writeMemory(p *Proposal) error {
	dir := MemoryDir(r.homeDir, p.ProjectPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := p.Name
	if name == "" {
		name = "learning"
	}
	file := name + ".md"
	for i := 2; ; i++ {
		if _, err := os.Stat(filepath.Join(dir, file)); os.IsNotExist(err) {
			break
		}
		file = fmt.Sprintf("%s-%d.md", name, i)
	}
	mt := p.MemoryType
	if mt == "" {
		mt = "project"
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: %s\nmetadata:\n  type: %s\n---\n\n%s\n",
		strings.TrimSuffix(file, ".md"), oneLine(p.Title), mt, p.Content)
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
		return err
	}

	index := filepath.Join(dir, "MEMORY.md")
	existing, _ := os.ReadFile(index)
	var b strings.Builder
	b.Write(existing)
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "- [%s](%s) — %s\n", oneLine(p.Title), file, oneLine(headRunes(p.Reason, 80)))
	return os.WriteFile(index, []byte(b.String()), 0o644)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func applyPrompt(p *Proposal) string {
	var target, how string
	switch p.Target {
	case TargetClaudeMD:
		target = "CLAUDE.md"
		how = "CLAUDE.md の既存の構成を読み、内容に合うセクションへ追記してください。合うセクションが無ければ新しい見出しを作ってください。"
	case TargetSkill:
		target = filepath.Join(".claude", "skills", p.Name, "SKILL.md")
		how = fmt.Sprintf("%s が存在すれば、内容に合う箇所（Pitfalls や手順など）へ追記してください。存在しなければ新規作成し、frontmatter に name: %s と、いつ使うスキルかが分かる description を付けてください。", target, p.Name)
	}
	return fmt.Sprintf(`学びの受信箱でユーザーが採用した変更を、このリポジトリに PR として反映してください。

対象: %s
要約: %s

追記する内容:
<content>
%s
</content>

手順:
1. デフォルトブランチの最新から learning/proposal-%d ブランチを切る
2. %s
   内容の趣旨は変えないこと。既存の記述と重複する部分があれば統合してよい。それ以外のファイルは変更しない
3. コミットして push し、gh pr create で PR を作る（本文に「学びの受信箱 #%d から反映」と書く）
4. PR の URL を報告して complete_goal する。PR のマージはしない
`, target, p.Title, p.Content, p.ID, how, p.ID)
}
