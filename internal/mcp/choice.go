package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// offerChoicesToolDef is the offer_choices tool definition. It is gated by the
// "choice_cards" experimental feature (see experimentalTools).
func offerChoicesToolDef() map[string]interface{} {
	return map[string]interface{}{
		"name": "offer_choices",
		"description": "ユーザーに選択肢のボタンを出します。会話の中に問いかけとボタンのカードが表示されますが、すぐに返り、あなたは待ちません。" +
			"ユーザーはボタンを押すか、ボタンを無視して普通に返信できます。押された選択肢は、ユーザーのメッセージとして送られてきます。" +
			"呼んだ後は待たずにターンを終えてかまいません。" +
			"escalate との使い分け: 答えを得るまで作業を進められないときだけ escalate（返事が来るまで止まる）を使い、" +
			"「次にこれをやりますか？」のように答えが無くても進められる問いかけや、先の選択肢を示したいときは offer_choices を使ってください。",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"session_id": sessionIDProperty,
				"prompt": map[string]interface{}{
					"type":        "string",
					"description": "ユーザーへの問いかけの文",
				},
				"choices": map[string]interface{}{
					"type":        "array",
					"description": "選択肢（2〜4個）",
					"minItems":    2,
					"maxItems":    4,
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"label": map[string]interface{}{
								"type":        "string",
								"description": "ボタンに表示する短い文",
							},
							"message": map[string]interface{}{
								"type":        "string",
								"description": "ボタンを押したときにユーザーのメッセージとして送られる文（任意。省略時は label）",
							},
						},
						"required": []string{"label"},
					},
				},
			},
			"required": []string{"prompt", "choices"},
		},
	}
}

func (s *Server) handleOfferChoices(args json.RawMessage) (interface{}, *jsonRPCError) {
	sessionID, rpcErr := s.resolveSessionID(args)
	if rpcErr != nil {
		return nil, rpcErr
	}
	var p struct {
		Prompt  string `json:"prompt"`
		Choices []struct {
			Label   string `json:"label"`
			Message string `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(args, &p); err != nil || strings.TrimSpace(p.Prompt) == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "prompt and choices are required"}
	}
	if len(p.Choices) < 2 || len(p.Choices) > 4 {
		return nil, &jsonRPCError{Code: -32602, Message: "choices must have 2 to 4 items"}
	}
	for _, c := range p.Choices {
		if strings.TrimSpace(c.Label) == "" {
			return nil, &jsonRPCError{Code: -32602, Message: "each choice needs a label"}
		}
	}
	body, _ := json.Marshal(map[string]interface{}{"prompt": p.Prompt, "choices": p.Choices})
	resp, err := http.Post(fmt.Sprintf("%s/api/sessions/%s/choice-cards", s.apiURL, sessionID), "application/json", bytes.NewReader(body))
	if err != nil {
		return toolResult("offer_choices failed: "+err.Error(), true), nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return toolResult(fmt.Sprintf("offer_choices failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody))), true), nil
	}
	return toolResult("選択肢を出しました。ユーザーはボタンか普通の返信で答えます。待たずにターンを終えてかまいません。押された選択肢はユーザーのメッセージとして届きます。", false), nil
}
