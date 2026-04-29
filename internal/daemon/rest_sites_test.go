package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const siteFixture = `/* @meta
{
  "name": "example",
  "description": "Example adapter",
  "domain": "example.com",
  "args": {"q": {"required": true, "description": "query"}}
}
*/
(function(args) { return args; })
`

// withSiteHome sets BORZ_HOME to a temp dir containing the given adapter
// files under sites/*.
func withSiteHome(t *testing.T, adapters map[string]string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	for rel, body := range adapters {
		p := filepath.Join(home, "sites", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRestSites_List(t *testing.T) {
	withSiteHome(t, map[string]string{"example.js": siteFixture})
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerSiteRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/sites", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["success"] != true {
		t.Fatalf("success: %+v", body)
	}
	data := body["data"].(map[string]interface{})
	sites := data["sites"].([]interface{})
	if len(sites) != 1 {
		t.Fatalf("sites: got %d want 1 (%+v)", len(sites), sites)
	}
	first := sites[0].(map[string]interface{})
	if first["name"] != "example" {
		t.Errorf("name: %+v", first)
	}
}

func TestRestSites_List_Empty(t *testing.T) {
	withSiteHome(t, nil)
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerSiteRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/sites", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status: got %d", rec.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &body)
	data := body["data"].(map[string]interface{})
	sites := data["sites"].([]interface{})
	if len(sites) != 0 {
		t.Errorf("expected empty list, got %+v", sites)
	}
}

func TestRestSites_List_MethodRejected(t *testing.T) {
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerSiteRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/sites", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 405 {
		t.Fatalf("POST /v1/sites: got %d want 405", rec.Code)
	}
}

func TestRestSites_Info_Success(t *testing.T) {
	withSiteHome(t, map[string]string{"example.js": siteFixture})
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerSiteRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/sites/info", strings.NewReader(`{"name":"example"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["success"] != true {
		t.Fatalf("success: %+v", body)
	}
	data := body["data"].(map[string]interface{})
	siteInfo := data["site"].(map[string]interface{})
	if siteInfo["name"] != "example" {
		t.Errorf("name: %+v", siteInfo)
	}
}

func TestRestSites_Info_NotFound(t *testing.T) {
	withSiteHome(t, nil)
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerSiteRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/sites/info", strings.NewReader(`{"name":"ghost"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status: got %d want 404", rec.Code)
	}
}

func TestRestSites_Info_MissingName(t *testing.T) {
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerSiteRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/sites/info", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status: got %d want 400", rec.Code)
	}
}

func TestRestSites_Info_BadJSON(t *testing.T) {
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerSiteRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/sites/info", strings.NewReader(`{bogus`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status: got %d want 400", rec.Code)
	}
}

func TestRestSites_Info_MethodRejected(t *testing.T) {
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerSiteRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/sites/info", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 405 {
		t.Fatalf("GET /v1/sites/info: got %d want 405", rec.Code)
	}
}

func TestRestSites_Run_NotFound(t *testing.T) {
	withSiteHome(t, nil)
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerSiteRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/sites/run", strings.NewReader(`{"name":"ghost"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status: got %d want 404 body=%s", rec.Code, rec.Body.String())
	}
}

func TestRestSites_Run_MissingName(t *testing.T) {
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerSiteRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/sites/run", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status: got %d want 400", rec.Code)
	}
}

func TestRestSites_Run_BadJSON(t *testing.T) {
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerSiteRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/sites/run", strings.NewReader(`{not json`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status: got %d want 400", rec.Code)
	}
}

func TestRestSites_Run_MethodRejected(t *testing.T) {
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerSiteRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/sites/run", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 405 {
		t.Fatalf("GET /v1/sites/run: got %d want 405", rec.Code)
	}
}

// Run-success path requires a CDP connection — covered by the integration
// layer. Here we exercise validation and resolution only.

func TestSiteRunBody_TabID(t *testing.T) {
	if got := (siteRunBody{}).tabID(); got != nil {
		t.Errorf("empty: %v", got)
	}
	if got := (siteRunBody{TabID: "x"}).tabID(); got != "x" {
		t.Errorf("TabID: %v", got)
	}
	if got := (siteRunBody{Tab: "y"}).tabID(); got != "y" {
		t.Errorf("Tab alias: %v", got)
	}
	if got := (siteRunBody{TabID: "x", Tab: "y"}).tabID(); got != "y" {
		t.Errorf("precedence: %v", got)
	}
}

func TestRegisterSiteRoutes_NoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("registerSiteRoutes panicked: %v", r)
		}
	}()
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerSiteRoutes(mux)
}
