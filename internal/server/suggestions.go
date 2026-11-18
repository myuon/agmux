package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/myuon/agmux/internal/experimental"
	"github.com/myuon/agmux/internal/learning"
	"github.com/myuon/agmux/internal/session"
	"github.com/myuon/agmux/internal/suggestion"
)

// Suggestion rules (#815): "in this situation, offer these choices". They run
// only while both proactive_suggestions and choice_cards are on.

const (
	// conditionTailRunes is how much of the conversation end the condition
	// judgement sees.
	conditionTailRunes = 4000
	// dismissCooldownFactor: a dismissed card silences its rule for this many
	// cooldowns.
	dismissCooldownFactor = 7
	suggestJudgeTimeout   = 2 * time.Minute
	suggestIdleTick       = time.Minute
	suggestIdleMaxAge     = 24 * time.Hour
	recentCardsWindow     = 7 * 24 * time.Hour
)

type suggestState struct {
	mu       sync.Mutex
	prev     map[string]session.Status // last seen status per session
	idleDone map[string]time.Time      // sessionID|ruleID -> idle episode (updated_at) already evaluated
	inflight map[string]bool           // ruleID|scope being evaluated

	// Injectable for tests; nil means the real implementation.
	generate learning.Generator
	tail     func(sessionID string, maxRunes int) string
	gitDirty func(dir string) (bool, error)
	home     func() string
}

// suggestionsEnabled reports whether rule evaluation and cards may run.
func suggestionsEnabled() bool {
	return experimental.Enabled(learning.ProactiveFeatureName) && experimental.Enabled(FeatureChoiceCards)
}

// suggestEvent is a situation that rules are matched against.
type suggestEvent struct {
	Trigger   string
	SessionID string
	Detector  string // signal_cluster only
	Summary   string
	Count     int
	Proposal  int64  // learning proposal id (signal_cluster)
	Report    string // goal completion report
}

func (s *Server) initSuggestions() {
	s.RegisterChoiceHandler(suggestion.HandlerAcceptProposal, func(ctx context.Context, card ChoiceCard, choice Choice) error {
		return s.decideProposalFromCard(choice, true)
	})
	s.RegisterChoiceHandler(suggestion.HandlerRejectProposal, func(ctx context.Context, card ChoiceCard, choice Choice) error {
		return s.decideProposalFromCard(choice, false)
	})
	if s.sqlDB != nil {
		if _, err := s.sqlDB.Exec(`
			CREATE TABLE IF NOT EXISTS suggestion_log (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				rule_id TEXT NOT NULL,
				scope TEXT NOT NULL,
				card_id TEXT NOT NULL,
				created_at TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_suggestion_log_rule ON suggestion_log(rule_id, scope, id);
		`); err != nil {
			s.logger.Warn("suggestions: init table failed", "error", err)
			return
		}
	}
	go s.suggestIdleLoop(context.Background())
}

// decideProposalFromCard runs the existing accept / reject of a learning
// proposal for a built-in card button. It is idempotent: a proposal already
// decided in the inbox is left alone.
func (s *Server) decideProposalFromCard(choice Choice, accept bool) error {
	if s.learning == nil {
		return fmt.Errorf("learning is not available")
	}
	var params struct {
		ProposalID int64 `json:"proposalId"`
	}
	if err := json.Unmarshal(choice.Action.Params, &params); err != nil || params.ProposalID == 0 {
		return fmt.Errorf("proposalId is missing")
	}
	p, err := s.learning.Store().Get(params.ProposalID)
	if err != nil {
		return err
	}
	if p.Status != learning.StatusPending {
		return nil
	}
	if accept {
		_, err = s.learning.Accept(params.ProposalID, "")
	} else {
		_, err = s.learning.Reject(params.ProposalID)
	}
	return err
}

func (s *Server) suggestHome() string {
	if s.suggest.home != nil {
		return s.suggest.home()
	}
	h, _ := os.UserHomeDir()
	return h
}

// rulesFor returns the effective rules for a project: built-in, then global,
// then project (later ones win on the same id).
func (s *Server) rulesFor(projectPath string) ([]*suggestion.Rule, []error) {
	var errs []error
	global, e := suggestion.LoadDir(suggestion.GlobalDir(s.suggestHome()), suggestion.SourceGlobal)
	errs = append(errs, e...)
	var project []*suggestion.Rule
	if projectPath != "" {
		project, e = suggestion.LoadDir(suggestion.ProjectDir(projectPath), suggestion.SourceProject)
		errs = append(errs, e...)
	}
	return suggestion.Merge(suggestion.Builtins(), global, project), errs
}

// fireSuggestions evaluates the rules for an event and returns the number of
// cards created. It does nothing unless the feature is on.
func (s *Server) fireSuggestions(ev suggestEvent) int {
	if !suggestionsEnabled() || s.sqlDB == nil {
		return 0
	}
	sess, err := s.sessions.Get(ev.SessionID)
	if err != nil || sess == nil || sess.Type == session.TypeController || sess.Type == session.TypeExternal {
		return 0
	}
	rules, errs := s.rulesFor(sess.ProjectPath)
	for _, e := range errs {
		s.logger.Warn("suggestions: invalid rule", "error", e)
	}
	n := 0
	for _, rule := range rules {
		if rule.Trigger != ev.Trigger || rule.Detector != ev.Detector {
			continue
		}
		if s.evaluateRule(rule, sess, ev) {
			n++
		}
	}
	return n
}

// resolveTarget decides which session gets the card and the cooldown scope.
func (s *Server) resolveTarget(rule *suggestion.Rule, sess *session.Session) (targetID, scope string) {
	if rule.Target == suggestion.TargetCoordinator && projectsEnabled() {
		if m, err := s.loadProjectModel(); err == nil {
			if p, ok := m.byID[sess.ID]; ok {
				path := m.sessionPath(p)
				if coord := m.coordinator[path]; coord != "" {
					return coord, "project:" + path
				}
			}
		}
	}
	return sess.ID, sess.ID
}

func (s *Server) evaluateRule(rule *suggestion.Rule, sess *session.Session, ev suggestEvent) bool {
	targetID, scope := s.resolveTarget(rule, sess)
	key := rule.ID + "|" + scope
	s.suggest.mu.Lock()
	if s.suggest.inflight == nil {
		s.suggest.inflight = map[string]bool{}
	}
	if s.suggest.inflight[key] {
		s.suggest.mu.Unlock()
		return false
	}
	s.suggest.inflight[key] = true
	s.suggest.mu.Unlock()
	defer func() {
		s.suggest.mu.Lock()
		delete(s.suggest.inflight, key)
		s.suggest.mu.Unlock()
	}()

	if s.suggestionBlocked(rule, scope) {
		return false
	}
	if !s.ruleMatches(rule, sess, ev) {
		return false
	}

	card, err := s.CreateChoiceCard(ChoiceCardInput{
		SessionID: targetID,
		Prompt:    renderQuestion(rule.Question, ev),
		Choices:   buildRuleChoices(rule, ev),
		Source:    "rule",
		RuleID:    rule.ID,
	})
	if err != nil {
		s.logger.Warn("suggestions: create card failed", "rule", rule.ID, "error", err)
		return false
	}
	if _, err := s.sqlDB.Exec(`INSERT INTO suggestion_log (rule_id, scope, card_id, created_at) VALUES (?, ?, ?, ?)`,
		rule.ID, scope, card.ID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		s.logger.Warn("suggestions: log failed", "rule", rule.ID, "error", err)
	}
	return true
}

func renderQuestion(q string, ev suggestEvent) string {
	q = strings.ReplaceAll(q, "{summary}", ev.Summary)
	return strings.ReplaceAll(q, "{count}", fmt.Sprint(ev.Count))
}

func buildRuleChoices(rule *suggestion.Rule, ev suggestEvent) []Choice {
	out := make([]Choice, len(rule.Choices))
	for i, c := range rule.Choices {
		ch := Choice{Label: c.Label}
		switch c.Action {
		case suggestion.ActionDismiss:
			ch.Action = ChoiceAction{Type: ActionDismiss}
		case suggestion.ActionAPI:
			ch.Action = ChoiceAction{Type: ActionAPI, Handler: c.Handler}
			if ev.Proposal != 0 {
				ch.Action.Params, _ = json.Marshal(map[string]int64{"proposalId": ev.Proposal})
			}
		default:
			ch.Action = ChoiceAction{Type: ActionSendMessage, Text: c.Message}
		}
		out[i] = ch
	}
	return out
}

// suggestionBlocked applies the cooldown and the dismissal record: a rule is
// not shown again for the same scope while an earlier card is within the
// cooldown, still open, or was dismissed less than dismissCooldownFactor
// cooldowns ago. A zero cooldown disables all of it.
func (s *Server) suggestionBlocked(rule *suggestion.Rule, scope string) bool {
	if rule.Cooldown <= 0 {
		return false
	}
	rows, err := s.sqlDB.Query(
		`SELECT l.created_at, COALESCE(c.status, ''), COALESCE(c.answered_at, '')
		 FROM suggestion_log l LEFT JOIN choice_cards c ON c.id = l.card_id
		 WHERE l.rule_id = ? AND l.scope = ? ORDER BY l.id DESC LIMIT 20`, rule.ID, scope)
	if err != nil {
		return false
	}
	defer rows.Close()
	now := time.Now()
	for rows.Next() {
		var created, status, answered string
		if err := rows.Scan(&created, &status, &answered); err != nil {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			continue
		}
		if now.Sub(at) < rule.Cooldown || status == CardOpen {
			return true
		}
		if status == CardDismissed {
			from := at
			if t, err := time.Parse(time.RFC3339Nano, answered); err == nil {
				from = t
			}
			if now.Sub(from) < rule.Cooldown*dismissCooldownFactor {
				return true
			}
		}
	}
	return false
}

// ruleMatches judges the rule's condition: an internal check, a built-in
// detector (already decided by the event) or a cheap model.
func (s *Server) ruleMatches(rule *suggestion.Rule, sess *session.Session, ev suggestEvent) bool {
	if rule.Detector != "" {
		return true
	}
	switch rule.Check {
	case suggestion.CheckGitDirty:
		check := s.suggest.gitDirty
		if check == nil {
			check = gitDirty
		}
		dirty, err := check(sess.ProjectPath)
		return err == nil && dirty
	case "":
	default:
		return false
	}

	gen := s.suggest.generate
	model := learning.DefaultModel
	if gen == nil {
		if s.learning == nil {
			return false
		}
		gen = s.learning.Generate
	}
	if s.learning != nil && s.learning.Model != nil {
		model = s.learning.Model()
	}
	tail := ""
	if s.suggest.tail != nil {
		tail = s.suggest.tail(sess.ID, conditionTailRunes)
	} else if s.learning != nil {
		tail, _ = s.learning.TranscriptTail(sess.ID, conditionTailRunes)
	}
	ctx, cancel := context.WithTimeout(context.Background(), suggestJudgeTimeout)
	defer cancel()
	raw, err := gen(ctx, model, conditionPrompt(rule, sess, ev, tail))
	if err != nil {
		s.logger.Warn("suggestions: condition judgement failed", "rule", rule.ID, "error", err)
		return false
	}
	return parseMatch(raw)
}

func conditionPrompt(rule *suggestion.Rule, sess *session.Session, ev suggestEvent, tail string) string {
	what := map[string]string{
		suggestion.TriggerGoalCompleted: "セッションのゴールが完了した",
		suggestion.TriggerTurnEnded:     "エージェントのターンが終わった",
		suggestion.TriggerSessionIdle:   "セッションがしばらく止まっている",
	}[rule.Trigger]
	report := ""
	if strings.TrimSpace(ev.Report) != "" {
		report = "\n完了報告: " + ev.Report
	}
	return fmt.Sprintf(`次の「条件」が、下の「状況」に当てはまるかを判定してください。迷う場合や情報が足りない場合は false にしてください。

## 条件
%s

## 状況
きっかけ: %s
セッション名: %s
プロジェクト: %s%s

## 会話の末尾
%s

JSON だけを出力する（前置き・コードフェンス不要）。
{"match": true または false, "reason": "一文"}
`, rule.Condition, what, sess.Name, sess.ProjectPath, report, tail)
}

func parseMatch(raw string) bool {
	raw = strings.TrimSpace(raw)
	i, j := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if i < 0 || j < i {
		return false
	}
	var out struct {
		Match bool `json:"match"`
	}
	if err := json.Unmarshal([]byte(raw[i:j+1]), &out); err != nil {
		return false
	}
	return out.Match
}

func gitDirty(dir string) (bool, error) {
	if dir == "" {
		return false, fmt.Errorf("no directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// --- triggers ---

// suggestGoalCompleted is called when a session's goal stack was emptied.
func (s *Server) suggestGoalCompleted(sessionID, report string) {
	if !suggestionsEnabled() {
		return
	}
	go s.fireSuggestions(suggestEvent{Trigger: suggestion.TriggerGoalCompleted, SessionID: sessionID, Report: report})
}

// suggestStatusChange detects the end of a turn (working -> idle).
func (s *Server) suggestStatusChange(sessionID string, status session.Status) {
	if !suggestionsEnabled() {
		return
	}
	s.suggest.mu.Lock()
	if s.suggest.prev == nil {
		s.suggest.prev = map[string]session.Status{}
	}
	prev := s.suggest.prev[sessionID]
	s.suggest.prev[sessionID] = status
	s.suggest.mu.Unlock()
	if prev != session.StatusWorking || status != session.StatusIdle {
		return
	}
	go func() {
		// Without any turn_ended rule nothing is evaluated (and no model call).
		sess, err := s.sessions.Get(sessionID)
		if err != nil || sess == nil {
			return
		}
		if !hasTrigger(s.rulesFor(sess.ProjectPath)) {
			return
		}
		s.fireSuggestions(suggestEvent{Trigger: suggestion.TriggerTurnEnded, SessionID: sessionID})
	}()
}

func hasTrigger(rules []*suggestion.Rule, _ []error) bool {
	for _, r := range rules {
		if r.Trigger == suggestion.TriggerTurnEnded {
			return true
		}
	}
	return false
}

// suggestSignalCluster is called when a repeated pattern became a learning
// proposal. It reports whether a card was shown (which already notifies).
func (s *Server) suggestSignalCluster(p *learning.Proposal, summary string, n int) bool {
	if !suggestionsEnabled() {
		return false
	}
	detector := suggestion.DetectorRepeatedCorrection
	if p.Origin == learning.OriginRepeatedTask {
		detector = suggestion.DetectorRepeatedTask
	}
	return s.fireSuggestions(suggestEvent{
		Trigger: suggestion.TriggerSignalCluster, SessionID: p.SessionID,
		Detector: detector, Summary: summary, Count: n, Proposal: p.ID,
	}) > 0
}

func (s *Server) suggestIdleLoop(ctx context.Context) {
	t := time.NewTicker(suggestIdleTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.suggestIdleScan(time.Now())
		}
	}
}

// suggestIdleScan evaluates session_idle rules for sessions that have been
// idle for longer than the rule's idle_after, once per idle episode.
func (s *Server) suggestIdleScan(now time.Time) {
	if !suggestionsEnabled() || s.sessions == nil {
		return
	}
	list, err := s.sessions.List()
	if err != nil {
		return
	}
	byProject := map[string][]*suggestion.Rule{}
	for i := range list {
		sess := &list[i]
		if sess.Status != session.StatusIdle || sess.Type != session.TypeWorker || now.Sub(sess.UpdatedAt) > suggestIdleMaxAge {
			continue
		}
		rules, ok := byProject[sess.ProjectPath]
		if !ok {
			rules, _ = s.rulesFor(sess.ProjectPath)
			byProject[sess.ProjectPath] = rules
		}
		for _, rule := range rules {
			if rule.Trigger != suggestion.TriggerSessionIdle || now.Sub(sess.UpdatedAt) < rule.IdleAfter {
				continue
			}
			k := sess.ID + "|" + rule.ID
			s.suggest.mu.Lock()
			if s.suggest.idleDone == nil {
				s.suggest.idleDone = map[string]time.Time{}
			}
			done := s.suggest.idleDone[k].Equal(sess.UpdatedAt)
			s.suggest.idleDone[k] = sess.UpdatedAt
			s.suggest.mu.Unlock()
			if done {
				continue
			}
			s.evaluateRule(rule, sess, suggestEvent{Trigger: suggestion.TriggerSessionIdle, SessionID: sess.ID})
		}
	}
}

// --- settings list ---

type suggestionRuleInfo struct {
	*suggestion.Rule
	CooldownText string `json:"cooldown"`
	ProjectPath  string `json:"projectPath,omitempty"`
	RecentCards  int    `json:"recentCards"`
}

func (s *Server) listSuggestionRules(w http.ResponseWriter, r *http.Request) {
	if !suggestionsEnabled() {
		writeError(w, http.StatusNotFound, "proactive_suggestions and choice_cards must both be enabled")
		return
	}
	counts := map[string]int{}
	total := 0
	if s.sqlDB != nil {
		since := time.Now().Add(-recentCardsWindow).UTC().Format(time.RFC3339Nano)
		if rows, err := s.sqlDB.Query(`SELECT rule_id, COUNT(*) FROM suggestion_log WHERE created_at >= ? GROUP BY rule_id`, since); err == nil {
			for rows.Next() {
				var id string
				var n int
				if rows.Scan(&id, &n) == nil {
					counts[id] = n
					total += n
				}
			}
			rows.Close()
		}
	}
	var infos []suggestionRuleInfo
	errs := []string{}
	add := func(rules []*suggestion.Rule, projectPath string) {
		for _, rule := range rules {
			infos = append(infos, suggestionRuleInfo{Rule: rule, CooldownText: rule.Cooldown.String(), ProjectPath: projectPath, RecentCards: counts[rule.ID]})
		}
	}
	add(suggestion.Builtins(), "")
	global, e := suggestion.LoadDir(suggestion.GlobalDir(s.suggestHome()), suggestion.SourceGlobal)
	add(global, "")
	for _, err := range e {
		errs = append(errs, err.Error())
	}
	for _, path := range s.knownProjectPaths() {
		rules, e := suggestion.LoadDir(suggestion.ProjectDir(path), suggestion.SourceProject)
		add(rules, path)
		for _, err := range e {
			errs = append(errs, err.Error())
		}
	}
	if infos == nil {
		infos = []suggestionRuleInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"rules": infos, "errors": errs, "recentCards": total, "windowDays": int(recentCardsWindow / (24 * time.Hour)),
	})
}

// knownProjectPaths lists the project directories of known sessions that have
// a suggestions directory.
func (s *Server) knownProjectPaths() []string {
	if s.sqlDB == nil {
		return nil
	}
	rows, err := s.sqlDB.Query(`SELECT DISTINCT project_path FROM sessions WHERE project_path != '' AND type != 'controller'`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if rows.Scan(&p) != nil {
			continue
		}
		if st, err := os.Stat(filepath.Join(p, ".agmux", "suggestions")); err == nil && st.IsDir() {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
