package secret

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type opCall struct {
	env  []string
	args []string
}

type fakeOP struct {
	calls  []opCall
	stdout string
	stderr string
	code   int
	err    error
}

func (f *fakeOP) Run(_ context.Context, env []string, args ...string) ([]byte, []byte, int, error) {
	f.calls = append(f.calls, opCall{env: env, args: args})
	return []byte(f.stdout), []byte(f.stderr), f.code, f.err
}

func fixedToken(tok string, found bool) TokenSource {
	return func(context.Context) (string, bool, error) { return tok, found, nil }
}

func newTestOP(f *fakeOP, ts TokenSource) *OnePasswordBackend {
	b := NewOnePasswordBackend(f, ts)
	b.env = func() []string { return []string{"PATH=/bin", opTokenEnv + "=inherited"} }
	return b
}

func TestValidateOPRef(t *testing.T) {
	for _, ok := range []string{"op://Private/db/password", "op://v/i/section/f", "op://vault/item-1/field_2"} {
		if err := ValidateOPRef(ok); err != nil {
			t.Errorf("ValidateOPRef(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "op://", "op://v/i", "http://v/i/f", "op://v//f", "op://v/i/f extra", "op://v/i/f?attr=otp", "op://v/i/a/b/c", "$(rm -rf)"} {
		if err := ValidateOPRef(bad); err == nil {
			t.Errorf("ValidateOPRef(%q) = nil, want error", bad)
		}
	}
}

func TestOnePasswordSetRejectedDeleteNoop(t *testing.T) {
	b := newTestOP(&fakeOP{}, nil)
	if b.Kind() != Backend1Password {
		t.Fatal("kind")
	}
	if err := b.Set(context.Background(), "X", "v"); err == nil {
		t.Error("Set must be rejected")
	}
	if err := b.Delete(context.Background(), "X"); err != nil {
		t.Errorf("Delete = %v, want nil", err)
	}
}

func TestOnePasswordGetInjectsToken(t *testing.T) {
	tok := "tok" + "en-" + "abc"
	f := &fakeOP{stdout: "resolved-value"}
	b := newTestOP(f, fixedToken(tok, true))
	got, err := b.Get(context.Background(), &Secret{Name: "X", Ref: "op://v/i/f"})
	if err != nil || got != "resolved-value" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	c := f.calls[0]
	if strings.Join(c.args, " ") != "read --no-newline op://v/i/f" {
		t.Errorf("args = %v", c.args)
	}
	count, last := 0, ""
	for _, kv := range c.env {
		if strings.HasPrefix(kv, opTokenEnv+"=") {
			count++
			last = kv
		}
	}
	if count != 1 || last != opTokenEnv+"="+tok {
		t.Errorf("env token entries = %d (%q)", count, last)
	}
}

func TestOnePasswordGetFallsBackToInheritedEnv(t *testing.T) {
	f := &fakeOP{stdout: "v"}
	b := newTestOP(f, fixedToken("", false))
	if _, err := b.Get(context.Background(), &Secret{Ref: "op://v/i/f"}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, kv := range f.calls[0].env {
		if kv == opTokenEnv+"=inherited" {
			found = true
		}
	}
	if !found {
		t.Error("inherited env must be passed through")
	}
}

func TestOnePasswordGetInvalidRefDoesNotRun(t *testing.T) {
	f := &fakeOP{}
	b := newTestOP(f, nil)
	if _, err := b.Get(context.Background(), &Secret{Ref: "nope"}); err == nil {
		t.Fatal("want error")
	}
	if len(f.calls) != 0 {
		t.Error("op must not run for an invalid ref")
	}
}

func TestOnePasswordErrors(t *testing.T) {
	tok := "tok" + "en-" + "secret123"
	cases := []struct {
		name   string
		f      *fakeOP
		ts     TokenSource
		want   string
		absent []string
	}{
		{"not installed", &fakeOP{err: errOPNotInstalled}, nil, "not installed", nil},
		{"ref not found", &fakeOP{code: 1, stderr: "[ERROR] \"op://v/i/f\" isn't an item in the \"v\" vault"}, fixedToken(tok, true), "reference not found", nil},
		{"bad token", &fakeOP{code: 1, stderr: "[ERROR] could not authenticate: invalid service account token " + tok}, fixedToken(tok, true), "token is missing, invalid or revoked", []string{tok}},
		{"no token", &fakeOP{code: 1, stderr: "[ERROR] You are not signed in"}, fixedToken("", false), "no service account token is registered", nil},
		{"other leaks token", &fakeOP{code: 3, stderr: "weird failure " + tok}, fixedToken(tok, true), "exit 3", []string{tok}},
		{"runner err leaks token", &fakeOP{err: errors.New("boom " + tok)}, fixedToken(tok, true), "boom", []string{tok}},
	}
	for _, c := range cases {
		b := newTestOP(c.f, c.ts)
		if c.name == "no token" {
			b.env = func() []string { return []string{"PATH=/bin"} }
		}
		_, err := b.Get(context.Background(), &Secret{Ref: "op://v/i/f"})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want containing %q", c.name, err, c.want)
			continue
		}
		for _, a := range c.absent {
			if strings.Contains(err.Error(), a) {
				t.Errorf("%s: error leaks %q", c.name, a)
			}
		}
	}
}

func TestKeychainTokenSource(t *testing.T) {
	kc := NewKeychainBackend(&fakeRunner{items: map[string]string{}})
	ts := KeychainTokenSource(kc, DefaultOPTokenSecret)
	if _, found, err := ts(context.Background()); err != nil || found {
		t.Fatalf("missing token: found=%v err=%v", found, err)
	}
	tok := "tok" + "en-" + "xyz"
	if err := kc.Set(context.Background(), DefaultOPTokenSecret, tok); err != nil {
		t.Fatal(err)
	}
	got, found, err := ts(context.Background())
	if err != nil || !found || got != tok {
		t.Fatalf("got=%q found=%v err=%v", got, found, err)
	}
}

func TestOPValuesAreMaskedByExec(t *testing.T) {
	val := "resolved" + "-from-op"
	b := newTestOP(&fakeOP{stdout: val}, nil)
	v, err := b.Get(context.Background(), &Secret{Name: "DB_PASSWORD", Ref: "op://v/i/f"})
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code, err := Exec(ExecOptions{
		Args:    []string{"sh", "-c", "echo $DB_PASSWORD; echo $DB_PASSWORD >&2"},
		BaseEnv: []string{"PATH=/usr/bin:/bin"},
		Inject:  []Injection{{Name: "DB_PASSWORD", EnvName: "DB_PASSWORD", Value: v}},
		Stdout:  &out,
		Stderr:  &errOut,
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if strings.Contains(out.String()+errOut.String(), val) || !strings.Contains(out.String(), MaskReplacement("DB_PASSWORD")) {
		t.Errorf("not masked: %q / %q", out.String(), errOut.String())
	}
}
