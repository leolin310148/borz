package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
)

// /v1/clipboard-write requires text; missing text is a 400 before dispatch.
func TestClipboardWrite_MissingText(t *testing.T) {
	s, _ := serverWithFakeCDP(t)
	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/clipboard-write", strings.NewReader(`{"tab":"T1"}`))
	mux.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("missing text: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "text") {
		t.Errorf("expected error about text; got %s", rec.Body.String())
	}
}

// clipboard-write JSON-encodes the text into writeText() and, with paste:true,
// fires the platform paste-as-plain-text key pair into the page.
func TestClipboardWrite_WritesAndPastes(t *testing.T) {
	s, f := serverWithFakeCDP(t)

	var writeExpr string
	f.On("Runtime.evaluate", func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		if strings.Contains(p.Expression, "writeText(") {
			writeExpr = p.Expression
		}
		return map[string]interface{}{
			"result": map[string]interface{}{"type": "string", "value": "ok"},
		}, nil
	})

	var keyEvents []map[string]interface{}
	f.On("Input.dispatchKeyEvent", func(params json.RawMessage) (interface{}, error) {
		var m map[string]interface{}
		_ = json.Unmarshal(params, &m)
		keyEvents = append(keyEvents, m)
		return map[string]interface{}{}, nil
	})

	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	rec := httptest.NewRecorder()
	// Text with a quote + newline to prove JSON-encoding survives embedding.
	req := httptest.NewRequest(http.MethodPost, "/v1/clipboard-write",
		strings.NewReader(`{"text":"echo \"hi\"\n","paste":true,"tab":"T1"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	if writeExpr == "" {
		t.Fatal("Runtime.evaluate was never called with a writeText expression")
	}
	// JSON-encoded payload must be embedded verbatim.
	if !strings.Contains(writeExpr, `navigator.clipboard.writeText(`) {
		t.Errorf("write script missing writeText(); got:\n%s", writeExpr)
	}
	if !strings.Contains(writeExpr, `echo \"hi\"\n`) {
		t.Errorf("write script missing JSON-encoded text; got:\n%s", writeExpr)
	}

	// Platform modifier mask, key "V", down + up.
	if len(keyEvents) != 2 {
		t.Fatalf("expected 2 key events (down+up), got %d: %+v", len(keyEvents), keyEvents)
	}
	for _, ev := range keyEvents {
		if ev["key"] != "V" {
			t.Errorf("key = %v want V", ev["key"])
		}
		if ev["modifiers"] != float64(pasteModifierMask) {
			t.Errorf("modifiers = %v want %d", ev["modifiers"], pasteModifierMask)
		}
	}
	if keyEvents[0]["type"] != "keyDown" || keyEvents[1]["type"] != "keyUp" {
		t.Errorf("event types = %v / %v want keyDown / keyUp", keyEvents[0]["type"], keyEvents[1]["type"])
	}
	commands, ok := keyEvents[0]["commands"].([]interface{})
	if !ok || len(commands) != 1 || commands[0] != "PasteAndMatchStyle" {
		t.Errorf("keyDown commands = %#v want PasteAndMatchStyle", keyEvents[0]["commands"])
	}
	if _, ok := keyEvents[1]["commands"]; ok {
		t.Errorf("keyUp unexpectedly included commands: %+v", keyEvents[1])
	}

	// Response surfaces written + pasted flags.
	var resp protocol.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if resp.Data == nil || resp.Data.Result == nil {
		t.Fatalf("missing result: %+v", resp.Data)
	}
	result := resp.Data.Result.(map[string]interface{})
	if result["written"] != true || result["pasted"] != true {
		t.Errorf("result = %+v want written+pasted true", result)
	}
}

// clipboard-write without paste must not emit any key events.
func TestClipboardWrite_NoPaste(t *testing.T) {
	s, f := serverWithFakeCDP(t)

	keyCount := 0
	f.On("Input.dispatchKeyEvent", func(json.RawMessage) (interface{}, error) {
		keyCount++
		return map[string]interface{}{}, nil
	})

	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/clipboard-write",
		strings.NewReader(`{"text":"hello","tab":"T1"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	if keyCount != 0 {
		t.Fatalf("paste=false should not fire key events, got %d", keyCount)
	}
}

// term-text returns the xterm buffer text in data.value and the structured
// metadata in data.result.
func TestTermText_ReadsBuffer(t *testing.T) {
	s, f := serverWithFakeCDP(t)

	f.On("Runtime.evaluate", func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		// The term-text probe is the only eval referencing buffer.active.
		if strings.Contains(p.Expression, "buffer.active") {
			return map[string]interface{}{
				"result": map[string]interface{}{
					"type": "object",
					"value": map[string]interface{}{
						"found":  true,
						"source": "xterm-buffer",
						"text":   "$ ls\nfile.txt",
						"lines":  2,
						"cols":   80,
						"rows":   24,
					},
				},
			}, nil
		}
		// Readiness probe etc.
		return map[string]interface{}{
			"result": map[string]interface{}{
				"type":  "string",
				"value": `{"readyState":"complete","href":"https://ready.test/"}`,
			},
		}, nil
	})

	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/term-text", strings.NewReader(`{"tab":"T1"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp protocol.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if resp.Data == nil || resp.Data.Value != "$ ls\nfile.txt" {
		t.Fatalf("value = %q want terminal text", func() string {
			if resp.Data == nil {
				return "<nil>"
			}
			return resp.Data.Value
		}())
	}
	result := resp.Data.Result.(map[string]interface{})
	if result["source"] != "xterm-buffer" || result["found"] != true {
		t.Errorf("result = %+v want source xterm-buffer found true", result)
	}
}

// A terminal that can't be found is still a 200, with found=false and a note.
func TestTermText_NotFound(t *testing.T) {
	s, f := serverWithFakeCDP(t)

	f.On("Runtime.evaluate", func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		if strings.Contains(p.Expression, "buffer.active") {
			return map[string]interface{}{
				"result": map[string]interface{}{
					"type": "object",
					"value": map[string]interface{}{
						"found":  false,
						"source": "none",
						"text":   "",
						"note":   "No xterm.js terminal found.",
					},
				},
			}, nil
		}
		return map[string]interface{}{
			"result": map[string]interface{}{"type": "string", "value": `{"readyState":"complete","href":"https://ready.test/"}`},
		}, nil
	})

	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/term-text", strings.NewReader(`{"tab":"T1"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp protocol.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if !resp.Success {
		t.Fatalf("not-found should still succeed: %+v", resp)
	}
	result := resp.Data.Result.(map[string]interface{})
	if result["found"] != false || result["note"] == "" {
		t.Errorf("result = %+v want found false + note", result)
	}
}

// dispatchPasteShortcut builds the platform paste-as-plain-text key descriptor.
func TestDispatchPasteShortcut(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	cdp := connectCdp(t, f)

	var events []map[string]interface{}
	f.On("Input.dispatchKeyEvent", func(params json.RawMessage) (interface{}, error) {
		var m map[string]interface{}
		_ = json.Unmarshal(params, &m)
		events = append(events, m)
		return map[string]interface{}{}, nil
	})

	target, err := cdp.EnsurePageTarget("T1")
	if err != nil {
		t.Fatalf("ensure target: %v", err)
	}
	if err := dispatchPasteShortcut(cdp, target.ID); err != nil {
		t.Fatalf("dispatchPasteShortcut: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected down+up, got %d", len(events))
	}
	if events[0]["code"] != "KeyV" || events[0]["modifiers"] != float64(pasteModifierMask) {
		t.Errorf("keyDown = %+v want code KeyV modifiers %d", events[0], pasteModifierMask)
	}
	commands, ok := events[0]["commands"].([]interface{})
	if !ok || len(commands) != 1 || commands[0] != "PasteAndMatchStyle" {
		t.Errorf("keyDown commands = %#v want PasteAndMatchStyle", events[0]["commands"])
	}
}

func TestClipboardWriteExplicitEmptyClears(t *testing.T) {
	s, f := serverWithFakeCDP(t)
	var expression string
	f.On("Runtime.evaluate", func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Expression string `json:"expression"`
		}
		json.Unmarshal(params, &p)
		if strings.Contains(p.Expression, "writeText(") {
			expression = p.Expression
		}
		return map[string]interface{}{"result": map[string]interface{}{"type": "string", "value": "ok"}}, nil
	})
	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/clipboard-write", strings.NewReader(`{"text":""}`)))
	if rec.Code != 200 || !strings.Contains(expression, `writeText("")`) {
		t.Fatalf("empty clipboard: %d %s %s", rec.Code, rec.Body.String(), expression)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/clipboard-write", strings.NewReader(`{"text":null}`)))
	if rec.Code != 400 {
		t.Fatal("null text must not clear the clipboard")
	}
}
