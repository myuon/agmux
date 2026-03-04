package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

type Client struct {
	apiKey string
	model  string
	http   *http.Client
}

func New(model string) (*Client, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}
	return &Client{
		apiKey: apiKey,
		model:  model,
		http:   &http.Client{Timeout: 30 * time.Second},
	}, nil
}

type AnalysisResult struct {
	Status   string `json:"status"`
	Action   string `json:"action"`
	Reason   string `json:"reason"`
	SendText string `json:"send_text"`
}

func (c *Client) Analyze(prompt string) (*AnalysisResult, error) {
	reqBody := map[string]interface{}{
		"model":      c.model,
		"max_tokens": 1024,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errBody)
		return nil, fmt.Errorf("api error %d: %v", resp.StatusCode, errBody)
	}

	var apiResp struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(apiResp.Content) == 0 {
		return nil, fmt.Errorf("empty response from API")
	}

	text := apiResp.Content[0].Text

	// Extract JSON from the response (may be wrapped in markdown code blocks)
	jsonStr := extractJSON(text)

	var result AnalysisResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("parse analysis result: %w (raw: %s)", err, text)
	}

	return &result, nil
}

func extractJSON(text string) string {
	// Try to find JSON block in markdown code blocks
	start := -1
	end := -1
	for i := 0; i < len(text); i++ {
		if text[i] == '{' && start == -1 {
			start = i
		}
		if text[i] == '}' {
			end = i + 1
		}
	}
	if start >= 0 && end > start {
		return text[start:end]
	}
	return text
}
