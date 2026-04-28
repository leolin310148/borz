// Package e2e_verify_site provides the local HTTP site used by browser e2e tests.
package e2e_verify_site

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
)

// Site is a running e2e verification site.
type Site struct {
	server *http.Server
	ln     net.Listener
}

// Start starts the verification site on addr. Pass "127.0.0.1:0" or an empty
// addr to allocate an available localhost port.
func Start(addr string) (*Site, error) {
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &Site{
		server: &http.Server{Handler: Handler()},
		ln:     ln,
	}
	go func() {
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Printf("e2e verify site: %v\n", err)
		}
	}()
	return s, nil
}

// URL returns the site base URL.
func (s *Site) URL() string {
	return "http://" + s.ln.Addr().String()
}

// Close stops the site.
func (s *Site) Close(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// Handler returns the verification site HTTP handler.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", root)
	mux.HandleFunc("/page2", pageTwo)
	mux.HandleFunc("/tab", tabPage)
	mux.HandleFunc("/frame.html", frame)
	mux.HandleFunc("/api/ping", jsonEndpoint(map[string]string{"ok": "true", "source": "e2e_verify_site"}))
	mux.HandleFunc("/api/data", jsonEndpoint(map[string]string{"message": "hello from e2e verify site"}))
	return mux
}

func root(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>E2E Verify Home</title>
  <style>
    body { font-family: system-ui, sans-serif; margin: 24px; }
    .spacer { height: 1600px; border-top: 1px solid #ddd; margin-top: 24px; }
    #hover-target { display: inline-block; padding: 12px; border: 1px solid #777; }
  </style>
</head>
<body>
  <h1 id="ready">E2E Verify Site</h1>
  <button id="click-button" type="button" aria-label="Click counter">Click me</button>
  <output id="clicked-result">not clicked</output>

  <div id="hover-target" role="button" tabindex="0" aria-label="Hover target">Hover here</div>
  <output id="hover-result">not hovered</output>

  <form id="text-form">
    <label for="text-input">Text input</label>
    <input id="text-input" name="text-input" aria-label="E2E text input" autocomplete="off">
    <button id="submit-button" type="submit" aria-label="Submit form">Submit form</button>
  </form>
  <output id="input-state">empty</output>
  <output id="submit-result">not submitted</output>

  <label>
    <input id="check-box" type="checkbox" aria-label="E2E checkbox">
    Toggle checkbox
  </label>
  <output id="checkbox-state">unchecked</output>

  <label for="color-select">Pick a color</label>
  <select id="color-select" aria-label="E2E color select">
    <option value="red">Red</option>
    <option value="green">Green</option>
    <option value="blue">Blue</option>
  </select>
  <output id="select-state">red</output>

  <p><a id="page-two-link" href="/page2">Go to page two</a></p>
  <iframe id="verify-frame" title="Verify frame" src="/frame.html"></iframe>

  <div class="spacer"></div>
  <div id="scroll-marker">Scroll marker</div>

  <script>
    const text = (id, value) => { document.getElementById(id).textContent = value; };
    window.addEventListener('DOMContentLoaded', () => {
      console.log('e2e site loaded');
      fetch('/api/ping?boot=1').catch(() => {});

      let clicks = 0;
      document.getElementById('click-button').addEventListener('click', () => {
        clicks += 1;
        text('clicked-result', 'clicked ' + clicks);
      });

      const hover = () => text('hover-result', 'hovered');
      document.getElementById('hover-target').addEventListener('mouseenter', hover);
      document.getElementById('hover-target').addEventListener('mousemove', hover);

      const input = document.getElementById('text-input');
      input.addEventListener('input', () => text('input-state', input.value || 'empty'));
      document.getElementById('text-form').addEventListener('submit', (event) => {
        event.preventDefault();
        text('submit-result', 'submitted ' + input.value);
      });

      const checkbox = document.getElementById('check-box');
      checkbox.addEventListener('change', () => text('checkbox-state', checkbox.checked ? 'checked' : 'unchecked'));

      const select = document.getElementById('color-select');
      select.addEventListener('change', () => text('select-state', select.value));
    });
  </script>
</body>
</html>`)
}

func pageTwo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html><head><title>E2E Verify Page Two</title></head><body><h1 id="page-two-ready">Page Two</h1><a href="/">Back home</a></body></html>`)
}

func tabPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html><head><title>E2E Verify Tab</title></head><body><h1 id="tab-ready">Tab page</h1></body></html>`)
}

func frame(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html><head><title>E2E Verify Frame</title></head><body><h2 id="frame-ready">Frame ready</h2></body></html>`)
}

func jsonEndpoint(body map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
}
