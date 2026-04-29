package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/protocol"
)

// extDaemon is fakeDaemon's analogue for the extension-bridge endpoints.
// Resets the client package's cachedInfo on entry and exit so a closed
// httptest server can't bleed into a later test that uses a different
// daemon (e.g. fakeDaemon → handleDaemon → StopDaemon).
func extDaemon(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)

	u := strings.TrimPrefix(ts.URL, "http://")
	host, portStr, _ := net.SplitHostPort(u)
	port, _ := strconv.Atoi(portStr)
	info := protocol.DaemonInfo{PID: os.Getpid(), Host: host, Port: port}
	b, _ := json.Marshal(info)
	if err := os.WriteFile(filepath.Join(home, "daemon.json"), b, 0o644); err != nil {
		t.Fatalf("write daemon.json: %v", err)
	}
	return ts
}

func TestTruncate(t *testing.T) {
	if got := truncate("abc", 10); got != "abc" {
		t.Errorf("short: got %q", got)
	}
	if got := truncate("abcdef", 3); got != "abc…" {
		t.Errorf("long: got %q", got)
	}
}

func TestHandleBookmarks_Subcommands(t *testing.T) {
	type seenRequest struct {
		path  string
		query string
		body  map[string]any
	}
	var seen []seenRequest
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		rec := seenRequest{path: r.URL.Path, query: r.URL.RawQuery}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&rec.body)
		}
		seen = append(seen, rec)
		switch r.URL.Path {
		case "/v1/bookmarks/tree":
			w.Write([]byte(`[{"id":"root","children":[{"id":"b1","title":"Example","url":"https://example.test"}]}]`))
		case "/v1/bookmarks/search":
			w.Write([]byte(`[{"id":"b2","url":"https://search.test"}]`))
		case "/v1/bookmarks/create", "/v1/bookmarks/update", "/v1/bookmarks/remove":
			w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	out := withCapturedStdout(t, func() { handleBookmarks([]string{"tree"}, false, nil) })
	if !strings.Contains(out, "+ [root]") || !strings.Contains(out, "- [b1] Example https://example.test") {
		t.Fatalf("tree output = %q", out)
	}
	out = withCapturedStdout(t, func() { handleBookmarks([]string{"search", "needle"}, false, nil) })
	if !strings.Contains(out, "Bookmarks (1 results)") || !strings.Contains(out, "(untitled)") {
		t.Fatalf("search output = %q", out)
	}
	out = withCapturedStdout(t, func() {
		handleBookmarks([]string{"create", "https://new.test", "New", "Title"}, false, []string{"--parent", "root"})
	})
	if !strings.Contains(out, "Bookmark created") {
		t.Fatalf("create output = %q", out)
	}
	out = withCapturedStdout(t, func() {
		handleBookmarks([]string{"update", "b2"}, false, []string{"--title", "Updated", "--url", "https://updated.test"})
	})
	if !strings.Contains(out, "Bookmark updated") {
		t.Fatalf("update output = %q", out)
	}
	out = withCapturedStdout(t, func() { handleBookmarks([]string{"remove", "b2"}, true, []string{"--recursive"}) })
	if !strings.Contains(out, `"ok":true`) {
		t.Fatalf("remove output = %q", out)
	}

	wantPaths := []string{"/v1/bookmarks/tree", "/v1/bookmarks/search", "/v1/bookmarks/create", "/v1/bookmarks/update", "/v1/bookmarks/remove"}
	if len(seen) != len(wantPaths) {
		t.Fatalf("seen requests = %+v", seen)
	}
	for i, want := range wantPaths {
		if seen[i].path != want {
			t.Fatalf("request %d path = %q, want %q", i, seen[i].path, want)
		}
	}
	if seen[1].query != "q=needle" {
		t.Fatalf("search query = %q", seen[1].query)
	}
	if seen[2].body["parentId"] != "root" || seen[2].body["title"] != "New Title" {
		t.Fatalf("create body = %+v", seen[2].body)
	}
	if seen[3].body["id"] != "b2" {
		t.Fatalf("update body = %+v", seen[3].body)
	}
	if seen[4].body["recursive"] != true {
		t.Fatalf("remove body = %+v", seen[4].body)
	}
}

func TestHandleBookmarks_JSONAndInvalidTreeFallback(t *testing.T) {
	calls := 0
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		calls++
		if calls == 1 {
			w.Write([]byte(`not-json`))
			return
		}
		w.Write([]byte(`[{"id":"root"}]`))
	})

	out := withCapturedStdout(t, func() { handleBookmarks([]string{"tree"}, false, nil) })
	if !strings.Contains(out, "not-json") {
		t.Fatalf("expected raw fallback, got %q", out)
	}
	out = withCapturedStdout(t, func() { handleBookmarks([]string{"tree"}, true, nil) })
	if !strings.Contains(out, `"id":"root"`) {
		t.Fatalf("expected JSON tree, got %q", out)
	}
}

func TestHandleBrowserHistory_Subcommands(t *testing.T) {
	var paths []string
	var bodies []map[string]any
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		switch r.URL.Path {
		case "/v1/browser-history/search":
			w.Write([]byte(`[{"id":"h1","url":"https://history.test","visitCount":2}]`))
		case "/v1/browser-history/delete-url":
			w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	out := withCapturedStdout(t, func() { handleBrowserHistory([]string{"search", "term"}, false, []string{"--limit", "5"}) })
	if !strings.Contains(out, "Browser history (1 results)") || !strings.Contains(out, "(untitled)") {
		t.Fatalf("history output = %q", out)
	}
	out = withCapturedStdout(t, func() { handleBrowserHistory([]string{"search"}, true, nil) })
	if !strings.Contains(out, `"id":"h1"`) {
		t.Fatalf("history JSON output = %q", out)
	}
	out = withCapturedStdout(t, func() { handleBrowserHistory([]string{"delete-url", "https://history.test"}, false, nil) })
	if !strings.Contains(out, "History URL deleted") {
		t.Fatalf("delete output = %q", out)
	}
	if paths[0] != "/v1/browser-history/search?maxResults=5&q=term" {
		t.Fatalf("search path = %q", paths[0])
	}
	if bodies[2]["url"] != "https://history.test" {
		t.Fatalf("delete body = %+v", bodies[2])
	}
}

func TestHandleDownloads_Subcommands(t *testing.T) {
	var seen []struct {
		path  string
		query string
		body  map[string]any
	}
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		rec := struct {
			path  string
			query string
			body  map[string]any
		}{path: r.URL.Path, query: r.URL.RawQuery}
		_ = json.NewDecoder(r.Body).Decode(&rec.body)
		seen = append(seen, rec)
		switch r.URL.Path {
		case "/v1/downloads/search":
			w.Write([]byte(`[{"id":7,"url":"https://file.test/a.zip","filename":"a.zip","state":"complete","bytesReceived":10,"totalBytes":10}]`))
		case "/v1/downloads/download", "/v1/downloads/erase", "/v1/downloads/cancel", "/v1/downloads/pause", "/v1/downloads/resume", "/v1/downloads/show", "/v1/downloads/show-default-folder":
			w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	out := withCapturedStdout(t, func() { handleDownloads([]string{"list"}, false, []string{"--limit", "3", "--state", "complete"}) })
	if !strings.Contains(out, "Downloads (1 results)") || !strings.Contains(out, "a.zip") {
		t.Fatalf("downloads list output = %q", out)
	}
	out = withCapturedStdout(t, func() { handleDownloads([]string{"search", "zip"}, true, nil) })
	if !strings.Contains(out, `"filename":"a.zip"`) {
		t.Fatalf("downloads search JSON output = %q", out)
	}
	out = withCapturedStdout(t, func() {
		handleDownloads([]string{"start", "https://file.test/b.zip"}, false, []string{"--filename", "b.zip", "--save-as"})
	})
	if !strings.Contains(out, "Download started") {
		t.Fatalf("download start output = %q", out)
	}
	out = withCapturedStdout(t, func() { handleDownloads([]string{"erase"}, false, []string{"--id", "7"}) })
	if !strings.Contains(out, "Download records erased") {
		t.Fatalf("erase output = %q", out)
	}
	for _, sub := range []string{"cancel", "pause", "resume", "show"} {
		out = withCapturedStdout(t, func() { handleDownloads([]string{sub, "7"}, false, nil) })
		if !strings.Contains(out, "Download "+sub+" requested") {
			t.Fatalf("%s output = %q", sub, out)
		}
	}
	out = withCapturedStdout(t, func() { handleDownloads([]string{"show-folder"}, false, nil) })
	if !strings.Contains(out, "Download folder shown") {
		t.Fatalf("show-folder output = %q", out)
	}

	if seen[0].path != "/v1/downloads/search" || seen[0].query != "limit=3&state=complete" {
		t.Fatalf("list request = %+v", seen[0])
	}
	if seen[1].query != "q=zip" {
		t.Fatalf("search request = %+v", seen[1])
	}
	if seen[2].body["saveAs"] != true || seen[2].body["filename"] != "b.zip" {
		t.Fatalf("download body = %+v", seen[2].body)
	}
	if seen[3].body["id"] != float64(7) {
		t.Fatalf("erase body = %+v", seen[3].body)
	}
}

func TestHandleDownloads_EraseByQuery(t *testing.T) {
	var body map[string]any
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"ok":true}`))
	})
	out := withCapturedStdout(t, func() { handleDownloads([]string{"erase", "old", "zip"}, false, nil) })
	if !strings.Contains(out, "Download records erased") {
		t.Fatalf("erase output = %q", out)
	}
	if body["q"] != "old zip" {
		t.Fatalf("erase body = %+v", body)
	}
}

func TestHandleWindows_Subcommands(t *testing.T) {
	var seen []struct {
		path string
		body map[string]any
	}
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		rec := struct {
			path string
			body map[string]any
		}{path: r.URL.Path}
		_ = json.NewDecoder(r.Body).Decode(&rec.body)
		seen = append(seen, rec)
		switch r.URL.Path {
		case "/v1/windows":
			w.Write([]byte(`[{"id":1,"type":"normal","state":"normal","focused":true,"width":800,"height":600,"tabs":[{"id":9,"title":"T"}]}]`))
		case "/v1/windows/create", "/v1/windows/update", "/v1/windows/close":
			w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	out := withCapturedStdout(t, func() { handleWindows([]string{"list"}, false, nil) })
	if !strings.Contains(out, "Windows (1 total)") || !strings.Contains(out, "* [1]") {
		t.Fatalf("windows list output = %q", out)
	}
	out = withCapturedStdout(t, func() { handleWindows([]string{"list"}, true, nil) })
	if !strings.Contains(out, `"id":1`) {
		t.Fatalf("windows JSON output = %q", out)
	}
	out = withCapturedStdout(t, func() { handleWindows([]string{"new", "https://new.test"}, false, []string{"--focused"}) })
	if !strings.Contains(out, "Window created") {
		t.Fatalf("new output = %q", out)
	}
	out = withCapturedStdout(t, func() { handleWindows([]string{"focus", "1"}, false, nil) })
	if !strings.Contains(out, "Window focused") {
		t.Fatalf("focus output = %q", out)
	}
	out = withCapturedStdout(t, func() { handleWindows([]string{"close", "1"}, false, nil) })
	if !strings.Contains(out, "Window closed") {
		t.Fatalf("close output = %q", out)
	}

	if seen[2].body["url"] != "https://new.test" || seen[2].body["focused"] != true {
		t.Fatalf("new body = %+v", seen[2].body)
	}
	if seen[3].body["id"] != float64(1) {
		t.Fatalf("focus body = %+v", seen[3].body)
	}
	if seen[4].body["id"] != float64(1) {
		t.Fatalf("close body = %+v", seen[4].body)
	}
}

func TestNonEmptyAndEmitRawOrMessage(t *testing.T) {
	if nonEmpty("", "fallback") != "fallback" || nonEmpty("value", "fallback") != "value" {
		t.Fatal("nonEmpty returned unexpected value")
	}
	out := withCapturedStdout(t, func() { emitRawOrMessage(json.RawMessage(`{"ok":true}`), true, "ignored") })
	if !strings.Contains(out, `"ok":true`) {
		t.Fatalf("raw output = %q", out)
	}
	out = withCapturedStdout(t, func() { emitRawOrMessage(json.RawMessage(`{"ok":true}`), false, "message") })
	if strings.TrimSpace(out) != "message" {
		t.Fatalf("message output = %q", out)
	}
}

func TestHandleExtension_PathStatusAndCall(t *testing.T) {
	var callBody map[string]any
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		switch r.URL.Path {
		case "/v1/ext/capabilities":
			w.Write([]byte(`{"name":"borz-ext","version":"1.2.3","supportedMethods":["bookmarks.search","downloads.search"],"connectedAt":123}`))
		case "/v1/ext/call":
			_ = json.NewDecoder(r.Body).Decode(&callBody)
			w.Write([]byte(`{"result":{"ok":true}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	out := withCapturedStdout(t, func() { handleExtension([]string{"path"}, false) })
	if !strings.Contains(out, filepath.Join("extension")) {
		t.Fatalf("path output = %q", out)
	}
	out = withCapturedStdout(t, func() { handleExtension([]string{"status"}, false) })
	if !strings.Contains(out, "borz-ext 1.2.3 connected") || !strings.Contains(out, "Supported extension RPC methods: 2") {
		t.Fatalf("status output = %q", out)
	}
	out = withCapturedStdout(t, func() { handleExtension([]string{"status"}, true) })
	if !strings.Contains(out, `"supportedMethods"`) {
		t.Fatalf("status JSON output = %q", out)
	}
	out = withCapturedStdout(t, func() { handleExtension([]string{"call", "bookmarks.search", `{"query":"go"}`}, false) })
	if !strings.Contains(out, `"ok": true`) {
		t.Fatalf("call output = %q", out)
	}
	if callBody["method"] != "bookmarks.search" {
		t.Fatalf("call body = %+v", callBody)
	}
	params := callBody["params"].(map[string]any)
	if params["query"] != "go" {
		t.Fatalf("call params = %+v", params)
	}
	out = withCapturedStdout(t, func() { handleExtension([]string{"call", "downloads.search"}, true) })
	if !strings.Contains(out, `"result"`) {
		t.Fatalf("call JSON output = %q", out)
	}
}

func TestHandleExtension_StatusInvalidJSONAndCallRawFallback(t *testing.T) {
	calls := map[string]int{}
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		calls[r.URL.Path]++
		if r.URL.Path == "/v1/ext/capabilities" {
			w.Write([]byte(`not-json`))
			return
		}
		w.Write([]byte(`not-json-result`))
	})

	out := withCapturedStdout(t, func() { handleExtension([]string{"status"}, false) })
	if !strings.Contains(out, "not-json") {
		t.Fatalf("status fallback output = %q", out)
	}
	out = withCapturedStdout(t, func() { handleExtension([]string{"call", "raw"}, false) })
	if !strings.Contains(out, "not-json-result") {
		t.Fatalf("call fallback output = %q", out)
	}
}

func TestPrintExtensionSetupHint(t *testing.T) {
	out := withCapturedStdout(t, func() { printExtensionSetupHint("v1.2.3", "/tmp/borz-extension") })
	for _, want := range []string{"borz extension v1.2.3 installed", "/tmp/borz-extension", "Load unpacked", "extension update"} {
		if !strings.Contains(out, want) {
			t.Fatalf("setup hint missing %q: %q", want, out)
		}
	}
}

func TestEmitTabEvents_Human(t *testing.T) {
	evs := []tabEvent{
		{Seq: 1, Name: "tabs.created", Data: json.RawMessage(`{"id":7}`)},
		{Seq: 2, Name: "tabs.updated", Data: json.RawMessage(`{"id":7,"status":"complete"}`)},
	}
	out := withCapturedStdout(t, func() { emitTabEvents(evs, false) })
	if !strings.Contains(out, "tabs.created") || !strings.Contains(out, `"id":7`) {
		t.Errorf("missing human line: %s", out)
	}
	if strings.Count(out, "\n") != 2 {
		t.Errorf("expected 2 lines, got %s", out)
	}
}

func TestEmitTabEvents_JSON(t *testing.T) {
	evs := []tabEvent{{Seq: 1, Name: "tabs.activated"}}
	out := withCapturedStdout(t, func() { emitTabEvents(evs, true) })
	line := strings.TrimSpace(out)
	if !strings.HasPrefix(line, "{") || !strings.Contains(line, `"tabs.activated"`) {
		t.Errorf("not JSONL: %q", line)
	}
}

func TestFetchTabEvents_Roundtrip(t *testing.T) {
	var seenSince string
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		if r.URL.Path != "/v1/tabs/events" {
			w.WriteHeader(404)
			return
		}
		seenSince = r.URL.Query().Get("since")
		w.Write([]byte(`{"events":[{"seq":3,"name":"tabs.created","data":{}}],"latest_seq":3,"connected":true}`))
	})

	evs, latest, err := fetchTabEvents(2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if seenSince != "2" {
		t.Errorf("since query = %q, want \"2\"", seenSince)
	}
	if len(evs) != 1 || evs[0].Seq != 3 || evs[0].Name != "tabs.created" {
		t.Errorf("events = %+v", evs)
	}
	if latest != 3 {
		t.Errorf("latest = %d", latest)
	}
}

func TestFetchTabEvents_OmitsSinceWhenZero(t *testing.T) {
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		if r.URL.Query().Has("since") {
			t.Errorf("since should not be set when 0")
		}
		w.Write([]byte(`{"events":[],"latest_seq":0,"connected":false}`))
	})
	if _, _, err := fetchTabEvents(0); err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestHandleTabEvents_NoTail(t *testing.T) {
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		w.Write([]byte(`{"events":[{"seq":1,"name":"tabs.created","data":{}}],"latest_seq":1,"connected":true}`))
	})
	out := withCapturedStdout(t, func() {
		handleTabEvents([]string{"events"}, false)
	})
	if !strings.Contains(out, "tabs.created") {
		t.Errorf("missing event in output: %q", out)
	}
}

func TestHandleTabEvents_TailStopsOnSignal(t *testing.T) {
	requests := 0
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		if r.URL.Path != "/v1/tabs/events" {
			w.WriteHeader(404)
			return
		}
		requests++
		if requests >= 2 {
			proc, err := os.FindProcess(os.Getpid())
			if err != nil {
				t.Errorf("FindProcess: %v", err)
			} else if err := proc.Signal(os.Interrupt); err != nil {
				t.Errorf("signal interrupt: %v", err)
			}
		}
		w.Write([]byte(`{"events":[{"seq":8,"name":"tabs.updated","data":{"id":1}}],"latest_seq":8,"connected":true}`))
	})

	done := make(chan string, 1)
	go func() {
		done <- withCapturedStdout(t, func() {
			handleTabEvents([]string{"events", "--tail", "--interval", "1"}, false)
		})
	}()

	select {
	case out := <-done:
		if !strings.Contains(out, "tabs.updated") {
			t.Fatalf("tail output = %q", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tail mode did not stop")
	}
}

func TestHandleCookies_All_InvalidJSONFallsBackToRaw(t *testing.T) {
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		w.Write([]byte(`not-json`))
	})
	out := withCapturedStdout(t, func() {
		handleCookies([]string{"all"}, false)
	})
	if !strings.Contains(out, "not-json") {
		t.Fatalf("expected raw fallback, got %q", out)
	}
}

func TestFetchTabEvents_InvalidJSON(t *testing.T) {
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		w.Write([]byte(`not-json`))
	})
	if _, _, err := fetchTabEvents(1); err == nil {
		t.Fatal("expected JSON decode error")
	}
}

func TestHandleCookies_All_Human(t *testing.T) {
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		if r.URL.Path != "/v1/cookies/all" {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte(`[{"name":"sid","value":"abc","domain":".example.com","path":"/"}]`))
	})
	out := withCapturedStdout(t, func() {
		handleCookies([]string{"all"}, false)
	})
	if !strings.Contains(out, "Cookies (1 total)") || !strings.Contains(out, "sid = abc") {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestHandleCookies_All_JSON(t *testing.T) {
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		w.Write([]byte(`[{"name":"sid","value":"abc","domain":".x"}]`))
	})
	out := withCapturedStdout(t, func() {
		handleCookies([]string{"all"}, true)
	})
	if !strings.Contains(out, `"name":"sid"`) {
		t.Errorf("expected raw JSON, got %q", out)
	}
}
