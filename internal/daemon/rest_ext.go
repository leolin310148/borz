package daemon

import (
	"encoding/json"
	"io"
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

	mux.HandleFunc("/v1/ext/capabilities", s.extGet("capabilities", nil))
	mux.HandleFunc("/v1/ext/call", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
			return
		}
		body, err := readExtBody(r)
		if err != nil {
			sendJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		method, _ := body["method"].(string)
		if method == "" {
			sendJSON(w, 400, map[string]string{"error": "method is required"})
			return
		}
		params := body["params"]
		s.extRequestAndWrite(w, method, params)
	})

	mux.HandleFunc("/v1/cookies/all", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
			return
		}
		params := map[string]any{}
		filter := queryFilter(r, "url", "name", "domain", "path", "storeId")
		if len(filter) > 0 {
			params["filter"] = filter
		}
		raw, err := s.extHub.Request("cookies.getAll", params, 10*time.Second)
		if err != nil {
			sendJSON(w, extErrStatus(err), map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("/v1/cookies/set", s.extPost("cookies.set"))
	mux.HandleFunc("/v1/cookies/remove", s.extPost("cookies.remove"))

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

	mux.HandleFunc("/v1/bookmarks/tree", s.extGet("bookmarks.getTree", nil))
	mux.HandleFunc("/v1/bookmarks/search", s.extGet("bookmarks.search", func(r *http.Request) map[string]any {
		return map[string]any{"query": r.URL.Query().Get("q")}
	}))
	mux.HandleFunc("/v1/bookmarks/create", s.extPost("bookmarks.create"))
	mux.HandleFunc("/v1/bookmarks/update", s.extPost("bookmarks.update"))
	mux.HandleFunc("/v1/bookmarks/remove", s.extPost("bookmarks.remove"))

	mux.HandleFunc("/v1/browser-history/search", s.extGet("history.search", func(r *http.Request) map[string]any {
		q := r.URL.Query()
		params := map[string]any{
			"text": q.Get("q"),
		}
		copyQueryInt(params, q, "maxResults")
		copyQueryInt(params, q, "limit")
		copyQueryFloat(params, q, "startTime")
		copyQueryFloat(params, q, "endTime")
		return params
	}))
	mux.HandleFunc("/v1/browser-history/delete-url", s.extPost("history.deleteUrl"))
	mux.HandleFunc("/v1/browser-history/delete-range", s.extPost("history.deleteRange"))

	mux.HandleFunc("/v1/downloads/search", s.extGet("downloads.search", func(r *http.Request) map[string]any {
		q := r.URL.Query()
		params := queryFilter(r, "q", "query", "url", "filename", "state", "mime", "danger", "exists", "paused", "orderBy", "startedAfter", "startedBefore", "endedAfter", "endedBefore")
		copyQueryInt(params, q, "id")
		copyQueryInt(params, q, "limit")
		copyQueryInt(params, q, "totalBytesGreater")
		copyQueryInt(params, q, "totalBytesLess")
		return params
	}))
	mux.HandleFunc("/v1/downloads/download", s.extPost("downloads.download"))
	mux.HandleFunc("/v1/downloads/erase", s.extPost("downloads.erase"))
	mux.HandleFunc("/v1/downloads/cancel", s.extPost("downloads.cancel"))
	mux.HandleFunc("/v1/downloads/pause", s.extPost("downloads.pause"))
	mux.HandleFunc("/v1/downloads/resume", s.extPost("downloads.resume"))
	mux.HandleFunc("/v1/downloads/show", s.extPost("downloads.show"))
	mux.HandleFunc("/v1/downloads/show-default-folder", s.extPost("downloads.showDefaultFolder"))

	mux.HandleFunc("/v1/windows", s.extGet("windows.getAll", func(r *http.Request) map[string]any {
		params := map[string]any{}
		if v := r.URL.Query().Get("populate"); v != "" {
			params["populate"] = v != "false" && v != "0"
		}
		return params
	}))
	mux.HandleFunc("/v1/windows/create", s.extPost("windows.create"))
	mux.HandleFunc("/v1/windows/update", s.extPost("windows.update"))
	mux.HandleFunc("/v1/windows/close", s.extPost("windows.remove"))

	mux.HandleFunc("/v1/ext/tabs/query", s.extGet("tabs.query", func(r *http.Request) map[string]any {
		params := queryFilter(r, "status", "title", "url", "windowType")
		q := r.URL.Query()
		for _, key := range []string{"active", "audible", "discarded", "highlighted", "muted", "pinned"} {
			if v := q.Get(key); v != "" {
				params[key] = v == "true" || v == "1"
			}
		}
		copyQueryInt(params, q, "windowId")
		return params
	}))
	mux.HandleFunc("/v1/ext/tabs/capture-visible", s.extPost("tabs.captureVisibleTab"))
	mux.HandleFunc("/v1/ext/tabs/duplicate", s.extPost("tabs.duplicate"))
	mux.HandleFunc("/v1/ext/tabs/discard", s.extPost("tabs.discard"))
	mux.HandleFunc("/v1/ext/tabs/reload", s.extPost("tabs.reload"))
	mux.HandleFunc("/v1/ext/tab-groups/query", s.extGet("tabGroups.query", nil))
}

func (s *Server) extGet(method string, build func(*http.Request) map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
			return
		}
		var params any
		if build != nil {
			params = build(r)
		}
		s.extRequestAndWrite(w, method, params)
	}
}

func (s *Server) extPost(method string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
			return
		}
		params, err := readExtBody(r)
		if err != nil {
			sendJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		s.extRequestAndWrite(w, method, params)
	}
}

func (s *Server) extRequestAndWrite(w http.ResponseWriter, method string, params any) {
	raw, err := s.extHub.Request(method, params, 10*time.Second)
	if err != nil {
		sendJSON(w, extErrStatus(err), map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if len(raw) == 0 || string(raw) == "null" {
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	_, _ = w.Write(raw)
}

func readExtBody(r *http.Request) (map[string]any, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	return body, nil
}

func queryFilter(r *http.Request, keys ...string) map[string]any {
	out := map[string]any{}
	q := r.URL.Query()
	for _, key := range keys {
		if v := q.Get(key); v != "" {
			out[key] = v
		}
	}
	return out
}

func copyQueryInt(out map[string]any, q map[string][]string, key string) {
	if vals := q[key]; len(vals) > 0 && vals[0] != "" {
		if n, err := strconv.Atoi(vals[0]); err == nil {
			out[key] = n
		}
	}
}

func copyQueryFloat(out map[string]any, q map[string][]string, key string) {
	if vals := q[key]; len(vals) > 0 && vals[0] != "" {
		if n, err := strconv.ParseFloat(vals[0], 64); err == nil {
			out[key] = n
		}
	}
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
