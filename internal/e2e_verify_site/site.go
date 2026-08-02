// Package e2e_verify_site provides the local HTTP site used by browser e2e tests.
package e2e_verify_site

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
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
	var cacheableRequests atomic.Uint64
	var noCacheRequests atomic.Uint64
	mux.HandleFunc("/", root)
	mux.HandleFunc("/page2", pageTwo)
	mux.HandleFunc("/url-fidelity", urlFidelity)
	mux.HandleFunc("/url-fidelity/", urlFidelity)
	mux.HandleFunc("/spa", spa)
	mux.HandleFunc("/spa/", spa)
	mux.HandleFunc("/delayed-render", delayedRender)
	mux.HandleFunc("/async-action", asyncAction)
	mux.HandleFunc("/file-upload", fileUpload)
	mux.HandleFunc("/dialogs", dialogs)
	mux.HandleFunc("/keyboard", keyboard)
	mux.HandleFunc("/clipboard", clipboard)
	mux.HandleFunc("/shadow-dom", shadowDOM)
	mux.HandleFunc("/accessibility-state", accessibilityState)
	mux.HandleFunc("/scrolling", scrolling)
	mux.HandleFunc("/redirect/start", redirectStart)
	mux.HandleFunc("/redirect/middle", redirectMiddle)
	mux.HandleFunc("/redirect/final", redirectFinal)
	mux.HandleFunc("/tab", tabPage)
	mux.HandleFunc("/frame.html", frame)
	mux.HandleFunc("/api/ping", jsonEndpoint(map[string]string{"ok": "true", "source": "e2e_verify_site"}))
	mux.HandleFunc("/api/data", jsonEndpoint(map[string]string{"message": "hello from e2e verify site"}))
	mux.HandleFunc("/api/network/echo", networkEcho)
	mux.HandleFunc("/api/network/slow", networkSlow)
	mux.HandleFunc("/api/network/stream", networkStream)
	mux.HandleFunc("/api/network/abort", networkAbort)
	mux.HandleFunc("/cache/cacheable", cachePage(&cacheableRequests, "public, max-age=3600"))
	mux.HandleFunc("/cache/no-cache", cachePage(&noCacheRequests, "no-store"))
	mux.HandleFunc("/api/fetch/get", fetchRequest)
	mux.HandleFunc("/api/fetch/post", fetchRequest)
	mux.HandleFunc("/api/fetch/put", fetchRequest)
	mux.HandleFunc("/api/fetch/status", fetchRequest)
	return mux
}

func cachePage(counter *atomic.Uint64, cacheControl string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		count := counter.Add(1)
		w.Header().Set("Cache-Control", cacheControl)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-E2E-Request-Count", strconv.FormatUint(count, 10))
		fmt.Fprintf(w, `<!doctype html><html><head><title>E2E Cache</title></head><body><h1 id="cache-ready">Cache fixture ready</h1><output id="request-count">%d</output></body></html>`, count)
	}
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

func urlFidelity(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/url-fidelity" && r.URL.Path != "/url-fidelity/路徑" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>E2E URL Fidelity</title>
</head>
<body>
  <h1 id="url-fidelity-ready">URL fidelity ready</h1>
  <dl>
    <dt>Path</dt><dd id="url-path"></dd>
    <dt>Query</dt><dd id="url-query"></dd>
    <dt>Fragment</dt><dd id="url-fragment"></dd>
    <dt>Unicode parameter</dt><dd id="url-unicode-param"></dd>
    <dt>Reserved parameter</dt><dd id="url-reserved-param"></dd>
  </dl>
  <script>
    const currentURL = new URL(window.location.href);
    document.getElementById('url-path').textContent = currentURL.pathname;
    document.getElementById('url-query').textContent = currentURL.search;
    document.getElementById('url-fragment').textContent = currentURL.hash;
    document.getElementById('url-unicode-param').textContent = currentURL.searchParams.get('name') || '';
    document.getElementById('url-reserved-param').textContent = currentURL.searchParams.get('reserved') || '';
  </script>
</body>
</html>`)
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

  <button id="deferred-button" type="button" aria-label="Open deferred confirm dialog">Open deferred confirm</button>
  <output id="deferred-result">deferred pending</output>

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

    // Opens the dialog AFTER the click handler returns, so the automation
    // command that triggers it is not itself blocked by the dialog. Lets a
    // test observe an unhandled dialog sitting on the page.
    document.getElementById('deferred-button').addEventListener('click', () => {
      setTimeout(() => {
        const result = confirm('E2E deferred confirm');
        document.getElementById('deferred-result').textContent = 'deferred: ' + result;
      }, 50);
    });
  </script>
</body>
</html>`)
}

func keyboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html>
<html>
<head><meta charset="utf-8"><title>E2E Keyboard</title></head>
<body>
  <h1 id="keyboard-ready">Keyboard fixture</h1>
  <label for="focus-first">First focus stop</label>
  <input id="focus-first" aria-label="First focus stop" autocomplete="off">
  <button id="enter-button" type="button">Enter activation</button>
  <button id="space-button" type="button">Space activation</button>
  <button id="open-panel" type="button">Open dismissible panel</button>
  <section id="dismissible-panel" aria-label="Dismissible panel" hidden>Press Escape to dismiss</section>

  <div id="arrow-list" role="listbox" tabindex="0" aria-label="Arrow choices" aria-activedescendant="arrow-one">
    <div id="arrow-one" role="option" aria-selected="true">Choice one</div>
    <div id="arrow-two" role="option" aria-selected="false">Choice two</div>
    <div id="arrow-three" role="option" aria-selected="false">Choice three</div>
  </div>

  <output id="activation-result">none</output>
  <output id="arrow-result">Choice one</output>
  <output id="key-event-data">none</output>
  <script>
    const activationResult = document.getElementById('activation-result');
    document.getElementById('enter-button').addEventListener('click', () => { activationResult.textContent = 'enter activated'; });
    document.getElementById('space-button').addEventListener('click', () => { activationResult.textContent = 'space activated'; });

    const panel = document.getElementById('dismissible-panel');
    document.getElementById('open-panel').addEventListener('click', () => { panel.hidden = false; });

    const list = document.getElementById('arrow-list');
    const options = Array.from(list.querySelectorAll('[role=option]'));
    let selected = 0;
    list.addEventListener('keydown', (event) => {
      if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
      event.preventDefault();
      selected = (selected + (event.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length;
      options.forEach((option, index) => option.setAttribute('aria-selected', String(index === selected)));
      list.setAttribute('aria-activedescendant', options[selected].id);
      document.getElementById('arrow-result').textContent = options[selected].textContent;
    });

    document.addEventListener('keydown', (event) => {
      document.getElementById('key-event-data').textContent = JSON.stringify({
        key: event.key,
        target: event.target.id,
        alt: event.altKey,
        ctrl: event.ctrlKey,
        meta: event.metaKey,
        shift: event.shiftKey
      });
      if (event.key === 'Escape') panel.hidden = true;
      if (event.altKey || event.ctrlKey || event.metaKey) event.preventDefault();
    });
  </script>
</body>
</html>`)
}

func clipboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html>
<html>
<head><meta charset="utf-8"><title>E2E Clipboard</title></head>
<body>
  <h1 id="clipboard-ready">Clipboard fixture</h1>
  <label for="clipboard-input">Clipboard paste input</label>
  <textarea id="clipboard-input" class="xterm-helper-textarea" aria-label="Clipboard paste input"></textarea>
  <output id="paste-event" data-count="0">none</output>
  <output id="input-event" data-count="0">none</output>
  <script>
    const input = document.getElementById('clipboard-input');
    const pasteEvent = document.getElementById('paste-event');
    const inputEvent = document.getElementById('input-event');
    input.addEventListener('paste', (event) => {
      pasteEvent.dataset.count = String(Number(pasteEvent.dataset.count) + 1);
      pasteEvent.textContent = event.clipboardData.getData('text/plain');
    });
    input.addEventListener('input', () => {
      inputEvent.dataset.count = String(Number(inputEvent.dataset.count) + 1);
      inputEvent.textContent = input.value;
    });
  </script>
</body>
</html>`)
}

func shadowDOM(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html>
<html>
<head><meta charset="utf-8"><title>E2E Shadow DOM</title></head>
<body>
  <h1 id="shadow-page-ready">Shadow DOM fixture</h1>
  <div id="shadow-host"></div>
  <script>
    const host = document.getElementById('shadow-host');
    const root = host.attachShadow({ mode: 'open' });
    root.innerHTML = `+"`"+`
      <section id="nested-shadow-controls" aria-label="Shadow controls">
        <button id="shadow-action-button" type="button" aria-label="Shadow action button">Activate shadow action</button>
        <label for="shadow-text-input">Shadow text input</label>
        <input id="shadow-text-input" aria-label="Shadow text input" autocomplete="off">
        <output id="shadow-result" role="status">shadow idle</output>
      </section>
    `+"`"+`;

    let clicks = 0;
    root.getElementById('shadow-action-button').addEventListener('click', () => {
      clicks += 1;
      root.getElementById('shadow-result').textContent = 'clicked ' + clicks;
    });
    root.getElementById('shadow-text-input').addEventListener('input', (event) => {
      root.getElementById('shadow-result').textContent = 'value: ' + event.target.value;
    });
    host.dataset.shadowReady = 'true';
  </script>
</body>
</html>`)
}

func accessibilityState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html>
<html>
<head><meta charset="utf-8"><title>E2E Accessibility State</title></head>
<body>
  <h1 id="accessibility-state-ready">Accessibility state fixture</h1>
  <button id="disabled-action" type="button" disabled aria-label="Disabled action">Disabled action</button>
  <button id="disclosure" type="button" aria-label="State disclosure" aria-expanded="false" aria-controls="disclosure-panel">Toggle details</button>
  <section id="disclosure-panel" hidden><p>Revealed accessibility details</p></section>
  <label><input id="state-checkbox" type="checkbox" aria-label="State checkbox" aria-checked="false"> Check state</label>
  <div id="state-options" role="listbox" aria-label="State choices">
    <div id="choice-one" role="option" aria-selected="true" tabindex="0">Choice one</div>
    <div id="choice-two" role="option" aria-selected="false" tabindex="-1">Choice two</div>
  </div>
  <div id="state-live" role="status" aria-live="polite" aria-label="State updates">State idle</div>
  <button id="mutate-state" type="button" aria-label="Mutate accessibility state">Mutate state</button>
  <script>
    const mutate = document.getElementById('mutate-state');
    let changed = false;
    mutate.addEventListener('click', () => {
      changed = !changed;
      document.getElementById('disabled-action').disabled = !changed;
      document.getElementById('disclosure').setAttribute('aria-expanded', String(changed));
      document.getElementById('disclosure-panel').hidden = !changed;
      const checkbox = document.getElementById('state-checkbox');
      checkbox.checked = changed;
      checkbox.setAttribute('aria-checked', String(changed));
      document.getElementById('choice-one').setAttribute('aria-selected', String(!changed));
      document.getElementById('choice-two').setAttribute('aria-selected', String(changed));
      const live = document.getElementById('state-live');
      live.textContent = changed ? 'Accessibility state updated' : 'State idle';
      live.dataset.state = changed ? 'updated' : 'idle';
    });
  </script>
</body>
</html>`)
}

func scrolling(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>E2E Scrolling</title>
  <style>
    html, body { margin: 0; }
    #scroll-canvas { position: relative; width: 2400px; height: 2200px; background: linear-gradient(135deg, #fff, #eef); }
    #scrolling-ready { position: absolute; top: 16px; left: 16px; margin: 0; }
    #outer-scroll { position: absolute; top: 80px; left: 80px; width: 360px; height: 260px; overflow: auto; border: 2px solid #345; }
    #outer-content { position: relative; width: 900px; height: 750px; }
    #inner-scroll { position: absolute; top: 140px; left: 180px; width: 240px; height: 160px; overflow: auto; border: 2px solid #678; }
    #inner-content { position: relative; width: 720px; height: 520px; }
    #nested-end-marker { position: absolute; right: 0; bottom: 0; }
    #viewport-end-marker { position: absolute; top: 2050px; left: 2250px; width: 120px; height: 80px; }
  </style>
</head>
<body>
  <main id="scroll-canvas">
    <h1 id="scrolling-ready">Scrolling fixture</h1>
    <section id="outer-scroll" aria-label="Outer scrolling container">
      <div id="outer-content">
        <section id="inner-scroll" aria-label="Inner scrolling container">
          <div id="inner-content">
            <span id="nested-end-marker">Nested end</span>
          </div>
        </section>
      </div>
    </section>
    <div id="viewport-end-marker">Viewport end</div>
  </main>
  <script>
    const outer = document.getElementById('outer-scroll');
    const inner = document.getElementById('inner-scroll');
    outer.scrollTo(40, 50);
    inner.scrollTo(60, 70);
    document.getElementById('scrolling-ready').dataset.initialized = 'true';
  </script>
</body>
</html>`)
}

func tabPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html><head><title>E2E Verify Tab</title></head><body><h1 id="tab-ready">Tab page</h1></body></html>`)
}

func redirectStart(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/redirect/middle", http.StatusFound)
}

func redirectMiddle(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/redirect/final", http.StatusTemporaryRedirect)
}

func redirectFinal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html><head><title>E2E Redirect Final</title></head><body><h1 id="redirect-ready">Redirect chain complete</h1></body></html>`)
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

func networkEcho(w http.ResponseWriter, r *http.Request) {
	status := http.StatusOK
	if raw := r.URL.Query().Get("status"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 100 || parsed > 599 {
			http.Error(w, "status must be between 100 and 599", http.StatusBadRequest)
			return
		}
		status = parsed
	}

	requestBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"method":      r.Method,
		"requestBody": string(requestBody),
		"response":    r.URL.Query().Get("response"),
	})
}

func networkSlow(w http.ResponseWriter, r *http.Request) {
	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		networkEcho(w, r)
	case <-r.Context().Done():
		return
	}
}

func networkStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "stream-chunk-one\n")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		_, _ = io.WriteString(w, "stream-chunk-two\n")
	case <-r.Context().Done():
	}
}

func networkAbort(w http.ResponseWriter, _ *http.Request) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "connection hijacking unavailable", http.StatusInternalServerError)
		return
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	const partial = "partial-response"
	_, _ = fmt.Fprintf(rw, "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(partial)+64, partial)
	_ = rw.Flush()
}

func fetchRequest(w http.ResponseWriter, r *http.Request) {
	endpoint := strings.TrimPrefix(r.URL.Path, "/api/fetch/")
	wantMethod := map[string]string{
		"get": http.MethodGet, "post": http.MethodPost, "put": http.MethodPut,
		"status": http.MethodGet,
	}[endpoint]
	if wantMethod == "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != wantMethod {
		w.Header().Set("Allow", wantMethod)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}
	status := http.StatusOK
	if endpoint == "status" {
		status = http.StatusUnprocessableEntity
	}

	var jsonBody interface{}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") && len(rawBody) > 0 {
		if err := json.Unmarshal(rawBody, &jsonBody); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
	}
	var formBody url.Values
	if strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		formBody, err = url.ParseQuery(string(rawBody))
		if err != nil {
			http.Error(w, "invalid form body", http.StatusBadRequest)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"method":       r.Method,
		"header":       r.Header.Get("X-E2E-Header"),
		"secondHeader": r.Header.Get("X-E2E-Second"),
		"contentType":  r.Header.Get("Content-Type"),
		"cookie":       r.Header.Get("Cookie"),
		"rawBody":      string(rawBody),
		"jsonBody":     jsonBody,
		"formBody":     formBody,
	})
}
