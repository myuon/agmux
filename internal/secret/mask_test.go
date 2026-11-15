package secret

import (
	"bytes"
	"strings"
	"testing"
)

// fakeToken builds a token-looking value at runtime so no literal secret-like
// string is committed (GitHub push protection blocks those).
func fakeToken(prefix string, n int) string {
	return prefix + "_" + strings.Repeat("aB3", n)
}

func TestMaskString(t *testing.T) {
	tok := fakeToken("ghp", 8)
	rules := []MaskRule{{Value: tok, Replacement: MaskReplacement("GH_TOKEN")}}
	got := MaskString("token="+tok+" and again "+tok+"\n", rules)
	want := "token=***(GH_TOKEN) and again ***(GH_TOKEN)\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// writeChunks writes s to a Masker split into chunks of size n.
func writeChunks(t *testing.T, s string, n int, rules []MaskRule) string {
	t.Helper()
	var out bytes.Buffer
	m := NewMasker(&out, rules)
	for i := 0; i < len(s); i += n {
		end := i + n
		if end > len(s) {
			end = len(s)
		}
		if _, err := m.Write([]byte(s[i:end])); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Flush(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestMaskerChunkBoundaries(t *testing.T) {
	tok := fakeToken("sk", 5)
	rules := []MaskRule{{Value: tok, Replacement: MaskReplacement("API_KEY")}}
	input := "a" + tok + "b" + tok + tok + "c" + tok[:len(tok)-1] + "d"
	want := "a***(API_KEY)b***(API_KEY)***(API_KEY)c" + tok[:len(tok)-1] + "d"
	for n := 1; n <= len(input); n++ {
		if got := writeChunks(t, input, n, rules); got != want {
			t.Fatalf("chunk size %d: got %q, want %q", n, got, want)
		}
	}
}

func TestMaskerHoldsPartialUntilFlush(t *testing.T) {
	tok := fakeToken("xo", 4)
	var out bytes.Buffer
	m := NewMasker(&out, []MaskRule{{Value: tok, Replacement: "***(X)"}})
	_, _ = m.Write([]byte("hello " + tok[:3]))
	if strings.Contains(out.String(), tok[:3]) {
		t.Fatalf("partial value emitted before it could be decided: %q", out.String())
	}
	if out.String() != "hello " {
		t.Fatalf("safe prefix not emitted: %q", out.String())
	}
	_, _ = m.Write([]byte(tok[3:] + "!"))
	_ = m.Flush()
	if out.String() != "hello ***(X)!" {
		t.Fatalf("got %q", out.String())
	}
}

func TestMaskerMultipleAndOverlapping(t *testing.T) {
	long := fakeToken("long", 4)
	short := long[:6] // a prefix of long
	other := fakeToken("oth", 3)
	inner := long[5:12] // a substring inside long
	rules := []MaskRule{
		{Value: short, Replacement: "***(SHORT)"},
		{Value: long, Replacement: "***(LONG)"},
		{Value: other, Replacement: "***(OTHER)"},
		{Value: inner, Replacement: "***(INNER)"},
		{Value: "", Replacement: "***(EMPTY)"},
	}
	input := long + " " + short + "z " + other + " " + inner
	want := "***(LONG) ***(SHORT)z ***(OTHER) ***(INNER)"
	for n := 1; n <= len(input); n++ {
		if got := writeChunks(t, input, n, rules); got != want {
			t.Fatalf("chunk size %d: got %q, want %q", n, got, want)
		}
	}
}

func TestMaskerNoRulesPassthrough(t *testing.T) {
	if got := writeChunks(t, "plain output", 3, nil); got != "plain output" {
		t.Fatalf("got %q", got)
	}
}
