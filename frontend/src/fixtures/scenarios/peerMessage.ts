// Scenario: a turn started by a message from another session (peer).
// The CLI writes no user line for it; the body arrives only in result.origin.
export const peerMessageLines = [
  {"type":"user","message":{"role":"user","content":[{"type":"text","text":"テストを回しておいて"}]},"parent_tool_use_id":null,"session_id":"00000000-0000-0000-0000-000000000020","uuid":"00000000-0000-0000-0000-000000000021"},
  {"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"テストはすべて通りました。"}]},"parent_tool_use_id":null,"session_id":"00000000-0000-0000-0000-000000000020","uuid":"00000000-0000-0000-0000-000000000022"},
  {"type":"result","subtype":"success","is_error":false,"duration_ms":12000,"num_turns":2,"result":"テストはすべて通りました。","total_cost_usd":0.05,"session_id":"00000000-0000-0000-0000-000000000020","uuid":"00000000-0000-0000-0000-000000000023"},
  {"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"別のセッションから頼まれた、マーケットメイクの検証を始めました。公開データと模擬だけで進めます。"}]},"parent_tool_use_id":null,"session_id":"00000000-0000-0000-0000-000000000020","uuid":"00000000-0000-0000-0000-000000000024"},
  {"type":"result","subtype":"success","is_error":false,"duration_ms":55000,"num_turns":3,"result":"別のセッションから頼まれた、マーケットメイクの検証を始めました。","total_cost_usd":0.4,"origin":{"kind":"peer","from":"uds:/tmp/cc-socks/00000.sock","name":"x-signal-lab-70","fromMode":"bypass","body":"x-signal-lab セッション(x-signal-lab-70)からの依頼です。\n\n背景:\n- 板に買いと売りの指値を出し続ける形が成り立つかを確かめたい\n\nお願い:\n- 公開データと模擬だけで検証してください"},"session_id":"00000000-0000-0000-0000-000000000020","uuid":"00000000-0000-0000-0000-000000000025"}
];
