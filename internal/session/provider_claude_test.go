package session

import "testing"

func TestClaudeProvider_Name(t *testing.T) {
	p := NewClaudeProvider("", "")
	if p.Name() != ProviderClaude {
		t.Errorf("expected %q, got %q", ProviderClaude, p.Name())
	}
}

func TestClaudeProvider_IsOneShot(t *testing.T) {
	p := NewClaudeProvider("", "")
	if p.IsOneShot() {
		t.Errorf("expected ClaudeProvider.IsOneShot() = false, got true")
	}
}
