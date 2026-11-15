package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/myuon/agmux/internal/secret"
)

// Responses the server delivers to a request_secret escalation (see
// internal/server/secret_request.go). They never carry the value.
const (
	secretResponseSaved     = "secret_saved"
	secretResponseCancelled = "secret_cancelled"
)

const defaultSecretRequestTimeoutSeconds = 600

// handleRequestSecret asks the user to register a named secret from the
// browser. It reuses the escalation machinery (blocking until the user saves,
// cancels, or the request times out). The value never reaches the agent.
func (s *Server) handleRequestSecret(args json.RawMessage) (interface{}, *jsonRPCError) {
	sessionID, rpcErr := s.resolveSessionID(args)
	if rpcErr != nil {
		return nil, rpcErr
	}
	var input struct {
		Name           string `json:"name"`
		Description    string `json:"description"`
		TimeoutSeconds *int   `json:"timeout_seconds,omitempty"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid arguments"}
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if err := secret.ValidateName(input.Name); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: err.Error()}
	}
	if input.Description == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "description is required"}
	}
	timeoutSeconds := defaultSecretRequestTimeoutSeconds
	if input.TimeoutSeconds != nil && *input.TimeoutSeconds > 0 {
		timeoutSeconds = *input.TimeoutSeconds
	}

	message := fmt.Sprintf("シークレット `%s` の登録を依頼しています: %s", input.Name, input.Description)
	response, timedOut, err := s.apiEscalateWith(sessionID, message, timeoutSeconds, map[string]string{
		"secret_name":        input.Name,
		"secret_description": input.Description,
	})
	if err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}
	return toolResult(formatSecretOutcome(input.Name, response, timedOut), false), nil
}

func formatSecretOutcome(name, response string, timedOut bool) string {
	switch {
	case timedOut:
		return "[TIMED OUT] ユーザーは時間内にシークレット " + name + " を登録しませんでした。登録済みとは想定しないでください。必要なら再度 request_secret を呼ぶか escalate で相談してください。"
	case response == secretResponseSaved:
		return fmt.Sprintf("%s を保存しました（agmux secret exec --env %s -- <cmd> で利用できます）。値はあなたには渡されません。値を表示・出力しようとしないでください。", name, name)
	case response == secretResponseCancelled:
		return "[CANCELLED] ユーザーがシークレット " + name + " の登録をキャンセルしました。このシークレットが必要な作業は中断し、必要なら escalate でユーザーに今後の方針を確認してください。"
	default:
		// The server only resolves secret requests with the constants above.
		return "[UNKNOWN] シークレット " + name + " の登録結果を確認できませんでした。登録済みとは想定しないでください。"
	}
}
