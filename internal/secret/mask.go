package secret

import (
	"bytes"
	"io"
	"sort"
	"sync"
)

// MaskRule replaces every exact occurrence of Value with Replacement.
type MaskRule struct {
	Value       string
	Replacement string
}

// MaskReplacement is the text shown in place of the secret named name.
func MaskReplacement(name string) string {
	return "***(" + name + ")"
}

// Masker is an io.Writer that forwards data to an underlying writer with
// every exact occurrence of the configured values replaced. Values split
// across Write calls are still masked: up to len(longest value)-1 bytes that
// could start a match are held back until more data (or Flush) arrives.
// When values overlap at the same position the longest one wins.
//
// Flush must be called once the stream ends to emit held-back bytes.
type Masker struct {
	mu     sync.Mutex
	w      io.Writer
	rules  []maskRule
	maxLen int
	buf    []byte
}

type maskRule struct {
	value       []byte
	replacement []byte
}

// NewMasker returns a Masker writing to w. Empty values are ignored.
func NewMasker(w io.Writer, rules []MaskRule) *Masker {
	m := &Masker{w: w}
	for _, r := range rules {
		if r.Value == "" {
			continue
		}
		m.rules = append(m.rules, maskRule{value: []byte(r.Value), replacement: []byte(r.Replacement)})
		if len(r.Value) > m.maxLen {
			m.maxLen = len(r.Value)
		}
	}
	// Longest first so the first match found at a position is the longest.
	sort.SliceStable(m.rules, func(i, j int) bool { return len(m.rules[i].value) > len(m.rules[j].value) })
	return m
}

// Write masks p and forwards whatever is safe to emit. It always reports
// len(p) bytes written unless the underlying writer fails.
func (m *Masker) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.rules) == 0 {
		return m.w.Write(p)
	}
	m.buf = append(m.buf, p...)
	if err := m.process(false); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush emits all held-back bytes (masking complete matches).
func (m *Masker) Flush() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.rules) == 0 {
		return nil
	}
	return m.process(true)
}

// process scans m.buf, writes the masked safe prefix and keeps the
// undecidable tail in m.buf.
func (m *Masker) process(final bool) error {
	var out bytes.Buffer
	buf := m.buf
	i := 0
	for i < len(buf) {
		rest := buf[i:]
		if !final && len(rest) < m.maxLen && m.couldExtend(rest) {
			// rest is a proper prefix of some value: wait for more data
			// (this also defers choosing between overlapping values).
			break
		}
		if r := m.matchAt(rest); r != nil {
			out.Write(r.replacement)
			i += len(r.value)
			continue
		}
		out.WriteByte(buf[i])
		i++
	}
	m.buf = append(m.buf[:0], buf[i:]...)
	if out.Len() == 0 {
		return nil
	}
	_, err := m.w.Write(out.Bytes())
	return err
}

// couldExtend reports whether rest is a proper prefix of some value.
func (m *Masker) couldExtend(rest []byte) bool {
	for _, r := range m.rules {
		if len(r.value) > len(rest) && bytes.HasPrefix(r.value, rest) {
			return true
		}
	}
	return false
}

// matchAt returns the longest rule whose value is a prefix of rest.
func (m *Masker) matchAt(rest []byte) *maskRule {
	for idx := range m.rules {
		if bytes.HasPrefix(rest, m.rules[idx].value) {
			return &m.rules[idx]
		}
	}
	return nil
}

// MaskString masks s in one shot (convenience for non-streaming use).
func MaskString(s string, rules []MaskRule) string {
	var b bytes.Buffer
	m := NewMasker(&b, rules)
	_, _ = m.Write([]byte(s))
	_ = m.Flush()
	return b.String()
}
