// Package suggestion defines suggestion rules: "in this situation, offer
// these choices". Rules are Markdown files with YAML frontmatter placed in
// <project>/.agmux/suggestions/ or ~/.agmux/suggestions/, plus a few built-in
// ones. Evaluation lives in the server package.
package suggestion

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Triggers.
const (
	TriggerGoalCompleted = "goal_completed"
	TriggerTurnEnded     = "turn_ended"
	TriggerSignalCluster = "signal_cluster"
	TriggerSessionIdle   = "session_idle"
)

// Detectors (built-in, used with TriggerSignalCluster).
const (
	DetectorRepeatedCorrection = "repeated_correction"
	DetectorRepeatedTask       = "repeated_task"
)

// Targets.
const (
	TargetSession     = "session"
	TargetCoordinator = "coordinator"
)

// Rule sources, in increasing priority.
const (
	SourceBuiltin = "builtin"
	SourceGlobal  = "global"
	SourceProject = "project"
)

// Choice actions allowed in rule files.
const (
	ActionSendMessage = "send_message"
	ActionDismiss     = "dismiss"
	ActionAPI         = "api"
)

const (
	DefaultCooldown  = 24 * time.Hour
	DefaultIdleAfter = 30 * time.Minute
)

// CheckGitDirty is an internal, model-free condition of built-in rules.
const CheckGitDirty = "git_dirty"

// Handler names of the built-in api actions.
const (
	HandlerAcceptProposal = "suggestion.accept_proposal"
	HandlerRejectProposal = "suggestion.reject_proposal"
)

// Choice is one button of a rule's card.
type Choice struct {
	Label   string `yaml:"label" json:"label"`
	Message string `yaml:"message,omitempty" json:"message,omitempty"`
	Action  string `yaml:"action,omitempty" json:"action,omitempty"`
	Handler string `yaml:"handler,omitempty" json:"handler,omitempty"`
}

// Rule is one suggestion rule.
type Rule struct {
	ID        string        `json:"id"`
	Trigger   string        `json:"trigger"`
	Detector  string        `json:"detector,omitempty"`
	Cooldown  time.Duration `json:"-"`
	IdleAfter time.Duration `json:"-"` // session_idle only
	Target    string        `json:"target"`
	Choices   []Choice      `json:"choices"`
	// Condition is the natural-language condition judged by a cheap model.
	Condition string `json:"condition,omitempty"`
	// Question is the card's prompt. {summary} and {count} are replaced for
	// signal_cluster events.
	Question string `json:"question"`
	// Check replaces the model judgement with an internal check (built-in only).
	Check string `json:"-"`

	Source string `json:"source"` // builtin | global | project
	Path   string `json:"path,omitempty"`
}

type frontmatter struct {
	ID        string   `yaml:"id"`
	Trigger   string   `yaml:"trigger"`
	Detector  string   `yaml:"detector"`
	Cooldown  string   `yaml:"cooldown"`
	IdleAfter string   `yaml:"idle_after"`
	Target    string   `yaml:"target"`
	Choices   []Choice `yaml:"choices"`
}

var sectionRe = regexp.MustCompile(`^(条件(?:（自然文）|\(自然文\))?|問いかけ)\s*[:：]\s*(.*)$`)

var validID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ParseDuration is time.ParseDuration plus a "d" (days) suffix.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return d, nil
}

// Parse parses a rule file. fallbackID is used when the frontmatter has no id.
func Parse(data []byte, fallbackID, source, path string) (*Rule, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil, fmt.Errorf("frontmatter (---) not found")
	}
	rest := text[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, fmt.Errorf("frontmatter is not closed")
	}
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
		return nil, fmt.Errorf("invalid frontmatter: %w", err)
	}
	body := strings.TrimPrefix(rest[end+4:], "\n")

	r := &Rule{
		ID: strings.TrimSpace(fm.ID), Trigger: strings.TrimSpace(fm.Trigger),
		Detector: strings.TrimSpace(fm.Detector), Target: strings.TrimSpace(fm.Target),
		Choices: fm.Choices, Source: source, Path: path,
		Cooldown: DefaultCooldown, IdleAfter: DefaultIdleAfter,
	}
	if r.ID == "" {
		r.ID = fallbackID
	}
	if strings.TrimSpace(fm.Cooldown) != "" {
		d, err := ParseDuration(fm.Cooldown)
		if err != nil {
			return nil, fmt.Errorf("cooldown: %w", err)
		}
		r.Cooldown = d
	}
	if strings.TrimSpace(fm.IdleAfter) != "" {
		d, err := ParseDuration(fm.IdleAfter)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("idle_after: invalid duration %q", fm.IdleAfter)
		}
		r.IdleAfter = d
	}

	// Body sections: "条件（自然文）: ..." and "問いかけ: ...", each may span lines.
	var cur *strings.Builder
	var cond, q strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if m := sectionRe.FindStringSubmatch(line); m != nil {
			if strings.HasPrefix(m[1], "条件") {
				cur = &cond
			} else {
				cur = &q
			}
			cur.WriteString(m[2])
			cur.WriteString("\n")
			continue
		}
		if cur != nil {
			cur.WriteString(line)
			cur.WriteString("\n")
		}
	}
	r.Condition, r.Question = strings.TrimSpace(cond.String()), strings.TrimSpace(q.String())

	if err := r.Validate(); err != nil {
		return nil, err
	}
	return r, nil
}

// Validate fills defaults and checks the rule.
func (r *Rule) Validate() error {
	if !validID.MatchString(r.ID) {
		return fmt.Errorf("invalid id %q (use letters, digits, - and _)", r.ID)
	}
	switch r.Detector {
	case "":
	case DetectorRepeatedCorrection, DetectorRepeatedTask:
		if r.Trigger == "" {
			r.Trigger = TriggerSignalCluster
		}
		if r.Trigger != TriggerSignalCluster {
			return fmt.Errorf("detector %s needs trigger %s", r.Detector, TriggerSignalCluster)
		}
	default:
		return fmt.Errorf("unknown detector %q", r.Detector)
	}
	switch r.Trigger {
	case TriggerGoalCompleted, TriggerTurnEnded, TriggerSessionIdle:
	case TriggerSignalCluster:
		if r.Detector == "" {
			return fmt.Errorf("trigger %s needs a detector", TriggerSignalCluster)
		}
	case "":
		return fmt.Errorf("trigger is required")
	default:
		return fmt.Errorf("unknown trigger %q", r.Trigger)
	}
	switch r.Target {
	case "":
		r.Target = TargetSession
	case TargetSession, TargetCoordinator:
	default:
		return fmt.Errorf("unknown target %q", r.Target)
	}
	if len(r.Choices) < 2 || len(r.Choices) > 4 {
		return fmt.Errorf("choices must have 2 to 4 items")
	}
	for i := range r.Choices {
		c := &r.Choices[i]
		if strings.TrimSpace(c.Label) == "" {
			return fmt.Errorf("choices[%d]: label is required", i)
		}
		switch c.Action {
		case "":
			c.Action = ActionSendMessage
		case ActionSendMessage, ActionDismiss:
		case ActionAPI:
			if c.Handler == "" {
				return fmt.Errorf("choices[%d]: api action needs a handler", i)
			}
		default:
			return fmt.Errorf("choices[%d]: unknown action %q", i, c.Action)
		}
	}
	if r.Question == "" {
		return fmt.Errorf("問いかけ is required")
	}
	if r.Detector == "" && r.Check == "" && r.Condition == "" {
		return fmt.Errorf("条件 is required (or specify a detector)")
	}
	return nil
}

// LoadDir reads *.md rules from dir. A missing directory yields no rules.
// Files that fail to parse are reported in errs and skipped.
func LoadDir(dir, source string) (rules []*Rule, errs []error) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	sort.Strings(files)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f, err))
			continue
		}
		stem := strings.TrimSuffix(filepath.Base(f), ".md")
		r, err := Parse(data, stem, source, f)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f, err))
			continue
		}
		rules = append(rules, r)
	}
	return rules, errs
}

// Merge combines rule layers; later layers override earlier ones with the
// same id (so pass builtin, global, project). The result is ordered by id.
func Merge(layers ...[]*Rule) []*Rule {
	byID := map[string]*Rule{}
	for _, l := range layers {
		for _, r := range l {
			byID[r.ID] = r
		}
	}
	out := make([]*Rule, 0, len(byID))
	for _, r := range byID {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// GlobalDir is ~/.agmux/suggestions.
func GlobalDir(home string) string { return filepath.Join(home, ".agmux", "suggestions") }

// ProjectDir is <project>/.agmux/suggestions.
func ProjectDir(project string) string { return filepath.Join(project, ".agmux", "suggestions") }

// Builtins returns the built-in rules.
func Builtins() []*Rule {
	return []*Rule{
		{
			ID: "write-to-harness", Trigger: TriggerSignalCluster, Detector: DetectorRepeatedCorrection,
			Cooldown: 0, // one card per proposal; the cluster is only proposed once
			Target:   TargetSession, Source: SourceBuiltin,
			Condition: "複数のセッションで同じ趣旨の訂正が繰り返された",
			Question:  "何回も指摘されている『{summary}』をハーネスに書きますか？",
			Choices: []Choice{
				{Label: "ハーネスに書く", Action: ActionAPI, Handler: HandlerAcceptProposal},
				{Label: "却下する", Action: ActionAPI, Handler: HandlerRejectProposal},
				{Label: "あとで", Action: ActionDismiss},
			},
		},
		{
			ID: "make-loop", Trigger: TriggerSignalCluster, Detector: DetectorRepeatedTask,
			Cooldown: 0,
			Target:   TargetSession, Source: SourceBuiltin,
			Condition: "同じ種類の作業を手で何度も依頼している",
			Question:  "『{summary}』を{count}回手で頼んでいます。ループにしますか？",
			Choices: []Choice{
				{Label: "ループにする", Action: ActionAPI, Handler: HandlerAcceptProposal},
				{Label: "却下する", Action: ActionAPI, Handler: HandlerRejectProposal},
				{Label: "あとで", Action: ActionDismiss},
			},
		},
		{
			ID: "commit-before-leave", Trigger: TriggerGoalCompleted, Check: CheckGitDirty,
			Cooldown: DefaultCooldown, Target: TargetSession, Source: SourceBuiltin,
			Condition: "作業ツリーに未コミットの変更が残ったままゴールが完了した",
			Question:  "変更がコミットされずに残っています。どうしますか？",
			Choices: []Choice{
				{Label: "コミットして PR を出す", Message: "変更をコミットして PR を出して", Action: ActionSendMessage},
				{Label: "今はいい", Action: ActionDismiss},
			},
		},
	}
}
