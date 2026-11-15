package secret

import (
	"bytes"
	"strings"
	"testing"
)

func TestExecInjectsAndMasks(t *testing.T) {
	val := fakeToken("exec", 6)
	var out, errOut bytes.Buffer
	code, err := Exec(ExecOptions{
		Args: []string{"sh", "-c", `echo "out:$MY_ENV"; echo "err:$MY_ENV" >&2; test "$MY_ENV" = "$EXPECT" && test -z "$DROPPED"`},
		// EXPECT is passed via the base env so the comparison happens in the
		// child; MY_ENV in the base env must be overridden.
		BaseEnv: []string{"PATH=/usr/bin:/bin", "EXPECT=" + val, "MY_ENV=old"},
		Inject:  []Injection{{Name: "MY_SECRET", EnvName: "MY_ENV", Value: val}},
		Stdout:  &out,
		Stderr:  &errOut,
	})
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%q", code, errOut.String())
	}
	if out.String() != "out:***(MY_SECRET)\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	if errOut.String() != "err:***(MY_SECRET)\n" {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestExecMasksValueSplitAcrossWrites(t *testing.T) {
	val := fakeToken("split", 6)
	half := len(val) / 2
	var out bytes.Buffer
	// printf the value in two separate writes with a sleep in between so the
	// masker receives it in two chunks.
	script := `printf '%s' "${V%` + val[half:] + `}"; sleep 0.2; printf '%s\n' "` + val[half:] + `"`
	code, err := Exec(ExecOptions{
		Args:    []string{"sh", "-c", script},
		BaseEnv: []string{"PATH=/usr/bin:/bin"},
		Inject:  []Injection{{Name: "S", EnvName: "V", Value: val}},
		Stdout:  &out,
		Stderr:  &out,
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v out=%q", code, err, out.String())
	}
	if out.String() != "***(S)\n" {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestExecPropagatesExitCodeAndStdin(t *testing.T) {
	var out bytes.Buffer
	code, err := Exec(ExecOptions{
		Args:    []string{"sh", "-c", `read line; echo "got:$line"; exit 7`},
		BaseEnv: []string{"PATH=/usr/bin:/bin"},
		Stdin:   strings.NewReader("hello\n"),
		Stdout:  &out,
		Stderr:  &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
	if out.String() != "got:hello\n" {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestExecSignalExitCode(t *testing.T) {
	var out bytes.Buffer
	code, err := Exec(ExecOptions{
		Args:    []string{"sh", "-c", `kill -TERM $$`},
		BaseEnv: []string{"PATH=/usr/bin:/bin"},
		Stdout:  &out,
		Stderr:  &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if code != 128+15 {
		t.Fatalf("exit code = %d, want 143", code)
	}
}

func TestExecCommandNotFound(t *testing.T) {
	var out bytes.Buffer
	code, err := Exec(ExecOptions{
		Args:   []string{"/nonexistent/agmux-test-cmd"},
		Stdout: &out,
		Stderr: &out,
	})
	if err == nil || code != 127 {
		t.Fatalf("code=%d err=%v", code, err)
	}
}
