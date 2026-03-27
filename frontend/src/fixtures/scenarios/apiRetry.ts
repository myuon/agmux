// Scenario: API retry — last event is a retry (trailing indicator visible)
export const apiRetryLines = [
  {
    "type": "assistant",
    "message": {
      "model": "claude-opus-4-6",
      "id": "msg_01EXAMPLE000000000000001",
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "text",
          "text": "ファイルを読み込んでいます..."
        }
      ],
      "stop_reason": null,
      "stop_sequence": null,
      "usage": {
        "input_tokens": 100,
        "output_tokens": 10
      }
    },
    "parent_tool_use_id": null,
    "session_id": "00000000-0000-0000-0000-000000000000",
    "uuid": "00000000-0000-0000-0000-000000000000"
  },
  {
    "type": "system",
    "subtype": "api_retry",
    "attempt": 1,
    "max_retries": 10,
    "retry_delay_ms": 615.34,
    "error_status": 529,
    "error": "rate_limit",
    "session_id": "00000000-0000-0000-0000-000000000000",
    "uuid": "00000000-0000-0000-0000-000000000001"
  },
  {
    "type": "system",
    "subtype": "api_retry",
    "attempt": 2,
    "max_retries": 10,
    "retry_delay_ms": 1005.22,
    "error_status": 529,
    "error": "rate_limit",
    "session_id": "00000000-0000-0000-0000-000000000000",
    "uuid": "00000000-0000-0000-0000-000000000002"
  },
  {
    "type": "system",
    "subtype": "api_retry",
    "attempt": 4,
    "max_retries": 10,
    "retry_delay_ms": 32956.75,
    "error_status": 529,
    "error": "rate_limit",
    "session_id": "00000000-0000-0000-0000-000000000000",
    "uuid": "00000000-0000-0000-0000-000000000004"
  },
];

// Scenario: API retry resolved — retries happened but assistant resumed (no indicator)
export const apiRetryResolvedLines = [
  {
    "type": "assistant",
    "message": {
      "model": "claude-opus-4-6",
      "id": "msg_01EXAMPLE000000000000001",
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "text",
          "text": "ファイルを読み込んでいます..."
        }
      ],
      "stop_reason": null,
      "stop_sequence": null,
      "usage": {
        "input_tokens": 100,
        "output_tokens": 10
      }
    },
    "parent_tool_use_id": null,
    "session_id": "00000000-0000-0000-0000-000000000000",
    "uuid": "00000000-0000-0000-0000-000000000000"
  },
  {
    "type": "system",
    "subtype": "api_retry",
    "attempt": 3,
    "max_retries": 10,
    "retry_delay_ms": 2180.75,
    "error_status": 529,
    "error": "rate_limit",
    "session_id": "00000000-0000-0000-0000-000000000000",
    "uuid": "00000000-0000-0000-0000-000000000003"
  },
  {
    "type": "assistant",
    "message": {
      "model": "claude-opus-4-6",
      "id": "msg_01EXAMPLE000000000000002",
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "text",
          "text": "リトライ後に処理を再開しました。"
        }
      ],
      "stop_reason": "end_turn",
      "stop_sequence": null,
      "usage": {
        "input_tokens": 200,
        "output_tokens": 20
      }
    },
    "parent_tool_use_id": null,
    "session_id": "00000000-0000-0000-0000-000000000000",
    "uuid": "00000000-0000-0000-0000-000000000005"
  },
];
