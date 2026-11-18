package suggestion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sample = `---
id: commit-before-leave
trigger: goal_completed        # comment
detector: ""
cooldown: 24h
target: session
choices:
  - label: コミットして PR を出す
    message: 変更をコミットして PR を出して
  - label: 今はいい
    action: dismiss
---
条件（自然文）: 作業ツリーに未コミットの変更が残ったまま
ゴールが完了した
問いかけ: 変更がコミットされずに残っています。どうしますか？
`

func TestParse(t *testing.T) {
	r, err := Parse([]byte(sample), "fallback", SourceProject, "/x.md")
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "commit-before-leave" || r.Trigger != TriggerGoalCompleted || r.Cooldown != 24*time.Hour || r.Target != TargetSession {
		t.Errorf("rule = %+v", r)
	}
	if r.Condition != "作業ツリーに未コミットの変更が残ったまま\nゴールが完了した" || r.Question != "変更がコミットされずに残っています。どうしますか？" {
		t.Errorf("body = %q / %q", r.Condition, r.Question)
	}
	if len(r.Choices) != 2 || r.Choices[0].Action != ActionSendMessage || r.Choices[0].Message == "" || r.Choices[1].Action != ActionDismiss {
		t.Errorf("choices = %+v", r.Choices)
	}
	if r.Source != SourceProject {
		t.Errorf("source = %s", r.Source)
	}
}

func TestParseDefaults(t *testing.T) {
	r, err := Parse([]byte("---\ntrigger: turn_ended\nchoices:\n  - label: a\n  - label: b\n---\n条件: x\n問いかけ: q\n"), "stem", SourceGlobal, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "stem" || r.Cooldown != DefaultCooldown || r.Target != TargetSession {
		t.Errorf("rule = %+v", r)
	}
}

func TestParseDetector(t *testing.T) {
	r, err := Parse([]byte("---\ndetector: repeated_task\nchoices:\n  - label: a\n  - label: b\n---\n問いかけ: q\n"), "d", SourceGlobal, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Trigger != TriggerSignalCluster || r.Detector != DetectorRepeatedTask {
		t.Errorf("rule = %+v", r)
	}
}

func TestParseErrors(t *testing.T) {
	ch := "choices:\n  - label: a\n  - label: b\n"
	cases := map[string]string{
		"no frontmatter":   "hello",
		"unclosed":         "---\nid: a\n",
		"bad trigger":      "---\ntrigger: nope\n" + ch + "---\n条件: x\n問いかけ: q\n",
		"no trigger":       "---\n" + ch + "---\n条件: x\n問いかけ: q\n",
		"one choice":       "---\ntrigger: turn_ended\nchoices:\n  - label: a\n---\n条件: x\n問いかけ: q\n",
		"five choices":     "---\ntrigger: turn_ended\nchoices:\n  - label: a\n  - label: b\n  - label: c\n  - label: d\n  - label: e\n---\n条件: x\n問いかけ: q\n",
		"no question":      "---\ntrigger: turn_ended\n" + ch + "---\n条件: x\n",
		"no condition":     "---\ntrigger: turn_ended\n" + ch + "---\n問いかけ: q\n",
		"bad detector":     "---\ndetector: foo\n" + ch + "---\n問いかけ: q\n",
		"detector+trigger": "---\ntrigger: turn_ended\ndetector: repeated_task\n" + ch + "---\n問いかけ: q\n",
		"cluster no det":   "---\ntrigger: signal_cluster\n" + ch + "---\n条件: x\n問いかけ: q\n",
		"bad cooldown":     "---\ntrigger: turn_ended\ncooldown: soon\n" + ch + "---\n条件: x\n問いかけ: q\n",
		"bad target":       "---\ntrigger: turn_ended\ntarget: all\n" + ch + "---\n条件: x\n問いかけ: q\n",
		"bad action":       "---\ntrigger: turn_ended\nchoices:\n  - label: a\n    action: boom\n  - label: b\n---\n条件: x\n問いかけ: q\n",
		"bad id":           "---\nid: a b\ntrigger: turn_ended\n" + ch + "---\n条件: x\n問いかけ: q\n",
	}
	for name, src := range cases {
		if _, err := Parse([]byte(src), "x", SourceGlobal, ""); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30m": 30 * time.Minute, "0": 0} {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("%q = %v, %v", in, got, err)
		}
	}
	if _, err := ParseDuration("x"); err == nil {
		t.Error("expected error")
	}
}

func TestLoadDirAndMergePriority(t *testing.T) {
	global, project := t.TempDir(), t.TempDir()
	mk := func(dir, name, q string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Replace(sample, "どうしますか", q, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk(global, "a.md", "GLOBAL")
	mk(project, "a.md", "PROJECT")
	if err := os.WriteFile(filepath.Join(global, "broken.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(global, "note.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	g, gerrs := LoadDir(global, SourceGlobal)
	p, perrs := LoadDir(project, SourceProject)
	missing, merrs := LoadDir(filepath.Join(project, "none"), SourceProject)
	if len(g) != 1 || len(gerrs) != 1 || len(p) != 1 || len(perrs) != 0 || len(missing) != 0 || len(merrs) != 0 {
		t.Fatalf("g=%d/%d p=%d/%d missing=%d/%d", len(g), len(gerrs), len(p), len(perrs), len(missing), len(merrs))
	}
	// Same id (commit-before-leave) in builtin, global, project: project wins.
	merged := Merge(Builtins(), g, p)
	var found *Rule
	for _, r := range merged {
		if r.ID == "commit-before-leave" {
			if found != nil {
				t.Fatal("duplicate id in merged rules")
			}
			found = r
		}
	}
	if found == nil || found.Source != SourceProject {
		t.Fatalf("found = %+v", found)
	}
	// Without the project layer the global rule beats the built-in.
	for _, r := range Merge(Builtins(), g) {
		if r.ID == "commit-before-leave" && r.Source != SourceGlobal {
			t.Errorf("source = %s, want global", r.Source)
		}
	}
}

func TestBuiltinsAreValid(t *testing.T) {
	ids := map[string]bool{}
	for _, r := range Builtins() {
		if err := r.Validate(); err != nil {
			t.Errorf("%s: %v", r.ID, err)
		}
		ids[r.ID] = true
	}
	for _, id := range []string{"write-to-harness", "make-loop", "commit-before-leave"} {
		if !ids[id] {
			t.Errorf("missing built-in %s", id)
		}
	}
}
