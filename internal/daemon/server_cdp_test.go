package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/protocol"
)

// serverWithFakeCDP wires a Server to a connected fake CDP so handleCommand
// and the /v1/* handlers complete end-to-end.
func serverWithFakeCDP(t *testing.T) (*Server, *fakeCDP) {
	t.Helper()
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")

	tabs := NewTabStateManager()
	cdp := NewCdpConnection(f.Host(), f.Port(), tabs)
	if err := cdp.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { cdp.Disconnect() })

	// Give the read loop a moment to handle attach events.
	time.Sleep(20 * time.Millisecond)

	s := &Server{
		opts:      ServerOptions{Host: "127.0.0.1", Port: 0},
		cdp:       cdp,
		startTime: time.Now(),
	}
	return s, f
}

func TestHandleCommand_Success(t *testing.T) {
	s, _ := serverWithFakeCDP(t)

	body := `{"id":"cmd1","action":"back"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/command", strings.NewReader(body))
	s.handleCommand(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	var resp protocol.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if !resp.Success || resp.ID != "cmd1" {
		t.Fatalf("resp: %+v", resp)
	}
}

func TestDispatchAndWrite_Success(t *testing.T) {
	s, _ := serverWithFakeCDP(t)

	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/back", strings.NewReader(`{}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDispatchAndWrite_FailedResp(t *testing.T) {
	s, _ := serverWithFakeCDP(t)

	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	// /v1/click without ref -> dispatcher fails -> HTTP 400.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/click", strings.NewReader(`{}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestV1Tabs_GET(t *testing.T) {
	s, _ := serverWithFakeCDP(t)

	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/tabs", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestV1Tabs_POST(t *testing.T) {
	s, f := serverWithFakeCDP(t)
	f.On("Target.createTarget", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"targetId": "T-POST"}, nil
	})

	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/tabs", strings.NewReader(`{"url":"https://x"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestV1Fetch_ScriptShape pins the JS shape sent to the page so the
// diagnostic-on-error contract from gh#1 can't silently regress. The fake
// returns whatever the script evaluates as a string; we capture the
// expression and assert it short-circuits about:blank and includes
// readyState/location in the error path.
func TestV1Fetch_ScriptShape(t *testing.T) {
	s, f := serverWithFakeCDP(t)

	var capturedExpr string
	f.On("Runtime.evaluate", func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		// Capture the fetch script (skip the readiness probe, which uses a
		// short JSON.stringify expression).
		if strings.Contains(p.Expression, "fetch(") {
			capturedExpr = p.Expression
		}
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
	req := httptest.NewRequest(http.MethodPost, "/v1/fetch",
		strings.NewReader(`{"url":"https://api.example/x.json","tab":"T1"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	if capturedExpr == "" {
		t.Fatal("Runtime.evaluate was never called with a fetch expression")
	}
	for _, want := range []string{"about:blank", "readyState", "location"} {
		if !strings.Contains(capturedExpr, want) {
			t.Errorf("fetch script missing %q diagnostic; got:\n%s", want, capturedExpr)
		}
	}
}
