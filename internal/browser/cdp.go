package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// cdpClient is a minimal Chrome DevTools Protocol client over a single
// websocket (browser-level or page-level). It never logs message contents.
type cdpClient struct {
	conn    *websocket.Conn
	wmu     sync.Mutex
	mu      sync.Mutex
	pending map[int64]chan cdpResponse
	nextID  atomic.Int64
	onEvent func(method string, params json.RawMessage)
	done    chan struct{}
	closed  bool
}

type cdpResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func dialCDP(ctx context.Context, url string, onEvent func(method string, params json.RawMessage)) (*cdpClient, error) {
	d := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := d.DialContext(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("dial cdp: %w", err)
	}
	conn.SetReadLimit(64 << 20) // screencast frames can be large
	c := &cdpClient{
		conn:    conn,
		pending: make(map[int64]chan cdpResponse),
		onEvent: onEvent,
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *cdpClient) readLoop() {
	defer close(c.done)
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			c.mu.Lock()
			c.closed = true
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		var msg cdpResponse
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		if msg.ID != 0 {
			c.mu.Lock()
			ch, ok := c.pending[msg.ID]
			delete(c.pending, msg.ID)
			c.mu.Unlock()
			if ok {
				ch <- msg
			}
			continue
		}
		if msg.Method != "" && c.onEvent != nil {
			c.onEvent(msg.Method, msg.Params)
		}
	}
}

func (c *cdpClient) write(id int64, method string, params any) error {
	payload := map[string]any{"id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.conn.WriteJSON(payload)
}

// Call sends a command and waits for its result.
func (c *cdpClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan cdpResponse, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("cdp connection closed")
	}
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.write(id, method, params); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, errors.New("cdp connection closed")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("cdp %s: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Notify sends a command without waiting for the response (used for frame acks).
func (c *cdpClient) Notify(method string, params any) error {
	return c.write(c.nextID.Add(1), method, params)
}

func (c *cdpClient) Done() <-chan struct{} { return c.done }

func (c *cdpClient) Close() { c.conn.Close() }
