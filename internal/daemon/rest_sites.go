package daemon

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/leolin310148/bb-browser-go/internal/site"
)

// registerSiteRoutes attaches /v1/sites* handlers. Site adapters are read from
// the server's disk; running an adapter turns into an eval dispatched through
// the usual CDP pipeline.
func (s *Server) registerSiteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/sites", s.handleSiteList)
	mux.HandleFunc("/v1/sites/info", s.handleSiteInfo)
	mux.HandleFunc("/v1/sites/run", s.handleSiteRun)
}

func (s *Server) handleSiteList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
		return
	}
	sites := site.AllSites()
	if sites == nil {
		sites = []*site.SiteMeta{}
	}
	sendJSON(w, 200, map[string]interface{}{
		"id":      newReqID(),
		"success": true,
		"data":    map[string]interface{}{"sites": sites},
	})
}

type siteInfoBody struct {
	Name string `json:"name"`
}

func (s *Server) handleSiteInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		sendJSON(w, 400, map[string]string{"error": "failed to read body"})
		return
	}
	var body siteInfoBody
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			sendJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
	}
	if body.Name == "" {
		sendJSON(w, 400, map[string]interface{}{
			"id":      newReqID(),
			"success": false,
			"error":   "name is required",
		})
		return
	}
	meta := site.FindSite(body.Name)
	if meta == nil {
		sendJSON(w, 404, map[string]interface{}{
			"id":      newReqID(),
			"success": false,
			"error":   "adapter not found: " + body.Name,
		})
		return
	}
	sendJSON(w, 200, map[string]interface{}{
		"id":      newReqID(),
		"success": true,
		"data":    map[string]interface{}{"site": meta},
	})
}

type siteRunBody struct {
	Name     string                 `json:"name"`
	Args     map[string]interface{} `json:"args"`
	TabID    interface{}            `json:"tabId,omitempty"`
	Tab      string                 `json:"tab,omitempty"`
	Activate bool                   `json:"activate,omitempty"`
}

func (b siteRunBody) tabID() interface{} {
	if b.TabID != nil {
		return b.TabID
	}
	if b.Tab != "" {
		return b.Tab
	}
	return nil
}

func (s *Server) handleSiteRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		sendJSON(w, 400, map[string]string{"error": "failed to read body"})
		return
	}
	var body siteRunBody
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			sendJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
	}
	if body.Name == "" {
		sendJSON(w, 400, map[string]interface{}{
			"id":      newReqID(),
			"success": false,
			"error":   "name is required",
		})
		return
	}
	meta := site.FindSite(body.Name)
	if meta == nil {
		sendJSON(w, 404, map[string]interface{}{
			"id":      newReqID(),
			"success": false,
			"error":   "adapter not found: " + body.Name,
		})
		return
	}

	tabID := ""
	switch v := body.tabID().(type) {
	case string:
		tabID = v
	}

	req, err := site.BuildEvalRequest(meta, body.Args, tabID)
	if err != nil {
		sendJSON(w, 500, map[string]interface{}{
			"id":      newReqID(),
			"success": false,
			"error":   err.Error(),
		})
		return
	}
	req.ID = newReqID()
	// If the body provided a non-string tabId (e.g. numeric index), use that
	// instead — BuildEvalRequest only accepts strings.
	if body.TabID != nil {
		if _, isString := body.TabID.(string); !isString {
			req.TabID = body.TabID
		}
	}
	if body.Activate {
		req.Activate = true
	}
	s.dispatchAndWrite(w, req)
}
