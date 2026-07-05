package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// callJSONRPC sends a JSON-RPC request to the server and returns the parsed response.
func callJSONRPC(t *testing.T, s *Server, method string, params string) *jsonRPCResponse {
	t.Helper()
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%s,"params":%s}`, jsonString(method), params)
	out := s.HandleJSONRPC([]byte(body))
	if out == nil {
		t.Fatalf("HandleJSONRPC returned nil for method %s", method)
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal response: %v (body: %s)", err, string(out))
	}
	return &resp
}

// toolResultText extracts the text content and isError flag from a tools/call result.
func toolResultText(t *testing.T, resp *jsonRPCResponse) (string, bool) {
	t.Helper()
	if resp.Result == nil {
		t.Fatalf("expected result, got error: %+v", resp.Error)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(*resp.Result, &result); err != nil {
		t.Fatalf("unmarshal tool result: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatalf("tool result has no content")
	}
	return result.Content[0].Text, result.IsError
}

func TestToolsListIncludesCreateAutomation(t *testing.T) {
	s := NewServerForHTTP("sess-1", "http://localhost:0")
	resp := callJSONRPC(t, s, "tools/list", `{}`)
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}

	var result struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Required []string `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(*resp.Result, &result); err != nil {
		t.Fatalf("unmarshal tools/list result: %v", err)
	}

	for _, tool := range result.Tools {
		if tool.Name == "create_automation" {
			want := []string{"name", "prompt", "triggerType", "triggerValue"}
			if len(tool.InputSchema.Required) != len(want) {
				t.Fatalf("create_automation required = %v, want %v", tool.InputSchema.Required, want)
			}
			for i, r := range want {
				if tool.InputSchema.Required[i] != r {
					t.Errorf("create_automation required[%d] = %q, want %q", i, tool.InputSchema.Required[i], r)
				}
			}
			return
		}
	}
	t.Fatalf("create_automation not found in tools/list")
}

func TestCreateAutomationValidation(t *testing.T) {
	s := NewServerForHTTP("sess-1", "http://localhost:0")

	tests := []struct {
		name    string
		args    string
		wantMsg string
	}{
		{
			name:    "missing name",
			args:    `{"prompt":"do it","triggerType":"interval","triggerValue":"30m"}`,
			wantMsg: "name is required",
		},
		{
			name:    "missing prompt",
			args:    `{"name":"daily","triggerType":"interval","triggerValue":"30m"}`,
			wantMsg: "prompt is required",
		},
		{
			name:    "invalid trigger type",
			args:    `{"name":"daily","prompt":"do it","triggerType":"weekly","triggerValue":"30m"}`,
			wantMsg: `triggerType must be "interval" or "cron"`,
		},
		{
			name:    "missing trigger value",
			args:    `{"name":"daily","prompt":"do it","triggerType":"interval"}`,
			wantMsg: "triggerValue is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := fmt.Sprintf(`{"name":"create_automation","arguments":%s}`, tt.args)
			resp := callJSONRPC(t, s, "tools/call", params)
			if resp.Error == nil {
				t.Fatalf("expected JSON-RPC error, got result")
			}
			if resp.Error.Code != -32602 {
				t.Errorf("error code = %d, want -32602", resp.Error.Code)
			}
			if resp.Error.Message != tt.wantMsg {
				t.Errorf("error message = %q, want %q", resp.Error.Message, tt.wantMsg)
			}
		})
	}
}

func TestCreateAutomationSendsAPIRequest(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"atm-123","name":"daily report","prompt":"summarize","triggerType":"interval","triggerValue":"30m","enabled":true}`)
	}))
	defer backend.Close()

	s := NewServerForHTTP("sess-1", backend.URL)
	params := `{"name":"create_automation","arguments":{"name":"daily report","prompt":"summarize","triggerType":"interval","triggerValue":"30m"}}`
	resp := callJSONRPC(t, s, "tools/call", params)
	if resp.Error != nil {
		t.Fatalf("tools/call error: %+v", resp.Error)
	}

	text, isError := toolResultText(t, resp)
	if isError {
		t.Fatalf("tool result isError = true, text: %s", text)
	}
	if !strings.Contains(text, "atm-123") || !strings.Contains(text, "daily report") {
		t.Errorf("result text %q should contain automation id and name", text)
	}
	// controller area is used when projectPath is omitted
	if !strings.Contains(text, "controller") {
		t.Errorf("result text %q should mention controller as target project", text)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("API method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/automations" {
		t.Errorf("API path = %s, want /api/automations", gotPath)
	}

	var sent struct {
		Name         string `json:"name"`
		Prompt       string `json:"prompt"`
		TriggerType  string `json:"triggerType"`
		TriggerValue string `json:"triggerValue"`
		ProjectPath  string `json:"projectPath"`
		Enabled      bool   `json:"enabled"`
	}
	if err := json.Unmarshal([]byte(gotBody), &sent); err != nil {
		t.Fatalf("unmarshal sent body: %v (body: %s)", err, gotBody)
	}
	if sent.Name != "daily report" || sent.Prompt != "summarize" ||
		sent.TriggerType != "interval" || sent.TriggerValue != "30m" {
		t.Errorf("unexpected request body: %+v", sent)
	}
	if !sent.Enabled {
		t.Errorf("enabled should default to true when omitted")
	}
}

func TestCreateAutomationEnabledFalse(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var sent struct {
			Enabled bool `json:"enabled"`
		}
		_ = json.Unmarshal(b, &sent)
		if sent.Enabled {
			t.Errorf("enabled should be false when explicitly set to false")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"atm-456","name":"n","prompt":"p","triggerType":"cron","triggerValue":"0 9 * * *","enabled":false}`)
	}))
	defer backend.Close()

	s := NewServerForHTTP("sess-1", backend.URL)
	params := `{"name":"create_automation","arguments":{"name":"n","prompt":"p","triggerType":"cron","triggerValue":"0 9 * * *","enabled":false}}`
	resp := callJSONRPC(t, s, "tools/call", params)
	if resp.Error != nil {
		t.Fatalf("tools/call error: %+v", resp.Error)
	}
	text, isError := toolResultText(t, resp)
	if isError {
		t.Fatalf("tool result isError = true, text: %s", text)
	}
	if !strings.Contains(text, "enabled: false") {
		t.Errorf("result text %q should mention enabled: false", text)
	}
}

func TestCreateAutomationAPIError(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid cron expression"}`)
	}))
	defer backend.Close()

	s := NewServerForHTTP("sess-1", backend.URL)
	params := `{"name":"create_automation","arguments":{"name":"n","prompt":"p","triggerType":"cron","triggerValue":"bad"}}`
	resp := callJSONRPC(t, s, "tools/call", params)
	if resp.Error != nil {
		t.Fatalf("tools/call should return tool-level error, got JSON-RPC error: %+v", resp.Error)
	}
	text, isError := toolResultText(t, resp)
	if !isError {
		t.Fatalf("tool result isError = false, want true (text: %s)", text)
	}
	if !strings.Contains(text, "invalid cron expression") {
		t.Errorf("result text %q should contain API error message", text)
	}
}
