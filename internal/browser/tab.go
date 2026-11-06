package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Input limits.
const (
	maxTextLen = 4096
	maxKeyLen  = 64
)

// FrameMetadata is the Page.screencastFrame metadata a client needs to map
// image pixels back to CSS pixels: cssX = imgX * deviceWidth / imageWidth.
type FrameMetadata struct {
	OffsetTop       float64 `json:"offsetTop"`
	PageScaleFactor float64 `json:"pageScaleFactor"`
	DeviceWidth     float64 `json:"deviceWidth"`
	DeviceHeight    float64 `json:"deviceHeight"`
	ScrollOffsetX   float64 `json:"scrollOffsetX"`
	ScrollOffsetY   float64 `json:"scrollOffsetY"`
	Timestamp       float64 `json:"timestamp"`
}

type frameMessage struct {
	Type     string        `json:"type"` // "frame"
	Format   string        `json:"format"`
	Data     string        `json:"data"` // base64 jpeg
	Metadata FrameMetadata `json:"metadata"`
}

type urlMessage struct {
	Type string `json:"type"` // "url"
	URL  string `json:"url"`
}

// Subscription receives serialized JSON messages (frames, url updates).
type Subscription struct {
	C    <-chan []byte
	ch   chan []byte
	tab  *UserTab
	once sync.Once
}

// Close unsubscribes.
func (s *Subscription) Close() { s.once.Do(func() { s.tab.unsubscribe(s) }) }

// UserTab is the dedicated tab shown to the user. It owns the screencast and
// fans frames out to subscribers.
type UserTab struct {
	targetID string
	cdp      *cdpClient
	logger   *slog.Logger

	mu        sync.Mutex
	subs      map[*Subscription]struct{}
	casting   bool
	closed    bool
	lastFrame []byte
	lastURL   []byte
	lastFocus []byte

	// evalFn overrides Runtime.evaluate (tests).
	evalFn func(ctx context.Context, expr string) (json.RawMessage, error)
}

func newUserTab(ctx context.Context, port int, targetID string, logger *slog.Logger) (*UserTab, error) {
	t := &UserTab{targetID: targetID, logger: logger, subs: map[*Subscription]struct{}{}}
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/devtools/page/%s", port, targetID)
	c, err := dialCDP(ctx, wsURL, t.onEvent)
	if err != nil {
		return nil, err
	}
	t.cdp = c
	go func() {
		<-c.Done()
		t.mu.Lock()
		t.closed = true
		for s := range t.subs {
			close(s.ch)
			delete(t.subs, s)
		}
		t.mu.Unlock()
	}()
	return t, nil
}

// TargetID returns the CDP target id.
func (t *UserTab) TargetID() string { return t.targetID }

func (t *UserTab) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

// Done is closed when the tab's CDP connection ends.
func (t *UserTab) Done() <-chan struct{} { return t.cdp.Done() }

// Close drops the CDP connection (does not close the tab itself).
func (t *UserTab) Close() { t.cdp.Close() }

func (t *UserTab) onEvent(method string, params json.RawMessage) {
	switch method {
	case "Page.screencastFrame":
		var f struct {
			Data      string        `json:"data"`
			Metadata  FrameMetadata `json:"metadata"`
			SessionID int           `json:"sessionId"`
		}
		if json.Unmarshal(params, &f) != nil {
			return
		}
		// Always ack, or Chrome stops sending frames.
		t.cdp.Notify("Page.screencastFrameAck", map[string]any{"sessionId": f.SessionID})
		msg, err := json.Marshal(frameMessage{Type: "frame", Format: "jpeg", Data: f.Data, Metadata: f.Metadata})
		if err != nil {
			return
		}
		t.mu.Lock()
		t.lastFrame = msg
		t.broadcastLocked(msg)
		t.mu.Unlock()
	case "Page.frameNavigated":
		var n struct {
			Frame struct {
				ParentID string `json:"parentId"`
				URL      string `json:"url"`
			} `json:"frame"`
		}
		if json.Unmarshal(params, &n) != nil || n.Frame.ParentID != "" {
			return
		}
		msg, _ := json.Marshal(urlMessage{Type: "url", URL: n.Frame.URL})
		t.mu.Lock()
		t.lastURL = msg
		t.lastFocus = nil // stale after navigation
		t.broadcastLocked(msg)
		t.mu.Unlock()
		t.scheduleFocusCheck()
	}
}

func (t *UserTab) broadcastLocked(msg []byte) {
	for s := range t.subs {
		select {
		case s.ch <- msg:
		default: // slow consumer: drop (a later frame supersedes this one)
		}
	}
}

// broadcastReliableLocked is like broadcastLocked but for state messages that
// must not be lost (a later frame does not supersede them): when a
// subscriber's queue is full (screencast frames flood it) the oldest queued
// message is discarded to make room.
func (t *UserTab) broadcastReliableLocked(msg []byte) {
	for s := range t.subs {
		select {
		case s.ch <- msg:
			continue
		default:
		}
		select {
		case <-s.ch: // drop the oldest
		default:
		}
		select {
		case s.ch <- msg:
		default:
		}
	}
}

// Subscribe registers a consumer and starts the screencast if needed.
func (t *UserTab) Subscribe(ctx context.Context) (*Subscription, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, errors.New("user tab closed")
	}
	s := &Subscription{ch: make(chan []byte, 8), tab: t}
	s.C = s.ch
	if t.lastURL != nil {
		s.ch <- t.lastURL
	}
	if t.lastFocus != nil {
		s.ch <- t.lastFocus
	}
	if t.lastFrame != nil {
		s.ch <- t.lastFrame
	}
	t.subs[s] = struct{}{}
	needStart := !t.casting
	t.casting = true
	t.mu.Unlock()

	if needStart {
		if err := t.startScreencast(ctx); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

func (t *UserTab) startScreencast(ctx context.Context) error {
	// Off-screen windows report visibilityState=hidden and frames stall;
	// focus emulation must be enabled BEFORE startScreencast.
	if _, err := t.cdp.Call(ctx, "Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true}); err != nil {
		t.setCasting(false)
		return err
	}
	if _, err := t.cdp.Call(ctx, "Page.enable", nil); err != nil {
		t.setCasting(false)
		return err
	}
	if _, err := t.cdp.Call(ctx, "Page.startScreencast", map[string]any{
		"format": "jpeg", "quality": 60, "everyNthFrame": 1,
	}); err != nil {
		t.setCasting(false)
		return err
	}
	return nil
}

func (t *UserTab) setCasting(v bool) {
	t.mu.Lock()
	t.casting = v
	t.mu.Unlock()
}

func (t *UserTab) unsubscribe(s *Subscription) {
	t.mu.Lock()
	if _, ok := t.subs[s]; ok {
		delete(t.subs, s)
		close(s.ch)
	}
	stop := len(t.subs) == 0 && t.casting && !t.closed
	if stop {
		t.casting = false
	}
	t.mu.Unlock()
	if stop {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		t.cdp.Call(ctx, "Page.stopScreencast", nil)
	}
}

// InputMessage is a client->server message. Only fields relevant to Type are used.
type InputMessage struct {
	Type string `json:"type"` // tap | scroll | key | text | navigate | back | reload

	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	DeltaX float64 `json:"deltaX"`
	DeltaY float64 `json:"deltaY"`

	Action    string `json:"action"` // key: down | up | press
	Key       string `json:"key"`
	Code      string `json:"code"`
	Text      string `json:"text"`
	KeyCode   int    `json:"keyCode"`
	Modifiers int    `json:"modifiers"`

	URL string `json:"url"`
}

// ErrInvalidInput marks a rejected client message.
var ErrInvalidInput = errors.New("invalid input")

// HandleInput dispatches one input message to the page.
func (t *UserTab) HandleInput(ctx context.Context, m InputMessage) error {
	switch m.Type {
	case "tap":
		if err := t.mouse(ctx, "mousePressed", m.X, m.Y); err != nil {
			return err
		}
		err := t.mouse(ctx, "mouseReleased", m.X, m.Y)
		t.scheduleFocusCheck()
		return err
	case "scroll":
		_, err := t.cdp.Call(ctx, "Input.dispatchMouseEvent", map[string]any{
			"type": "mouseWheel", "x": m.X, "y": m.Y, "deltaX": m.DeltaX, "deltaY": m.DeltaY,
		})
		return err
	case "key":
		if len(m.Key) > maxKeyLen || len(m.Code) > maxKeyLen || len(m.Text) > maxKeyLen {
			return fmt.Errorf("%w: key too long", ErrInvalidInput)
		}
		if m.Key == "Tab" || m.Key == "Enter" {
			defer t.scheduleFocusCheck()
		}
		switch m.Action {
		case "down":
			return t.key(ctx, m, true)
		case "up":
			return t.key(ctx, m, false)
		case "press", "":
			if err := t.key(ctx, m, true); err != nil {
				return err
			}
			return t.key(ctx, m, false)
		}
		return fmt.Errorf("%w: unknown key action", ErrInvalidInput)
	case "text":
		if m.Text == "" || len(m.Text) > maxTextLen {
			return fmt.Errorf("%w: bad text length", ErrInvalidInput)
		}
		_, err := t.cdp.Call(ctx, "Input.insertText", map[string]any{"text": m.Text})
		return err
	case "navigate":
		if err := validateNavigateURL(m.URL); err != nil {
			return err
		}
		_, err := t.cdp.Call(ctx, "Page.navigate", map[string]any{"url": m.URL})
		t.scheduleFocusCheck()
		return err
	case "back":
		return t.back(ctx)
	case "reload":
		_, err := t.cdp.Call(ctx, "Page.reload", nil)
		return err
	}
	return fmt.Errorf("%w: unknown type", ErrInvalidInput)
}

func (t *UserTab) mouse(ctx context.Context, typ string, x, y float64) error {
	_, err := t.cdp.Call(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": typ, "x": x, "y": y, "button": "left", "clickCount": 1,
	})
	return err
}

func (t *UserTab) key(ctx context.Context, m InputMessage, down bool) error {
	p := map[string]any{"key": m.Key, "code": m.Code, "modifiers": m.Modifiers}
	if m.KeyCode != 0 {
		p["windowsVirtualKeyCode"] = m.KeyCode
	}
	switch {
	case !down:
		p["type"] = "keyUp"
	case m.Text != "":
		p["type"] = "keyDown"
		p["text"] = m.Text
	default:
		p["type"] = "rawKeyDown"
	}
	_, err := t.cdp.Call(ctx, "Input.dispatchKeyEvent", p)
	return err
}

func (t *UserTab) back(ctx context.Context) error {
	raw, err := t.cdp.Call(ctx, "Page.getNavigationHistory", nil)
	if err != nil {
		return err
	}
	var h struct {
		CurrentIndex int `json:"currentIndex"`
		Entries      []struct {
			ID int `json:"id"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return err
	}
	if h.CurrentIndex <= 0 || h.CurrentIndex >= len(h.Entries) {
		return nil
	}
	_, err = t.cdp.Call(ctx, "Page.navigateToHistoryEntry", map[string]any{"entryId": h.Entries[h.CurrentIndex-1].ID})
	return err
}

// validateNavigateURL allows only http(s) and about:blank; file:, javascript:,
// chrome: and similar are rejected.
func validateNavigateURL(raw string) error {
	if raw == "about:blank" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || len(raw) > 8192 {
		return fmt.Errorf("%w: bad url", ErrInvalidInput)
	}
	if s := strings.ToLower(u.Scheme); s != "http" && s != "https" {
		return fmt.Errorf("%w: unsupported scheme", ErrInvalidInput)
	}
	return nil
}
