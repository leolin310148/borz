package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/daemon/extbridge"
	"github.com/leolin310148/borz/internal/observability"
	"github.com/leolin310148/borz/internal/protocol"
)

// ServerOptions configures the daemon HTTP server.
type ServerOptions struct {
	Host    string
	Port    int
	Token   string
	CDPHost string
	CDPPort int

	// IdleTabCloseMinutes auto-closes tabs after this many minutes without a
	// user-initiated action. 0 disables. Negative values are clamped to 0.
	IdleTabCloseMinutes int

	// Version is reported by /v1/doctor so REST clients can see which
	// borz binary is serving them. Optional.
	Version string
}

// Server is the borz daemon HTTP server.
type Server struct {
	opts         ServerOptions
	cdp          *CdpConnection
	extHub       *extbridge.Hub
	recordings   *recordingManager
	httpSrv      *http.Server
	startTime    time.Time
	mu           sync.Mutex
	cancelReaper context.CancelFunc
	shutdownOnce sync.Once
	shutdownErr  error
	logger       *observability.Logger
}

// NewServer creates a daemon server.
func NewServer(opts ServerOptions) *Server {
	if opts.Host == "" {
		opts.Host = config.DaemonHost
	}
	if opts.Port == 0 {
		opts.Port = config.DaemonPort
	}
	tabManager := NewTabStateManager()
	cdp := NewCdpConnection(opts.CDPHost, opts.CDPPort, tabManager)
	extHub := extbridge.NewHub()

	return &Server{
		opts:       opts,
		cdp:        cdp,
		extHub:     extHub,
		recordings: newRecordingManager(cdp, extHub),
	}
}

// ExtHub exposes the extension WS hub for tests and integration.
func (s *Server) ExtHub() *extbridge.Hub { return s.extHub }

// Run starts the daemon server (blocks until shutdown).
func (s *Server) Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return s.RunContext(ctx)
}

// RunContext starts the daemon server and blocks until ctx is cancelled,
// /shutdown is called, or the HTTP server fails.
func (s *Server) RunContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	protectedMux := http.NewServeMux()
	protectedMux.HandleFunc("/command", s.handleCommand)
	protectedMux.HandleFunc("/status", s.handleStatus)
	protectedMux.HandleFunc("/shutdown", s.handleShutdown)
	s.registerRESTRoutes(protectedMux)
	s.registerSiteRoutes(protectedMux)
	s.registerExtRoutes(protectedMux)
	s.registerRecordingRoutes(protectedMux)
	s.registerRecordingPreviewRoutes(protectedMux)
	protectedMux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		sendRESTError(w, http.StatusNotFound, "Not found")
	})

	root := http.NewServeMux()
	root.HandleFunc("/healthz", s.handleHealthz)
	s.registerDocsRoutes(root)
	root.Handle("/", s.authMiddleware(protectedMux))

	addr := fmt.Sprintf("%s:%d", s.opts.Host, s.opts.Port)
	s.httpSrv = &http.Server{
		Addr:    addr,
		Handler: corsMiddleware(root),
	}
	fmt.Fprintf(os.Stderr, "borz daemon starting on %s (cdp=%s:%d, idleTabCloseMinutes=%d)\n",
		addr, s.opts.CDPHost, s.opts.CDPPort, s.opts.IdleTabCloseMinutes)

	// Bind the listener BEFORE writing daemon.json, so a bind failure
	// doesn't clobber a live daemon's state.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if isAddrInUse(err) {
			return fmt.Errorf("a daemon may already be running on %s (try `borz daemon status` or `borz daemon shutdown`): %w", addr, err)
		}
		return err
	}
	serveStarted := false
	defer func() {
		if !serveStarted {
			_ = ln.Close()
		}
	}()

	if logger, logErr := observability.Open("daemon", s.opts.Version); logErr != nil {
		fmt.Fprintf(os.Stderr, "borz operational log unavailable: %v\n", logErr)
	} else {
		s.logger = logger
		s.cdp.logger = logger
		defer logger.Close()
		s.log("info", "daemon_started", observability.Fields{})
	}

	s.startTime = time.Now()

	// Start CDP connection async (two-phase startup)
	go func() {
		if err := s.cdp.Connect(); err != nil {
			fmt.Fprintf(os.Stderr, "CDP connection failed: %v\n", err)
		}
	}()

	// Idle-tab reaper. Disabled when IdleTabCloseMinutes <= 0.
	reaperCtx, cancelReaper := context.WithCancel(context.Background())
	s.cancelReaper = cancelReaper
	if s.opts.IdleTabCloseMinutes > 0 {
		threshold := time.Duration(s.opts.IdleTabCloseMinutes) * time.Minute
		tickEvery := reaperTickInterval
		// Real-browser E2E uses an isolated profile and short durations so this
		// behavior can be exercised without a minute-long test.
		if os.Getenv("BORZ_E2E") == "1" {
			if override, err := time.ParseDuration(os.Getenv("BORZ_E2E_IDLE_TAB_THRESHOLD")); err == nil && override > 0 {
				threshold = override
			}
			if override, err := time.ParseDuration(os.Getenv("BORZ_E2E_IDLE_TAB_TICK")); err == nil && override > 0 {
				tickEvery = override
			}
		}
		fmt.Fprintf(os.Stderr, "borz idle-tab reaper enabled (threshold=%s)\n", threshold)
		go runIdleTabReaper(
			reaperCtx,
			s.cdp.TabManager,
			s.cdp,
			threshold,
			tickEvery,
			func() string { return s.cdp.GetCurrentTargetID() },
			time.Now,
		)
	}

	// Write daemon.json only after the listener is held.
	info := protocol.DaemonInfo{
		PID:   os.Getpid(),
		Host:  s.opts.Host,
		Port:  s.opts.Port,
		Token: s.opts.Token,
	}
	infoJSON, _ := json.Marshal(info)
	if _, err := config.EnsureRuntimeDir(); err != nil {
		return err
	}
	if err := os.WriteFile(config.DaemonJSONPath(), infoJSON, 0600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "borz daemon state written to %s (pid=%d, host=%s, port=%d)\n",
		config.DaemonJSONPath(), info.PID, info.Host, info.Port)

	fmt.Fprintf(os.Stderr, "borz daemon listening on %s\n", addr)
	errCh := make(chan error, 1)
	serveStarted = true
	go func() {
		if err := s.httpSrv.Serve(ln); err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		s.log("error", "daemon_serve_failed", observability.Fields{ErrorCode: observability.ErrorCode(err.Error())})
		return err
	}

	fmt.Fprintf(os.Stderr, "borz daemon shutdown requested\n")
	return s.shutdown()
}

func (s *Server) shutdown() error {
	s.shutdownOnce.Do(func() {
		fmt.Fprintf(os.Stderr, "borz daemon shutting down\n")
		s.log("info", "daemon_stopping", observability.Fields{})
		// Clean up daemon.json
		os.Remove(config.DaemonJSONPath())
		if s.cancelReaper != nil {
			s.cancelReaper()
		}
		s.cdp.Disconnect()
		if s.httpSrv == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.shutdownErr = s.httpSrv.Shutdown(ctx)
	})
	return s.shutdownErr
}

func (s *Server) uptime() int {
	if s.startTime.IsZero() {
		return 0
	}
	return int(time.Since(s.startTime).Seconds())
}

func (s *Server) log(level, event string, fields observability.Fields) {
	if s == nil || s.logger == nil {
		return
	}
	if err := s.logger.Log(level, event, fields); err != nil {
		fmt.Fprintf(os.Stderr, "borz operational log write failed: %v\n", err)
	}
}

// --- Middleware ---

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.opts.Token != "" {
			auth := r.Header.Get("Authorization")
			// Browser WebSocket clients (the Chrome extension) cannot set
			// custom headers, so also accept ?token= on the query.
			if auth == "" {
				if q := r.URL.Query().Get("token"); q != "" {
					auth = "Bearer " + q
				}
			}
			if !validBearerToken(auth, s.opts.Token) {
				w.Header().Set("WWW-Authenticate", "Bearer")
				if strings.HasPrefix(r.URL.Path, "/v1/") {
					sendRESTError(w, http.StatusUnauthorized, "Unauthorized")
				} else {
					sendJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
				}
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func validBearerToken(auth, token string) bool {
	expected := "Bearer " + token
	return len(auth) == len(expected) && subtle.ConstantTimeCompare([]byte(auth), []byte(expected)) == 1
}

// --- Handlers ---

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	surface := requestSurface(r)
	sessionID := requestSessionID(r)
	if r.Method != "POST" {
		s.log("warn", "request_rejected", observability.Fields{Surface: surface, SessionID: sessionID, DurationMS: time.Since(started).Milliseconds(), ErrorCode: "validation"})
		sendMethodNotAllowed(w, http.MethodPost)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.log("warn", "request_rejected", observability.Fields{Surface: surface, SessionID: sessionID, DurationMS: time.Since(started).Milliseconds(), ErrorCode: "validation"})
		sendJSON(w, 400, map[string]string{"error": "Failed to read body"})
		return
	}

	var req protocol.Request
	if err := json.Unmarshal(body, &req); err != nil {
		s.log("warn", "request_rejected", observability.Fields{Surface: surface, SessionID: sessionID, DurationMS: time.Since(started).Milliseconds(), ErrorCode: "validation"})
		sendJSON(w, 400, map[string]string{"error": "Invalid JSON"})
		return
	}

	// Wait for CDP to be ready
	if !s.cdp.Connected() {
		if err := s.cdp.WaitUntilReady(time.Duration(config.CommandTimeout) * time.Second); err != nil {
			s.logCommand(&req, surface, sessionID, started, false, observability.ErrorCode(err.Error()))
			cdpTarget := fmt.Sprintf("%s:%d", s.cdp.Host, s.cdp.Port)
			sendJSON(w, 503, map[string]interface{}{
				"id":      req.ID,
				"success": false,
				"error":   fmt.Sprintf("Chrome not connected (CDP at %s)", cdpTarget),
				"reason":  s.cdp.GetLastError(),
				"hint":    "Make sure Chrome is running. Try: borz daemon shutdown && borz tab list",
			})
			return
		}
	}

	// Dispatch with timeout
	done := make(chan *protocol.Response, 1)
	go func() {
		done <- DispatchRequest(s.cdp, &req)
	}()

	select {
	case resp := <-done:
		errorCode := ""
		if !resp.Success {
			errorCode = observability.ErrorCode(resp.Error)
		}
		s.logCommand(&req, surface, sessionID, started, resp.Success, errorCode)
		sendJSON(w, 200, resp)
	case <-time.After(time.Duration(config.CommandTimeout) * time.Second):
		s.logCommand(&req, surface, sessionID, started, false, "command_timeout")
		sendJSON(w, 200, &protocol.Response{
			ID: req.ID, Success: false, Error: "Command timeout",
		})
	}
}

func (s *Server) logCommand(req *protocol.Request, surface, sessionID string, started time.Time, success bool, errorCode string) {
	if req == nil {
		return
	}
	level := "info"
	if !success {
		level = "warn"
	}
	s.log(level, "command_completed", observability.Fields{
		SessionID: sessionID, RequestID: req.ID, Surface: surface,
		Action: string(req.Action), Tab: safeTab(req.TabID), URLHost: observability.URLHost(req.URL),
		DurationMS: time.Since(started).Milliseconds(), Success: boolPtr(success), ErrorCode: errorCode,
		TextBytes: len(req.Text), ScriptBytes: len(req.Script), FileCount: len(req.Files),
		HasRef: strings.TrimSpace(req.Ref) != "", WaitFor: strings.TrimSpace(req.WaitFor) != "",
	})
}

func boolPtr(v bool) *bool { return &v }

func requestSurface(r *http.Request) string {
	if r == nil {
		return "raw"
	}
	v := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Borz-Surface")))
	switch v {
	case "cli", "mcp", "rest", "n8n":
		return v
	default:
		return "raw"
	}
}

func requestSessionID(r *http.Request) string {
	if r == nil {
		return ""
	}
	v := strings.TrimSpace(r.Header.Get("X-Borz-Session-ID"))
	if len(v) == 0 || len(v) > 64 {
		return ""
	}
	for _, ch := range v {
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '-' && ch != '_' {
			return ""
		}
	}
	return v
}

func safeTab(tab any) string {
	if tab == nil {
		return ""
	}
	v := strings.TrimSpace(fmt.Sprint(tab))
	if len(v) > 12 {
		v = v[len(v)-4:]
	}
	for _, ch := range v {
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '-' && ch != '_' {
			return ""
		}
	}
	return v
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		sendMethodNotAllowed(w, http.MethodGet)
		return
	}

	allTabs := s.cdp.TabManager.AllTabs()
	tabs := make([]map[string]interface{}, 0, len(allTabs))
	for _, tab := range allTabs {
		networkRequests, consoleMessages, jsErrors := tab.EventCounts()
		tabs = append(tabs, map[string]interface{}{
			"shortId":         tab.ShortID,
			"targetId":        tab.TargetID,
			"networkRequests": networkRequests,
			"consoleMessages": consoleMessages,
			"jsErrors":        jsErrors,
			"lastActionSeq":   tab.LastActionSequence(),
		})
	}

	sendJSON(w, 200, map[string]interface{}{
		"running":             true,
		"cdpConnected":        s.cdp.Connected(),
		"cdpHost":             s.cdp.Host,
		"cdpPort":             s.cdp.Port,
		"uptime":              s.uptime(),
		"currentSeq":          s.cdp.TabManager.CurrentSeq(),
		"currentTargetId":     s.cdp.GetCurrentTargetID(),
		"tabs":                tabs,
		"version":             s.opts.Version,
		"idleTabCloseMinutes": s.opts.IdleTabCloseMinutes,
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendMethodNotAllowed(w, http.MethodGet)
		return
	}
	sendJSON(w, 200, map[string]interface{}{
		"ok":           true,
		"cdpConnected": s.cdp.Connected(),
		"uptime":       s.uptime(),
	})
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		sendMethodNotAllowed(w, http.MethodPost)
		return
	}

	sendJSON(w, 200, map[string]interface{}{"code": 0, "message": "Shutting down"})

	go func() {
		time.Sleep(100 * time.Millisecond)
		s.shutdown()
		os.Exit(0)
	}()
}

func isAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "address already in use") ||
		strings.Contains(s, "only one usage of each socket address")
}

func sendJSON(w http.ResponseWriter, status int, data interface{}) {
	body, err := json.Marshal(data)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"error":"failed to encode JSON response"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	w.Write(body)
}

func sendMethodNotAllowed(w http.ResponseWriter, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
}

func sendRESTError(w http.ResponseWriter, status int, message string) {
	sendJSON(w, status, &protocol.Response{ID: newReqID(), Success: false, Error: message})
}

func sendRESTMethodNotAllowed(w http.ResponseWriter, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	sendRESTError(w, http.StatusMethodNotAllowed, "Method not allowed")
}
