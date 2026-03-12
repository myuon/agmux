package monitor

import "testing"

func TestLastLineHasToolUse(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{
			name: "assistant with tool_use",
			line: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`,
			want: true,
		},
		{
			name: "assistant with tool_use and tool_result",
			line: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{}},{"type":"tool_result","content":"ok"}]}}`,
			want: true,
		},
		{
			name: "assistant with text only",
			line: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}`,
			want: false,
		},
		{
			name: "user message",
			line: `{"type":"user","message":{"role":"user","content":"do something"}}`,
			want: false,
		},
		{
			name: "system message",
			line: `{"type":"system","session_id":"abc123"}`,
			want: false,
		},
		{
			name: "empty string",
			line: "",
			want: false,
		},
		{
			name: "invalid json",
			line: `{invalid`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lastLineHasToolUse(tt.line)
			if got != tt.want {
				t.Errorf("lastLineHasToolUse() = %v, want %v", got, tt.want)
			}
		})
	}
}
