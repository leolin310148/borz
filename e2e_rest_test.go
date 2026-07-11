package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/client"
	e2everify "github.com/leolin310148/borz/internal/e2e_verify_site"
	"github.com/leolin310148/borz/internal/protocol"
)

func TestE2EClientModeAgainstServer(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	token := "e2e-remote-token"
	env, serverURL := startE2EServer(t, home, token)
	runE2ECLI(t, env, "client", "setup", serverURL, "--token", token)

	statusOut := runE2ECLI(t, env, "--remote", "status")
	requireContains(t, statusOut, `"cdpConnected": true`, "remote status")

	openResp := runE2EJSON(t, env, "--remote", "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	if openResp.Data == nil || openResp.Data.Tab == "" {
		t.Fatalf("remote open response did not include tab: %+v", openResp.Data)
	}
	homeTab := openResp.Data.Tab
	pageTwo := runE2EJSON(t, env, "--remote", "open", site.URL()+"/page2", "--new", "--wait-for", "#page-two-ready", "--timeout", "10000", "--json")
	if pageTwo.Data == nil || pageTwo.Data.Tab == "" {
		t.Fatalf("remote second open response did not include tab: %+v", pageTwo.Data)
	}
	pageTwoTab := pageTwo.Data.Tab
	for _, tab := range []string{homeTab, pageTwoTab} {
		tab := tab
		t.Cleanup(func() {
			runE2ERESTJSON(t, serverURL, token, "/v1/close", map[string]interface{}{"tab": tab})
		})
	}

	snapshot := runE2EJSON(t, env, "--remote", "snapshot", "-i", "--tab", homeTab, "--json")
	clickRef := refByName(t, snapshot.Data.SnapshotData, "Click counter")
	inputRef := refByName(t, snapshot.Data.SnapshotData, "E2E text input")
	runE2EJSON(t, env, "--remote", "click", clickRef, "--tab", homeTab, "--json")
	runE2EJSON(t, env, "--remote", "fill", inputRef, "remote form value", "--tab", homeTab, "--wait-for", "#input-state", "--timeout", "5000", "--json")
	value := runE2EJSON(t, env, "--remote", "eval", `document.querySelector("#text-input").value`, "--tab", homeTab, "--json")
	if value.Data.Result != "remote form value" {
		t.Fatalf("remote explicitly targeted form value = %#v", value.Data.Result)
	}
	requireEvalStringWithPrefix(t, env, []string{"--remote"}, "document.title", "E2E Verify Page Two")

	runE2EJSON(t, env, "--remote", "console", "--clear", "--tab", homeTab, "--json")
	runE2EJSON(t, env, "--remote", "errors", "--clear", "--tab", homeTab, "--json")
	runE2EJSON(t, env, "--remote", "network", "clear", "--tab", homeTab, "--json")
	runE2EJSON(t, env, "--remote", "eval", `console.log("e2e-remote-console"); setTimeout(() => { throw new Error("e2e remote error"); }, 0); await fetch("/api/ping?from=remote-client"); true`, "--tab", homeTab, "--json")
	runE2EJSON(t, env, "--remote", "wait", "200", "--tab", homeTab, "--json")
	consoleResp := runE2EJSON(t, env, "--remote", "console", "--filter", "e2e-remote-console", "--tab", homeTab, "--json")
	if len(consoleResp.Data.ConsoleMessages) == 0 {
		t.Fatalf("remote console diagnostics missing targeted message: %+v", consoleResp.Data)
	}
	errorsResp := runE2EJSON(t, env, "--remote", "errors", "--filter", "e2e remote error", "--tab", homeTab, "--json")
	if len(errorsResp.Data.JSErrors) == 0 {
		t.Fatalf("remote error diagnostics missing targeted error: %+v", errorsResp.Data)
	}
	networkResp := runE2EJSON(t, env, "--remote", "network", "requests", "--filter", "from=remote-client", "--tab", homeTab, "--json")
	if len(networkResp.Data.NetworkRequests) == 0 {
		t.Fatalf("remote network diagnostics missing targeted request: %+v", networkResp.Data)
	}

	tabs := runE2EJSON(t, env, "--remote", "tab", "list", "--json")
	if len(tabs.Data.Tabs) < 2 {
		t.Fatalf("remote tab list returned fewer than two tabs: %+v", tabs.Data)
	}

	runE2ECLI(t, env, "client", "setup", serverURL, "--no-check")
	_, unauthorized := runE2ECLIError(t, env, "--remote", "status")
	requireContains(t, unauthorized, "borz HTTP 401", "remote missing-token failure")
	runE2ECLI(t, env, "client", "setup", serverURL, "--token", token)
}

func TestE2ERESTAgainstServer(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	token := "e2e-rest-token"
	_, serverURL := startE2EServer(t, home, token)

	healthResp, err := http.Get(serverURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer healthResp.Body.Close()
	var health struct {
		OK           bool `json:"ok"`
		CDPConnected bool `json:"cdpConnected"`
	}
	if err := json.NewDecoder(healthResp.Body).Decode(&health); err != nil {
		t.Fatalf("decode /healthz: %v", err)
	}
	if healthResp.StatusCode != http.StatusOK || !health.OK || !health.CDPConnected {
		t.Fatalf("GET /healthz = status %d, body %+v", healthResp.StatusCode, health)
	}

	openAPIResp, err := http.Get(serverURL + "/openapi.yaml")
	if err != nil {
		t.Fatalf("GET /openapi.yaml: %v", err)
	}
	defer openAPIResp.Body.Close()
	var openAPIBody bytes.Buffer
	if _, err := openAPIBody.ReadFrom(openAPIResp.Body); err != nil {
		t.Fatalf("read /openapi.yaml: %v", err)
	}
	if openAPIResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /openapi.yaml status = %d, want 200", openAPIResp.StatusCode)
	}
	requireContains(t, openAPIBody.String(), "openapi: 3.1.0", "OpenAPI document")
	requireContains(t, openAPIBody.String(), "/v1/open:", "OpenAPI document")

	unauthorizedReq, err := http.NewRequest(http.MethodPost, serverURL+"/v1/snapshot", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("build unauthorized REST request: %v", err)
	}
	unauthorizedResp, err := http.DefaultClient.Do(unauthorizedReq)
	if err != nil {
		t.Fatalf("POST /v1/snapshot without bearer token: %v", err)
	}
	defer unauthorizedResp.Body.Close()
	if unauthorizedResp.StatusCode != http.StatusUnauthorized || unauthorizedResp.Header.Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("unauthorized REST response = status %d, WWW-Authenticate %q", unauthorizedResp.StatusCode, unauthorizedResp.Header.Get("WWW-Authenticate"))
	}

	first := runE2ERESTJSON(t, serverURL, token, "/v1/open", map[string]interface{}{
		"url": site.URL() + "/", "new": true, "waitFor": "#ready", "timeoutMs": 10000,
	})
	firstTab := first.Data.Tab
	if firstTab == "" {
		t.Fatalf("REST open response did not include tab: %+v", first.Data)
	}
	second := runE2ERESTJSON(t, serverURL, token, "/v1/open", map[string]interface{}{
		"url": site.URL() + "/page2", "new": true, "waitFor": "#page-two-ready", "timeoutMs": 10000,
	})
	secondTab := second.Data.Tab
	if secondTab == "" || secondTab == firstTab {
		t.Fatalf("second REST open tab = %q, want non-empty tab distinct from %q", secondTab, firstTab)
	}
	t.Cleanup(func() {
		runE2ERESTJSON(t, serverURL, token, "/v1/close", map[string]interface{}{"tab": firstTab})
		runE2ERESTJSON(t, serverURL, token, "/v1/close", map[string]interface{}{"tab": secondTab})
	})

	snapshot := runE2ERESTJSON(t, serverURL, token, "/v1/snapshot", map[string]interface{}{
		"interactive": true, "tab": firstTab,
	})
	if snapshot.Data.Tab != firstTab || snapshot.Data.SnapshotData == nil {
		t.Fatalf("targeted REST snapshot = %+v, want tab %q with snapshot data", snapshot.Data, firstTab)
	}
	clickRef := refByName(t, snapshot.Data.SnapshotData, "Click counter")
	clicked := runE2ERESTJSON(t, serverURL, token, "/v1/click", map[string]interface{}{
		"ref": clickRef, "tab": firstTab,
	})
	if clicked.Data.Tab != firstTab {
		t.Fatalf("targeted REST click tab = %q, want %q", clicked.Data.Tab, firstTab)
	}
	clickResult := runE2ERESTJSON(t, serverURL, token, "/v1/eval", map[string]interface{}{
		"script": `document.querySelector("#clicked-result").textContent`, "tab": firstTab,
	})
	if clickResult.Data.Tab != firstTab || clickResult.Data.Result != "clicked 1" {
		t.Fatalf("targeted REST eval = tab %q result %#v, want tab %q result %q", clickResult.Data.Tab, clickResult.Data.Result, firstTab, "clicked 1")
	}
	secondTitle := runE2ERESTJSON(t, serverURL, token, "/v1/eval", map[string]interface{}{
		"script": "document.title", "tab": secondTab,
	})
	if secondTitle.Data.Tab != secondTab || secondTitle.Data.Result != "E2E Verify Page Two" {
		t.Fatalf("second targeted REST eval = tab %q result %#v, want tab %q title %q", secondTitle.Data.Tab, secondTitle.Data.Result, secondTab, "E2E Verify Page Two")
	}
}

func TestE2ERESTWaitAndDelaySemantics(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	token := "e2e-rest-delay-token"
	_, serverURL := startE2EServer(t, home, token)
	opened := runE2ERESTJSON(t, serverURL, token, "/v1/open", map[string]interface{}{
		"url": site.URL() + "/async-action", "new": true,
		"waitFor": "#async-action-ready", "timeoutMs": 10000,
	})
	tab := opened.Data.Tab
	if tab == "" {
		t.Fatalf("REST delay open response did not include tab: %+v", opened.Data)
	}
	t.Cleanup(func() {
		runE2ERESTJSON(t, serverURL, token, "/v1/close", map[string]interface{}{"tab": tab})
	})

	snapshot := runE2ERESTJSON(t, serverURL, token, "/v1/snapshot", map[string]interface{}{
		"interactive": true, "tab": tab,
	})
	actionRef := refByName(t, snapshot.Data.SnapshotData, "Start async action")

	const delayMs = 700
	started := time.Now()
	runE2ERESTJSON(t, serverURL, token, "/v1/click", map[string]interface{}{
		"ref": actionRef, "tab": tab,
		"waitFor": "#async-action-result", "timeoutMs": 5000,
		"preDelayMs": delayMs, "postDelayMs": delayMs,
	})
	if elapsed := time.Since(started); elapsed < 1800*time.Millisecond {
		t.Fatalf("REST click with pre/wait/post delays returned after %s, want at least 1.8s", elapsed)
	}
	result := runE2ERESTJSON(t, serverURL, token, "/v1/eval", map[string]interface{}{
		"script": `document.querySelector("#async-action-result").textContent`, "tab": tab,
	})
	if result.Data.Result != "Async action 1 complete" {
		t.Fatalf("REST delayed action result = %#v", result.Data.Result)
	}

	const failedPostDelayMs = 5000
	started = time.Now()
	status, failed := runE2ERESTJSONResponse(t, serverURL, token, "/v1/click", map[string]interface{}{
		"ref": actionRef, "tab": tab,
		"waitFor": "#never-rendered-after-rest-action", "timeoutMs": 600,
		"postDelayMs": failedPostDelayMs,
	})
	failedElapsed := time.Since(started)
	if status != http.StatusBadRequest || failed.Success || failed.ID == "" {
		t.Fatalf("REST timed-out action = status %d, response %+v", status, failed)
	}
	requireContains(t, failed.Error, `wait-for selector "#never-rendered-after-rest-action"`, "REST action timeout error")
	requireContains(t, failed.Error, "timeout after 600ms", "REST action timeout error")
	if failedElapsed >= 3500*time.Millisecond {
		t.Fatalf("failed REST action took %s with %dms post-delay; post-delay should be skipped", failedElapsed, failedPostDelayMs)
	}
	cleared := runE2ERESTJSON(t, serverURL, token, "/v1/eval", map[string]interface{}{
		"script": `document.querySelector("#async-action-result") === null`, "tab": tab,
	})
	if cleared.Data.Result != true {
		t.Fatalf("REST timed-out action did not run before its wait failed: result %#v", cleared.Data.Result)
	}

	for field, value := range map[string]int{"timeoutMs": -1, "preDelayMs": -1, "postDelayMs": -1} {
		status, invalid := runE2ERESTJSONResponse(t, serverURL, token, "/v1/click", map[string]interface{}{
			"ref": actionRef, "tab": tab, field: value,
		})
		if status != http.StatusBadRequest || invalid.Error != field+" must be a non-negative integer" {
			t.Errorf("REST %s lower bound = status %d, response %+v", field, status, invalid)
		}
	}
}

func TestE2ERESTMalformedRequests(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	token := "e2e-rest-malformed-token"
	_, serverURL := startE2EServer(t, home, token)

	request := func(method, path, bearer, contentType, body string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, serverURL+path, strings.NewReader(body))
		if err != nil {
			t.Fatalf("build %s %s: %v", method, path, err)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read %s %s: %v", method, path, err)
		}
		return resp, raw
	}

	assertError := func(name, method, path, bearer, contentType, body string, wantStatus int, wantError string) {
		t.Helper()
		resp, raw := request(method, path, bearer, contentType, body)
		if resp.StatusCode != wantStatus {
			t.Fatalf("%s status = %d, want %d; body=%s", name, resp.StatusCode, wantStatus, raw)
		}
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Fatalf("%s Content-Type = %q, want application/json", name, got)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
			t.Fatalf("%s CORS origin = %q, want *", name, got)
		}
		var envelope protocol.Response
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("decode %s envelope: %v\n%s", name, err, raw)
		}
		if envelope.ID == "" || envelope.Success || envelope.Data != nil || !strings.Contains(envelope.Error, wantError) {
			t.Fatalf("%s envelope = %+v, want id, success=false, error containing %q", name, envelope, wantError)
		}
	}

	assertError("invalid JSON", http.MethodPost, "/v1/snapshot", token, "text/plain", `{not-json`, http.StatusBadRequest, "invalid JSON")
	assertError("missing required field with ignored unknown field", http.MethodPost, "/v1/upload", token, "application/json", `{"unknownField":true}`, http.StatusBadRequest, "files (or file) is required")
	assertError("wrong method", http.MethodGet, "/v1/snapshot", token, "", "", http.StatusMethodNotAllowed, "Method not allowed")
	assertError("bad bearer token", http.MethodPost, "/v1/snapshot", "wrong-token", "application/json", `{}`, http.StatusUnauthorized, "Unauthorized")
	assertError("unknown route", http.MethodPost, "/v1/does-not-exist", token, "application/json", `{}`, http.StatusNotFound, "Not found")

	wrongMethod, _ := request(http.MethodGet, "/v1/snapshot", token, "", "")
	if got := wrongMethod.Header.Get("Allow"); got != http.MethodPost {
		t.Fatalf("wrong-method Allow = %q, want POST", got)
	}
	badToken, _ := request(http.MethodPost, "/v1/snapshot", "wrong-token", "application/json", `{}`)
	if got := badToken.Header.Get("WWW-Authenticate"); got != "Bearer" {
		t.Fatalf("bad-token WWW-Authenticate = %q, want Bearer", got)
	}

	preflight, raw := request(http.MethodOptions, "/v1/snapshot", "", "", "")
	if preflight.StatusCode != http.StatusNoContent || len(raw) != 0 {
		t.Fatalf("CORS preflight = status %d body %q, want 204 with empty body", preflight.StatusCode, raw)
	}
	if preflight.Header.Get("Access-Control-Allow-Origin") != "*" ||
		!strings.Contains(preflight.Header.Get("Access-Control-Allow-Methods"), http.MethodPost) ||
		!strings.Contains(preflight.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("CORS preflight headers = %v", preflight.Header)
	}
}

func TestE2ERESTOpenAPIConformance(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	token := "e2e-openapi-conformance-token"
	_, serverURL := startE2EServer(t, home, token)

	request := func(method, path, bearer string, body io.Reader) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, serverURL+path, body)
		if err != nil {
			t.Fatalf("build %s %s: %v", method, path, err)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read %s %s: %v", method, path, err)
		}
		return resp, raw
	}

	specResp, specBody := request(http.MethodGet, "/openapi.yaml", "", nil)
	if specResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /openapi.yaml status = %d, want 200", specResp.StatusCode)
	}
	operations := parseE2EOpenAPIOperations(t, specBody)

	paths := make([]string, 0, len(operations))
	for path := range operations {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		methods := operations[path]
		t.Run("route_"+strings.Trim(strings.ReplaceAll(path, "/", "_"), "_"), func(t *testing.T) {
			wrongMethod, _ := request(http.MethodDelete, path, token, nil)
			if wrongMethod.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("DELETE %s status = %d, want 405", path, wrongMethod.StatusCode)
			}
			gotMethods := strings.Split(wrongMethod.Header.Get("Allow"), ", ")
			wantMethods := make([]string, 0, len(methods))
			for method := range methods {
				wantMethods = append(wantMethods, method)
			}
			slices.Sort(wantMethods)
			if gotMethods[0] != "" {
				slices.Sort(gotMethods)
			}
			if gotMethods[0] != "" && !slices.Equal(gotMethods, wantMethods) {
				t.Fatalf("%s Allow = %v, OpenAPI documents %v", path, gotMethods, wantMethods)
			}

			method := wantMethods[0]
			var body io.Reader
			if method != http.MethodGet {
				body = strings.NewReader(`{}`)
			}
			unauthorized, _ := request(method, path, "", body)
			if methods[method] {
				if unauthorized.StatusCode != http.StatusUnauthorized {
					t.Fatalf("unauthenticated %s %s status = %d, want 401", method, path, unauthorized.StatusCode)
				}
			} else if unauthorized.StatusCode == http.StatusUnauthorized {
				t.Fatalf("unauthenticated %s %s returned 401, but OpenAPI declares security: []", method, path)
			}
		})
	}

	statusResp, statusBody := request(http.MethodGet, "/status", token, nil)
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated GET /status = %d: %s", statusResp.StatusCode, statusBody)
	}
	tabsResp, tabsBody := request(http.MethodGet, "/v1/tabs", token, nil)
	if tabsResp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated GET /v1/tabs = %d: %s", tabsResp.StatusCode, tabsBody)
	}

	opened := runE2ERESTJSON(t, serverURL, token, "/v1/open", map[string]interface{}{
		"url": site.URL() + "/", "new": true, "waitFor": "#ready", "timeoutMs": 10000,
	})
	tab := opened.Data.Tab
	if tab == "" {
		t.Fatalf("OpenAPI smoke open response did not include tab: %+v", opened.Data)
	}
	t.Cleanup(func() {
		runE2ERESTJSON(t, serverURL, token, "/v1/close", map[string]interface{}{"tab": tab})
	})
	snapshot := runE2ERESTJSON(t, serverURL, token, "/v1/snapshot", map[string]interface{}{
		"interactive": true, "tab": tab,
	})
	if snapshot.Data.Tab != tab || snapshot.Data.SnapshotData == nil {
		t.Fatalf("OpenAPI smoke snapshot = %+v, want tab %q with snapshot data", snapshot.Data, tab)
	}
	title := runE2ERESTJSON(t, serverURL, token, "/v1/get", map[string]interface{}{
		"attribute": "title", "tab": tab,
	})
	if title.Data.Tab != tab || title.Data.Value != "E2E Verify Home" {
		t.Fatalf("OpenAPI smoke get = tab %q value %q, want tab %q title %q", title.Data.Tab, title.Data.Value, tab, "E2E Verify Home")
	}
}

// parseE2EOpenAPIOperations extracts the path, method, and effective security
// from borz's deliberately conventional OpenAPI YAML formatting. Keeping this
// parser test-local avoids adding a production YAML dependency for one E2E.
func parseE2EOpenAPIOperations(t *testing.T, spec []byte) map[string]map[string]bool {
	t.Helper()
	operations := make(map[string]map[string]bool)
	globalBearer := false
	inGlobalSecurity := false
	inPaths := false
	currentPath := ""
	currentMethod := ""

	for _, line := range strings.Split(string(spec), "\n") {
		switch {
		case line == "security:":
			inGlobalSecurity = true
		case inGlobalSecurity && strings.HasPrefix(line, "  - bearerAuth:"):
			globalBearer = true
		case inGlobalSecurity && strings.TrimSpace(line) == "- {}":
			t.Fatal("OpenAPI global security permits anonymous access, but token servers require bearer auth")
		case inGlobalSecurity && line != "" && !strings.HasPrefix(line, " "):
			inGlobalSecurity = false
		}

		if line == "paths:" {
			inPaths = true
			continue
		}
		if !inPaths {
			continue
		}
		if line != "" && !strings.HasPrefix(line, " ") {
			break
		}
		if strings.HasPrefix(line, "  /") && strings.HasSuffix(line, ":") {
			currentPath = strings.TrimSuffix(strings.TrimSpace(line), ":")
			operations[currentPath] = make(map[string]bool)
			currentMethod = ""
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "      ") && strings.HasSuffix(trimmed, ":") {
			method := strings.ToUpper(strings.TrimSuffix(trimmed, ":"))
			if slices.Contains([]string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}, method) {
				currentMethod = method
				operations[currentPath][currentMethod] = globalBearer
			}
			continue
		}
		if currentPath != "" && currentMethod != "" && line == "      security: []" {
			operations[currentPath][currentMethod] = false
		}
	}

	if !globalBearer {
		t.Fatal("OpenAPI document is missing global bearerAuth security")
	}
	if len(operations) == 0 {
		t.Fatal("OpenAPI document did not contain any operations")
	}
	for path, methods := range operations {
		if len(methods) == 0 {
			t.Fatalf("OpenAPI path %s has no supported HTTP operations", path)
		}
	}
	return operations
}

func TestE2ESiteAdapterTrustAgainstVerifySite(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	verifySite, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = verifySite.Close(ctx)
	})

	adapterDir := filepath.Join(home, "bb-sites", "e2e")
	if err := os.MkdirAll(adapterDir, 0o755); err != nil {
		t.Fatalf("create community adapter directory: %v", err)
	}
	adapterPath := filepath.Join(adapterDir, "verify.js")
	baseURL := verifySite.URL()
	writeAdapter := func(version string) {
		t.Helper()
		adapter := fmt.Sprintf(`/* @meta
{
  "name": "e2e/verify",
  "description": "Local E2E fixture adapter",
  "domain": "127.0.0.1",
  "startUrl": %q,
  "args": {},
  "readOnly": false
}
*/
async function() {
  const version = %q;
  document.body.dataset.e2eAdapterVersion = version;
  return {version, title: document.title, origin: location.origin};
}`, baseURL+"/", version)
		if err := os.WriteFile(adapterPath, []byte(adapter), 0o644); err != nil {
			t.Fatalf("write community adapter %q: %v", version, err)
		}
	}
	writeAdapter("trusted-v1")

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", baseURL+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("fixture open response did not include a tab: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})
	type adapterInfo struct {
		SHA256  string `json:"sha256"`
		Source  string `json:"source"`
		Trusted bool   `json:"trusted"`
	}
	readInfo := func() adapterInfo {
		t.Helper()
		out := runE2ECLI(t, env, "site", "info", "e2e/verify", "--json")
		var info adapterInfo
		if err := json.Unmarshal([]byte(out), &info); err != nil {
			t.Fatalf("decode site info: %v\n%s", err, out)
		}
		return info
	}

	initial := readInfo()
	if initial.SHA256 == "" || initial.Source != "community" || initial.Trusted {
		t.Fatalf("initial adapter info = %+v, want untrusted community adapter with SHA256", initial)
	}
	_, untrustedOut := runE2ECLIError(t, env, "site", "run", "e2e/verify", "--tab", tab, "--json")
	requireContains(t, untrustedOut, "not trusted yet", "untrusted adapter error")
	requireContains(t, untrustedOut, initial.SHA256, "untrusted adapter error")

	trustOut := runE2ECLI(t, env, "site", "trust", "e2e/verify")
	requireContains(t, trustOut, initial.SHA256, "site trust output")
	trusted := readInfo()
	if trusted.SHA256 != initial.SHA256 || !trusted.Trusted {
		t.Fatalf("trusted adapter info = %+v, want trusted SHA256 %q", trusted, initial.SHA256)
	}

	firstRun := runE2EJSON(t, env, "site", "run", "e2e/verify", "--tab", tab, "--json")
	firstResult, ok := firstRun.Data.Result.(map[string]interface{})
	if !ok || fmt.Sprint(firstResult["version"]) != "trusted-v1" || fmt.Sprint(firstResult["title"]) != "E2E Verify Home" || fmt.Sprint(firstResult["origin"]) != baseURL {
		t.Fatalf("trusted adapter result = %#v, want version trusted-v1, title E2E Verify Home, origin %q", firstRun.Data.Result, baseURL)
	}
	if firstRun.Data.Tab != tab {
		t.Fatalf("trusted adapter tab = %q, want fixture tab %q", firstRun.Data.Tab, tab)
	}

	writeAdapter("changed-version-two")
	changed := readInfo()
	if changed.SHA256 == "" || changed.SHA256 == initial.SHA256 || changed.Trusted {
		t.Fatalf("changed adapter info = %+v, want a new untrusted SHA256", changed)
	}
	_, changedOut := runE2ECLIError(t, env, "site", "run", "e2e/verify", "--tab", tab, "--json")
	requireContains(t, changedOut, "changed hash", "changed adapter error")
	requireContains(t, changedOut, initial.SHA256, "changed adapter error")
	requireContains(t, changedOut, changed.SHA256, "changed adapter error")

	forcedRun := runE2EJSON(t, env, "site", "run", "e2e/verify", "--tab", tab, "--force", "--json")
	forcedResult, ok := forcedRun.Data.Result.(map[string]interface{})
	if !ok || fmt.Sprint(forcedResult["version"]) != "changed-version-two" {
		t.Fatalf("forced changed adapter result = %#v", forcedRun.Data.Result)
	}
	if afterForce := readInfo(); afterForce.Trusted || afterForce.SHA256 != changed.SHA256 {
		t.Fatalf("one-off force unexpectedly changed trust state: %+v", afterForce)
	}
}
