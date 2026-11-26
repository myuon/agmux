package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

type Message struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`

	// onlySession, when set, restricts delivery to clients subscribed to that
	// session (see BroadcastToSubscribers). Never serialized.
	onlySession string
}

type wsClient struct {
	conn *websocket.Conn
	send chan []byte

	subMu sync.RWMutex
	subs  map[string]bool // session IDs subscribed for stream_update
}

func (c *wsClient) setSubscriptions(ids []string) {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	c.subMu.Lock()
	c.subs = set
	c.subMu.Unlock()
}

func (c *wsClient) isSubscribed(id string) bool {
	c.subMu.RLock()
	defer c.subMu.RUnlock()
	return c.subs[id]
}

// clientFrame is a text frame sent from the client.
type clientFrame struct {
	Type       string   `json:"type"`
	SessionIDs []string `json:"sessionIds"`
}

type Hub struct {
	mu         sync.RWMutex
	clients    map[*wsClient]bool
	broadcast  chan Message
	register   chan *wsClient
	unregister chan *wsClient
	logger     *slog.Logger
	upgrader   websocket.Upgrader
}

func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		clients:    make(map[*wsClient]bool),
		broadcast:  make(chan Message, 256),
		register:   make(chan *wsClient),
		unregister: make(chan *wsClient),
		logger:     logger.With("component", "ws"),
		upgrader:   websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }, EnableCompression: true},
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()

		case msg := <-h.broadcast:
			data, err := json.Marshal(msg)
			if err != nil {
				h.logger.Error("ws marshal error", "error", err)
				continue
			}
			h.mu.RLock()
			for client := range h.clients {
				if msg.onlySession != "" && !client.isSubscribed(msg.onlySession) {
					continue
				}
				select {
				case client.send <- data:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

func (h *Hub) Broadcast(msg Message) {
	h.enqueue(msg)
}

// BroadcastToSubscribers sends msg only to clients that subscribed to
// sessionID via a {"type":"subscribe","sessionIds":[...]} frame.
func (h *Hub) BroadcastToSubscribers(sessionID string, msg Message) {
	msg.onlySession = sessionID
	h.enqueue(msg)
}

func (h *Hub) enqueue(msg Message) {
	select {
	case h.broadcast <- msg:
	default:
		h.logger.Warn("ws broadcast channel full, dropping message")
	}
}

func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("ws upgrade error", "error", err)
		return
	}

	client := &wsClient{
		conn: conn,
		send: make(chan []byte, 256),
	}
	h.register <- client

	go client.writePump()
	go client.readPump(h)
}

func (c *wsClient) writePump() {
	defer c.conn.Close()
	for msg := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}

func (c *wsClient) readPump(h *Hub) {
	defer func() {
		h.unregister <- c
		c.conn.Close()
	}()
	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		var frame clientFrame
		if json.Unmarshal(raw, &frame) != nil || frame.Type != "subscribe" {
			continue
		}
		c.setSubscriptions(frame.SessionIDs)
		// Ack so the client can catch up on lines it missed before the
		// subscription became effective.
		ack, err := json.Marshal(Message{Type: "subscribed", Data: map[string]interface{}{"sessionIds": frame.SessionIDs}})
		if err == nil {
			h.sendTo(c, ack)
		}
	}
}

// sendTo queues data for a single client, dropping it if the client is gone or slow.
func (h *Hub) sendTo(c *wsClient, data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if !h.clients[c] {
		return
	}
	select {
	case c.send <- data:
	default:
	}
}
