package learning

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// proactiveEnv wires a Generator that answers each of the prompts used by the
// proactive flow, and records how many times each kind of call happened.
type proactiveEnv struct {
	*env
	reviewOut     string // output for the review prompt
	clusterAnswer string // output for the cluster judgement prompt
	loopOut       string // output for the loop proposal prompt
	created       []string
	loops         bool
	suggested     []*Proposal
	calls         map[string]int
}

func setupProactive(t *testing.T, on bool) *proactiveEnv {
	t.Helper()
	e := &proactiveEnv{env: setup(t, ""), calls: map[string]int{}}
	e.r.Proactive = func() bool { return on }
	e.r.OnSuggestion = func(p *Proposal, summary string, n int) { e.suggested = append(e.suggested, p) }
	e.r.Generate = func(ctx context.Context, model, prompt string) (string, error) {
		switch {
		case strings.Contains(prompt, "## セッションの会話"):
			e.calls["review"]++
			e.prompts = append(e.prompts, prompt)
			return e.reviewOut, nil
		case strings.Contains(prompt, "## 候補クラスタ"):
			e.calls["cluster"]++
			return e.clusterAnswer, nil
		case strings.Contains(prompt, "## 繰り返されている訂正"):
			e.calls["correction"]++
			return `{"proposals":[{"target":"claude_md","title":"PR は日本語で書く","content":"- PR の本文は日本語で書く","reason":"2 セッションで指摘"}]}`, nil
		case strings.Contains(prompt, "## 手で依頼された作業"):
			e.calls["loop"]++
			return e.loopOut, nil
		}
		t.Fatalf("unexpected prompt: %.80s", prompt)
		return "", nil
	}
	e.r.LoopsEnabled = func() bool { return e.loops }
	e.r.CreateAutomation = func(name, prompt, projectPath, triggerType, triggerValue string) (string, error) {
		e.created = append(e.created, name+"|"+prompt+"|"+triggerType+"|"+triggerValue)
		return "auto1", nil
	}
	return e
}

func (e *proactiveEnv) addSession(t *testing.T, id string) {
	t.Helper()
	if _, err := e.r.db.Exec(`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider) VALUES (?, ?, ?, '', 'idle', 'worker', 'stream', 'claude')`, id, "作業"+id, e.project); err != nil {
		t.Fatal(err)
	}
}

func (e *proactiveEnv) review(t *testing.T, id string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(e.streams, id+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"user","message":{"content":"` + strings.Repeat("会話です。", 120) + `"}}` + "\n")
	f.Close()
	if _, err := e.r.Review(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}

const correctionOut = `{"proposals":[],"signals":[{"kind":"correction","summary":"PR の本文を日本語で書く","excerpt":"PR は日本語で書いてって言ったよね"}]}`

func TestParseSignalsTolerant(t *testing.T) {
	got := ParseSignals("前置き\n```json\n" + `{"proposals":[],"signals":[
	 {"kind":"correction","summary":" 日本語で書く ","excerpt":"x"},
	 {"kind":"bogus","summary":"y"},
	 {"kind":"correction","summary":""}
	]}` + "\n```")
	if len(got) != 1 || got[0].Summary != "日本語で書く" || got[0].Kind != SignalCorrection {
		t.Fatalf("unexpected: %+v", got)
	}
	if ParseSignals("not json") != nil || ParseSignals(`{"proposals":[]}`) != nil {
		t.Fatal("expected none")
	}
}

func TestParseClusterChoice(t *testing.T) {
	cands := []cluster{{ID: 4}, {ID: 7}}
	for raw, want := range map[string]int64{
		`{"cluster_id": 7}`:     7,
		`{"cluster_id": "4"}`:   4,
		`{"cluster_id": "new"}`: 0,
		`{"cluster_id": 99}`:    0, // not a candidate
		`garbage`:               0,
	} {
		if got := parseClusterChoice(raw, cands); got != want {
			t.Errorf("%s: got %d want %d", raw, got, want)
		}
	}
}

func TestRepeatedCorrectionSuggestedAtTwoSessions(t *testing.T) {
	e := setupProactive(t, true)
	e.addSession(t, "s2")
	e.addSession(t, "s3")
	e.reviewOut = correctionOut

	e.review(t, "s1")
	if len(e.suggested) != 0 || e.calls["cluster"] != 0 {
		t.Fatalf("one session must not suggest: %+v", e.calls)
	}
	if !strings.Contains(e.prompts[0], "## シグナル") {
		t.Fatal("review prompt should ask for signals when proactive is on")
	}

	// The same session repeating itself still counts as one session.
	e.clusterAnswer = `{"cluster_id": 1}`
	e.review(t, "s1")
	if len(e.suggested) != 0 {
		t.Fatal("same session twice must not suggest")
	}

	e.review(t, "s2")
	if len(e.suggested) != 1 {
		t.Fatalf("expected a suggestion at two sessions, got %d (calls %+v)", len(e.suggested), e.calls)
	}
	p := e.suggested[0]
	if p.Origin != OriginRepeatedCorrection || p.ClusterID != 1 || p.Target != TargetClaudeMD || p.SessionID != "s2" {
		t.Fatalf("unexpected proposal: %+v", p)
	}
	got, err := e.r.store.Get(p.ID)
	if err != nil || got.Status != StatusPending || len(got.Evidence) != 3 {
		t.Fatalf("stored proposal wrong: %v %+v", err, got)
	}

	// No duplicate suggestion once proposed.
	e.review(t, "s3")
	if len(e.suggested) != 1 || e.calls["correction"] != 1 {
		t.Fatalf("must not re-suggest: %d %+v", len(e.suggested), e.calls)
	}

	// Accepting goes through the existing apply flow.
	acc, err := e.r.Accept(p.ID, "")
	if err != nil || acc.Status != StatusApplying || len(e.started) != 1 {
		t.Fatalf("accept: %v %+v", err, acc)
	}
}

func TestRejectedCorrectionIsNotSuggestedAgain(t *testing.T) {
	e := setupProactive(t, true)
	e.addSession(t, "s2")
	e.addSession(t, "s3")
	e.reviewOut = correctionOut
	e.clusterAnswer = `{"cluster_id": 1}`

	e.review(t, "s1")
	e.review(t, "s2")
	if len(e.suggested) != 1 {
		t.Fatal("expected a suggestion")
	}
	if _, err := e.r.Reject(e.suggested[0].ID); err != nil {
		t.Fatal(err)
	}
	e.review(t, "s3")
	if len(e.suggested) != 1 || e.calls["correction"] != 1 {
		t.Fatalf("rejected cluster must stay quiet: %d %+v", len(e.suggested), e.calls)
	}
}

func TestDifferentCorrectionsAreSeparateClusters(t *testing.T) {
	e := setupProactive(t, true)
	e.addSession(t, "s2")
	e.reviewOut = correctionOut
	e.clusterAnswer = `{"cluster_id": "new"}`

	e.review(t, "s1")
	e.review(t, "s2")
	if len(e.suggested) != 0 {
		t.Fatal("unrelated corrections must not combine")
	}
	cs, _ := e.r.store.recentClusters(e.project, SignalCorrection, 30)
	if len(cs) != 2 {
		t.Fatalf("expected 2 clusters, got %d", len(cs))
	}
	// The judgement prompt only carries clusters of the same project/kind.
}

func TestProactiveOffDoesNothing(t *testing.T) {
	e := setupProactive(t, false)
	e.addSession(t, "s2")
	e.reviewOut = correctionOut
	e.review(t, "s1")
	e.review(t, "s2")
	if strings.Contains(e.prompts[0], "シグナル") {
		t.Fatal("prompt must not mention signals when off")
	}
	var n int
	e.r.db.QueryRow(`SELECT COUNT(*) FROM learning_signals`).Scan(&n)
	if n != 0 || len(e.suggested) != 0 || e.calls["cluster"] != 0 || e.calls["correction"] != 0 {
		t.Fatalf("off must not extract/match/suggest: signals=%d %+v", n, e.calls)
	}
}

const taskOut = `{"proposals":[],"signals":[{"kind":"task","summary":"依存パッケージを更新して PR を作る","excerpt":"依存を更新して PR 出しておいて"}]}`
const loopOut = `{"name":"依存パッケージの定期更新","prompt":"依存パッケージを更新し、テストが通れば PR を作る","trigger_type":"cron","trigger_value":"0 9 * * 1","reason":"週次の作業に見える"}`

func (e *proactiveEnv) addAutomationSession(t *testing.T, id string) {
	t.Helper()
	e.addSession(t, id)
	if _, err := e.r.db.Exec(`UPDATE sessions SET automation_id = 'auto0' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
}

func TestRepeatedTaskSuggestedAtThreeManualSessions(t *testing.T) {
	e := setupProactive(t, true)
	for _, id := range []string{"s2", "s3"} {
		e.addSession(t, id)
	}
	e.addAutomationSession(t, "a1")
	e.addAutomationSession(t, "a2")
	e.reviewOut = taskOut
	e.loopOut = loopOut
	e.clusterAnswer = `{"cluster_id": 1}`

	e.review(t, "s1")
	e.review(t, "s2")
	// Sessions started by an automation are not counted.
	e.review(t, "a1")
	e.review(t, "a2")
	if len(e.suggested) != 0 {
		t.Fatalf("two manual requests must not suggest (automation sessions ignored): %+v", e.calls)
	}

	e.review(t, "s3")
	if len(e.suggested) != 1 {
		t.Fatalf("expected a suggestion at 3 manual sessions: %+v", e.calls)
	}
	p := e.suggested[0]
	if p.Origin != OriginRepeatedTask || p.Target != TargetLoop || p.Title != "依存パッケージの定期更新" ||
		p.TriggerType != "cron" || p.TriggerValue != "0 9 * * 1" || p.ClusterID != 1 {
		t.Fatalf("unexpected proposal: %+v", p)
	}
	got, _ := e.r.store.Get(p.ID)
	if len(got.Evidence) != 3 {
		t.Fatalf("evidence should be the 3 manual requests, got %d", len(got.Evidence))
	}

	// Accepting creates the automation (disabled by the creator) and records it.
	acc, err := e.r.Accept(p.ID, "編集後のプロンプト")
	if err != nil || acc.Status != StatusApplied || acc.AutomationID != "auto1" {
		t.Fatalf("accept: %v %+v", err, acc)
	}
	if len(e.created) != 1 || e.created[0] != "依存パッケージの定期更新|編集後のプロンプト|cron|0 9 * * 1" {
		t.Fatalf("automation params wrong: %v", e.created)
	}
	if len(e.started) != 0 {
		t.Fatal("loop proposals must not start an apply session")
	}
}

func TestLoopTriggerFallsBack(t *testing.T) {
	for name, trig := range map[string]string{
		"invalid":    `"trigger_type":"interval","trigger_value":"often"`,
		"queue-off":  `"trigger_type":"queue","trigger_value":"0s"`,
		"bogus-type": `"trigger_type":"weekly","trigger_value":"1"`,
	} {
		e := setupProactive(t, true)
		e.reviewOut = taskOut
		e.loopOut = `{"name":"n","prompt":"p",` + trig + `}`
		e.clusterAnswer = `{"cluster_id": 1}`
		for _, id := range []string{"s2", "s3"} {
			e.addSession(t, id)
		}
		e.review(t, "s1")
		e.review(t, "s2")
		e.review(t, "s3")
		if len(e.suggested) != 1 || e.suggested[0].TriggerType != "interval" || e.suggested[0].TriggerValue != "24h" {
			t.Fatalf("%s: expected interval 24h fallback, got %+v", name, e.suggested)
		}
	}

	// queue is kept when loops is on.
	e := setupProactive(t, true)
	e.loops = true
	e.reviewOut = taskOut
	e.loopOut = `{"name":"n","prompt":"p","trigger_type":"queue","trigger_value":"0s"}`
	e.clusterAnswer = `{"cluster_id": 1}`
	for _, id := range []string{"s2", "s3"} {
		e.addSession(t, id)
	}
	e.review(t, "s1")
	e.review(t, "s2")
	e.review(t, "s3")
	if len(e.suggested) != 1 || e.suggested[0].TriggerType != "queue" {
		t.Fatalf("queue should be kept with loops on: %+v", e.suggested)
	}
}

func TestRejectedLoopIsNotSuggestedAgain(t *testing.T) {
	e := setupProactive(t, true)
	for _, id := range []string{"s2", "s3", "s4"} {
		e.addSession(t, id)
	}
	e.reviewOut = taskOut
	e.loopOut = loopOut
	e.clusterAnswer = `{"cluster_id": 1}`
	e.review(t, "s1")
	e.review(t, "s2")
	e.review(t, "s3")
	if len(e.suggested) != 1 {
		t.Fatal("expected a suggestion")
	}
	if _, err := e.r.Reject(e.suggested[0].ID); err != nil {
		t.Fatal(err)
	}
	e.review(t, "s4")
	if len(e.suggested) != 1 || e.calls["loop"] != 1 {
		t.Fatalf("rejected cluster must stay quiet: %d %+v", len(e.suggested), e.calls)
	}
}

func TestTaskSignalsOffDoNothing(t *testing.T) {
	e := setupProactive(t, false)
	for _, id := range []string{"s2", "s3"} {
		e.addSession(t, id)
	}
	e.reviewOut = taskOut
	e.review(t, "s1")
	e.review(t, "s2")
	e.review(t, "s3")
	if len(e.suggested) != 0 || e.calls["loop"] != 0 || e.calls["cluster"] != 0 {
		t.Fatalf("off must not act: %+v", e.calls)
	}
}
