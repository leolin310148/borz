package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/leolin310148/bb-browser-go/internal/config"
	"github.com/leolin310148/bb-browser-go/internal/protocol"
)

// registerRESTRoutes attaches /v1/* handlers to mux. Each handler builds a
// protocol.Request from the JSON body (or query string for GETs) and dispatches
// it through the existing CDP pipeline.
func (s *Server) registerRESTRoutes(mux *http.ServeMux) {
	// Navigation
	mux.HandleFunc("/v1/open", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionOpen, URL: body.URL, New: body.New, TabID: body.tabID()}
	}))
	mux.HandleFunc("/v1/back", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionBack, TabID: body.tabID()}
	}))
	mux.HandleFunc("/v1/forward", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionForward, TabID: body.tabID()}
	}))
	mux.HandleFunc("/v1/refresh", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionRefresh, TabID: body.tabID()}
	}))
	mux.HandleFunc("/v1/close", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionClose, TabID: body.tabID()}
	}))

	// Interaction
	mux.HandleFunc("/v1/click", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionClick, Ref: body.Ref, TabID: body.tabID()}
	}))
	mux.HandleFunc("/v1/hover", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionHover, Ref: body.Ref, TabID: body.tabID()}
	}))
	mux.HandleFunc("/v1/fill", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionFill, Ref: body.Ref, Text: body.Text, TabID: body.tabID()}
	}))
	mux.HandleFunc("/v1/type", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionType_, Ref: body.Ref, Text: body.Text, TabID: body.tabID()}
	}))
	mux.HandleFunc("/v1/check", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionCheck, Ref: body.Ref, TabID: body.tabID()}
	}))
	mux.HandleFunc("/v1/uncheck", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionUncheck, Ref: body.Ref, TabID: body.tabID()}
	}))
	mux.HandleFunc("/v1/select", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionSelect, Ref: body.Ref, Value: body.Value, TabID: body.tabID()}
	}))
	mux.HandleFunc("/v1/press", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionPress, Key: body.Key, Modifiers: body.Modifiers, TabID: body.tabID()}
	}))
	mux.HandleFunc("/v1/key", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{
			Action:    protocol.ActionKey,
			KeyType:   body.KeyType,
			Key:       body.Key,
			Code:      body.Code,
			Text:      body.Text,
			Modifiers: body.Modifiers,
			TabID:     body.tabID(),
		}
	}))
	mux.HandleFunc("/v1/mouse", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{
			Action:     protocol.ActionMouse,
			MouseType:  body.MouseType,
			X:          body.X,
			Y:          body.Y,
			Button:     body.Button,
			DeltaX:     body.DeltaX,
			DeltaY:     body.DeltaY,
			ClickCount: body.ClickCount,
			Modifiers:  body.Modifiers,
			TabID:      body.tabID(),
		}
	}))
	mux.HandleFunc("/v1/clipboard-read", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionClipboardRead, TabID: body.tabID()}
	}))
	mux.HandleFunc("/v1/scroll", s.restJSON(func(body restBody) *protocol.Request {
		req := &protocol.Request{Action: protocol.ActionScroll, Direction: body.Direction, TabID: body.tabID()}
		if body.Pixels != nil {
			req.Pixels = body.Pixels
		}
		return req
	}))
	mux.HandleFunc("/v1/eval", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionEval, Script: body.Script, TabID: body.tabID()}
	}))
	mux.HandleFunc("/v1/wait", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionWait, Ms: body.Ms, TabID: body.tabID()}
	}))

	// Observation
	mux.HandleFunc("/v1/snapshot", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{
			Action:      protocol.ActionSnapshot,
			Interactive: body.Interactive,
			Compact:     body.Compact,
			MaxDepth:    body.MaxDepth,
			Selector:    body.Selector,
			TabID:       body.tabID(),
		}
	}))
	mux.HandleFunc("/v1/screenshot", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionScreenshot, Path: body.Path, TabID: body.tabID()}
	}))
	mux.HandleFunc("/v1/get", s.restJSON(func(body restBody) *protocol.Request {
		return &protocol.Request{Action: protocol.ActionGet, Attribute: body.Attribute, Ref: body.Ref, TabID: body.tabID()}
	}))
	mux.HandleFunc("/v1/network", s.restJSON(func(body restBody) *protocol.Request {
		cmd := body.Command
		if cmd == "" {
			cmd = "requests"
		}
		return &protocol.Request{
			Action:         protocol.ActionNetwork,
			NetworkCommand: cmd,
			Filter:         body.Filter,
			WithBody:       body.WithBody,
			Method:         body.Method,
			Status:         body.Status,
			Since:          body.sinceValue(),
			TabID:          body.tabID(),
		}
	}))
	mux.HandleFunc("/v1/console", s.restJSON(func(body restBody) *protocol.Request {
		cmd := body.Command
		if cmd == "" {
			cmd = "get"
		}
		return &protocol.Request{
			Action:         protocol.ActionConsole,
			ConsoleCommand: cmd,
			Filter:         body.Filter,
			Since:          body.sinceValue(),
			TabID:          body.tabID(),
		}
	}))
	mux.HandleFunc("/v1/errors", s.restJSON(func(body restBody) *protocol.Request {
		cmd := body.Command
		if cmd == "" {
			cmd = "get"
		}
		return &protocol.Request{
			Action:        protocol.ActionErrors,
			ErrorsCommand: cmd,
			Filter:        body.Filter,
			Since:         body.sinceValue(),
			TabID:         body.tabID(),
		}
	}))

	// Tabs — GET lists, POST creates
	mux.HandleFunc("/v1/tabs", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.dispatchAndWrite(w, &protocol.Request{ID: newReqID(), Action: protocol.ActionTabList})
		case http.MethodPost:
			body, err := readBody(r)
			if err != nil {
				sendJSON(w, 400, map[string]string{"error": err.Error()})
				return
			}
			url := body.URL
			if url == "" {
				url = "about:blank"
			}
			s.dispatchAndWrite(w, &protocol.Request{ID: newReqID(), Action: protocol.ActionTabNew, URL: url})
		default:
			sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
		}
	})
	mux.HandleFunc("/v1/tabs/select", s.restJSON(func(body restBody) *protocol.Request {
		req := &protocol.Request{Action: protocol.ActionTabSelect}
		if body.Index != nil {
			req.Index = body.Index
		} else if body.TabID != nil {
			req.TabID = body.TabID
		}
		return req
	}))
	mux.HandleFunc("/v1/tabs/close", s.restJSON(func(body restBody) *protocol.Request {
		req := &protocol.Request{Action: protocol.ActionTabClose}
		if body.Index != nil {
			req.Index = body.Index
		} else if body.TabID != nil {
			req.TabID = body.TabID
		}
		return req
	}))

	// Fetch (authenticated HTTP through the browser session)
	mux.HandleFunc("/v1/fetch", s.restJSON(func(body restBody) *protocol.Request {
		method := body.Method
		if method == "" {
			method = "GET"
		}
		script := fmt.Sprintf(`(async () => {
			try {
				const resp = await fetch(%q, { method: %q, credentials: 'include' });
				const contentType = resp.headers.get('content-type') || '';
				const isJson = contentType.includes('application/json');
				const text = await resp.text();
				return {
					status: resp.status,
					statusText: resp.statusText,
					contentType: contentType,
					body: isJson ? JSON.parse(text) : text
				};
			} catch(e) {
				return { error: e.message };
			}
		})()`, body.URL, method)
		return &protocol.Request{Action: protocol.ActionEval, Script: script, TabID: body.tabID()}
	}))
}

// restBody is the union of fields accepted by /v1/* POST bodies. Fields not
// relevant to a given route are simply ignored.
type restBody struct {
	URL         string      `json:"url,omitempty"`
	New         bool        `json:"new,omitempty"`
	Ref         string      `json:"ref,omitempty"`
	Text        string      `json:"text,omitempty"`
	Key         string      `json:"key,omitempty"`
	Modifiers   []string    `json:"modifiers,omitempty"`
	Direction   string      `json:"direction,omitempty"`
	Pixels      *int        `json:"pixels,omitempty"`
	Attribute   string      `json:"attribute,omitempty"`
	Interactive bool        `json:"interactive,omitempty"`
	Compact     bool        `json:"compact,omitempty"`
	MaxDepth    *int        `json:"maxDepth,omitempty"`
	Selector    string      `json:"selector,omitempty"`
	Value       string      `json:"value,omitempty"`
	Script      string      `json:"script,omitempty"`
	Ms          *int        `json:"ms,omitempty"`
	Path        string      `json:"path,omitempty"`
	Method      string      `json:"method,omitempty"`
	Command     string      `json:"command,omitempty"`
	Filter      string      `json:"filter,omitempty"`
	Status      string      `json:"status,omitempty"`
	WithBody    bool        `json:"withBody,omitempty"`
	Since       interface{} `json:"since,omitempty"`
	TabID       interface{} `json:"tabId,omitempty"`
	Tab         string      `json:"tab,omitempty"`
	Index       *int        `json:"index,omitempty"`

	// Key input
	KeyType string `json:"keyType,omitempty"`
	Code    string `json:"code,omitempty"`

	// Mouse input
	MouseType  string   `json:"mouseType,omitempty"`
	X          *float64 `json:"x,omitempty"`
	Y          *float64 `json:"y,omitempty"`
	Button     string   `json:"button,omitempty"`
	DeltaX     *float64 `json:"deltaX,omitempty"`
	DeltaY     *float64 `json:"deltaY,omitempty"`
	ClickCount *int     `json:"clickCount,omitempty"`
}

// tabID returns the tab identifier to pass through to the dispatcher.
// Accepts either `tabId` (string or number) or the short `tab` alias.
func (b restBody) tabID() interface{} {
	if b.TabID != nil {
		return b.TabID
	}
	if b.Tab != "" {
		return b.Tab
	}
	return nil
}

// sinceValue normalizes the `since` field: accepts number, numeric string, or
// the sentinel "last_action".
func (b restBody) sinceValue() interface{} {
	switch v := b.Since.(type) {
	case nil:
		return nil
	case float64:
		return int(v)
	case string:
		if v == "last_action" {
			return v
		}
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return b.Since
}

// restJSON wraps a request-builder into an http.HandlerFunc that handles
// method validation, body parsing, dispatch, and response serialization.
func (s *Server) restJSON(build func(restBody) *protocol.Request) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
			return
		}
		body, err := readBody(r)
		if err != nil {
			sendJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		req := build(body)
		req.ID = newReqID()
		s.dispatchAndWrite(w, req)
	}
}

// dispatchAndWrite waits for CDP readiness then runs DispatchRequest with the
// same timeout semantics as handleCommand.
func (s *Server) dispatchAndWrite(w http.ResponseWriter, req *protocol.Request) {
	if !s.cdp.Connected() {
		if err := s.cdp.WaitUntilReady(time.Duration(config.CommandTimeout) * time.Second); err != nil {
			cdpTarget := fmt.Sprintf("%s:%d", s.cdp.Host, s.cdp.Port)
			sendJSON(w, 503, map[string]interface{}{
				"id":      req.ID,
				"success": false,
				"error":   fmt.Sprintf("Chrome not connected (CDP at %s)", cdpTarget),
				"reason":  s.cdp.LastError,
			})
			return
		}
	}

	done := make(chan *protocol.Response, 1)
	go func() { done <- DispatchRequest(s.cdp, req) }()

	select {
	case resp := <-done:
		status := 200
		if !resp.Success {
			status = 400
		}
		sendJSON(w, status, resp)
	case <-time.After(time.Duration(config.CommandTimeout) * time.Second):
		sendJSON(w, 504, &protocol.Response{ID: req.ID, Success: false, Error: "Command timeout"})
	}
}

func readBody(r *http.Request) (restBody, error) {
	var body restBody
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return body, fmt.Errorf("failed to read body: %w", err)
	}
	if len(raw) == 0 {
		return body, nil
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return body, fmt.Errorf("invalid JSON: %w", err)
	}
	return body, nil
}

func newReqID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
