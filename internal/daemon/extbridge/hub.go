// Package extbridge bridges a Chrome extension to the daemon over WebSocket.
//
// The extension extends the daemon with capabilities CDP cannot provide
// (chrome.cookies cross-domain, chrome.tabs/windows events, bookmarks, history,
// downloads, etc.). Communication is JSON over a single WebSocket initiated
// by the extension service worker:
//
//	daemon → extension : {type:"request", id, method, params}
//	extension → daemon : {type:"response", id, result?, error?}
//	                     {type:"event",    name,  data}
package extbridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultRequestTimeout = 10 * time.Second
	eventRingSize         = 512
	writeWait             = 5 * time.Second
	pongWait              = 60 * time.Second
	pingPeriod            = 30 * time.Second
)

// Event is a push from the extension (e.g. tab created/closed).
type Event struct {
	Seq  uint64          `json:"seq"`
	Time time.Time       `json:"time"`
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`
}

type pending struct {
	ch chan response
}

type response struct {
	Result json.RawMessage
	Err    string
}

type wireMessage struct {
	Type   string          `json:"type"`
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Name   string          `json:"name,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

// Hub manages connected extension clients and routes requests/events.
//
// Only one extension is expected at a time, but Hub tolerates multiple:
// requests fan out to the most recently connected client.
type Hub struct {
	upgrader websocket.Upgrader

	mu      sync.Mutex
	clients map[*client]struct{}
	pending map[string]*pending
	nextID  uint64

	evMu     sync.Mutex
	evRing   []Event
	evHead   int
	evCount  int
	evNextSeq uint64
}

type client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
	once sync.Once
}

// NewHub creates a Hub with sensible defaults.
func NewHub() *Hub {
	return &Hub{
		upgrader: websocket.Upgrader{
			// Daemon binds 127.0.0.1 by default; the extension talks to
			// localhost. Don't enforce origin check — keeps "load unpacked"
			// development frictionless. Token auth in the URL query gates
			// the upgrade path itself.
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		clients: make(map[*client]struct{}),
		pending: make(map[string]*pending),
		evRing:  make([]Event, eventRingSize),
	}
}

// ServeWS upgrades an HTTP request to WebSocket and registers the client.
// Blocks until the client disconnects.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &client{
		hub:  h,
		conn: conn,
		send: make(chan []byte, 32),
	}
	h.register(c)
	go c.writeLoop()
	c.readLoop()
	h.unregister(c)
}

// Connected reports the number of currently attached extension clients.
func (h *Hub) Connected() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// Request sends a method call to the most recently connected client and
// waits for a response. Returns ErrNoClient if no extension is connected.
func (h *Hub) Request(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	c := h.pickClient()
	if c == nil {
		return nil, ErrNoClient
	}

	id := fmt.Sprintf("r%d", atomic.AddUint64(&h.nextID, 1))
	p := &pending{ch: make(chan response, 1)}
	h.mu.Lock()
	h.pending[id] = p
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.pending, id)
		h.mu.Unlock()
	}()

	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		rawParams = b
	}
	msg := wireMessage{Type: "request", ID: id, Method: method, Params: rawParams}
	b, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	select {
	case c.send <- b:
	default:
		return nil, errors.New("extension send buffer full")
	}

	select {
	case resp := <-p.ch:
		if resp.Err != "" {
			return nil, errors.New(resp.Err)
		}
		return resp.Result, nil
	case <-time.After(timeout):
		return nil, ErrTimeout
	}
}

// Events returns events with Seq greater than sinceSeq, in chronological order.
// Pass 0 to get everything currently buffered.
func (h *Hub) Events(sinceSeq uint64) []Event {
	h.evMu.Lock()
	defer h.evMu.Unlock()
	if h.evCount == 0 {
		return nil
	}
	out := make([]Event, 0, h.evCount)
	for i := 0; i < h.evCount; i++ {
		idx := (h.evHead - h.evCount + i + len(h.evRing)) % len(h.evRing)
		ev := h.evRing[idx]
		if ev.Seq > sinceSeq {
			out = append(out, ev)
		}
	}
	return out
}

// LatestSeq returns the highest assigned event sequence.
func (h *Hub) LatestSeq() uint64 {
	h.evMu.Lock()
	defer h.evMu.Unlock()
	return h.evNextSeq
}

func (h *Hub) recordEvent(name string, data json.RawMessage) {
	h.evMu.Lock()
	defer h.evMu.Unlock()
	h.evNextSeq++
	ev := Event{Seq: h.evNextSeq, Time: time.Now(), Name: name, Data: data}
	h.evRing[h.evHead] = ev
	h.evHead = (h.evHead + 1) % len(h.evRing)
	if h.evCount < len(h.evRing) {
		h.evCount++
	}
}

func (h *Hub) register(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) unregister(c *client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		c.once.Do(func() { close(c.send) })
	}
	h.mu.Unlock()
}

func (h *Hub) pickClient() *client {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		return c
	}
	return nil
}

func (h *Hub) deliverResponse(id string, result json.RawMessage, errStr string) {
	h.mu.Lock()
	p, ok := h.pending[id]
	if ok {
		delete(h.pending, id)
	}
	h.mu.Unlock()
	if !ok {
		return
	}
	p.ch <- response{Result: result, Err: errStr}
}

func (c *client) readLoop() {
	c.conn.SetReadLimit(1 << 20)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var msg wireMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "response":
			c.hub.deliverResponse(msg.ID, msg.Result, msg.Error)
		case "event":
			c.hub.recordEvent(msg.Name, msg.Data)
		}
	}
}

func (c *client) writeLoop() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// Sentinel errors.
var (
	ErrNoClient = errors.New("no extension connected")
	ErrTimeout  = errors.New("extension request timed out")
)
