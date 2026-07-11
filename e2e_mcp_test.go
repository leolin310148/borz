package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/client"
	e2everify "github.com/leolin310148/borz/internal/e2e_verify_site"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpprotocol "github.com/mark3labs/mcp-go/mcp"
)

func TestE2EMCPStdioAgainstVerifySite(t *testing.T) {
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

	startE2EDaemon(t, home)
	stdio := transport.NewStdio(os.Args[0], []string{
		"BORZ_E2E_HELPER=1",
		"BORZ_HOME=" + home,
	}, "-test.run=TestE2ECLIHelper", "--", "mcp")
	mcpClient := mcpclient.NewClient(stdio)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("start MCP stdio client: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = mcpClient.Close()
		}
	})

	initRequest := mcpprotocol.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcpprotocol.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcpprotocol.Implementation{Name: "borz-e2e", Version: "test"}
	initialized, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		t.Fatalf("initialize MCP stdio server: %v", err)
	}
	if initialized.ServerInfo.Name != "borz" || initialized.ProtocolVersion == "" || initialized.Capabilities.Tools == nil {
		t.Fatalf("unexpected MCP initialize result: %+v", initialized)
	}

	baseURL := site.URL() + "/"
	navigate := callE2EMCPTool(t, ctx, mcpClient, "browser_navigate", map[string]interface{}{
		"url": baseURL, "new": true, "waitFor": "#ready", "timeout": 10000,
	})
	requireContains(t, navigate, "Navigated to "+baseURL, "MCP navigate result")
	requireContains(t, navigate, "Page: "+baseURL, "MCP navigate result")

	t.Cleanup(func() {
		if !closed {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			_, _ = callMCPTool(cleanupCtx, mcpClient, "browser_close", nil)
		}
	})
	snapshot := callE2EMCPTool(t, ctx, mcpClient, "browser_snapshot", map[string]interface{}{"interactive": true})
	clickRef := refFromMCPSnapshot(t, snapshot, "Click counter")
	clicked := callE2EMCPTool(t, ctx, mcpClient, "browser_click", map[string]interface{}{"ref": clickRef})
	requireContains(t, clicked, "Clicked element @"+clickRef, "MCP click result")

	evaluated := callE2EMCPTool(t, ctx, mcpClient, "browser_eval", map[string]interface{}{
		"script": `document.querySelector("#clicked-result").textContent`,
	})
	if evaluated != `"clicked 1"` {
		t.Fatalf("MCP eval result = %q, want JSON-shaped string %q", evaluated, `"clicked 1"`)
	}
	closedTab := callE2EMCPTool(t, ctx, mcpClient, "browser_close", nil)
	if closedTab != "Tab closed" {
		t.Fatalf("MCP close result = %q, want %q", closedTab, "Tab closed")
	}

	if err := mcpClient.Close(); err != nil {
		t.Fatalf("close MCP stdio client and wait for server exit: %v", err)
	}
	closed = true
}

func TestE2EMCPMultiTabAgainstVerifySite(t *testing.T) {
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

	startE2EDaemon(t, home)
	stdio := transport.NewStdio(os.Args[0], []string{
		"BORZ_E2E_HELPER=1",
		"BORZ_HOME=" + home,
	}, "-test.run=TestE2ECLIHelper", "--", "mcp")
	mcpClient := mcpclient.NewClient(stdio)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("start MCP stdio client: %v", err)
	}
	clientClosed := false
	t.Cleanup(func() {
		if !clientClosed {
			_ = mcpClient.Close()
		}
	})

	initRequest := mcpprotocol.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcpprotocol.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcpprotocol.Implementation{Name: "borz-e2e-multi-tab", Version: "test"}
	if _, err := mcpClient.Initialize(ctx, initRequest); err != nil {
		t.Fatalf("initialize MCP stdio server: %v", err)
	}

	firstURL := site.URL() + "/?mcp-tab=first"
	secondURL := site.URL() + "/page2?mcp-tab=second"
	firstNew := callE2EMCPTool(t, ctx, mcpClient, "browser_tab_new", map[string]interface{}{"url": firstURL})
	requireContains(t, firstNew, "Opened new tab at "+firstURL, "first MCP tab-new result")
	secondNew := callE2EMCPTool(t, ctx, mcpClient, "browser_tab_new", map[string]interface{}{"url": secondURL})
	requireContains(t, secondNew, "Opened new tab at "+secondURL, "second MCP tab-new result")

	tabIDForURL := func(tabList, url string) string {
		t.Helper()
		for _, line := range strings.Split(tabList, "\n") {
			if !strings.Contains(line, url) {
				continue
			}
			_, after, ok := strings.Cut(line, "(tab: ")
			if !ok {
				break
			}
			if id := strings.TrimSuffix(after, ")"); id != "" {
				return id
			}
		}
		t.Fatalf("MCP tab list did not contain a tab id for %s:\n%s", url, tabList)
		return ""
	}

	tabs := callE2EMCPTool(t, ctx, mcpClient, "browser_tab_list", nil)
	firstTab := tabIDForURL(tabs, firstURL)
	secondTab := tabIDForURL(tabs, secondURL)
	if firstTab == secondTab {
		t.Fatalf("MCP tab-new returned non-distinct tabs: first=%q second=%q", firstTab, secondTab)
	}
	firstOpen, secondOpen := true, true
	t.Cleanup(func() {
		if clientClosed {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if firstOpen {
			_, _ = callMCPTool(cleanupCtx, mcpClient, "browser_tab_close", map[string]interface{}{"tab": firstTab})
		}
		if secondOpen {
			_, _ = callMCPTool(cleanupCtx, mcpClient, "browser_tab_close", map[string]interface{}{"tab": secondTab})
		}
	})

	selected := callE2EMCPTool(t, ctx, mcpClient, "browser_tab_select", map[string]interface{}{"tab": firstTab})
	requireContains(t, selected, "Switched tab", "MCP tab-select result")
	tabs = callE2EMCPTool(t, ctx, mcpClient, "browser_tab_list", nil)
	requireContains(t, tabs, "* ", "selected MCP tab-list result")
	for _, line := range strings.Split(tabs, "\n") {
		if strings.HasPrefix(line, "* ") && !strings.Contains(line, "(tab: "+firstTab+")") {
			t.Fatalf("MCP selected unexpected tab, want %q:\n%s", firstTab, tabs)
		}
	}

	secondTitle := callE2EMCPTool(t, ctx, mcpClient, "browser_get", map[string]interface{}{
		"attribute": "title", "tab": secondTab,
	})
	if secondTitle != "E2E Verify Page Two" {
		t.Fatalf("explicit MCP tab targeting returned title %q, want %q", secondTitle, "E2E Verify Page Two")
	}
	tabs = callE2EMCPTool(t, ctx, mcpClient, "browser_tab_list", nil)
	for _, line := range strings.Split(tabs, "\n") {
		if strings.HasPrefix(line, "* ") && !strings.Contains(line, "(tab: "+firstTab+")") {
			t.Fatalf("explicit MCP targeting changed active tab, want %q:\n%s", firstTab, tabs)
		}
	}

	closedFirst := callE2EMCPTool(t, ctx, mcpClient, "browser_tab_close", map[string]interface{}{"tab": firstTab})
	if closedFirst != "Tab closed" {
		t.Fatalf("MCP tab-close result = %q, want %q", closedFirst, "Tab closed")
	}
	firstOpen = false
	tabs = callE2EMCPTool(t, ctx, mcpClient, "browser_tab_list", nil)
	if strings.Contains(tabs, "(tab: "+firstTab+")") || !strings.Contains(tabs, "(tab: "+secondTab+")") {
		t.Fatalf("MCP tab list after close did not preserve only the remaining test tab:\n%s", tabs)
	}
	remainingTitle := callE2EMCPTool(t, ctx, mcpClient, "browser_get", map[string]interface{}{
		"attribute": "title", "tab": secondTab,
	})
	if remainingTitle != "E2E Verify Page Two" {
		t.Fatalf("remaining MCP tab unusable after closing peer: title=%q", remainingTitle)
	}

	callE2EMCPTool(t, ctx, mcpClient, "browser_tab_close", map[string]interface{}{"tab": secondTab})
	secondOpen = false
	if err := mcpClient.Close(); err != nil {
		t.Fatalf("close MCP stdio client and wait for server exit: %v", err)
	}
	clientClosed = true
}

func TestE2EMCPErrorPathsAgainstVerifySite(t *testing.T) {
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

	startE2EDaemon(t, home)
	stdio := transport.NewStdio(os.Args[0], []string{
		"BORZ_E2E_HELPER=1",
		"BORZ_HOME=" + home,
	}, "-test.run=TestE2ECLIHelper", "--", "mcp")
	mcpClient := mcpclient.NewClient(stdio)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("start MCP stdio client: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = mcpClient.Close()
		}
	})

	initRequest := mcpprotocol.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcpprotocol.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcpprotocol.Implementation{Name: "borz-e2e-errors", Version: "test"}
	if _, err := mcpClient.Initialize(ctx, initRequest); err != nil {
		t.Fatalf("initialize MCP stdio server: %v", err)
	}

	baseURL := site.URL() + "/"
	callE2EMCPTool(t, ctx, mcpClient, "browser_navigate", map[string]interface{}{
		"url": baseURL, "new": true, "waitFor": "#ready", "timeout": 10000,
	})
	t.Cleanup(func() {
		if !closed {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			_, _ = callMCPTool(cleanupCtx, mcpClient, "browser_close", nil)
		}
	})

	invalidParams := callE2EMCPRawTool(t, ctx, stdio, "invalid-params", "browser_navigate", map[string]interface{}{"url": 42})
	requireE2EMCPErrorText(t, invalidParams, "url is required", "invalid MCP parameters")

	unknownRef := callE2EMCPRawTool(t, ctx, stdio, "unknown-ref", "browser_click", map[string]interface{}{"ref": "999999"})
	requireE2EMCPErrorText(t, unknownRef, "ref", "unknown MCP ref")

	unknownTab := callE2EMCPRawTool(t, ctx, stdio, "unknown-tab", "browser_get", map[string]interface{}{
		"attribute": "url", "tab": "missing-e2e-tab",
	})
	requireE2EMCPErrorText(t, unknownTab, "tab not found", "unknown MCP tab")

	jsException := callE2EMCPRawTool(t, ctx, stdio, "js-exception", "browser_eval", map[string]interface{}{
		"script": `throw new Error("mcp-e2e-boom")`,
	})
	requireE2EMCPErrorText(t, jsException, "mcp-e2e-boom", "MCP JavaScript exception")

	snapshot := callE2EMCPTool(t, ctx, mcpClient, "browser_snapshot", map[string]interface{}{"interactive": true})
	clickRef := refFromMCPSnapshot(t, snapshot, "Click counter")
	waitTimeout := callE2EMCPRawTool(t, ctx, stdio, "wait-timeout", "browser_click", map[string]interface{}{
		"ref": clickRef, "waitFor": "#never-rendered-for-mcp", "timeout": 600,
	})
	requireE2EMCPErrorText(t, waitTimeout, `wait-for selector "#never-rendered-for-mcp"`, "MCP wait timeout")

	unsupported := sendE2EMCPRequest(t, ctx, stdio, "unsupported-call", "unsupported/e2e", map[string]interface{}{})
	if unsupported.Error == nil {
		t.Fatalf("unsupported MCP call returned no JSON-RPC error: %+v", unsupported)
	}
	if unsupported.Error.Code != mcpprotocol.METHOD_NOT_FOUND {
		t.Fatalf("unsupported MCP call error code = %d, want %d: %+v", unsupported.Error.Code, mcpprotocol.METHOD_NOT_FOUND, unsupported.Error)
	}
	requireContains(t, strings.ToLower(unsupported.Error.Message), "not found", "unsupported MCP call error")

	valid := callE2EMCPTool(t, ctx, mcpClient, "browser_eval", map[string]interface{}{
		"script": `document.querySelector("#clicked-result").textContent`,
	})
	if valid != `"clicked 1"` {
		t.Fatalf("valid MCP call after errors = %q, want %q", valid, `"clicked 1"`)
	}

	if err := mcpClient.Close(); err != nil {
		t.Fatalf("close MCP stdio client and wait for server exit: %v", err)
	}
	closed = true
}

func TestE2EEvalSurfaceParity(t *testing.T) {
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

	const token = "e2e-eval-parity-token"
	env, serverURL := startE2EServer(t, home, token)
	opened := runE2EJSON(t, env, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := opened.Data.Tab
	if tab == "" {
		t.Fatalf("eval parity open response did not include tab: %+v", opened.Data)
	}
	t.Cleanup(func() {
		runE2ERESTJSON(t, serverURL, token, "/v1/close", map[string]interface{}{"tab": tab})
	})

	stdio := transport.NewStdio(os.Args[0], []string{
		"BORZ_E2E_HELPER=1",
		"BORZ_HOME=" + home,
	}, "-test.run=TestE2ECLIHelper", "--", "mcp")
	mcpClient := mcpclient.NewClient(stdio)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("start MCP stdio client: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = mcpClient.Close()
		}
	})

	initRequest := mcpprotocol.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcpprotocol.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcpprotocol.Implementation{Name: "borz-e2e-eval-parity", Version: "test"}
	if _, err := mcpClient.Initialize(ctx, initRequest); err != nil {
		t.Fatalf("initialize MCP stdio server: %v", err)
	}

	const topLevelAwait = `await Promise.resolve({surface: "parity", unicode: "等待 🚀", nested: {value: 43}})`
	cliResult := runE2EJSON(t, env, "eval", topLevelAwait, "--tab", tab, "--json").Data.Result

	mcpText := callE2EMCPTool(t, ctx, mcpClient, "browser_eval", map[string]interface{}{
		"script": topLevelAwait, "tab": tab,
	})
	var mcpResult interface{}
	if err := json.Unmarshal([]byte(mcpText), &mcpResult); err != nil {
		t.Fatalf("decode MCP eval result %q: %v", mcpText, err)
	}

	status, rawREST := runE2ERESTJSONResponse(t, serverURL, token, "/v1/eval", map[string]interface{}{
		"script": topLevelAwait, "tab": tab,
	})
	if status != http.StatusBadRequest || rawREST.Success {
		t.Fatalf("REST top-level await without wrapper = status %d, response %+v", status, rawREST)
	}
	requireContains(t, rawREST.Error, "SyntaxError", "unwrapped REST eval error")
	requireContains(t, rawREST.Error, "await", "unwrapped REST eval error")

	restResult := runE2ERESTJSON(t, serverURL, token, "/v1/eval", map[string]interface{}{
		"script": `(async () => ({surface: "parity", unicode: "等待 🚀", nested: {value: await Promise.resolve(43)}}))()`,
		"tab":    tab,
	}).Data.Result
	if !reflect.DeepEqual(cliResult, mcpResult) || !reflect.DeepEqual(cliResult, restResult) {
		t.Fatalf("eval surface results differ: CLI=%#v MCP=%#v REST=%#v", cliResult, mcpResult, restResult)
	}

	cliOptOut := runE2EJSONResponse(t, env, "eval", topLevelAwait, "--no-auto-await", "--tab", tab, "--json")
	if cliOptOut.Success {
		t.Fatalf("CLI eval opt-out unexpectedly succeeded: %+v", cliOptOut)
	}
	requireContains(t, cliOptOut.Error, "SyntaxError", "CLI eval opt-out error")
	requireContains(t, cliOptOut.Error, "await", "CLI eval opt-out error")

	mcpOptOut, err := callMCPTool(ctx, mcpClient, "browser_eval", map[string]interface{}{
		"script": topLevelAwait, "tab": tab, "noAutoAwait": true,
	})
	if err != nil {
		t.Fatalf("call MCP eval opt-out: %v", err)
	}
	requireE2EMCPErrorText(t, mcpOptOut, "SyntaxError", "MCP eval opt-out error")
	requireE2EMCPErrorText(t, mcpOptOut, "await", "MCP eval opt-out error")

	if err := mcpClient.Close(); err != nil {
		t.Fatalf("close MCP stdio client and wait for server exit: %v", err)
	}
	closed = true
}

func sendE2EMCPRequest(t *testing.T, ctx context.Context, stdio *transport.Stdio, id, method string, params interface{}) *transport.JSONRPCResponse {
	t.Helper()
	response, err := stdio.SendRequest(ctx, transport.JSONRPCRequest{
		JSONRPC: mcpprotocol.JSONRPC_VERSION,
		ID:      mcpprotocol.NewRequestId(id),
		Method:  method,
		Params:  params,
	})
	if err != nil {
		t.Fatalf("send MCP request %s (%s): %v", id, method, err)
	}
	if got := response.ID.Value(); got != id {
		t.Fatalf("MCP response id = %#v, want %q for method %s", got, id, method)
	}
	return response
}

func callE2EMCPRawTool(t *testing.T, ctx context.Context, stdio *transport.Stdio, id, name string, args map[string]interface{}) *mcpprotocol.CallToolResult {
	t.Helper()
	response := sendE2EMCPRequest(t, ctx, stdio, id, "tools/call", mcpprotocol.CallToolParams{
		Name: name, Arguments: args,
	})
	if response.Error != nil {
		t.Fatalf("MCP tool %s returned JSON-RPC error for id %s: %+v", name, id, response.Error)
	}
	result, err := mcpprotocol.ParseCallToolResult(&response.Result)
	if err != nil {
		t.Fatalf("parse MCP tool %s result for id %s: %v", name, id, err)
	}
	return result
}

func requireE2EMCPErrorText(t *testing.T, result *mcpprotocol.CallToolResult, want, label string) {
	t.Helper()
	if !result.IsError || len(result.Content) != 1 {
		t.Fatalf("%s result = %+v, want one error content item", label, result)
	}
	text, ok := result.Content[0].(mcpprotocol.TextContent)
	if !ok {
		t.Fatalf("%s returned non-text error content: %#v", label, result.Content[0])
	}
	requireContains(t, text.Text, want, label)
}

func callE2EMCPTool(t *testing.T, ctx context.Context, client *mcpclient.Client, name string, args map[string]interface{}) string {
	t.Helper()
	result, err := callMCPTool(ctx, client, name, args)
	if err != nil {
		t.Fatalf("call MCP tool %s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("MCP tool %s returned an error result: %+v", name, result)
	}
	if len(result.Content) != 1 {
		t.Fatalf("MCP tool %s returned %d content items, want 1: %+v", name, len(result.Content), result)
	}
	text, ok := result.Content[0].(mcpprotocol.TextContent)
	if !ok || text.Type != "text" {
		t.Fatalf("MCP tool %s returned non-text content: %#v", name, result.Content[0])
	}
	return text.Text
}

func callMCPTool(ctx context.Context, client *mcpclient.Client, name string, args map[string]interface{}) (*mcpprotocol.CallToolResult, error) {
	request := mcpprotocol.CallToolRequest{}
	request.Params.Name = name
	request.Params.Arguments = args
	return client.CallTool(ctx, request)
}

func refFromMCPSnapshot(t *testing.T, snapshot, name string) string {
	t.Helper()
	for _, line := range strings.Split(snapshot, "\n") {
		if !strings.Contains(line, `"`+name+`"`) {
			continue
		}
		_, after, ok := strings.Cut(line, "[ref=")
		if !ok {
			break
		}
		ref, _, ok := strings.Cut(after, "]")
		if ok && ref != "" {
			return ref
		}
	}
	t.Fatalf("MCP snapshot did not contain a ref for %q:\n%s", name, snapshot)
	return ""
}
