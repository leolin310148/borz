package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/leolin310148/bb-browser-go/internal/config"
	"github.com/leolin310148/bb-browser-go/internal/protocol"
)

// ServerOptions configures the daemon HTTP server.
type ServerOptions struct {
	Host    string
	Port    int
	Token   string
	CDPHost string
	CDPPort int
}

// Server is the bb-browser daemon HTTP server.
type Server struct {
	opts      ServerOptions
	cdp       *CdpConnection
	httpSrv   *http.Server
	startTime time.Time
	mu        sync.Mutex
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

	return &Server{
		opts: opts,
		cdp:  cdp,
	}
}

// Run starts the daemon server (blocks until shutdown).
func (s *Server) Run() error {
	protectedMux := http.NewServeMux()
	protectedMux.HandleFunc("/command", s.handleCommand)
	protectedMux.HandleFunc("/status", s.handleStatus)
	protectedMux.HandleFunc("/shutdown", s.handleShutdown)
	s.registerRESTRoutes(protectedMux)

	root := http.NewServeMux()
	root.HandleFunc("/healthz", s.handleHealthz)
	root.Handle("/", s.authMiddleware(protectedMux))

	addr := fmt.Sprintf("%s:%d", s.opts.Host, s.opts.Port)
	s.httpSrv = &http.Server{
		Addr:    addr,
		Handler: corsMiddleware(root),
	}

	s.startTime = time.Now()

	// Start CDP connection async (two-phase startup)
	go func() {
		if err := s.cdp.Connect(); err != nil {
			fmt.Fprintf(os.Stderr, "CDP connection failed: %v\n", err)
		}
	}()

	// Write daemon.json
	info := protocol.DaemonInfo{
		PID:   os.Getpid(),
		Host:  s.opts.Host,
		Port:  s.opts.Port,
		Token: s.opts.Token,
	}
	infoJSON, _ := json.Marshal(info)
	os.MkdirAll(config.HomeDir(), 0755)
	os.WriteFile(config.DaemonJSONPath(), infoJSON, 0600)

	// Handle graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(os.Stderr, "bb-browser daemon listening on %s\n", addr)
		if err := s.httpSrv.ListenAndServe(); err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-stop:
	case err := <-errCh:
		return err
	}

	return s.shutdown()
}

func (s *Server) shutdown() error {
	// Clean up daemon.json
	os.Remove(config.DaemonJSONPath())
	s.cdp.Disconnect()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) uptime() int {
	if s.startTime.IsZero() {
		return 0
	}
	return int(time.Since(s.startTime).Seconds())
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
			if auth != "Bearer "+s.opts.Token {
				sendJSON(w, 401, map[string]string{"error": "Unauthorized"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// --- Handlers ---

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		sendJSON(w, 400, map[string]string{"error": "Failed to read body"})
		return
	}

	var req protocol.Request
	if err := json.Unmarshal(body, &req); err != nil {
		sendJSON(w, 400, map[string]string{"error": "Invalid JSON"})
		return
	}

	// Wait for CDP to be ready
	if !s.cdp.Connected() {
		if err := s.cdp.WaitUntilReady(time.Duration(config.CommandTimeout) * time.Second); err != nil {
			cdpTarget := fmt.Sprintf("%s:%d", s.cdp.Host, s.cdp.Port)
			sendJSON(w, 503, map[string]interface{}{
				"id":      req.ID,
				"success": false,
				"error":   fmt.Sprintf("Chrome not connected (CDP at %s)", cdpTarget),
				"reason":  s.cdp.LastError,
				"hint":    "Make sure Chrome is running. Try: bb-browser daemon shutdown && bb-browser tab list",
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
		sendJSON(w, 200, resp)
	case <-time.After(time.Duration(config.CommandTimeout) * time.Second):
		sendJSON(w, 200, &protocol.Response{
			ID: req.ID, Success: false, Error: "Command timeout",
		})
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
		return
	}

	allTabs := s.cdp.TabManager.AllTabs()
	tabs := make([]map[string]interface{}, 0, len(allTabs))
	for _, tab := range allTabs {
		tabs = append(tabs, map[string]interface{}{
			"shortId":         tab.ShortID,
			"targetId":        tab.TargetID,
			"networkRequests": tab.NetworkRequests.Size(),
			"consoleMessages": tab.ConsoleMessages.Size(),
			"jsErrors":        tab.JSErrors.Size(),
			"lastActionSeq":   tab.LastActionSeq,
		})
	}

	sendJSON(w, 200, map[string]interface{}{
		"running":         true,
		"cdpConnected":    s.cdp.Connected(),
		"uptime":          s.uptime(),
		"currentSeq":      s.cdp.TabManager.CurrentSeq(),
		"currentTargetId": s.cdp.CurrentTargetID,
		"tabs":            tabs,
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, map[string]interface{}{
		"ok":           true,
		"cdpConnected": s.cdp.Connected(),
		"uptime":       s.uptime(),
	})
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
		return
	}

	sendJSON(w, 200, map[string]interface{}{"code": 0, "message": "Shutting down"})

	go func() {
		time.Sleep(100 * time.Millisecond)
		s.shutdown()
		os.Exit(0)
	}()
}

func sendJSON(w http.ResponseWriter, status int, data interface{}) {
	body, _ := json.Marshal(data)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}
