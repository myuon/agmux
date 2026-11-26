package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type testClient struct {
	conn *websocket.Conn
	msgs chan string
}

// dialHub connects and pumps incoming message types into a channel (a gorilla
// connection cannot be read again after a read deadline error).
func dialHub(t *testing.T, srv *httptest.Server) *testClient {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	tc := &testClient{conn: c, msgs: make(chan string, 16)}
	go func() {
		for {
			_, raw, err := c.ReadMessage()
			if err != nil {
				return
			}
			var m Message
			_ = json.Unmarshal(raw, &m)
			tc.msgs <- m.Type
		}
	}()
	return tc
}

func readType(c *testClient, d time.Duration) (string, bool) {
	select {
	case typ := <-c.msgs:
		return typ, true
	case <-time.After(d):
		return "", false
	}
}

func TestHubStreamUpdateSubscription(t *testing.T) {
	hub := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	go hub.Run()
	srv := httptest.NewServer(http.HandlerFunc(hub.HandleWS))
	defer srv.Close()

	subscribed := dialHub(t, srv)
	other := dialHub(t, srv)

	if err := subscribed.conn.WriteJSON(map[string]any{"type": "subscribe", "sessionIds": []string{"X"}}); err != nil {
		t.Fatal(err)
	}
	if typ, ok := readType(subscribed, 2*time.Second); !ok || typ != "subscribed" {
		t.Fatalf("expected subscribed ack, got %q ok=%v", typ, ok)
	}

	hub.BroadcastToSubscribers("X", Message{Type: "stream_update", Data: map[string]any{"sessionId": "X"}})
	if typ, ok := readType(subscribed, 2*time.Second); !ok || typ != "stream_update" {
		t.Fatalf("subscribed client should get stream_update, got %q ok=%v", typ, ok)
	}
	if typ, ok := readType(other, 300*time.Millisecond); ok {
		t.Fatalf("never-subscribed client got %q", typ)
	}

	// A different session must not reach the subscriber of X.
	hub.BroadcastToSubscribers("Y", Message{Type: "stream_update", Data: map[string]any{"sessionId": "Y"}})
	if typ, ok := readType(subscribed, 300*time.Millisecond); ok {
		t.Fatalf("got stream_update for unsubscribed session: %q", typ)
	}

	// Non-stream messages reach everyone.
	hub.Broadcast(Message{Type: "session_status", Data: nil})
	for name, c := range map[string]*testClient{"subscribed": subscribed, "other": other} {
		if typ, ok := readType(c, 2*time.Second); !ok || typ != "session_status" {
			t.Fatalf("%s client should get broadcast, got %q ok=%v", name, typ, ok)
		}
	}

	// Subscribe replaces the set: dropping X stops delivery.
	if err := subscribed.conn.WriteJSON(map[string]any{"type": "subscribe", "sessionIds": []string{}}); err != nil {
		t.Fatal(err)
	}
	if typ, ok := readType(subscribed, 2*time.Second); !ok || typ != "subscribed" {
		t.Fatalf("expected ack, got %q", typ)
	}
	hub.BroadcastToSubscribers("X", Message{Type: "stream_update"})
	if typ, ok := readType(subscribed, 300*time.Millisecond); ok {
		t.Fatalf("got %q after unsubscribing", typ)
	}
}
