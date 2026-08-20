package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/leolin310148/borz/internal/observability"
	"github.com/leolin310148/borz/internal/protocol"
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

	currentMu  sync.RWMutex
	tabOrderMu sync.Mutex
	tabOrder   []string
	connectMu  sync.Mutex
	socket     *websocket.Conn
	writeMu    sync.Mutex // serializes socket.WriteMessage calls (gorilla requires one writer)
	pending    sync.Map   // id -> *pendingCommand
	nextID     atomic.Int64
	sessions   sync.Map // targetId -> sessionId
	attached   sync.Map // sessionId -> targetId
	connected  atomic.Bool
	logger     *observability.Logger
	maxTabs    int
	// tabLimitMu serializes capacity checks. tabLimitClosing tracks successful
	// close requests until Chrome reports target destruction, preventing a
	// burst of targetCreated events from over-closing while events are in flight.
	tabLimitMu      sync.Mutex
	tabLimitClosing sync.Map // targetId -> struct{}

	// ensureBrowser, when set, is invoked by Connect after the CDP endpoint
	// turns out to be unreachable, giving the daemon a chance to launch its
	// managed browser before giving up. Guarded by connectMu along with
	// lastEnsureAt (rate limit so a crash-looping browser is not relaunched
	// on every request).
	ensureBrowser func() error
	lastEnsureAt  time.Time

	LastError string
	lastErrMu sync.RWMutex

	readyMu   sync.Mutex
	readyCh   chan struct{}
	readyErr  error
	readyOnce sync.Once

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

// ensureBrowserMinInterval rate-limits managed-browser launches so a browser
// that dies immediately after starting is not relaunched on every request.
const ensureBrowserMinInterval = 15 * time.Second

// SetEnsureBrowser installs the managed-browser launch hook. Must be called
// before Connect (it is not safe to set concurrently with connection attempts).
func (c *CdpConnection) SetEnsureBrowser(fn func() error) {
	c.ensureBrowser = fn
}

// SetMaxTabs configures the page-tab cap. It must be called before Connect.
// Values <= 0 disable the cap.
func (c *CdpConnection) SetMaxTabs(maxTabs int) {
	if maxTabs < 0 {
		maxTabs = 0
	}
	c.maxTabs = maxTabs
}

func (c *CdpConnection) registerTab(targetID string) {
	c.TabManager.AddTab(targetID)
	c.enforceTabLimit()
}

func (c *CdpConnection) enforceTabLimit() {
	if c.maxTabs <= 0 {
		return
	}
	c.tabLimitMu.Lock()
	defer c.tabLimitMu.Unlock()
	closeTabsOverLimit(
		c.TabManager,
		c,
		c.maxTabs,
		c.GetCurrentTargetID(),
		func(targetID string) bool {
			_, closing := c.tabLimitClosing.Load(targetID)
			return closing
		},
		func(targetID string, closing bool) {
			if closing {
				c.tabLimitClosing.Store(targetID, struct{}{})
			} else {
				c.tabLimitClosing.Delete(targetID)
			}
		},
	)
}

// maybeEnsureBrowser launches the managed browser after an unreachable CDP
// endpoint. Returns true when a launch was attempted and succeeded, meaning
// the caller should retry the endpoint once. Caller must hold connectMu.
func (c *CdpConnection) maybeEnsureBrowser() bool {
	if c.ensureBrowser == nil {
		return false
	}
	if time.Since(c.lastEnsureAt) < ensureBrowserMinInterval {
		return false
	}
	c.lastEnsureAt = time.Now()
	fmt.Fprintf(os.Stderr, "CDP unreachable; launching managed browser for %s:%d\n", c.Host, c.Port)
	c.log("info", "cdp_ensure_browser_started", observability.Fields{})
	if err := c.ensureBrowser(); err != nil {
		fmt.Fprintf(os.Stderr, "managed browser launch failed: %v\n", err)
		c.log("warn", "cdp_ensure_browser_failed", observability.Fields{ErrorCode: "browser_not_found"})
		return false
	}
	c.log("info", "cdp_ensure_browser_completed", observability.Fields{})
	return true
}

func (c *CdpConnection) setLastError(err string) {
	c.lastErrMu.Lock()
	defer c.lastErrMu.Unlock()
	c.LastError = err
}

func (c *CdpConnection) GetLastError() string {
	c.lastErrMu.RLock()
	defer c.lastErrMu.RUnlock()
	return c.LastError
}

func (c *CdpConnection) GetCurrentTargetID() string {
	c.currentMu.RLock()
	defer c.currentMu.RUnlock()
	return c.CurrentTargetID
}

func (c *CdpConnection) SetCurrentTargetID(targetID string) {
	c.currentMu.Lock()
	defer c.currentMu.Unlock()
	c.CurrentTargetID = targetID
}

func (c *CdpConnection) ClearCurrentTargetIDIf(targetID string) {
	c.currentMu.Lock()
	defer c.currentMu.Unlock()
	if c.CurrentTargetID == targetID {
		c.CurrentTargetID = ""
	}
}

func (c *CdpConnection) completeReady(err error) {
	c.readyMu.Lock()
	c.readyErr = err
	c.readyMu.Unlock()
	c.readyOnce.Do(func() {
		close(c.readyCh)
	})
}

func (c *CdpConnection) readyError() error {
	c.readyMu.Lock()
	defer c.readyMu.Unlock()
	return c.readyErr
}

// Connect establishes the WebSocket connection to Chrome.
func (c *CdpConnection) Connect() error {
	c.connectMu.Lock()
	defer c.connectMu.Unlock()

	if c.connected.Load() {
		return nil
	}

	// Fetch the WebSocket debugger URL
	versionURL := fmt.Sprintf("http://%s:%d/json/version", c.Host, c.Port)
	fmt.Fprintf(os.Stderr, "CDP connect attempt: %s\n", versionURL)
	c.log("info", "cdp_connect_started", observability.Fields{})
	httpClient := &http.Client{Timeout: 5 * time.Second}
	webSocketURL, err := fetchCDPWebSocketURL(httpClient, versionURL, c.Host, c.Port)
	if err != nil && c.maybeEnsureBrowser() {
		webSocketURL, err = fetchCDPWebSocketURL(httpClient, versionURL, c.Host, c.Port)
	}
	if err != nil {
		c.setLastError(err.Error())
		fmt.Fprintf(os.Stderr, "CDP connect failed: %v\n", err)
		c.log("warn", "cdp_connect_failed", observability.Fields{ErrorCode: "cdp_disconnected"})
		c.completeReady(err)
		return err
	}

	// A dying Chrome can still answer /json/version while its browser
	// WebSocket is already gone. Treat that handshake failure like any other
	// unreachable managed endpoint so --ensure-browser can replace it.
	ws, _, err := websocket.DefaultDialer.Dial(webSocketURL, nil)
	if err != nil && c.maybeEnsureBrowser() {
		if retryURL, fetchErr := fetchCDPWebSocketURL(httpClient, versionURL, c.Host, c.Port); fetchErr != nil {
			err = fetchErr
		} else {
			ws, _, err = websocket.DefaultDialer.Dial(retryURL, nil)
		}
	}
	if err != nil {
		err = fmt.Errorf("WebSocket dial failed: %w", err)
		c.setLastError(err.Error())
		fmt.Fprintf(os.Stderr, "CDP connect failed: %v\n", err)
		c.log("warn", "cdp_connect_failed", observability.Fields{ErrorCode: "cdp_disconnected"})
		c.completeReady(err)
		return err
	}
	c.socket = ws
	c.connected.Store(true)
	c.setLastError("")

	// Start message reader
	go c.readLoop()

	// Discover and auto-attach existing page targets
	if _, err := c.BrowserCommand("Target.setDiscoverTargets", map[string]interface{}{"discover": true}); err != nil {
		c.setLastError(err.Error())
		c.Disconnect()
		fmt.Fprintf(os.Stderr, "CDP setup failed: %v\n", err)
		c.log("warn", "cdp_setup_failed", observability.Fields{ErrorCode: "cdp_disconnected"})
		c.completeReady(err)
		return err
	}

	result, err := c.BrowserCommand("Target.getTargets", nil)
	if err != nil {
		c.setLastError(err.Error())
		c.Disconnect()
		fmt.Fprintf(os.Stderr, "CDP target discovery failed: %v\n", err)
		c.log("warn", "cdp_target_discovery_failed", observability.Fields{ErrorCode: "cdp_disconnected"})
		c.completeReady(err)
		return err
	}

	pageTargets := 0
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
				pageTargets++
				// Best-effort attach — some targets may not be attachable
				c.AttachAndEnable(t.TargetID) // ignore error
			}
		}
	}
	fmt.Fprintf(os.Stderr, "CDP connected: %s:%d (targets=%d, pages=%d)\n",
		c.Host, c.Port, len(targets.TargetInfos), pageTargets)
	c.log("info", "cdp_connected", observability.Fields{TargetCount: len(targets.TargetInfos), PageCount: pageTargets})

	// Signal ready
	c.completeReady(nil)

	return nil
}

func fetchCDPWebSocketURL(httpClient *http.Client, versionURL, host string, port int) (string, error) {
	resp, err := httpClient.Get(versionURL)
	if err != nil {
		return "", fmt.Errorf("cannot reach Chrome CDP at %s:%d: %w", host, port, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cannot reach Chrome CDP at %s:%d: /json/version returned HTTP %d", host, port, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read CDP version response: %w", err)
	}
	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(body, &version); err != nil {
		return "", fmt.Errorf("invalid CDP version response: %w", err)
	}
	if version.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("CDP endpoint missing webSocketDebuggerUrl")
	}
	return version.WebSocketDebuggerURL, nil
}

// WaitUntilReady blocks until CDP connection is established.
func (c *CdpConnection) WaitUntilReady(timeout time.Duration) error {
	if c.connected.Load() {
		return nil
	}
	select {
	case <-c.readyCh:
		if !c.connected.Load() {
			if err := c.Connect(); err == nil {
				return nil
			}
		}
		if c.connected.Load() {
			return nil
		}
		if err := c.readyError(); err != nil {
			return err
		}
		if lastErr := c.GetLastError(); lastErr != "" {
			return fmt.Errorf("CDP not connected: %s", lastErr)
		}
		return fmt.Errorf("CDP not connected")
	case <-time.After(timeout):
		return fmt.Errorf("CDP connection timeout after %v", timeout)
	}
}

// Disconnect closes the CDP connection.
//
// Note: c.socket is intentionally NOT cleared. Setting it to nil here would
// race with readLoop / Browser- / SessionCommand reads on c.socket. Closing
// the socket is enough — Read/Write return errors and the readLoop exits via
// its err branch. Code that needs to know whether the connection is live
// reads c.connected (atomic) instead of nil-checking c.socket.
func (c *CdpConnection) Disconnect() {
	c.connected.Store(false)
	if c.socket != nil {
		c.socket.Close()
	}
	c.rejectInflight(fmt.Errorf("CDP connection closed"))
}

func (c *CdpConnection) rejectInflight(err error) {
	c.pending.Range(func(key, value interface{}) bool {
		cmd := value.(*pendingCommand)
		c.pending.Delete(key)
		select {
		case cmd.errCh <- err:
		default:
		}
		return true
	})

	var listeners []sessionListener
	c.sessionMu.Lock()
	for id, listener := range c.sessionListeners {
		listeners = append(listeners, listener)
		delete(c.sessionListeners, id)
	}
	c.sessionMu.Unlock()
	for _, listener := range listeners {
		select {
		case listener.errCh <- err:
		default:
		}
	}
}

func (c *CdpConnection) readLoop() {
	for {
		if !c.connected.Load() {
			return
		}
		_, raw, err := c.socket.ReadMessage()
		if err != nil {
			if !c.connected.Load() {
				return
			}
			c.connected.Store(false)
			c.setLastError("CDP WebSocket closed unexpectedly")
			fmt.Fprintf(os.Stderr, "CDP WebSocket closed unexpectedly: %v\n", err)
			c.log("warn", "cdp_disconnected", observability.Fields{ErrorCode: "cdp_disconnected"})
			c.rejectInflight(fmt.Errorf("CDP connection closed"))
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

func (c *CdpConnection) log(level, event string, fields observability.Fields) {
	if c == nil || c.logger == nil {
		return
	}
	_ = c.logger.Log(level, event, fields)
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
		var cdpErr struct {
			Message string `json:"message"`
		}
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
		c.tabLimitClosing.Delete(targetID)
		// More than one attach can briefly exist for a target when discovery
		// and a user command race. A late detachedFromTarget for the older
		// session must not erase the newer session or clear the selected tab.
		if current, exists := c.sessions.Load(targetID); exists && current != params.SessionID {
			return
		}
		c.sessions.CompareAndDelete(targetID, params.SessionID)
		var replacement string
		c.attached.Range(func(sessionID, attachedTarget interface{}) bool {
			if attachedTarget == targetID {
				replacement = sessionID.(string)
				return false
			}
			return true
		})
		if replacement != "" {
			c.sessions.Store(targetID, replacement)
			return
		}
		// A detached CDP session does not mean the page target was closed.
		// Chrome can detach a session while DevTools, another CDP client, or a
		// raced attach replaces it. Keep the tab state (including snapshot refs)
		// and the selected-tab pointer; EnsurePageTarget will attach a fresh
		// session on the next request. Target.targetDestroyed is the definitive
		// lifecycle event that removes both.
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
	c.tabLimitClosing.Delete(params.TargetID)
	if v, ok := c.sessions.LoadAndDelete(params.TargetID); ok {
		sessionID := v.(string)
		c.attached.Delete(sessionID)
	}
	// A raced duplicate attach may have left additional session mappings.
	// Target destruction is definitive, so remove every mapping for it.
	c.attached.Range(func(sessionID, targetID interface{}) bool {
		if targetID == params.TargetID {
			c.attached.Delete(sessionID)
		}
		return true
	})
	c.TabManager.RemoveTab(params.TargetID)
	c.ClearCurrentTargetIDIf(params.TargetID)
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
	case "Runtime.executionContextCreated":
		var params struct {
			Context struct {
				ID      int64 `json:"id"`
				AuxData struct {
					FrameID   string `json:"frameId"`
					IsDefault bool   `json:"isDefault"`
				} `json:"auxData"`
			} `json:"context"`
		}
		json.Unmarshal(paramsRaw, &params)
		if params.Context.AuxData.IsDefault {
			tab.SetFrameExecutionContext(params.Context.AuxData.FrameID, params.Context.ID)
		}

	case "Runtime.executionContextDestroyed":
		var params struct {
			ExecutionContextID int64 `json:"executionContextId"`
		}
		json.Unmarshal(paramsRaw, &params)
		tab.RemoveExecutionContext(params.ExecutionContextID)

	case "Runtime.executionContextsCleared":
		tab.ClearExecutionContexts()

	case "Page.javascriptDialogOpening":
		// Record every dialog, armed handler or not. An unhandled dialog
		// blocks the renderer, so without this the only symptom would be
		// unrelated commands timing out 30s at a time.
		var params struct {
			Type              string `json:"type"`
			Message           string `json:"message"`
			URL               string `json:"url"`
			DefaultPrompt     string `json:"defaultPrompt"`
			HasBrowserHandler bool   `json:"hasBrowserHandler"`
		}
		json.Unmarshal(paramsRaw, &params)
		// borz can hold more than one CDP session on the same target, so the
		// same opening event is delivered once per session. Only the first
		// delivery may consume the armed handler or record the pending dialog —
		// a redelivery must not overwrite an AutoHandled record with an
		// unhandled one. A page cannot open an identical second dialog until
		// the first closes, by which point PendingDialog is nil again.
		if prev := tab.PeekPendingDialog(); prev != nil &&
			prev.Type == params.Type && prev.Message == params.Message {
			return
		}
		ev := &protocol.DialogEventInfo{
			Type:              params.Type,
			Message:           params.Message,
			URL:               params.URL,
			DefaultPrompt:     params.DefaultPrompt,
			HasBrowserHandler: params.HasBrowserHandler,
			OpenedAt:          time.Now().UnixMilli(),
		}
		handler := tab.ConsumeDialogHandler()
		if handler != nil {
			ev.AutoHandled = true
			ev.HandledAs = dialogHandledAs(handler.Accept)
			ev.PromptText = handler.PromptText
		}
		tab.SetPendingDialog(ev)
		if handler != nil {
			args := map[string]interface{}{
				"accept": handler.Accept,
			}
			if handler.PromptText != "" {
				args["promptText"] = handler.PromptText
			}
			go c.SessionCommand(targetID, "Page.handleJavaScriptDialog", args)
		}

	case "Page.javascriptDialogClosed":
		// Fires however the dialog was resolved — by our armed handler, by
		// `dialog accept`, or by a human clicking it in headful Chrome.
		var params struct {
			Result    bool   `json:"result"`
			UserInput string `json:"userInput"`
		}
		json.Unmarshal(paramsRaw, &params)
		// Also delivered once per attached session; ResolvePendingDialog is a
		// no-op when there is no pending dialog left, so the copy is ignored.
		tab.ResolvePendingDialog(params.Result, params.UserInput, time.Now())

	case "Page.fileChooserOpened":
		// Only fires while Page.setInterceptFileChooserDialog is enabled,
		// which ActionFileChooser turns on when arming. The native dialog is
		// suppressed; fulfill (or cancel) it with the armed handler, then
		// stop intercepting — arming is one-shot, like dialog.
		if handler := tab.ConsumeFileChooserHandler(); handler != nil {
			var params struct {
				Mode          string `json:"mode"`
				BackendNodeID int    `json:"backendNodeId"`
			}
			json.Unmarshal(paramsRaw, &params)
			go func() {
				if handler.Accept && params.BackendNodeID != 0 {
					c.SessionCommand(targetID, "DOM.setFileInputFiles", map[string]interface{}{
						"files":         handler.Files,
						"backendNodeId": params.BackendNodeID,
					})
				}
				c.SessionCommand(targetID, "Page.setInterceptFileChooserDialog", map[string]interface{}{"enabled": false})
			}()
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
			RedirectResponse *struct {
				Status        int             `json:"status"`
				StatusText    string          `json:"statusText"`
				Headers       json.RawMessage `json:"headers"`
				MimeType      string          `json:"mimeType"`
				FromDiskCache bool            `json:"fromDiskCache"`
			} `json:"redirectResponse"`
			Type      string  `json:"type"`
			Timestamp float64 `json:"timestamp"`
		}
		if json.Unmarshal(paramsRaw, &params) == nil && params.RequestID != "" {
			if params.RedirectResponse != nil {
				status := params.RedirectResponse.Status
				tab.UpdateNetworkResponse(params.RequestID, &status, params.RedirectResponse.StatusText,
					normalizeHeaders(params.RedirectResponse.Headers), params.RedirectResponse.MimeType, params.RedirectResponse.FromDiskCache)
			}
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
				Status        int             `json:"status"`
				StatusText    string          `json:"statusText"`
				Headers       json.RawMessage `json:"headers"`
				MimeType      string          `json:"mimeType"`
				FromDiskCache bool            `json:"fromDiskCache"`
			} `json:"response"`
		}
		if json.Unmarshal(paramsRaw, &params) == nil && params.RequestID != "" {
			status := params.Response.Status
			tab.UpdateNetworkResponse(params.RequestID, &status, params.Response.StatusText,
				normalizeHeaders(params.Response.Headers), params.Response.MimeType, params.Response.FromDiskCache)
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
			Type string `json:"type"`
			Args []struct {
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
				Text         string `json:"text"`
				URL          string `json:"url"`
				LineNumber   int    `json:"lineNumber"`
				ColumnNumber int    `json:"columnNumber"`
				Exception    struct {
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
	return c.attachAndEnable(targetID, true)
}

// attachAndEnable optionally registers a target as a user-visible tab. OOPIF
// iframe targets need a CDP session for eval but must not enter TabManager or
// count toward the page-tab retention limit.
func (c *CdpConnection) attachAndEnable(targetID string, registerAsTab bool) error {
	if _, ok := c.sessions.Load(targetID); ok {
		if registerAsTab {
			c.registerTab(targetID)
		}
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
	if registerAsTab {
		c.registerTab(targetID)
	}

	// Enable domains (best-effort)
	for _, domain := range []string{"Page.enable", "Runtime.enable", "Network.enable", "DOM.enable", "Accessibility.enable"} {
		c.SessionCommand(targetID, domain, nil)
	}

	return nil
}

func (c *CdpConnection) attachOOPIFForFrame(frameID string) (bool, error) {
	targets, err := c.GetTargets()
	if err != nil {
		return false, err
	}
	for _, target := range targets {
		if target.Type == "iframe" && target.ID == frameID {
			if err := c.attachAndEnable(target.ID, false); err != nil {
				return true, err
			}
			return true, nil
		}
	}
	return false, nil
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

// stablePageTargets returns page targets in the TabStateManager's stable
// creation order. Target.getTargets does not promise an order and Chrome can
// reshuffle its result between two immediately adjacent commands, which made
// a displayed tab index select a different page on the next invocation.
// Targets not registered yet are appended in target-ID order so startup races
// are deterministic too.
func stablePageTargets(c *CdpConnection, targets []CdpTargetInfo) []CdpTargetInfo {
	byID := make(map[string]CdpTargetInfo)
	for _, target := range targets {
		if target.Type == "page" {
			byID[target.ID] = target
		}
	}

	c.tabOrderMu.Lock()
	defer c.tabOrderMu.Unlock()

	// Keep every surviving target exactly where it was the first time it was
	// exposed. This also covers startup: a target initially appended as
	// "unregistered" must not jump elsewhere when its asynchronous attach
	// creates TabState a moment later.
	order := make([]string, 0, len(byID))
	seen := make(map[string]bool, len(byID))
	for _, targetID := range c.tabOrder {
		if _, ok := byID[targetID]; ok {
			order = append(order, targetID)
			seen[targetID] = true
		}
	}

	// New registered tabs retain their creation order.
	for _, tab := range c.TabManager.AllTabs() {
		if _, ok := byID[tab.TargetID]; ok && !seen[tab.TargetID] {
			order = append(order, tab.TargetID)
			seen[tab.TargetID] = true
		}
	}

	remaining := make([]string, 0, len(byID))
	for targetID := range byID {
		if !seen[targetID] {
			remaining = append(remaining, targetID)
		}
	}
	sort.Strings(remaining)
	order = append(order, remaining...)
	c.tabOrder = append(c.tabOrder[:0], order...)

	pages := make([]CdpTargetInfo, 0, len(order))
	for _, targetID := range order {
		pages = append(pages, byID[targetID])
	}
	return pages
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
			seq = ts.LastActionSequence()
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

	pages := stablePageTargets(c, allTargets)
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
	} else if currentTargetID := c.GetCurrentTargetID(); currentTargetID != "" {
		for i, t := range pages {
			if t.ID == currentTargetID {
				target = &pages[i]
				break
			}
		}
	}

	if target == nil {
		target = &pages[0]
	}

	// Only mutate CurrentTargetID when the caller did NOT pass an explicit
	// tabRef. Explicit-tab requests are routed by target.ID locally, so
	// changing the daemon's "current tab" pointer would race with concurrent
	// callers that rely on the implicit current tab. Actions that semantically
	// switch focus (open, tab_select, Activate=true) update CurrentTargetID
	// themselves at the dispatch site.
	if tabRef == "" {
		c.SetCurrentTargetID(target.ID)
	}
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
	return c.BrowserCommandWithTimeout(method, params, 30*time.Second)
}

// BrowserCommandWithTimeout sends a browser-level CDP command with a
// caller-provided timeout.
func (c *CdpConnection) BrowserCommandWithTimeout(method string, params interface{}, timeout time.Duration) (json.RawMessage, error) {
	if !c.connected.Load() {
		return nil, fmt.Errorf("CDP not connected")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
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
		c.connected.Store(false)
		c.rejectInflight(fmt.Errorf("CDP connection closed"))
		return nil, err
	}

	select {
	case result := <-cmd.ch:
		return result, nil
	case err := <-cmd.errCh:
		return nil, err
	case <-time.After(timeout):
		c.pending.Delete(id)
		return nil, fmt.Errorf("timeout waiting for %s", method)
	}
}

// SessionCommand sends a session-level CDP command (flat protocol).
func (c *CdpConnection) SessionCommand(targetID, method string, params interface{}) (json.RawMessage, error) {
	return c.SessionCommandWithTimeout(targetID, method, params, 30*time.Second)
}

// SessionCommandWithTimeout sends a session-level CDP command with a caller-provided timeout.
func (c *CdpConnection) SessionCommandWithTimeout(targetID, method string, params interface{}, timeout time.Duration) (json.RawMessage, error) {
	if !c.connected.Load() {
		return nil, fmt.Errorf("CDP not connected")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	// A native JS dialog left open with no handler blocks the renderer, so
	// anything that needs the page would sit here until the timeout. Fail
	// immediately with an actionable error instead.
	if blockedByDialog(method) {
		if ev := c.unhandledDialog(targetID); ev != nil {
			return nil, dialogBlockedError(method, ev)
		}
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
		c.connected.Store(false)
		c.rejectInflight(fmt.Errorf("CDP connection closed"))
		return nil, err
	}

	select {
	case result := <-listener.ch:
		return result, nil
	case err := <-listener.errCh:
		return nil, err
	case <-time.After(timeout):
		c.sessionMu.Lock()
		delete(c.sessionListeners, id)
		c.sessionMu.Unlock()
		// A dialog may have opened after the pre-flight check above; say so
		// rather than reporting a bare timeout the caller can't act on.
		if ev := c.unhandledDialog(targetID); ev != nil {
			return nil, dialogBlockedError(method, ev)
		}
		return nil, fmt.Errorf("timeout waiting for %s on session %s", method, sessionID)
	}
}

// dialogBlockingMethods are the CDP domains dispatched to the renderer main
// thread, which a modal JS dialog parks in a nested message loop. Page.* is
// deliberately absent: Page.handleJavaScriptDialog is how a dialog gets
// resolved, so it must never be blocked.
var dialogBlockingMethods = []string{"Runtime.", "DOM.", "Accessibility.", "Page.captureScreenshot"}

func blockedByDialog(method string) bool {
	for _, prefix := range dialogBlockingMethods {
		if strings.HasPrefix(method, prefix) {
			return true
		}
	}
	return false
}

// unhandledDialog returns the dialog currently blocking the tab, or nil. A
// dialog borz already answered is not blocking — the closing event is only
// microseconds behind — so those are ignored to avoid a spurious failure
// racing a correctly armed handler.
func (c *CdpConnection) unhandledDialog(targetID string) *protocol.DialogEventInfo {
	if c.TabManager == nil {
		return nil
	}
	tab := c.TabManager.GetTab(targetID)
	if tab == nil {
		return nil
	}
	ev := tab.PeekPendingDialog()
	if ev == nil || ev.AutoHandled {
		return nil
	}
	return ev
}

func dialogBlockedError(method string, ev *protocol.DialogEventInfo) error {
	kind := ev.Type
	if kind == "" {
		kind = "javascript"
	}
	return fmt.Errorf(
		"%s blocked: a native %s dialog is open on this tab and is blocking the page: %q. "+
			"Resolve it with `borz dialog accept` (or `borz dialog dismiss`), then retry. "+
			"To avoid the block next time, arm the handler BEFORE the action that triggers the dialog",
		method, kind, truncateDialogMessage(ev.Message))
}

func truncateDialogMessage(msg string) string {
	const limit = 200
	if len(msg) <= limit {
		return msg
	}
	return msg[:limit] + "…"
}

// PageCommand sends a command scoped to the active frame of a tab.
func (c *CdpConnection) PageCommand(targetID, method string, params map[string]interface{}) (json.RawMessage, error) {
	tab := c.TabManager.GetTab(targetID)
	activeFrameID := ""
	if tab != nil {
		activeFrameID = tab.ActiveFrame()
	}
	if activeFrameID != "" {
		if params == nil {
			params = map[string]interface{}{}
		}
		params["frameId"] = activeFrameID
	}
	return c.SessionCommand(targetID, method, params)
}

// Evaluate executes JavaScript on a target and returns the result.
func (c *CdpConnection) Evaluate(targetID, expression string, returnByValue bool) (json.RawMessage, error) {
	return c.EvaluateWithTimeout(targetID, expression, returnByValue, 30*time.Second)
}

// EvaluateWithTimeout executes JavaScript on a target and returns the result.
func (c *CdpConnection) EvaluateWithTimeout(targetID, expression string, returnByValue bool, timeout time.Duration) (json.RawMessage, error) {
	started := time.Now()
	evalTargetID := targetID
	params := map[string]interface{}{
		"expression":    expression,
		"awaitPromise":  true,
		"returnByValue": returnByValue,
	}
	if tab := c.TabManager.GetTab(targetID); tab != nil && tab.ActiveFrame() != "" {
		activeFrameID := tab.ActiveFrame()
		// Site-isolated cross-origin iframes are represented as OOPIF targets.
		// Their target ID is the frame ID and they have their own CDP session;
		// evaluate directly in that session's default world when available.
		_, oopif := c.sessions.Load(activeFrameID)
		if !oopif {
			var err error
			oopif, err = c.attachOOPIFForFrame(activeFrameID)
			if err != nil {
				return nil, fmt.Errorf("attach site-isolated frame %s: %w", activeFrameID, err)
			}
		}
		if oopif {
			evalTargetID = activeFrameID
		} else {
			contextID, ok := tab.FrameExecutionContext(activeFrameID)
			if !ok {
				// Runtime.enable normally supplies the default context for every
				// frame. If navigation raced those events, create a stable isolated
				// world as a fallback so cross-origin frame eval remains available.
				worldTimeout := min(timeout, 2*time.Second)
				if worldTimeout <= 0 {
					worldTimeout = 2 * time.Second
				}
				worldRaw, err := c.SessionCommandWithTimeout(targetID, "Page.createIsolatedWorld", map[string]interface{}{
					"frameId":   activeFrameID,
					"worldName": "__borz_eval__",
				}, worldTimeout)
				if err != nil {
					return nil, fmt.Errorf("resolve execution context for active frame %s: %w", activeFrameID, err)
				}
				var world struct {
					ExecutionContextID int64 `json:"executionContextId"`
				}
				if json.Unmarshal(worldRaw, &world) != nil || world.ExecutionContextID == 0 {
					return nil, fmt.Errorf("resolve execution context for active frame %s: Page.createIsolatedWorld returned no context", activeFrameID)
				}
				contextID = world.ExecutionContextID
				tab.SetFrameExecutionContext(activeFrameID, contextID)
			}
			params["contextId"] = contextID
		}
	}
	remaining := timeout - time.Since(started)
	if timeout <= 0 {
		remaining = timeout
	} else if remaining <= 0 {
		return nil, fmt.Errorf("timeout resolving Runtime.evaluate execution context")
	}
	result, err := c.SessionCommandWithTimeout(evalTargetID, "Runtime.evaluate", params, remaining)
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
