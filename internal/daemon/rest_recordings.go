package daemon

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/recorder"
)

func (s *Server) recordManager() *recordingManager {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recordings == nil {
		s.recordings = newRecordingManager(s.cdp, s.extHub)
	}
	return s.recordings
}

func (s *Server) registerRecordingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/recordings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			sendJSON(w, 200, map[string]any{"recordings": s.recordManager().List()})
		case http.MethodPost:
			var opts recorder.CaptureOptions
			if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
				sendJSON(w, 400, map[string]string{"error": "invalid JSON: " + err.Error()})
				return
			}
			if opts.Mode == "" || opts.Mode == "cdp" {
				if !s.cdp.Connected() {
					if err := s.cdp.WaitUntilReady(time.Duration(config.CommandTimeout) * time.Second); err != nil {
						sendJSON(w, 503, map[string]string{"error": "Chrome not connected: " + err.Error()})
						return
					}
				}
			}
			info, err := s.recordManager().Start(opts)
			if err != nil {
				sendJSON(w, 400, map[string]string{"error": err.Error()})
				return
			}
			sendJSON(w, 200, info)
		default:
			sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
		}
	})

	mux.HandleFunc("/v1/recordings/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/v1/recordings/")
		parts := strings.Split(strings.Trim(rest, "/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			sendJSON(w, 404, map[string]string{"error": "recording id is required"})
			return
		}
		id := parts[0]
		if id == "current" || id == "-" {
			id = ""
		}
		action := "info"
		if len(parts) > 1 {
			action = parts[1]
		}
		switch action {
		case "info":
			if r.Method != http.MethodGet {
				sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
				return
			}
			info, err := s.recordManager().Info(id)
			if err != nil {
				sendJSON(w, 404, map[string]string{"error": err.Error()})
				return
			}
			sendJSON(w, 200, info)
		case "stop":
			if r.Method != http.MethodPost {
				sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
				return
			}
			info, err := s.recordManager().Stop(id, r.URL.Query().Get("recover") == "true")
			if err != nil {
				sendJSON(w, 400, map[string]string{"error": err.Error()})
				return
			}
			sendJSON(w, 200, info)
		case "pause":
			info, err := s.recordManager().Pause(id)
			if err != nil {
				sendJSON(w, 400, map[string]string{"error": err.Error()})
				return
			}
			sendJSON(w, 200, info)
		case "resume":
			info, err := s.recordManager().Resume(id)
			if err != nil {
				sendJSON(w, 400, map[string]string{"error": err.Error()})
				return
			}
			sendJSON(w, 200, info)
		case "redact":
			if r.Method != http.MethodPost {
				sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
				return
			}
			var mask recorder.RedactionMask
			if err := json.NewDecoder(r.Body).Decode(&mask); err != nil {
				sendJSON(w, 400, map[string]string{"error": "invalid JSON: " + err.Error()})
				return
			}
			info, err := s.recordManager().Info(id)
			if err != nil {
				sendJSON(w, 404, map[string]string{"error": err.Error()})
				return
			}
			if err := recorder.AddRedaction(info.Path, mask); err != nil {
				sendJSON(w, 400, map[string]string{"error": err.Error()})
				return
			}
			sendJSON(w, 200, map[string]any{"ok": true})
		default:
			sendJSON(w, 404, map[string]string{"error": "unknown recording action"})
		}
	})
}

func (s *Server) registerRecordingPreviewRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/recordings/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/recordings/"), "/")
		if id == "" {
			sendJSON(w, 404, map[string]string{"error": "recording id is required"})
			return
		}
		info, err := s.recordManager().Info(id)
		if err != nil {
			sendJSON(w, 404, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>borz recording</title><style>body{font:14px system-ui;margin:24px}code{background:#f6f8fa;padding:2px 4px}</style>`))
		w.Write([]byte(`<h1>borz recording ` + htmlEscape(info.ID) + `</h1>`))
		w.Write([]byte(`<p>Status: <strong>` + htmlEscape(info.Status) + `</strong></p>`))
		w.Write([]byte(`<p>Frames: ` + strconv.Itoa(info.FrameCount) + ` · Events: ` + strconv.Itoa(info.EventCount) + `</p>`))
		w.Write([]byte(`<p>Bundle: <code>` + htmlEscape(info.Path) + `</code></p>`))
		w.Write([]byte(`<p>Render with <code>borz record render ` + htmlEscape(info.Path) + ` --preset share</code></p>`))
	})
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;")
	return r.Replace(s)
}
