package daemon

import (
	"net/http"
	"strconv"
	"time"

	"github.com/leolin310148/bb-browser-go/internal/daemon/extbridge"
)

// registerExtRoutes wires endpoints backed by the optional Chrome extension.
// These cover capabilities CDP cannot provide: cross-domain cookies, browser-
// level tab/window events, bookmarks, history, downloads, etc.
func (s *Server) registerExtRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/ext/ws", func(w http.ResponseWriter, r *http.Request) {
		s.extHub.ServeWS(w, r)
	})

	mux.HandleFunc("/v1/ext/status", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, 200, map[string]any{
			"connected":  s.extHub.Connected(),
			"latest_seq": s.extHub.LatestSeq(),
		})
	})

	mux.HandleFunc("/v1/cookies/all", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
			return
		}
		raw, err := s.extHub.Request("cookies.getAll", nil, 10*time.Second)
		if err != nil {
			sendJSON(w, extErrStatus(err), map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	})

	mux.HandleFunc("/v1/tabs/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
			return
		}
		var since uint64
		if v := r.URL.Query().Get("since"); v != "" {
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				sendJSON(w, 400, map[string]string{"error": "since must be a non-negative integer"})
				return
			}
			since = n
		}
		evs := s.extHub.Events(since)
		sendJSON(w, 200, map[string]any{
			"events":     evs,
			"latest_seq": s.extHub.LatestSeq(),
			"connected":  s.extHub.Connected() > 0,
		})
	})
}

// extErrStatus maps extension bridge errors to HTTP status codes.
func extErrStatus(err error) int {
	switch err {
	case extbridge.ErrNoClient:
		return http.StatusServiceUnavailable
	case extbridge.ErrTimeout:
		return http.StatusGatewayTimeout
	default:
		return http.StatusBadGateway
	}
}

