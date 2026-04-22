package daemon

import (
	_ "embed"
	"net/http"
)

//go:embed embed/openapi.yaml
var openAPISpec []byte

// docsHTML is a minimal Swagger UI page served at /docs. It pulls Swagger UI
// from a CDN so the daemon binary stays small; the only thing we ship is the
// spec itself.
const docsHTML = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>bb-browser daemon API</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
window.ui = SwaggerUIBundle({
  url: "/openapi.yaml",
  dom_id: "#swagger-ui",
  deepLinking: true,
  presets: [SwaggerUIBundle.presets.apis],
});
</script>
</body>
</html>`

func (s *Server) registerDocsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
			return
		}
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.Write(openAPISpec)
	})
	mux.HandleFunc("/docs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			sendJSON(w, 405, map[string]string{"error": "Method not allowed"})
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(docsHTML))
	})
}
