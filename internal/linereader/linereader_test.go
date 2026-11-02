package linereader

import (
	"bytes"
	"io"
	"runtime"
	"strings"
	"testing"
)

type got struct {
	data string
	size int64
	over bool
}

func readAll(t *testing.T, in string, opt Options) []got {
	t.Helper()
	r := New(strings.NewReader(in), opt)
	var out []got
	for {
		l, err := r.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out = append(out, got{string(l.Data), l.Size, l.Oversized})
	}
}

func TestNormalLines(t *testing.T) {
	g := readAll(t, "a\n\nbc\r\nlast", Options{MaxLine: 10})
	want := []got{{"a", 1, false}, {"", 0, false}, {"bc", 2, false}, {"last", 4, false}}
	if len(g) != len(want) {
		t.Fatalf("got %+v", g)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Errorf("line %d: got %+v want %+v", i, g[i], want[i])
		}
	}
}

func TestLimitBoundary(t *testing.T) {
	exact := strings.Repeat("x", 10)
	plus1 := strings.Repeat("y", 11)
	g := readAll(t, exact+"\n"+plus1+"\nz\n", Options{MaxLine: 10, KeepPrefix: 4})
	if len(g) != 3 {
		t.Fatalf("got %+v", g)
	}
	if g[0].over || g[0].data != exact {
		t.Errorf("exact limit line must be returned whole: %+v", g[0])
	}
	if !g[1].over || g[1].size != 11 || g[1].data != "yyyy" {
		t.Errorf("limit+1 must be oversized with prefix: %+v", g[1])
	}
	if g[2].data != "z" || g[2].over {
		t.Errorf("line after oversized: %+v", g[2])
	}
}

func TestLimitBoundaryCRLF(t *testing.T) {
	// CRLF is not counted toward the limit (Scanner strips \r).
	exact := strings.Repeat("x", 10)
	g := readAll(t, exact+"\r\n"+exact+"y\r\nz", Options{MaxLine: 10, KeepPrefix: 4})
	if len(g) != 3 {
		t.Fatalf("got %+v", g)
	}
	if g[0].over || g[0].data != exact || g[0].size != 10 {
		t.Errorf("exact+CRLF: %+v", g[0])
	}
	if !g[1].over || g[1].size != 11 {
		t.Errorf("limit+1 with CRLF: %+v", g[1])
	}
	if g[2].data != "z" {
		t.Errorf("tail: %+v", g[2])
	}
}

func TestOversizedWithoutTrailingNewline(t *testing.T) {
	g := readAll(t, "a\n"+strings.Repeat("q", 100), Options{MaxLine: 10, KeepPrefix: 3})
	if len(g) != 2 || !g[1].over || g[1].size != 100 || g[1].data != "qqq" {
		t.Fatalf("got %+v", g)
	}
}

func TestOversizedCRLFSize(t *testing.T) {
	g := readAll(t, strings.Repeat("q", 100)+"\r\nok\n", Options{MaxLine: 10, KeepPrefix: 3})
	if len(g) != 2 || !g[0].over || g[0].size != 100 || g[1].data != "ok" {
		t.Fatalf("got %+v", g)
	}
}

func TestMultipleOversizedInARow(t *testing.T) {
	big := strings.Repeat("a", 200000) // spans several 64KiB read buffers
	in := "first\n" + big + "\n" + big + "b\n" + big + "\nlast\n"
	g := readAll(t, in, Options{MaxLine: 1000, KeepPrefix: 8})
	if len(g) != 5 {
		t.Fatalf("got %d lines", len(g))
	}
	if g[0].data != "first" || g[4].data != "last" {
		t.Errorf("normal lines wrong: %+v %+v", g[0], g[4])
	}
	for i, size := range map[int]int64{1: 200000, 2: 200001, 3: 200000} {
		if !g[i].over || g[i].size != size || len(g[i].data) != 8 {
			t.Errorf("line %d: over=%v size=%d prefix=%d", i, g[i].over, g[i].size, len(g[i].data))
		}
	}
}

func TestObserveSeesWholeOversizedLine(t *testing.T) {
	big := strings.Repeat("0123456789", 30000) // 300000 bytes
	var seen bytes.Buffer
	r := New(strings.NewReader("x\n"+big+"\ny\n"), Options{MaxLine: 1000, KeepPrefix: 4, Observe: func(c []byte) { seen.Write(c) }})
	for i := 0; i < 3; i++ {
		if _, err := r.Next(); err != nil {
			t.Fatal(err)
		}
	}
	if seen.String() != big {
		t.Errorf("observe saw %d bytes, want %d", seen.Len(), len(big))
	}
}

type zReader struct {
	remaining int64
	nl        bool
}

func (z *zReader) Read(p []byte) (int, error) {
	if z.remaining <= 0 {
		if z.nl {
			z.nl = false
			p[0] = '\n'
			return 1, nil
		}
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > z.remaining {
		n = z.remaining
	}
	for i := int64(0); i < n; i++ {
		p[i] = 'z'
	}
	z.remaining -= n
	return int(n), nil
}

func TestHugeLineMemoryBounded(t *testing.T) {
	const huge = 600 << 20 // 600MiB streamed through, never stored
	in := io.MultiReader(strings.NewReader("a\n"), &zReader{remaining: huge, nl: true}, strings.NewReader("b\n"))
	r := New(in, Options{MaxLine: 1 << 20})
	var ms runtime.MemStats
	var peak uint64
	var lines []Line
	var datas []string
	for {
		l, err := r.Next()
		if err != nil {
			break
		}
		runtime.ReadMemStats(&ms)
		if ms.HeapAlloc > peak {
			peak = ms.HeapAlloc
		}
		lines = append(lines, Line{Size: l.Size, Oversized: l.Oversized})
		n := len(l.Data)
		if n > 4 {
			n = 4
		}
		datas = append(datas, string(l.Data[:n]))
	}
	if len(lines) != 3 || !lines[1].Oversized || lines[1].Size != huge || datas[2] != "b" {
		t.Fatalf("unexpected result: %+v %v", lines, datas)
	}
	if peak > 64<<20 {
		t.Errorf("heap grew to %d bytes while draining a %d byte line", peak, int64(huge))
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestReadError(t *testing.T) {
	r := New(io.MultiReader(strings.NewReader("a\npart"), errReader{}), Options{})
	if l, err := r.Next(); err != nil || string(l.Data) != "a" {
		t.Fatalf("first: %v %q", err, l.Data)
	}
	if l, err := r.Next(); err != nil || string(l.Data) != "part" {
		t.Fatalf("partial: %v %q", err, l.Data)
	}
	if _, err := r.Next(); err == nil || err == io.EOF {
		t.Fatalf("want read error, got %v", err)
	}
}
