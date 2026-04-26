// Package protocol defines the communication types between CLI, daemon, and CDP.
package protocol

// ActionType enumerates all supported command actions.
type ActionType string

const (
	ActionOpen       ActionType = "open"
	ActionSnapshot   ActionType = "snapshot"
	ActionClick      ActionType = "click"
	ActionHover      ActionType = "hover"
	ActionFill       ActionType = "fill"
	ActionType_      ActionType = "type"
	ActionCheck      ActionType = "check"
	ActionUncheck    ActionType = "uncheck"
	ActionSelect     ActionType = "select"
	ActionGet        ActionType = "get"
	ActionScreenshot ActionType = "screenshot"
	ActionClose      ActionType = "close"
	ActionWait       ActionType = "wait"
	ActionPress      ActionType = "press"
	ActionScroll     ActionType = "scroll"
	ActionBack       ActionType = "back"
	ActionForward    ActionType = "forward"
	ActionRefresh    ActionType = "refresh"
	ActionEval       ActionType = "eval"
	ActionTabList    ActionType = "tab_list"
	ActionTabNew     ActionType = "tab_new"
	ActionTabSelect  ActionType = "tab_select"
	ActionTabClose   ActionType = "tab_close"
	ActionFrame      ActionType = "frame"
	ActionFrameMain  ActionType = "frame_main"
	ActionDialog     ActionType = "dialog"
	ActionNetwork    ActionType = "network"
	ActionConsole    ActionType = "console"
	ActionErrors     ActionType = "errors"
	ActionTrace      ActionType = "trace"
	ActionHistory    ActionType = "history"

	ActionKey           ActionType = "key"
	ActionMouse         ActionType = "mouse"
	ActionClipboardRead ActionType = "clipboard_read"
)

// Request is sent from CLI to daemon.
type Request struct {
	ID     string     `json:"id"`
	Action ActionType `json:"action"`

	// Navigation
	URL string `json:"url,omitempty"`
	// New forces `open` to always create a fresh tab, bypassing reuse-by-URL.
	New bool `json:"new,omitempty"`
	// WaitFor, when set, polls document.querySelector(WaitFor) after Page.navigate
	// until it resolves to a non-null node or TimeoutMs elapses.
	WaitFor string `json:"waitFor,omitempty"`
	// TimeoutMs caps WaitFor (default 10000 when WaitFor is set).
	TimeoutMs *int `json:"timeoutMs,omitempty"`

	// Interaction
	Ref  string `json:"ref,omitempty"`
	Text string `json:"text,omitempty"`
	Key  string `json:"key,omitempty"`

	// Scroll
	Direction string `json:"direction,omitempty"`
	Pixels    *int   `json:"pixels,omitempty"`

	// Observation
	Attribute   string `json:"attribute,omitempty"`
	Interactive bool   `json:"interactive,omitempty"`
	Compact     bool   `json:"compact,omitempty"`
	MaxDepth    *int   `json:"maxDepth,omitempty"`
	Selector    string `json:"selector,omitempty"`
	Role        string `json:"role,omitempty"`

	// Tab/Frame
	TabID interface{} `json:"tabId,omitempty"` // number or string
	Index *int        `json:"index,omitempty"`
	// Activate brings the resolved tab to the foreground (Target.activateTarget
	// + Page.bringToFront) before running the action. Use it when the action
	// needs real focus — clipboard reads, paste shortcuts, video autoplay,
	// pages that throttle backgrounded tabs.
	Activate bool `json:"activate,omitempty"`

	// Dialog
	DialogResponse string `json:"dialogResponse,omitempty"`
	PromptText     string `json:"promptText,omitempty"`

	// Network/Console/Errors/Trace
	NetworkCommand string `json:"networkCommand,omitempty"`
	ConsoleCommand string `json:"consoleCommand,omitempty"`
	ErrorsCommand  string `json:"errorsCommand,omitempty"`
	TraceCommand   string `json:"traceCommand,omitempty"`

	// Mode requests an alternate output format from a command. For 'snapshot',
	// "text" returns a plain-text reader-mode dump of the page in
	// SnapshotData.Snapshot (Refs/Elements are empty).
	Mode string `json:"mode,omitempty"`

	// Observation filters
	Filter   string      `json:"filter,omitempty"`
	WithBody bool        `json:"withBody,omitempty"`
	Since    interface{} `json:"since,omitempty"` // number or "last_action"
	Method   string      `json:"method,omitempty"`
	Status   string      `json:"status,omitempty"`
	Limit    *int        `json:"limit,omitempty"`

	// Eval
	Script string `json:"script,omitempty"`

	// Select
	Value string `json:"value,omitempty"`

	// Wait
	Ms *int `json:"ms,omitempty"`

	// History
	HistoryCommand string `json:"historyCommand,omitempty"`

	// Route options (not used in Go CLI-only port)
	RouteOptions interface{} `json:"routeOptions,omitempty"`

	// Path for screenshot
	Path string `json:"path,omitempty"`

	// Modifiers for press
	Modifiers []string `json:"modifiers,omitempty"`

	// Wait type
	WaitType string `json:"waitType,omitempty"`

	// Low-level key input (ActionKey). KeyType is one of:
	//   "press" (default, keyDown + optional char + keyUp)
	//   "down"  (keyDown only)
	//   "up"    (keyUp only)
	//   "type"  (char event per rune in Text — reaches canvas apps / SSH sessions)
	KeyType string `json:"keyType,omitempty"`
	Code    string `json:"code,omitempty"`

	// Low-level mouse input (ActionMouse). MouseType is one of:
	//   "move", "down", "up", "click", "wheel"
	MouseType  string   `json:"mouseType,omitempty"`
	X          *float64 `json:"x,omitempty"`
	Y          *float64 `json:"y,omitempty"`
	Button     string   `json:"button,omitempty"` // left (default) | right | middle | none
	DeltaX     *float64 `json:"deltaX,omitempty"`
	DeltaY     *float64 `json:"deltaY,omitempty"`
	ClickCount *int     `json:"clickCount,omitempty"`
}

// RefInfo stores element reference information from a snapshot.
type RefInfo struct {
	BackendDOMNodeID int    `json:"backendDOMNodeId,omitempty"`
	XPath            string `json:"xpath,omitempty"`
	Role             string `json:"role"`
	Name             string `json:"name,omitempty"`
	TagName          string `json:"tagName,omitempty"`
}

// ElementInfo is a RefInfo with its ref ID inlined, for iterable consumers
// (REST/n8n) that prefer arrays over a map keyed by ref.
type ElementInfo struct {
	Ref              string `json:"ref"`
	BackendDOMNodeID int    `json:"backendDOMNodeId,omitempty"`
	XPath            string `json:"xpath,omitempty"`
	Role             string `json:"role"`
	Name             string `json:"name,omitempty"`
	TagName          string `json:"tagName,omitempty"`
}

// TabInfo represents a browser tab.
type TabInfo struct {
	Index  int         `json:"index"`
	URL    string      `json:"url"`
	Title  string      `json:"title"`
	Active bool        `json:"active"`
	TabID  interface{} `json:"tabId"`
	Tab    string      `json:"tab,omitempty"`
}

// SnapshotData holds the accessibility tree and ref mapping.
//
// Refs is a map keyed by ref ID (for O(1) lookup). Elements is the same
// data as an array in snapshot order, easier to iterate/filter in REST
// consumers like n8n.
type SnapshotData struct {
	Snapshot string              `json:"snapshot"`
	Refs     map[string]*RefInfo `json:"refs"`
	Elements []*ElementInfo      `json:"elements"`
}

// NetworkRequestInfo represents a captured network request.
type NetworkRequestInfo struct {
	RequestID             string            `json:"requestId"`
	URL                   string            `json:"url"`
	Method                string            `json:"method"`
	Type                  string            `json:"type"`
	Timestamp             int64             `json:"timestamp"`
	Status                *int              `json:"status,omitempty"`
	StatusText            string            `json:"statusText,omitempty"`
	Failed                bool              `json:"failed,omitempty"`
	FailureReason         string            `json:"failureReason,omitempty"`
	RequestHeaders        map[string]string `json:"requestHeaders,omitempty"`
	RequestBody           string            `json:"requestBody,omitempty"`
	RequestBodyTruncated  bool              `json:"requestBodyTruncated,omitempty"`
	ResponseHeaders       map[string]string `json:"responseHeaders,omitempty"`
	ResponseBody          string            `json:"responseBody,omitempty"`
	ResponseBodyBase64    bool              `json:"responseBodyBase64,omitempty"`
	ResponseBodyTruncated bool              `json:"responseBodyTruncated,omitempty"`
	MimeType              string            `json:"mimeType,omitempty"`
	BodyError             string            `json:"bodyError,omitempty"`
	Seq                   int               `json:"seq,omitempty"`
}

// ConsoleMessageInfo represents a captured console message.
type ConsoleMessageInfo struct {
	Type       string `json:"type"`
	Text       string `json:"text"`
	Timestamp  int64  `json:"timestamp"`
	URL        string `json:"url,omitempty"`
	LineNumber *int   `json:"lineNumber,omitempty"`
	Seq        int    `json:"seq,omitempty"`
}

// JSErrorInfo represents a captured JavaScript error.
type JSErrorInfo struct {
	Message      string `json:"message"`
	URL          string `json:"url,omitempty"`
	LineNumber   *int   `json:"lineNumber,omitempty"`
	ColumnNumber *int   `json:"columnNumber,omitempty"`
	StackTrace   string `json:"stackTrace,omitempty"`
	Timestamp    int64  `json:"timestamp"`
	Seq          int    `json:"seq,omitempty"`
}

// TraceEvent represents a recorded user action.
type TraceEvent struct {
	Type        string `json:"type"`
	Timestamp   int64  `json:"timestamp"`
	URL         string `json:"url"`
	Ref         *int   `json:"ref,omitempty"`
	XPath       string `json:"xpath,omitempty"`
	CSSSelector string `json:"cssSelector,omitempty"`
	Value       string `json:"value,omitempty"`
	Key         string `json:"key,omitempty"`
	Direction   string `json:"direction,omitempty"`
	Pixels      *int   `json:"pixels,omitempty"`
	Checked     *bool  `json:"checked,omitempty"`
	ElementRole string `json:"elementRole,omitempty"`
	ElementName string `json:"elementName,omitempty"`
	ElementTag  string `json:"elementTag,omitempty"`
}

// TraceStatus represents trace recording state.
type TraceStatus struct {
	Recording  bool `json:"recording"`
	EventCount int  `json:"eventCount"`
	TabID      *int `json:"tabId,omitempty"`
}

// ResponseData is the data field of a successful response.
type ResponseData struct {
	Title    string      `json:"title,omitempty"`
	URL      string      `json:"url,omitempty"`
	TabID    interface{} `json:"tabId,omitempty"`
	Tab      string      `json:"tab,omitempty"`
	Seq      *int        `json:"seq,omitempty"`
	Cursor   *int        `json:"cursor,omitempty"`

	SnapshotData   *SnapshotData   `json:"snapshotData,omitempty"`
	Value          string          `json:"value,omitempty"`
	ScreenshotPath string          `json:"screenshotPath,omitempty"`
	DataURL        string          `json:"dataUrl,omitempty"`
	Result         interface{}     `json:"result,omitempty"`

	Tabs        []TabInfo `json:"tabs,omitempty"`
	ActiveIndex *int      `json:"activeIndex,omitempty"`

	FrameInfo  interface{} `json:"frameInfo,omitempty"`
	DialogInfo interface{} `json:"dialogInfo,omitempty"`

	NetworkRequests []NetworkRequestInfo `json:"networkRequests,omitempty"`
	RouteCount      *int                 `json:"routeCount,omitempty"`
	ConsoleMessages []ConsoleMessageInfo `json:"consoleMessages,omitempty"`
	JSErrors        []JSErrorInfo        `json:"jsErrors,omitempty"`

	TraceEvents []TraceEvent `json:"traceEvents,omitempty"`
	TraceStatus *TraceStatus `json:"traceStatus,omitempty"`

	HistoryItems   interface{} `json:"historyItems,omitempty"`
	HistoryDomains interface{} `json:"historyDomains,omitempty"`
}

// Response is sent from daemon to CLI.
type Response struct {
	ID      string        `json:"id"`
	Success bool          `json:"success"`
	Data    *ResponseData `json:"data,omitempty"`
	Error   string        `json:"error,omitempty"`
}

// DaemonInfo is persisted in ~/.bb-browser/daemon.json.
type DaemonInfo struct {
	PID   int    `json:"pid"`
	Host  string `json:"host"`
	Port  int    `json:"port"`
	Token string `json:"token"`
}

// DaemonStatus is returned by GET /status.
type DaemonStatus struct {
	Running         bool        `json:"running"`
	CDPConnected    bool        `json:"cdpConnected"`
	Uptime          int         `json:"uptime"`
	CurrentSeq      int         `json:"currentSeq,omitempty"`
	CurrentTargetID string      `json:"currentTargetId,omitempty"`
	Tabs            interface{} `json:"tabs,omitempty"`
}
