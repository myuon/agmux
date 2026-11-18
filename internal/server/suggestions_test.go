package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/experimental"
	"github.com/myuon/agmux/internal/learning"
	"github.com/myuon/agmux/internal/session"
	"github.com/myuon/agmux/internal/suggestion"
)

// dbSessions is a SessionService stub backed by the sessions table.
type dbSessions struct {
	session.SessionService
	s    *Server
	mu   sync.Mutex
	sent []string
}

func (d *dbSessions) Get(id string) (*session.Session, error) {
	var x session.Session
	var status, typ string
	err := d.s.sqlDB.QueryRow(`SELECT id, name, project_path, status, type, updated_at FROM sessions WHERE id = ?`, id).
		Scan(&x.ID, &x.Name, &x.ProjectPath, &status, &typ, &x.UpdatedAt)
	x.Status, x.Type = session.Status(status), session.SessionType(typ)
	return &x, err
}

func (d *dbSessions) List() ([]session.Session, error) {
	rows, err := d.s.sqlDB.Query(`SELECT id FROM sessions`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	var out []session.Session
	for _, id := range ids {
		x, err := d.Get(id)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
	}
	return out, nil
}

func (d *dbSessions) GetStreamLinesBefore(id string, before, limit int) ([]string, int, int, error) {
	return nil, 0, 3, nil
}

func (d *dbSessions) SendKeysWithImages(id, text string, images []session.ImageData) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sent = append(d.sent, id+":"+text)
	return nil
}

func (d *dbSessions) UpdateStatus(id string, status session.Status) error { return nil }

type suggestEnv struct {
	s       *Server
	home    string
	project string
	cmu     sync.Mutex
	calls   int
	prompts []string
	answer  string // generator output
	dirty   bool
	sess    *dbSessions
}

func newSuggestEnv(t *testing.T, features ...string) *suggestEnv {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	s := newAutomationTestServer(t)
	e := &suggestEnv{s: s, home: t.TempDir(), project: t.TempDir(), answer: `{"match": true}`}
	s.hub = NewHub(s.logger)
	e.sess = &dbSessions{s: s}
	s.sessions = e.sess
	s.suggest.home = func() string { return e.home }
	s.suggest.generate = func(ctx context.Context, model, prompt string) (string, error) {
		e.cmu.Lock()
		defer e.cmu.Unlock()
		e.calls++
		e.prompts = append(e.prompts, prompt)
		return e.answer, nil
	}
	s.suggest.tail = func(id string, n int) string { return "TAIL-OF-CONVERSATION" }
	s.suggest.gitDirty = func(dir string) (bool, error) { return e.dirty, nil }
	for _, f := range features {
		if err := experimental.Set(f, true); err != nil {
			t.Fatal(err)
		}
	}
	s.initSuggestions()
	addSess(t, s, "s1", e.project, 5, nil)
	return e
}

func (e *suggestEnv) on(t *testing.T) {
	t.Helper()
	for _, f := range []string{learning.ProactiveFeatureName, FeatureChoiceCards} {
		if err := experimental.Set(f, true); err != nil {
			t.Fatal(err)
		}
	}
}

func writeRule(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const ruleFile = `---
id: %ID%
trigger: %TRIGGER%
cooldown: 1h
choices:
  - label: やる
    message: やって
  - label: 今はいい
    action: dismiss
---
条件（自然文）: 条件です
問いかけ: %Q%
`

func rule(id, trigger, q string) string {
	return strings.NewReplacer("%ID%", id, "%TRIGGER%", trigger, "%Q%", q).Replace(ruleFile)
}

func (e *suggestEnv) cards(t *testing.T) []ChoiceCard {
	t.Helper()
	cs, err := e.s.queryChoiceCards(``)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestSuggestion_GoalCompletedRuleMakesCard(t *testing.T) {
	e := newSuggestEnv(t)
	e.on(t)
	writeRule(t, suggestion.ProjectDir(e.project), "r.md", rule("my-rule", "goal_completed", "どうしますか"))

	if n := e.s.fireSuggestions(suggestEvent{Trigger: "goal_completed", SessionID: "s1", Report: "REPORT-TEXT"}); n != 1 {
		t.Fatalf("fired %d", n)
	}
	cs := e.cards(t)
	if len(cs) != 1 || cs[0].Source != "rule" || cs[0].RuleID != "my-rule" || cs[0].SessionID != "s1" || cs[0].Prompt != "どうしますか" {
		t.Fatalf("cards = %+v", cs)
	}
	if cs[0].Choices[0].Action.Type != ActionSendMessage || cs[0].Choices[0].Action.Text != "やって" || cs[0].Choices[1].Action.Type != ActionDismiss {
		t.Errorf("choices = %+v", cs[0].Choices)
	}
	if len(e.prompts) != 1 || !strings.Contains(e.prompts[0], "TAIL-OF-CONVERSATION") || !strings.Contains(e.prompts[0], "条件です") || !strings.Contains(e.prompts[0], "REPORT-TEXT") {
		t.Errorf("judge prompt = %q", e.prompts)
	}
}

func TestSuggestion_ConditionMiss(t *testing.T) {
	e := newSuggestEnv(t)
	e.on(t)
	writeRule(t, suggestion.ProjectDir(e.project), "r.md", rule("my-rule", "goal_completed", "q"))
	e.answer = "```json\n{\"match\": false, \"reason\": \"no\"}\n```"
	if n := e.s.fireSuggestions(suggestEvent{Trigger: "goal_completed", SessionID: "s1"}); n != 0 {
		t.Fatalf("fired %d", n)
	}
	e.answer = "garbage"
	if n := e.s.fireSuggestions(suggestEvent{Trigger: "goal_completed", SessionID: "s1"}); n != 0 {
		t.Fatalf("fired %d on garbage", n)
	}
	if len(e.cards(t)) != 0 {
		t.Error("unexpected card")
	}
}

func TestSuggestion_ProjectOverridesGlobal(t *testing.T) {
	e := newSuggestEnv(t)
	e.on(t)
	writeRule(t, suggestion.GlobalDir(e.home), "r.md", rule("same", "goal_completed", "GLOBAL"))
	writeRule(t, suggestion.GlobalDir(e.home), "only-global.md", rule("only-global", "turn_ended", "G2"))
	writeRule(t, suggestion.ProjectDir(e.project), "r.md", rule("same", "goal_completed", "PROJECT"))

	e.s.fireSuggestions(suggestEvent{Trigger: "goal_completed", SessionID: "s1"})
	cs := e.cards(t)
	if len(cs) != 1 || cs[0].Prompt != "PROJECT" {
		t.Fatalf("cards = %+v", cs)
	}
	rules, _ := e.s.rulesFor(e.project)
	var ids []string
	for _, r := range rules {
		ids = append(ids, r.ID+":"+r.Source)
	}
	got := strings.Join(ids, ",")
	if !strings.Contains(got, "same:project") || !strings.Contains(got, "only-global:global") || !strings.Contains(got, "commit-before-leave:builtin") {
		t.Errorf("rules = %s", got)
	}
}

func TestSuggestion_TurnEndedOnlyWithRules(t *testing.T) {
	e := newSuggestEnv(t)
	e.on(t)
	getCalls := func() int { e.cmu.Lock(); defer e.cmu.Unlock(); return e.calls }
	waitCalls := func(want int) bool {
		for i := 0; i < 100; i++ {
			if getCalls() == want {
				return true
			}
			time.Sleep(20 * time.Millisecond)
		}
		return false
	}

	// No turn_ended rule: the model is never called.
	e.s.suggestStatusChange("s1", session.StatusWorking)
	e.s.suggestStatusChange("s1", session.StatusIdle)
	time.Sleep(200 * time.Millisecond)
	if getCalls() != 0 {
		t.Fatalf("model called %d times without turn_ended rules", getCalls())
	}

	writeRule(t, suggestion.ProjectDir(e.project), "t.md", rule("turn", "turn_ended", "ターン終了"))
	// idle without a preceding working is not a turn end.
	e.s.suggestStatusChange("s1", session.StatusIdle)
	time.Sleep(100 * time.Millisecond)
	if getCalls() != 0 {
		t.Fatalf("model called on idle->idle")
	}
	e.s.suggestStatusChange("s1", session.StatusWorking)
	e.s.suggestStatusChange("s1", session.StatusIdle)
	if !waitCalls(1) {
		t.Fatalf("model calls = %d, want 1", getCalls())
	}
	for i := 0; i < 100 && len(e.cards(t)) == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if cs := e.cards(t); len(cs) != 1 || cs[0].RuleID != "turn" {
		t.Fatalf("cards = %+v", cs)
	}
}

func TestSuggestion_SessionIdle(t *testing.T) {
	e := newSuggestEnv(t)
	e.on(t)
	writeRule(t, suggestion.ProjectDir(e.project), "i.md", strings.Replace(rule("idle-rule", "session_idle", "止まっています"), "cooldown: 1h", "cooldown: 1h\nidle_after: 10m", 1))

	// updated 5 minutes ago: not idle long enough.
	e.s.suggestIdleScan(time.Now())
	if len(e.cards(t)) != 0 {
		t.Fatal("card before idle_after")
	}
	e.s.suggestIdleScan(time.Now().Add(20 * time.Minute))
	if cs := e.cards(t); len(cs) != 1 || cs[0].RuleID != "idle-rule" {
		t.Fatalf("cards = %+v", cs)
	}
	// Same idle episode is not evaluated twice.
	calls := e.calls
	e.s.suggestIdleScan(time.Now().Add(21 * time.Minute))
	if e.calls != calls {
		t.Error("idle episode evaluated twice")
	}
}

func TestSuggestion_CooldownAndDismiss(t *testing.T) {
	e := newSuggestEnv(t)
	e.on(t)
	writeRule(t, suggestion.ProjectDir(e.project), "r.md", rule("my-rule", "goal_completed", "q"))
	fire := func() int { return e.s.fireSuggestions(suggestEvent{Trigger: "goal_completed", SessionID: "s1"}) }
	backdate := func(d time.Duration) {
		at := time.Now().Add(-d).UTC().Format(time.RFC3339Nano)
		if _, err := e.s.sqlDB.Exec(`UPDATE suggestion_log SET created_at = ?`, at); err != nil {
			t.Fatal(err)
		}
	}

	if fire() != 1 {
		t.Fatal("first fire")
	}
	if fire() != 0 {
		t.Fatal("second fire within cooldown/open card")
	}
	// Answer the card and age it past the cooldown: shown again.
	card := e.cards(t)[0]
	if _, err := e.s.closeChoiceCard(card.ID, CardAnswered, "やる"); err != nil {
		t.Fatal(err)
	}
	if fire() != 0 {
		t.Fatal("within cooldown after answer")
	}
	backdate(2 * time.Hour)
	if fire() != 1 {
		t.Fatal("after cooldown")
	}

	// Dismissed: stays silent well beyond the cooldown.
	card = e.cards(t)[1]
	if _, err := e.s.closeChoiceCard(card.ID, CardDismissed, ""); err != nil {
		t.Fatal(err)
	}
	backdate(2 * time.Hour)
	if _, err := e.s.sqlDB.Exec(`UPDATE choice_cards SET answered_at = ?`, time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if fire() != 0 {
		t.Fatal("dismissed rule shown again within dismiss window")
	}
	backdate(30 * time.Hour)
	if _, err := e.s.sqlDB.Exec(`UPDATE choice_cards SET answered_at = ?`, time.Now().Add(-30*time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if fire() != 1 {
		t.Fatal("after dismiss window")
	}
}

func TestSuggestion_OffDoesNothing(t *testing.T) {
	for _, only := range []string{learning.ProactiveFeatureName, FeatureChoiceCards, ""} {
		e := newSuggestEnv(t)
		if only != "" {
			if err := experimental.Set(only, true); err != nil {
				t.Fatal(err)
			}
		}
		writeRule(t, suggestion.ProjectDir(e.project), "r.md", rule("my-rule", "goal_completed", "q"))
		writeRule(t, suggestion.ProjectDir(e.project), "t.md", rule("turn", "turn_ended", "q"))
		e.dirty = true
		e.s.suggestGoalCompleted("s1", "r")
		e.s.suggestStatusChange("s1", session.StatusWorking)
		e.s.suggestStatusChange("s1", session.StatusIdle)
		e.s.suggestIdleScan(time.Now().Add(time.Hour))
		if n := e.s.fireSuggestions(suggestEvent{Trigger: "goal_completed", SessionID: "s1"}); n != 0 {
			t.Fatalf("[%s] fired %d", only, n)
		}
		p := &learning.Proposal{SessionID: "s1", Origin: learning.OriginRepeatedTask}
		if e.s.suggestSignalCluster(p, "x", 3) {
			t.Fatalf("[%s] signal cluster fired", only)
		}
		time.Sleep(100 * time.Millisecond)
		e.cmu.Lock()
		calls := e.calls
		e.cmu.Unlock()
		if calls != 0 || len(e.cards(t)) != 0 {
			t.Fatalf("[%s] calls=%d cards=%d", only, calls, len(e.cards(t)))
		}
		if rec := doJSON(t, e.s, http.MethodGet, "/api/suggestions/rules", ""); rec.Code != http.StatusNotFound {
			t.Errorf("[%s] rules list = %d", only, rec.Code)
		}
	}
}

func TestSuggestion_CommitBeforeLeave(t *testing.T) {
	e := newSuggestEnv(t)
	e.on(t)
	e.dirty = false
	if n := e.s.fireSuggestions(suggestEvent{Trigger: "goal_completed", SessionID: "s1"}); n != 0 {
		t.Fatal("clean tree fired")
	}
	e.dirty = true
	if n := e.s.fireSuggestions(suggestEvent{Trigger: "goal_completed", SessionID: "s1"}); n != 1 {
		t.Fatal("dirty tree did not fire")
	}
	if e.calls != 0 {
		t.Error("built-in git check must not call the model")
	}
	cs := e.cards(t)
	if cs[0].RuleID != "commit-before-leave" || len(cs[0].Choices) != 2 || cs[0].Choices[0].Label != "コミットして PR を出す" {
		t.Fatalf("card = %+v", cs[0])
	}
	// Pressing the button sends the message.
	rec := doJSON(t, e.s, http.MethodPost, "/api/choice-cards/"+cs[0].ID+"/answer", `{"choiceId":"`+cs[0].Choices[0].ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("answer = %d %s", rec.Code, rec.Body.String())
	}
	if len(e.sess.sent) != 1 || e.sess.sent[0] != "s1:変更をコミットして PR を出して" {
		t.Errorf("sent = %v", e.sess.sent)
	}
}

func TestSuggestion_GitDirtyReal(t *testing.T) {
	dir := t.TempDir()
	if dirty, err := gitDirty(dir); err == nil && dirty {
		t.Error("non-repo reported dirty")
	}
}

func newProposal(t *testing.T, e *suggestEnv, origin string) *learning.Proposal {
	t.Helper()
	store, err := learning.NewStore(e.s.sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	e.s.learning = learning.NewReviewer(store, e.s.sqlDB, t.TempDir(), t.TempDir(), "claude", e.s.logger)
	p := &learning.Proposal{
		SessionID: "s1", ProjectPath: e.project, Target: learning.TargetMemory,
		Name: "pr-japanese", MemoryType: "feedback", Title: "PR は日本語", Content: "PR は日本語で書く", Origin: origin,
	}
	if err := store.Insert(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSuggestion_BuiltinCardsCallExistingHandlers(t *testing.T) {
	e := newSuggestEnv(t)
	e.on(t)
	p := newProposal(t, e, learning.OriginRepeatedCorrection)

	if !e.s.suggestSignalCluster(p, "PR は日本語で書く", 2) {
		t.Fatal("card not created")
	}
	cs := e.cards(t)
	if len(cs) != 1 || cs[0].RuleID != "write-to-harness" || !strings.Contains(cs[0].Prompt, "PR は日本語で書く") {
		t.Fatalf("cards = %+v", cs)
	}
	if e.calls != 0 {
		t.Error("detector rules must not call the model")
	}
	adopt := cs[0].Choices[0]
	if adopt.Action.Type != ActionAPI || adopt.Action.Handler != suggestion.HandlerAcceptProposal {
		t.Fatalf("adopt = %+v", adopt)
	}
	rec := doJSON(t, e.s, http.MethodPost, "/api/choice-cards/"+cs[0].ID+"/answer", `{"choiceId":"`+adopt.ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("answer = %d %s", rec.Code, rec.Body.String())
	}
	got, _ := e.s.learning.Store().Get(p.ID)
	if got.Status != learning.StatusApplied {
		t.Errorf("proposal status = %s, want applied", got.Status)
	}

	// Loop card: reject button rejects the proposal.
	p2 := newProposal(t, e, learning.OriginRepeatedTask)
	if !e.s.suggestSignalCluster(p2, "依存を更新する", 3) {
		t.Fatal("loop card not created")
	}
	cs = e.cards(t)
	loop := cs[1]
	if loop.RuleID != "make-loop" || !strings.Contains(loop.Prompt, "3回") {
		t.Fatalf("loop card = %+v", loop)
	}
	rec = doJSON(t, e.s, http.MethodPost, "/api/choice-cards/"+loop.ID+"/answer", `{"choiceId":"`+loop.Choices[1].ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("reject = %d %s", rec.Code, rec.Body.String())
	}
	got, _ = e.s.learning.Store().Get(p2.ID)
	if got.Status != learning.StatusRejected {
		t.Errorf("proposal status = %s, want rejected", got.Status)
	}
}

func TestSuggestion_CoordinatorTarget(t *testing.T) {
	e := newSuggestEnv(t)
	e.on(t)
	if err := experimental.Set("projects", true); err != nil {
		t.Fatal(err)
	}
	// s0 is the most recently active so it becomes the project coordinator.
	addSess(t, e.s, "s0", e.project, 60, nil)
	touchSess(t, e.s, "s0", 0)
	writeRule(t, suggestion.ProjectDir(e.project), "c.md", strings.Replace(rule("coord", "goal_completed", "q"), "cooldown: 1h", "cooldown: 1h\ntarget: coordinator", 1))

	e.s.fireSuggestions(suggestEvent{Trigger: "goal_completed", SessionID: "s1"})
	cs := e.cards(t)
	if len(cs) != 1 || cs[0].SessionID != "s0" {
		t.Fatalf("cards = %+v (want card on coordinator s0)", cs)
	}

	// projects off: goes to the session itself.
	if err := experimental.Set("projects", false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.sqlDB.Exec(`DELETE FROM suggestion_log`); err != nil {
		t.Fatal(err)
	}
	e.s.fireSuggestions(suggestEvent{Trigger: "goal_completed", SessionID: "s1"})
	cs = e.cards(t)
	if len(cs) != 2 || cs[1].SessionID != "s1" {
		t.Fatalf("cards = %+v (want card on s1)", cs)
	}
}

func TestSuggestion_RulesListAPI(t *testing.T) {
	e := newSuggestEnv(t)
	e.on(t)
	writeRule(t, suggestion.GlobalDir(e.home), "g.md", rule("g-rule", "turn_ended", "q"))
	writeRule(t, suggestion.ProjectDir(e.project), "p.md", rule("p-rule", "goal_completed", "q"))
	writeRule(t, suggestion.ProjectDir(e.project), "bad.md", "no frontmatter")
	e.s.fireSuggestions(suggestEvent{Trigger: "goal_completed", SessionID: "s1"})

	rec := doJSON(t, e.s, http.MethodGet, "/api/suggestions/rules", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Rules []struct {
			ID          string `json:"id"`
			Source      string `json:"source"`
			ProjectPath string `json:"projectPath"`
			RecentCards int    `json:"recentCards"`
		} `json:"rules"`
		Errors      []string `json:"errors"`
		RecentCards int      `json:"recentCards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, r := range out.Rules {
		seen[r.ID] = r.Source
	}
	if seen["commit-before-leave"] != "builtin" || seen["g-rule"] != "global" || seen["p-rule"] != "project" {
		t.Errorf("rules = %v", seen)
	}
	if len(out.Errors) != 1 || out.RecentCards != 1 {
		t.Errorf("errors=%v recent=%d", out.Errors, out.RecentCards)
	}
}
