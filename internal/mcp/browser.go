package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// agentTabResponse mirrors the server's browser.AgentTab JSON.
type agentTabResponse struct {
	TargetID string `json:"targetId"`
	CDPURL   string `json:"cdpUrl"`
	Reused   bool   `json:"reused"`
}

func (s *Server) handleGetLoggedInBrowser(args json.RawMessage) (interface{}, *jsonRPCError) {
	sessionID, rpcErr := s.resolveSessionID(args)
	if rpcErr != nil {
		return nil, rpcErr
	}
	tab, err := s.apiAgentTab(sessionID)
	if err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}
	return toolResult(formatAgentTab(sessionID, tab), false), nil
}

// agentBrowserSessionName is the agent-browser --session name for an agmux
// session. It embeds the full agmux session id so two agmux sessions can never
// share an agent-browser daemon (and thereby a tab).
func agentBrowserSessionName(sessionID string) string {
	var b strings.Builder
	for _, r := range sessionID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	return "agmux-" + b.String()
}

func formatAgentTab(sessionID string, tab *agentTabResponse) string {
	name := agentBrowserSessionName(sessionID)
	prefix := fmt.Sprintf("agent-browser --session %s --cdp %s", name, tab.CDPURL)
	state := "新しいタブを割り当てました"
	if tab.Reused {
		state = "このセッション専用の既存タブを再利用します"
	}
	return fmt.Sprintf(`ユーザーのログイン状態を共有した Chrome に接続できます（%s）。

このセッション専用タブの接続情報:
- ターゲットID: %s
- CDP エンドポイント（このセッション専用。自分のタブだけが見える）: %s
- agent-browser セッション名: %s

agent-browser のコマンドは、毎回必ず次の形式で実行してください（--session と --cdp を省略しない）:

  %s <command> [args]

例:
  %s open https://example.com/
  %s snapshot -i

注意:
- このエンドポイントからはあなたのセッション専用のタブだけが見えます（ユーザーのタブや他セッションのタブは見えず、操作もできません）。エンドポイントを迂回して Chrome に直接接続しないでください。
- 終了時は `+"`agent-browser --session %s close`"+` で足ります（タブ自体はセッション終了時に自動で閉じられます）。
- ログイン状態はユーザー本人のものです。必要なサイトの閲覧・操作に限り、Cookie やページ内容を出力・記録しないでください。
- ログインが必要なのにログインされていない場合（ログインページにリダイレクトされた等）は、自分でログインしようとせず request_browser_login でユーザーに再ログインを依頼してください。
- 通常の Web 閲覧にはこのブラウザを使わず、agent-browser を単独で使ってください。
- Chrome が再起動された等でタブが無効になった場合は、このツールをもう一度呼ぶと新しいタブが割り当てられます（返された --cdp の URL を使い直すこと）。
`, state, tab.TargetID, tab.CDPURL, name, prefix, prefix, prefix, name)
}

func (s *Server) apiAgentTab(sessionID string) (*agentTabResponse, error) {
	url := fmt.Sprintf("%s/api/remote-browser/agent-tabs/%s", s.apiURL, sessionID)
	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}
	var out agentTabResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out.CDPURL == "" {
		return nil, fmt.Errorf("API returned no cdp url")
	}
	return &out, nil
}
