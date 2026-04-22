package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/leolin310148/bb-browser-go/internal/protocol"
)

// CdpTargetInfo describes a CDP target (browser tab).
type CdpTargetInfo struct {
	ID    string `json:"targetId"`
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

type pendingCommand struct {
	ch     chan json.RawMessage
	errCh  chan error
	method string
}

// CdpConnection manages the browser-level WebSocket to Chrome DevTools Protocol.
type CdpConnection struct {
	Host string
	Port int

	TabManager      *TabStateManager
	CurrentTargetID string

	socket    *websocket.Conn
	writeMu   sync.Mutex // serializes socket.WriteMessage calls (gorilla requires one writer)
	pending   sync.Map   // id -> *pendingCommand
	nextID    atomic.Int64
	sessions  sync.Map // targetId -> sessionId
	attached  sync.Map // sessionId -> targetId
	connected atomic.Bool

	LastError string

	readyMu    sync.Mutex
	readyCh    chan struct{}
	readyErr   error
	readyOnce  sync.Once

	// sessionListeners for flat-mode session events
	sessionMu        sync.Mutex
	sessionListeners map[int64]sessionListener
}

type sessionListener struct {
	sessionID string
	ch        chan json.RawMessage
	errCh     chan error
}

// NewCdpConnection creates a new CDP connection.
func NewCdpConnection(host string, port int, tabManager *TabStateManager) *CdpConnection {
	c := &CdpConnection{
		Host:             host,
		Port:             port,
		TabManager:       tabManager,
		readyCh:          make(chan struct{}),
		sessionListeners: make(map[int64]sessionListener),
	}
	c.nextID.Store(1)
	return c
}

// Connected returns whether the CDP WebSocket is open.
func (c *CdpConnection) Connected() bool {
	return c.connected.Load()
}

// Connect establishes the WebSocket connection to Chrome.
func (c *CdpConnection) Connect() error {
	if c.connected.Load() {
		return nil
	}

	// Fetch the WebSocket debugger URL
	versionURL := fmt.Sprintf("http://%s:%d/json/version", c.Host, c.Port)
	resp, err := http.Get(versionURL)
	if err != nil {
		c.LastError = err.Error()
		return fmt.Errorf("cannot reach Chrome CDP at %s:%d: %w", c.Host, c.Port, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(body, &version); err != nil {
		c.LastError = "invalid JSON from /json/version"
		return fmt.Errorf("invalid CDP version response: %w", err)
	}
	if version.WebSocketDebuggerURL == "" {
		c.LastError = "missing webSocketDebuggerUrl"
		return fmt.Errorf("CDP endpoint missing webSocketDebuggerUrl")
	}

	// Connect WebSocket
	ws, _, err := websocket.DefaultDialer.Dial(version.WebSocketDebuggerURL, nil)
	if err != nil {
		c.LastError = err.Error()
		return fmt.Errorf("WebSocket dial failed: %w", err)
	}
	c.socket = ws
	c.connected.Store(true)
	c.LastError = ""

	// Start message reader
	go c.readLoop()

	// Discover and auto-attach existing page targets
	if _, err := c.BrowserCommand("Target.setDiscoverTargets", map[string]interface{}{"discover": true}); err != nil {
		return err
	}

	result, err := c.BrowserCommand("Target.getTargets", nil)
	if err != nil {
		return err
	}

	var targets struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			Title    string `json:"title"`
			URL      string `json:"url"`
		} `json:"targetInfos"`
	}
	if err := json.Unmarshal(result, &targets); err == nil {
		for _, t := range targets.TargetInfos {
			if t.Type == "page" {
				// Best-effort attach — some targets may not be attachable
				c.AttachAndEnable(t.TargetID) // ignore error
			}
		}
	}

	// Signal ready
	c.readyOnce.Do(func() {
		close(c.readyCh)
	})

	return nil
}

// WaitUntilReady blocks until CDP connection is established.
func (c *CdpConnection) WaitUntilReady(timeout time.Duration) error {
	if c.connected.Load() {
		return nil
	}
	select {
	case <-c.readyCh:
		return c.readyErr
	case <-time.After(timeout):
		return fmt.Errorf("CDP connection timeout after %v", timeout)
	}
}

// Disconnect closes the CDP connection.
func (c *CdpConnection) Disconnect() {
	c.connected.Store(false)
	if c.socket != nil {
		c.socket.Close()
		c.socket = nil
	}
	// Reject all pending
	c.pending.Range(func(key, value interface{}) bool {
		cmd := value.(*pendingCommand)
		cmd.errCh <- fmt.Errorf("CDP connection closed")
		c.pending.Delete(key)
		return true
	})
}

func (c *CdpConnection) readLoop() {
	for {
		if c.socket == nil {
			return
		}
		_, raw, err := c.socket.ReadMessage()
		if err != nil {
			c.connected.Store(false)
			c.LastError = "CDP WebSocket closed unexpectedly"
			// Reject all pending
			c.pending.Range(func(key, value interface{}) bool {
				cmd := value.(*pendingCommand)
				cmd.errCh <- fmt.Errorf("CDP connection closed")
				c.pending.Delete(key)
				return true
			})
			return
		}

		var msg map[string]json.RawMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		// Response to a command (has "id" field)
		if idRaw, ok := msg["id"]; ok {
			var id int64
			if json.Unmarshal(idRaw, &id) == nil {
				if v, ok := c.pending.LoadAndDelete(id); ok {
					cmd := v.(*pendingCommand)
					if errRaw, hasErr := msg["error"]; hasErr {
						var cdpErr struct {
							Message string `json:"message"`
						}
						json.Unmarshal(errRaw, &cdpErr)
						cmd.errCh <- fmt.Errorf("%s: %s", cmd.method, cdpErr.Message)
					} else if result, hasResult := msg["result"]; hasResult {
						cmd.ch <- result
					} else {
						cmd.ch <- json.RawMessage("{}")
					}
				}
			}
			// Also check session listeners
			c.handleSessionResponse(raw, msg)
			continue
		}

		// Event messages
		var method string
		if methodRaw, ok := msg["method"]; ok {
			json.Unmarshal(methodRaw, &method)
		}

		switch method {
		case "Target.attachedToTarget":
			c.handleAttached(msg)
		case "Target.detachedFromTarget":
			c.handleDetached(msg)
		case "Target.targetCreated":
			c.handleTargetCreated(msg)
		case "Target.targetDestroyed":
			c.handleTargetDestroyed(msg)
		default:
			// Flat protocol: session events carry sessionId
			if sessionRaw, ok := msg["sessionId"]; ok {
				var sessionID string
				json.Unmarshal(sessionRaw, &sessionID)
				if v, ok := c.attached.Load(sessionID); ok {
					targetID := v.(string)
					c.handleSessionEvent(targetID, method, msg)
				}
			}
		}
	}
}

func (c *CdpConnection) handleSessionResponse(raw []byte, msg map[string]json.RawMessage) {
	sessionRaw, ok := msg["sessionId"]
	if !ok {
		return
	}
	var sessionID string
	json.Unmarshal(sessionRaw, &sessionID)

	idRaw, ok := msg["id"]
	if !ok {
		return
	}
	var id int64
	if json.Unmarshal(idRaw, &id) != nil {
		return
	}

	c.sessionMu.Lock()
	listener, ok := c.sessionListeners[id]
	if ok && listener.sessionID == sessionID {
		delete(c.sessionListeners, id)
	} else {
		ok = false
	}
	c.sessionMu.Unlock()

	if !ok {
		return
	}

	if errRaw, hasErr := msg["error"]; hasErr {
		var cdpErr struct{ Message string `json:"message"` }
		json.Unmarshal(errRaw, &cdpErr)
		listener.errCh <- fmt.Errorf("%s", cdpErr.Message)
	} else if result, hasResult := msg["result"]; hasResult {
		listener.ch <- result
	} else {
		listener.ch <- json.RawMessage("{}")
	}
}

func (c *CdpConnection) handleAttached(msg map[string]json.RawMessage) {
	var params struct {
		SessionID  string `json:"sessionId"`
		TargetInfo struct {
			TargetID string `json:"targetId"`
		} `json:"targetInfo"`
	}
	if raw, ok := msg["params"]; ok {
		json.Unmarshal(raw, &params)
	}
	if params.SessionID != "" && params.TargetInfo.TargetID != "" {
		c.sessions.Store(params.TargetInfo.TargetID, params.SessionID)
		c.attached.Store(params.SessionID, params.TargetInfo.TargetID)
	}
}

func (c *CdpConnection) handleDetached(msg map[string]json.RawMessage) {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	if raw, ok := msg["params"]; ok {
		json.Unmarshal(raw, &params)
	}
	if params.SessionID == "" {
		return
	}
	if v, ok := c.attached.LoadAndDelete(params.SessionID); ok {
		targetID := v.(string)
		c.sessions.Delete(targetID)
		c.TabManager.RemoveTab(targetID)
		if c.CurrentTargetID == targetID {
			c.CurrentTargetID = ""
		}
	}
}

func (c *CdpConnection) handleTargetCreated(msg map[string]json.RawMessage) {
	var params struct {
		TargetInfo struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfo"`
	}
	if raw, ok := msg["params"]; ok {
		json.Unmarshal(raw, &params)
	}
	if params.TargetInfo.Type == "page" && params.TargetInfo.TargetID != "" {
		go c.AttachAndEnable(params.TargetInfo.TargetID)
	}
}

func (c *CdpConnection) handleTargetDestroyed(msg map[string]json.RawMessage) {
	var params struct {
		TargetID string `json:"targetId"`
	}
	if raw, ok := msg["params"]; ok {
		json.Unmarshal(raw, &params)
	}
	if params.TargetID == "" {
		return
	}
	if v, ok := c.sessions.LoadAndDelete(params.TargetID); ok {
		sessionID := v.(string)
		c.attached.Delete(sessionID)
	}
	c.TabManager.RemoveTab(params.TargetID)
	if c.CurrentTargetID == params.TargetID {
		c.CurrentTargetID = ""
	}
}

func normalizeHeaders(raw json.RawMessage) map[string]string {
	var headers map[string]interface{}
	if json.Unmarshal(raw, &headers) != nil {
		return nil
	}
	result := make(map[string]string, len(headers))
	for k, v := range headers {
		result[k] = fmt.Sprintf("%v", v)
	}
	return result
}

func (c *CdpConnection) handleSessionEvent(targetID, method string, msg map[string]json.RawMessage) {
	tab := c.TabManager.GetTab(targetID)
	if tab == nil {
		return
	}

	paramsRaw := msg["params"]

	switch method {
	case "Page.javascriptDialogOpening":
		if tab.DialogHandler != nil {
			params := map[string]interface{}{
				"accept": tab.DialogHandler.Accept,
			}
			if tab.DialogHandler.PromptText != "" {
				params["promptText"] = tab.DialogHandler.PromptText
			}
			go c.SessionCommand(targetID, "Page.handleJavaScriptDialog", params)
		}

	case "Network.requestWillBeSent":
		var params struct {
			RequestID string `json:"requestId"`
			Request   struct {
				URL      string          `json:"url"`
				Method   string          `json:"method"`
				Headers  json.RawMessage `json:"headers"`
				PostData string          `json:"postData"`
			} `json:"request"`
			Type      string  `json:"type"`
			Timestamp float64 `json:"timestamp"`
		}
		if json.Unmarshal(paramsRaw, &params) == nil && params.RequestID != "" {
			tab.AddNetworkRequest(params.RequestID, protocol.NetworkRequestInfo{
				URL:            params.Request.URL,
				Method:         params.Request.Method,
				Type:           params.Type,
				Timestamp:      int64(params.Timestamp * 1000),
				RequestHeaders: normalizeHeaders(params.Request.Headers),
				RequestBody:    params.Request.PostData,
			})
		}

	case "Network.responseReceived":
		var params struct {
			RequestID string `json:"requestId"`
			Response  struct {
				Status     int             `json:"status"`
				StatusText string          `json:"statusText"`
				Headers    json.RawMessage `json:"headers"`
				MimeType   string          `json:"mimeType"`
			} `json:"response"`
		}
		if json.Unmarshal(paramsRaw, &params) == nil && params.RequestID != "" {
			status := params.Response.Status
			tab.UpdateNetworkResponse(params.RequestID, &status, params.Response.StatusText,
				normalizeHeaders(params.Response.Headers), params.Response.MimeType)
		}

	case "Network.loadingFailed":
		var params struct {
			RequestID string `json:"requestId"`
			ErrorText string `json:"errorText"`
		}
		if json.Unmarshal(paramsRaw, &params) == nil && params.RequestID != "" {
			tab.UpdateNetworkFailure(params.RequestID, params.ErrorText)
		}

	case "Runtime.consoleAPICalled":
		var params struct {
			Type       string `json:"type"`
			Args       []struct {
				Value       interface{} `json:"value"`
				Description string      `json:"description"`
			} `json:"args"`
			Timestamp  float64 `json:"timestamp"`
			StackTrace *struct {
				CallFrames []struct {
					URL        string `json:"url"`
					LineNumber int    `json:"lineNumber"`
				} `json:"callFrames"`
			} `json:"stackTrace"`
		}
		if json.Unmarshal(paramsRaw, &params) == nil {
			var texts []string
			for _, arg := range params.Args {
				if s, ok := arg.Value.(string); ok {
					texts = append(texts, s)
				} else if arg.Value != nil {
					texts = append(texts, fmt.Sprintf("%v", arg.Value))
				} else if arg.Description != "" {
					texts = append(texts, arg.Description)
				}
			}
			consoleType := params.Type
			typeMap := map[string]string{"warning": "warn"}
			if mapped, ok := typeMap[consoleType]; ok {
				consoleType = mapped
			}
			validTypes := map[string]bool{"log": true, "info": true, "warn": true, "error": true, "debug": true}
			if !validTypes[consoleType] {
				consoleType = "log"
			}

			info := protocol.ConsoleMessageInfo{
				Type:      consoleType,
				Text:      strings.Join(texts, " "),
				Timestamp: int64(params.Timestamp),
			}
			if params.StackTrace != nil && len(params.StackTrace.CallFrames) > 0 {
				frame := params.StackTrace.CallFrames[0]
				info.URL = frame.URL
				ln := frame.LineNumber
				info.LineNumber = &ln
			}
			tab.AddConsoleMessage(info)
		}

	case "Runtime.exceptionThrown":
		var params struct {
			ExceptionDetails struct {
				Text      string `json:"text"`
				URL       string `json:"url"`
				LineNumber   int `json:"lineNumber"`
				ColumnNumber int `json:"columnNumber"`
				Exception struct {
					Description string `json:"description"`
				} `json:"exception"`
				StackTrace *struct {
					CallFrames []struct {
						FunctionName string `json:"functionName"`
						URL          string `json:"url"`
						LineNumber   int    `json:"lineNumber"`
						ColumnNumber int    `json:"columnNumber"`
					} `json:"callFrames"`
				} `json:"stackTrace"`
			} `json:"exceptionDetails"`
		}
		if json.Unmarshal(paramsRaw, &params) == nil {
			details := params.ExceptionDetails
			message := details.Exception.Description
			if message == "" {
				message = details.Text
			}
			if message == "" {
				message = "JavaScript exception"
			}

			info := protocol.JSErrorInfo{
				Message:   message,
				Timestamp: time.Now().UnixMilli(),
			}
			if details.URL != "" {
				info.URL = details.URL
			} else if details.StackTrace != nil && len(details.StackTrace.CallFrames) > 0 {
				info.URL = details.StackTrace.CallFrames[0].URL
			}
			if details.LineNumber > 0 {
				ln := details.LineNumber
				info.LineNumber = &ln
			}
			if details.ColumnNumber > 0 {
				cn := details.ColumnNumber
				info.ColumnNumber = &cn
			}
			if details.StackTrace != nil && len(details.StackTrace.CallFrames) > 0 {
				var lines []string
				for _, frame := range details.StackTrace.CallFrames {
					name := frame.FunctionName
					if name == "" {
						name = "<anonymous>"
					}
					lines = append(lines, fmt.Sprintf("%s (%s:%d:%d)", name, frame.URL, frame.LineNumber, frame.ColumnNumber))
				}
				info.StackTrace = strings.Join(lines, "\n")
			}
			tab.AddJSError(info)
		}
	}
}

// --- Target management ---

// AttachAndEnable attaches to a target and enables CDP domains.
func (c *CdpConnection) AttachAndEnable(targetID string) error {
	if _, ok := c.sessions.Load(targetID); ok {
		c.TabManager.AddTab(targetID)
		return nil
	}

	result, err := c.BrowserCommand("Target.attachToTarget", map[string]interface{}{
		"targetId": targetID,
		"flatten":  true,
	})
	if err != nil {
		// Some targets cannot be attached (DevTools, extensions, etc.)
		// Register in tab manager anyway so tab_list still works.
		return err
	}

	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(result, &attached); err != nil {
		return err
	}

	c.sessions.Store(targetID, attached.SessionID)
	c.attached.Store(attached.SessionID, targetID)
	c.TabManager.AddTab(targetID)

	// Enable domains (best-effort)
	for _, domain := range []string{"Page.enable", "Runtime.enable", "Network.enable", "DOM.enable", "Accessibility.enable"} {
		c.SessionCommand(targetID, domain, nil)
	}

	return nil
}

// GetTargets returns all CDP targets.
func (c *CdpConnection) GetTargets() ([]CdpTargetInfo, error) {
	result, err := c.BrowserCommand("Target.getTargets", nil)
	if err != nil {
		return nil, err
	}

	var data struct {
		TargetInfos []CdpTargetInfo `json:"targetInfos"`
	}
	if err := json.Unmarshal(result, &data); err != nil {
		return nil, err
	}

	var pages []CdpTargetInfo
	for _, t := range data.TargetInfos {
		pages = append(pages, CdpTargetInfo{
			ID:    t.ID,
			Type:  t.Type,
			Title: t.Title,
			URL:   t.URL,
		})
	}
	return pages, nil
}

// findTargetByExactURL returns the page target whose URL exactly matches the
// given string. If multiple tabs match, the one with the highest LastActionSeq
// wins (most recently interacted with); ties fall back to first-seen order.
// Returns nil if no match or on error.
func findTargetByExactURL(c *CdpConnection, url string) *CdpTargetInfo {
	targets, err := c.GetTargets()
	if err != nil {
		return nil
	}
	var best *CdpTargetInfo
	bestSeq := -1
	for i, t := range targets {
		if t.Type != "page" || t.URL != url {
			continue
		}
		seq := -1
		if ts := c.TabManager.GetTab(t.ID); ts != nil {
			seq = ts.LastActionSeq
		}
		if best == nil || seq > bestSeq {
			best = &targets[i]
			bestSeq = seq
		}
	}
	return best
}

// EnsurePageTarget resolves a tab reference to a valid page target.
func (c *CdpConnection) EnsurePageTarget(tabRef string) (*CdpTargetInfo, error) {
	allTargets, err := c.GetTargets()
	if err != nil {
		return nil, err
	}

	var pages []CdpTargetInfo
	for _, t := range allTargets {
		if t.Type == "page" {
			pages = append(pages, t)
		}
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("no page target found")
	}

	var target *CdpTargetInfo

	if tabRef != "" {
		// Try short ID
		if resolved := c.TabManager.ResolveShortID(tabRef); resolved != "" {
			for i, t := range pages {
				if t.ID == resolved {
					target = &pages[i]
					break
				}
			}
		}
		// Try full target ID
		if target == nil {
			for i, t := range pages {
				if t.ID == tabRef {
					target = &pages[i]
					break
				}
			}
		}
		// Try numeric index
		if target == nil {
			if num, err := strconv.Atoi(tabRef); err == nil && num >= 0 && num < len(pages) {
				target = &pages[num]
			}
		}
		if target == nil {
			return nil, fmt.Errorf("tab not found: %s", tabRef)
		}
	} else if c.CurrentTargetID != "" {
		for i, t := range pages {
			if t.ID == c.CurrentTargetID {
				target = &pages[i]
				break
			}
		}
	}

	if target == nil {
		target = &pages[0]
	}

	c.CurrentTargetID = target.ID
	// Best-effort attach — the target may already be attached via auto-attach
	c.AttachAndEnable(target.ID)
	return target, nil
}

// HasSession checks if a session exists for a target.
func (c *CdpConnection) HasSession(targetID string) bool {
	_, ok := c.sessions.Load(targetID)
	return ok
}

// --- Command sending ---

// BrowserCommand sends a browser-level CDP command and returns the result.
func (c *CdpConnection) BrowserCommand(method string, params interface{}) (json.RawMessage, error) {
	if c.socket == nil {
		return nil, fmt.Errorf("CDP not connected")
	}

	id := c.nextID.Add(1)
	payload := map[string]interface{}{
		"id":     id,
		"method": method,
	}
	if params != nil {
		payload["params"] = params
	}

	cmd := &pendingCommand{
		ch:     make(chan json.RawMessage, 1),
		errCh:  make(chan error, 1),
		method: method,
	}
	c.pending.Store(id, cmd)

	data, _ := json.Marshal(payload)
	c.writeMu.Lock()
	err := c.socket.WriteMessage(websocket.TextMessage, data)
	c.writeMu.Unlock()
	if err != nil {
		c.pending.Delete(id)
		return nil, err
	}

	select {
	case result := <-cmd.ch:
		return result, nil
	case err := <-cmd.errCh:
		return nil, err
	case <-time.After(30 * time.Second):
		c.pending.Delete(id)
		return nil, fmt.Errorf("timeout waiting for %s", method)
	}
}

// SessionCommand sends a session-level CDP command (flat protocol).
func (c *CdpConnection) SessionCommand(targetID, method string, params interface{}) (json.RawMessage, error) {
	if c.socket == nil {
		return nil, fmt.Errorf("CDP not connected")
	}

	sessionIDVal, ok := c.sessions.Load(targetID)
	if !ok {
		if err := c.AttachAndEnable(targetID); err != nil {
			return nil, err
		}
		sessionIDVal, ok = c.sessions.Load(targetID)
		if !ok {
			return nil, fmt.Errorf("no session for target %s", targetID)
		}
	}
	sessionID := sessionIDVal.(string)

	id := c.nextID.Add(1)
	payload := map[string]interface{}{
		"id":        id,
		"method":    method,
		"sessionId": sessionID,
	}
	if params != nil {
		payload["params"] = params
	}

	listener := sessionListener{
		sessionID: sessionID,
		ch:        make(chan json.RawMessage, 1),
		errCh:     make(chan error, 1),
	}
	c.sessionMu.Lock()
	c.sessionListeners[id] = listener
	c.sessionMu.Unlock()

	data, _ := json.Marshal(payload)
	c.writeMu.Lock()
	err := c.socket.WriteMessage(websocket.TextMessage, data)
	c.writeMu.Unlock()
	if err != nil {
		c.sessionMu.Lock()
		delete(c.sessionListeners, id)
		c.sessionMu.Unlock()
		return nil, err
	}

	select {
	case result := <-listener.ch:
		return result, nil
	case err := <-listener.errCh:
		return nil, err
	case <-time.After(30 * time.Second):
		c.sessionMu.Lock()
		delete(c.sessionListeners, id)
		c.sessionMu.Unlock()
		return nil, fmt.Errorf("timeout waiting for %s on session %s", method, sessionID)
	}
}

// PageCommand sends a command scoped to the active frame of a tab.
func (c *CdpConnection) PageCommand(targetID, method string, params map[string]interface{}) (json.RawMessage, error) {
	tab := c.TabManager.GetTab(targetID)
	if tab != nil && tab.ActiveFrameID != "" {
		if params == nil {
			params = map[string]interface{}{}
		}
		params["frameId"] = tab.ActiveFrameID
	}
	return c.SessionCommand(targetID, method, params)
}

// Evaluate executes JavaScript on a target and returns the result.
func (c *CdpConnection) Evaluate(targetID, expression string, returnByValue bool) (json.RawMessage, error) {
	result, err := c.SessionCommand(targetID, "Runtime.evaluate", map[string]interface{}{
		"expression":    expression,
		"awaitPromise":  true,
		"returnByValue": returnByValue,
	})
	if err != nil {
		return nil, err
	}

	var evalResult struct {
		Result struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(result, &evalResult); err != nil {
		return nil, err
	}

	if evalResult.ExceptionDetails != nil {
		msg := evalResult.ExceptionDetails.Exception.Description
		if msg == "" {
			msg = evalResult.ExceptionDetails.Text
		}
		if msg == "" {
			msg = "Runtime.evaluate failed"
		}
		return nil, fmt.Errorf("%s", msg)
	}

	return evalResult.Result.Value, nil
}
