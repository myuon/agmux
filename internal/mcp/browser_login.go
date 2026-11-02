package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/myuon/agmux/internal/browser"
)

// Responses the remote browser page sends when answering a browser-login
// request (see frontend RemoteBrowserPage).
const (
	loginResponseCompleted = "login_completed"
	loginResponseCancelled = "login_cancelled"
)

const defaultLoginTimeoutSeconds = 600

// handleRequestBrowserLogin asks the user to log in via the remote browser.
// It reuses the escalation machinery (blocking until answer or timeout) with a
// login URL attached.
func (s *Server) handleRequestBrowserLogin(args json.RawMessage) (interface{}, *jsonRPCError) {
	sessionID, rpcErr := s.resolveSessionID(args)
	if rpcErr != nil {
		return nil, rpcErr
	}
	var input struct {
		URL            string `json:"url"`
		Reason         string `json:"reason"`
		TimeoutSeconds *int   `json:"timeout_seconds,omitempty"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid arguments"}
	}
	input.URL = strings.TrimSpace(input.URL)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "reason is required"}
	}
	if err := browser.ValidateLoginURL(input.URL); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: err.Error()}
	}
	timeoutSeconds := defaultLoginTimeoutSeconds
	if input.TimeoutSeconds != nil && *input.TimeoutSeconds > 0 {
		timeoutSeconds = *input.TimeoutSeconds
	}

	response, timedOut, err := s.apiEscalate(sessionID, input.Reason, timeoutSeconds, input.URL)
	if err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}
	return toolResult(formatLoginOutcome(input.URL, response, timedOut), false), nil
}

func formatLoginOutcome(url, response string, timedOut bool) string {
	switch {
	case timedOut:
		return "[TIMED OUT] ユーザーは時間内に応答しませんでした。ログインが完了したとは想定しないでください。必要なら自分のタブで " + url + " を開き直してログイン状態を確認してください（ログイン済みでなければ、再度 request_browser_login を呼ぶか escalate で相談してください）。"
	case response == loginResponseCompleted:
		return "[COMPLETED] ユーザーが「ログイン完了」を押しました。ただし agmux はログインの成否を検証していません。自分のタブで対象ページを開き直し、ログインページにリダイレクトされないことを確認してから作業を再開してください。まだログイン画面になる場合は、その旨を escalate で伝えてください。"
	case response == loginResponseCancelled:
		return "[CANCELLED] ユーザーがログインを行わずキャンセルしました。ログインは完了していません。ログインが必要な作業は中断し、必要なら escalate でユーザーに今後の方針を確認してください。"
	default:
		return fmt.Sprintf("[RESPONDED] ユーザーから次の応答がありました（ログイン完了とは限りません）: %s", response)
	}
}
