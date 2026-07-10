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
	mux.HandleFunc("/spa", spa)
	mux.HandleFunc("/spa/", spa)
	mux.HandleFunc("/delayed-render", delayedRender)
	mux.HandleFunc("/async-action", asyncAction)
	mux.HandleFunc("/file-upload", fileUpload)
	mux.HandleFunc("/dialogs", dialogs)
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

func spa(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/spa" && r.URL.Path != "/spa/" && r.URL.Path != "/spa/details" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>E2E SPA Home</title>
</head>
<body>
  <h1>E2E Verify SPA</h1>
  <nav aria-label="SPA routes">
    <a href="/spa" data-spa-route="home" aria-label="Go to SPA home">Home</a>
    <a href="/spa/details" data-spa-route="details" aria-label="Go to SPA details">Details</a>
  </nav>
  <main id="spa-view">
    <h2 id="spa-route-heading"></h2>
    <p id="spa-route-content"></p>
    <div id="spa-ready" role="status" aria-live="polite"></div>
  </main>
  <script>
    const spaRoutes = {
      home: {
        path: '/spa',
        title: 'E2E SPA Home',
        heading: 'SPA home',
        content: 'Home route content'
      },
      details: {
        path: '/spa/details',
        title: 'E2E SPA Details',
        heading: 'SPA details',
        content: 'Details route content'
      }
    };

    function spaRouteForPath(path) {
      return path === spaRoutes.details.path ? 'details' : 'home';
    }

    function renderSPARoute() {
      const routeName = spaRouteForPath(window.location.pathname);
      const route = spaRoutes[routeName];
      document.title = route.title;
      document.getElementById('spa-route-heading').textContent = route.heading;
      document.getElementById('spa-route-content').textContent = route.content;

      const ready = document.getElementById('spa-ready');
      ready.dataset.route = routeName;
      ready.textContent = routeName + ' route ready';

      document.querySelectorAll('[data-spa-route]').forEach((link) => {
        if (link.dataset.spaRoute === routeName) {
          link.setAttribute('aria-current', 'page');
        } else {
          link.removeAttribute('aria-current');
        }
      });
    }

    document.querySelector('nav').addEventListener('click', (event) => {
      const link = event.target.closest('[data-spa-route]');
      if (!link) return;
      event.preventDefault();
      history.pushState({ route: link.dataset.spaRoute }, '', link.href);
      renderSPARoute();
    });
    window.addEventListener('popstate', renderSPARoute);
    renderSPARoute();
  </script>
</body>
</html>`)
}

func delayedRender(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>E2E Delayed Render</title>
</head>
<body>
  <h1 id="delayed-page-ready">Delayed render fixture</h1>
  <main id="delayed-content" aria-live="polite"></main>
  <script>
    window.setTimeout(() => {
      const marker = document.createElement('p');
      marker.id = 'delayed-marker';
      marker.textContent = 'Delayed marker ready';
      document.getElementById('delayed-content').appendChild(marker);
    }, 750);
  </script>
</body>
</html>`)
}

func asyncAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>E2E Async Action</title>
</head>
<body>
  <h1 id="async-action-ready">Async action fixture</h1>
  <button id="async-action-button" type="button" aria-label="Start async action">Start async action</button>
  <main id="async-action-content" aria-live="polite"></main>
  <script>
    let asyncActionCount = 0;
    document.getElementById('async-action-button').addEventListener('click', () => {
      asyncActionCount += 1;
      document.getElementById('async-action-content').replaceChildren();
      window.setTimeout(() => {
        const result = document.createElement('p');
        result.id = 'async-action-result';
        result.dataset.count = String(asyncActionCount);
        result.textContent = 'Async action ' + asyncActionCount + ' complete';
        document.getElementById('async-action-content').appendChild(result);
      }, 750);
    });
  </script>
</body>
</html>`)
}

func fileUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>E2E File Upload</title>
</head>
<body>
  <h1 id="file-upload-ready">File upload fixture</h1>

  <label for="single-file">Single file</label>
  <input id="single-file" type="file" aria-label="Single file upload">
  <output id="single-upload-state" data-file-count="0">No single file selected</output>

  <label for="multiple-files">Multiple files</label>
  <input id="multiple-files" type="file" multiple aria-label="Multiple file upload">
  <output id="multiple-upload-state" data-file-count="0">No multiple files selected</output>

  <script>
    async function showFiles(input, output) {
      const files = Array.from(input.files);
      const descriptions = await Promise.all(files.map(async (file) => {
        return file.name + ': ' + await file.text();
      }));
      output.textContent = descriptions.join(' | ');
      output.dataset.fileCount = String(files.length);
    }

    const single = document.getElementById('single-file');
    single.addEventListener('change', () => {
      showFiles(single, document.getElementById('single-upload-state'));
    });

    const multiple = document.getElementById('multiple-files');
    multiple.addEventListener('change', () => {
      showFiles(multiple, document.getElementById('multiple-upload-state'));
    });
  </script>
</body>
</html>`)
}

func dialogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>E2E Dialogs</title>
</head>
<body>
  <h1 id="dialogs-ready">Dialog fixture</h1>

  <button id="alert-button" type="button" aria-label="Open alert dialog">Open alert</button>
  <output id="alert-result">alert pending</output>

  <button id="confirm-button" type="button" aria-label="Open confirm dialog">Open confirm</button>
  <output id="confirm-result">confirm pending</output>

  <button id="prompt-button" type="button" aria-label="Open prompt dialog">Open prompt</button>
  <output id="prompt-result">prompt pending</output>

  <script>
    document.getElementById('alert-button').addEventListener('click', () => {
      alert('E2E alert');
      document.getElementById('alert-result').textContent = 'alert accepted';
    });

    document.getElementById('confirm-button').addEventListener('click', () => {
      const result = confirm('E2E confirm');
      document.getElementById('confirm-result').textContent = 'confirm: ' + result;
    });

    document.getElementById('prompt-button').addEventListener('click', () => {
      const result = prompt('E2E prompt', 'default prompt');
      document.getElementById('prompt-result').textContent = 'prompt: ' + result;
    });
  </script>
</body>
</html>`)
}

func tabPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html><head><title>E2E Verify Tab</title></head><body><h1 id="tab-ready">Tab page</h1></body></html>`)
}

func frame(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html>
<html>
<head><title>E2E Verify Frame</title></head>
<body>
  <h2 id="frame-ready">Frame ready</h2>
  <section id="frame-controls" aria-label="Frame controls">
    <label for="frame-text-input">Frame text</label>
    <input id="frame-text-input" aria-label="Frame text input" autocomplete="off">
    <button id="frame-submit" type="button" aria-label="Submit frame input">Submit frame input</button>
    <p id="frame-result" role="status" aria-live="polite">Frame result pending</p>
  </section>
  <script>
    document.getElementById('frame-submit').addEventListener('click', () => {
      const value = document.getElementById('frame-text-input').value;
      document.getElementById('frame-result').textContent = 'Frame received: ' + value;
    });
  </script>
</body>
</html>`)
}

func jsonEndpoint(body map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
}
