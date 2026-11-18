// Package learning implements the "learning inbox" experimental feature:
// after a session finishes, a cheap model reviews the conversation and
// proposes additions to CLAUDE.md, skills or memory. Nothing is written until
// a person accepts a proposal.
package learning

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/myuon/agmux/internal/search"
	"github.com/myuon/agmux/internal/session"
)

// FeatureName is the experimental flag that gates this package.
const FeatureName = "learning_inbox"

const (
	DefaultModel          = "haiku"
	reviewTimeout         = 5 * time.Minute
	maxTranscriptRunes    = 60000
	minTranscriptRunes    = 400
	maxClaudeMDRunes      = 20000
	maxMemoryIndexRunes   = 8000
	maxProposalsPerReview = 5
)

// Generator runs the review prompt through an LLM and returns its raw output.
type Generator func(ctx context.Context, model, prompt string) (string, error)

// SessionStarter starts a session in projectPath that runs prompt, returning
// its ID. Used to turn accepted repo changes into a PR.
type SessionStarter func(name, projectPath, prompt string) (string, error)

type Reviewer struct {
	store      *Store
	db         *sql.DB
	streamsDir string
	homeDir    string
	logger     *slog.Logger

	Generate     Generator
	StartSession SessionStarter
	Model        func() string
	// OnProposals is called after a review produced at least one proposal.
	OnProposals func(sessionID, sessionName string, n int)

	// Proactive reports whether proactive_suggestions is on (and learning_inbox
	// with it). When off, no signals are requested, stored or matched.
	Proactive func() bool
	// LoopsEnabled reports whether the experimental loops feature is on
	// (queue triggers are only offered then).
	LoopsEnabled func() bool
	// OnSuggestion is called when a repeated pattern produced a proposal.
	// summary is the cluster's representative summary, n the session count.
	OnSuggestion func(p *Proposal, summary string, n int)
	// CreateAutomation creates a disabled Automation for an accepted loop
	// proposal and returns its ID.
	CreateAutomation func(name, prompt, projectPath, triggerType, triggerValue string) (string, error)

	mu      sync.Mutex
	running map[string]bool
}

func NewReviewer(store *Store, db *sql.DB, streamsDir, homeDir, claudeCommand string, logger *slog.Logger) *Reviewer {
	return &Reviewer{
		store:      store,
		db:         db,
		streamsDir: streamsDir,
		homeDir:    homeDir,
		logger:     logger.With("component", "learning"),
		Generate:   ClaudeGenerator(claudeCommand),
		Model:      func() string { return DefaultModel },
		running:    map[string]bool{},
	}
}

// ClaudeGenerator runs `claude -p` without tools.
func ClaudeGenerator(command string) Generator {
	if command == "" {
		command = "claude"
	}
	return func(ctx context.Context, model, prompt string) (string, error) {
		cmd := exec.CommandContext(ctx, command,
			"-p", "--model", model, "--output-format", "text",
			"--tools", "", "--strict-mcp-config", "--no-session-persistence")
		cmd.Dir = os.TempDir()
		for _, env := range os.Environ() {
			if !strings.HasPrefix(env, "CLAUDECODE=") {
				cmd.Env = append(cmd.Env, env)
			}
		}
		cmd.Stdin = strings.NewReader(prompt)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("claude review: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return stdout.String(), nil
	}
}

type sessionInfo struct {
	id, name, projectPath, provider, typ string
}

func (r *Reviewer) loadSession(id string) (*sessionInfo, error) {
	var s sessionInfo
	err := r.db.QueryRow(`SELECT id, name, project_path, provider, type FROM sessions WHERE id = ?`, id).
		Scan(&s.id, &s.name, &s.projectPath, &s.provider, &s.typ)
	if err != nil {
		return nil, fmt.Errorf("session %s: %w", id, err)
	}
	return &s, nil
}

// ReviewAsync starts a review in the background unless one is already
// running for the session.
func (r *Reviewer) ReviewAsync(sessionID string) bool {
	r.mu.Lock()
	if r.running[sessionID] {
		r.mu.Unlock()
		return false
	}
	r.running[sessionID] = true
	r.mu.Unlock()
	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.running, sessionID)
			r.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), reviewTimeout)
		defer cancel()
		n, err := r.Review(ctx, sessionID)
		if err != nil {
			r.logger.Warn("learning review failed", "sessionId", sessionID, "error", err)
			return
		}
		r.logger.Info("learning review done", "sessionId", sessionID, "proposals", n)
	}()
	return true
}

// Review reviews the part of the session's conversation not reviewed yet and
// stores proposals. It returns the number of proposals created.
func (r *Reviewer) Review(ctx context.Context, sessionID string) (int, error) {
	sess, err := r.loadSession(sessionID)
	if err != nil {
		return 0, err
	}
	if sess.typ == "controller" || sess.projectPath == "" {
		return 0, nil
	}
	if st, err := os.Stat(sess.projectPath); err != nil || !st.IsDir() {
		return 0, nil
	}
	offset, err := r.store.ReviewOffset(sessionID)
	if err != nil {
		return 0, err
	}
	transcript, newOffset, err := r.readTranscript(sess, offset)
	if err != nil {
		return 0, err
	}
	if len([]rune(transcript)) < minTranscriptRunes {
		return 0, r.store.SetReviewOffset(sessionID, newOffset)
	}

	prompt, err := r.buildPrompt(sess, transcript)
	if err != nil {
		return 0, err
	}
	raw, err := r.Generate(ctx, r.Model(), prompt)
	if err != nil {
		return 0, err
	}
	proposals, err := ParseProposals(raw)
	if err != nil {
		return 0, err
	}
	var signals []*Signal
	if r.proactiveOn() {
		signals = ParseSignals(raw)
	}
	for _, p := range proposals {
		p.SessionID, p.SessionName, p.ProjectPath = sess.id, sess.name, sess.projectPath
		if err := r.store.Insert(p); err != nil {
			return 0, err
		}
	}
	if err := r.store.SetReviewOffset(sessionID, newOffset); err != nil {
		return 0, err
	}
	if len(signals) > 0 && r.proactiveOn() {
		r.processSignals(ctx, sess, signals)
	}
	if len(proposals) > 0 && r.OnProposals != nil {
		r.OnProposals(sess.id, sess.name, len(proposals))
	}
	return len(proposals), nil
}

func (r *Reviewer) readTranscript(sess *sessionInfo, offset int64) (string, int64, error) {
	f, err := os.Open(filepath.Join(r.streamsDir, sess.id+".jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", offset, nil
		}
		return "", offset, err
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() < offset {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", offset, err
	}
	p := session.GetProvider(session.ProviderName(sess.provider), "", "")
	br := bufio.NewReaderSize(f, 1<<20)
	var b strings.Builder
	pos := offset
	for {
		line, err := br.ReadBytes('\n')
		if len(line) == 0 || line[len(line)-1] != '\n' {
			break
		}
		pos += int64(len(line))
		for _, nl := range p.NormalizeStreamLine(line[:len(line)-1]) {
			for _, m := range search.ExtractMessages(nl) {
				who := "agent"
				if m.Role == "user" {
					who = "user"
				}
				fmt.Fprintf(&b, "[%s] %s\n\n", who, m.Text)
			}
		}
		if err != nil {
			break
		}
	}
	return tailRunes(b.String(), maxTranscriptRunes), pos, nil
}

func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…(前略)…\n" + string(r[len(r)-n:])
}

func headRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n…(後略)…"
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// MemoryDir returns Claude Code's auto-memory directory for a project.
func MemoryDir(homeDir, projectPath string) string {
	return filepath.Join(homeDir, ".claude", "projects", nonAlnum.ReplaceAllString(projectPath, "-"), "memory")
}

var frontmatterDesc = regexp.MustCompile(`(?m)^description:\s*(.+)$`)

func listSkills(projectPath string) string {
	dirs, _ := filepath.Glob(filepath.Join(projectPath, ".claude", "skills", "*", "SKILL.md"))
	var b strings.Builder
	for _, d := range dirs {
		name := filepath.Base(filepath.Dir(d))
		desc := ""
		if data, err := os.ReadFile(d); err == nil {
			if m := frontmatterDesc.FindSubmatch(data); m != nil {
				desc = headRunes(strings.TrimSpace(string(m[1])), 200)
			}
		}
		fmt.Fprintf(&b, "- %s: %s\n", name, desc)
	}
	if b.Len() == 0 {
		return "(なし)"
	}
	return b.String()
}

func readOr(path, fallback string, limit int) string {
	data, err := os.ReadFile(path)
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return fallback
	}
	return headRunes(string(data), limit)
}

func (r *Reviewer) buildPrompt(sess *sessionInfo, transcript string) (string, error) {
	rejected, err := r.store.Rejected(sess.projectPath, 20)
	if err != nil {
		return "", err
	}
	pending, err := r.store.Pending(sess.projectPath)
	if err != nil {
		return "", err
	}
	var prior strings.Builder
	for _, p := range rejected {
		fmt.Fprintf(&prior, "- [却下済み] (%s) %s: %s\n", p.Target, p.Title, headRunes(p.Content, 200))
	}
	for _, p := range pending {
		fmt.Fprintf(&prior, "- [提案済み・未処理] (%s) %s: %s\n", p.Target, p.Title, headRunes(p.Content, 200))
	}
	if prior.Len() == 0 {
		prior.WriteString("(なし)\n")
	}

	signalsPart := ""
	if r.proactiveOn() {
		signalsPart = signalsInstruction
	}
	return fmt.Sprintf(reviewPrompt,
		maxProposalsPerReview,
		signalsPart,
		sess.projectPath,
		readOr(filepath.Join(sess.projectPath, "CLAUDE.md"), "(CLAUDE.md なし)", maxClaudeMDRunes),
		listSkills(sess.projectPath),
		readOr(filepath.Join(MemoryDir(r.homeDir, sess.projectPath), "MEMORY.md"), "(メモリなし)", maxMemoryIndexRunes),
		prior.String(),
		transcript,
	), nil
}

const reviewPrompt = `あなたは、AI コーディングエージェントの作業セッションを振り返り、次回以降のセッションに役立つ「学び」を抽出するレビュアーです。
抽出した学びは人間が一件ずつ確認し、採用したものだけが CLAUDE.md・スキル・メモリに反映されます。

## 提案してよいもの
- 何度も繰り返す価値のある手順を確立した（→ skill）
- 失敗や試行錯誤の末に正しいやり方に辿り着いた（→ CLAUDE.md、skill の Pitfalls、または memory）
- ユーザーがエージェントのやり方を訂正した、好みを示した（→ memory、または CLAUDE.md）
- コードやgit履歴からは読み取れない、プロジェクト固有の前提や罠（→ memory、または CLAUDE.md）

## 書き方のルール
- "lessons, not logs": 事件の経緯、日付、PR 番号、セッション固有の値は書かない。次に同じ状況になったときに使える一般化したルールとして書く
- 1件は短く具体的に。CLAUDE.md なら数行、memory なら1つの事実
- 既存の CLAUDE.md・スキル・メモリにすでに書かれていることは提案しない
- 下の「過去の提案」と同じ趣旨のものは提案しない（却下済みのものは特に）
- コードを読めば分かること、一般常識、このセッションでしか意味がないことは提案しない
- 既存の文書と同じ言語で書く
- 本当に価値があるものだけ。無ければ空配列でよい。最大 %d 件

## 出力形式
JSON だけを出力する。前置きや説明、コードフェンスは不要。
{"proposals":[{"target":"claude_md|skill|memory","name":"skill名 または memory の slug（kebab-case）。claude_md では空","memory_type":"memory のときだけ: user|feedback|project|reference","title":"一行の要約","content":"追記する本文（Markdown）。skill を新規作成する場合は frontmatter を含まない本文","reason":"なぜ提案するか。根拠となったやりとりを短く引用"}]}

- target が skill の場合、name が既存スキルと同じなら既存の SKILL.md への追記、違えば新規スキルになる
- target が memory の場合、content は1つの事実。feedback/project なら **Why:** と **How to apply:** の行を付ける
%s
## プロジェクト
%s

## 既存の CLAUDE.md
<claude_md>
%s
</claude_md>

## 既存のスキル
%s

## 既存のメモリ（索引）
<memory_index>
%s
</memory_index>

## 過去の提案
%s
## セッションの会話
<transcript>
%s
</transcript>
`

var codeFence = regexp.MustCompile("(?s)^```(?:json)?\\s*(.*?)\\s*```$")

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

func slugify(s string) string {
	s = slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	return s
}

// ParseProposals parses and validates the reviewer output.
func ParseProposals(raw string) ([]*Proposal, error) {
	raw = extractJSON(raw)
	var out struct {
		Proposals []struct {
			Target     string `json:"target"`
			Name       string `json:"name"`
			MemoryType string `json:"memory_type"`
			Title      string `json:"title"`
			Content    string `json:"content"`
			Reason     string `json:"reason"`
		} `json:"proposals"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("parse reviewer output: %w", err)
	}
	var res []*Proposal
	for _, p := range out.Proposals {
		p.Content = strings.TrimSpace(p.Content)
		p.Title = strings.TrimSpace(p.Title)
		if p.Content == "" || p.Title == "" {
			continue
		}
		prop := &Proposal{Target: p.Target, Title: p.Title, Content: p.Content, Reason: strings.TrimSpace(p.Reason)}
		switch p.Target {
		case TargetClaudeMD:
		case TargetSkill:
			prop.Name = slugify(p.Name)
			if prop.Name == "" {
				continue
			}
		case TargetMemory:
			prop.Name = slugify(p.Name)
			if prop.Name == "" {
				prop.Name = slugify(p.Title)
			}
			if prop.Name == "" {
				prop.Name = "learning"
			}
			mt := p.MemoryType
			switch mt {
			case "user", "feedback", "project", "reference":
			default:
				mt = "project"
			}
			prop.MemoryType = mt
		default:
			continue
		}
		res = append(res, prop)
		if len(res) >= maxProposalsPerReview {
			break
		}
	}
	return res, nil
}

// Store returns the proposal store.
func (r *Reviewer) Store() *Store { return r.store }
