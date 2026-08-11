package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/client"
	e2everify "github.com/leolin310148/borz/internal/e2e_verify_site"
)

func TestE2ECLINetworkDiagnostics(t *testing.T) {
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

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("network diagnostics open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	runE2EJSON(t, env, "network", "clear", "--tab", tab, "--json")
	runE2EJSON(t, env, "eval", `await Promise.all([
		fetch('/api/network/echo?status=201&response=created', {method: 'POST', body: 'post payload'}),
		fetch('/api/network/echo?status=404&response=missing'),
		fetch('/api/network/slow?status=202&response=slow')
	])`, "--tab", tab, "--json")

	filtered := runE2EJSON(t, env, "network", "requests", "--filter", "/api/network/", "--method", "POST", "--status", "2xx", "--with-body", "--tab", tab, "--json")
	if len(filtered.Data.NetworkRequests) != 1 {
		t.Fatalf("filtered network requests = %+v, want one POST 2xx request", filtered.Data.NetworkRequests)
	}
	post := filtered.Data.NetworkRequests[0]
	if post.Method != http.MethodPost || post.Status == nil || *post.Status != http.StatusCreated || !strings.Contains(post.ResponseBody, `"requestBody":"post payload"`) || !strings.Contains(post.ResponseBody, `"response":"created"`) {
		t.Fatalf("filtered POST request = %+v", post)
	}

	limited := runE2EJSON(t, env, "network", "requests", "--filter", "/api/network/", "--limit", "2", "--tab", tab, "--json")
	if len(limited.Data.NetworkRequests) != 2 {
		t.Fatalf("limited network requests = %+v, want newest two", limited.Data.NetworkRequests)
	}
	if !strings.Contains(limited.Data.NetworkRequests[0].URL, "status=404") || !strings.Contains(limited.Data.NetworkRequests[1].URL, "/api/network/slow") || limited.Data.NetworkRequests[1].Status == nil || *limited.Data.NetworkRequests[1].Status != http.StatusAccepted {
		t.Fatalf("limited network requests = %+v, want 404 echo then 202 slow response", limited.Data.NetworkRequests)
	}

	runE2EJSON(t, env, "eval", `await fetch('/api/network/echo?status=200&response=since-old')`, "--tab", tab, "--json")
	runE2EJSON(t, env, "eval", `await fetch('/api/network/echo?status=200&response=since-new')`, "--tab", tab, "--json")
	since := runE2EJSON(t, env, "network", "requests", "--filter", "response=since-", "--since", "last_action", "--tab", tab, "--json")
	if len(since.Data.NetworkRequests) != 1 || !strings.Contains(since.Data.NetworkRequests[0].URL, "response=since-new") {
		t.Fatalf("network requests since last_action = %+v, want only since-new", since.Data.NetworkRequests)
	}

	runE2EJSON(t, env, "network", "clear", "--tab", tab, "--json")
	cleared := runE2EJSON(t, env, "network", "requests", "--filter", "/api/network/", "--tab", tab, "--json")
	if len(cleared.Data.NetworkRequests) != 0 {
		t.Fatalf("network clear left requests: %+v", cleared.Data.NetworkRequests)
	}
}

func TestE2ECLICacheNavigation(t *testing.T) {
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

	env := startE2EDaemon(t, home)
	cacheableURL := site.URL() + "/cache/cacheable"
	openResp := runE2EJSON(t, env, "open", cacheableURL, "--new", "--wait-for", "#cache-ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("cache fixture open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, "document.querySelector('#request-count').textContent", "1")
	runE2EJSON(t, env, "network", "clear", "--tab", tab, "--json")
	runE2EJSON(t, env, "refresh", "--tab", tab, "--wait-for", "#cache-ready", "--timeout", "10000", "--json")
	refreshCount := runE2EJSON(t, env, "eval", "document.querySelector('#request-count').textContent", "--tab", tab, "--json").Data.Result
	if refreshCount != "1" && refreshCount != "2" {
		t.Fatalf("cacheable refresh count = %#v, want cached 1 or revalidated 2", refreshCount)
	}
	cacheableRequests := runE2EJSON(t, env, "network", "requests", "--filter", "/cache/cacheable", "--tab", tab, "--json").Data.NetworkRequests
	if len(cacheableRequests) == 0 {
		t.Fatal("cacheable refresh produced no network metadata")
	}
	cacheable := cacheableRequests[len(cacheableRequests)-1]
	if cacheable.Status == nil || *cacheable.Status != http.StatusOK || networkHeader(cacheable.ResponseHeaders, "Cache-Control") != "public, max-age=3600" {
		t.Fatalf("cacheable refresh metadata = %+v", cacheable)
	}
	if cacheable.FromDiskCache && refreshCount != "1" {
		t.Fatalf("disk-cached response advanced fixture counter: count=%#v request=%+v", refreshCount, cacheable)
	}

	// Navigating away and back may use the back-forward cache, the HTTP cache,
	// or revalidate. All are valid as long as at most one server request occurs.
	runE2EJSON(t, env, "open", site.URL()+"/", "--tab", tab, "--wait-for", "#ready", "--timeout", "10000", "--json")
	runE2EJSON(t, env, "network", "clear", "--tab", tab, "--json")
	runE2EJSON(t, env, "back", "--tab", tab, "--wait-for", "#cache-ready", "--timeout", "10000", "--json")
	backCount := runE2EJSON(t, env, "eval", "document.querySelector('#request-count').textContent", "--tab", tab, "--json").Data.Result
	if backCount != refreshCount && !(refreshCount == "1" && backCount == "2") && !(refreshCount == "2" && backCount == "3") {
		t.Fatalf("cacheable back-navigation count advanced unexpectedly: refresh=%#v back=%#v", refreshCount, backCount)
	}
	backRequests := runE2EJSON(t, env, "network", "requests", "--filter", "/cache/cacheable", "--tab", tab, "--json").Data.NetworkRequests
	if len(backRequests) > 1 {
		t.Fatalf("cacheable back navigation made multiple requests: %+v", backRequests)
	}

	runE2EJSON(t, env, "open", site.URL()+"/cache/no-cache", "--tab", tab, "--wait-for", "#cache-ready", "--timeout", "10000", "--json")
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, "document.querySelector('#request-count').textContent", "1")
	runE2EJSON(t, env, "network", "clear", "--tab", tab, "--json")
	runE2EJSON(t, env, "refresh", "--tab", tab, "--wait-for", "#cache-ready", "--timeout", "10000", "--json")
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, "document.querySelector('#request-count').textContent", "2")
	noCacheRequests := runE2EJSON(t, env, "network", "requests", "--filter", "/cache/no-cache", "--tab", tab, "--json").Data.NetworkRequests
	if len(noCacheRequests) == 0 {
		t.Fatal("no-cache refresh produced no network metadata")
	}
	noCache := noCacheRequests[len(noCacheRequests)-1]
	if noCache.FromDiskCache || noCache.Status == nil || *noCache.Status != http.StatusOK || networkHeader(noCache.ResponseHeaders, "Cache-Control") != "no-store" {
		t.Fatalf("no-cache refresh metadata = %+v", noCache)
	}
}

func networkHeader(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func TestE2ECLIStreamingNetworkFailures(t *testing.T) {
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

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("streaming network open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	runE2EJSON(t, env, "network", "clear", "--tab", tab, "--json")
	stream := runE2EJSON(t, env, "eval", `await fetch('/api/network/stream').then(response => response.text())`, "--tab", tab, "--json")
	if stream.Data.Result != "stream-chunk-one\nstream-chunk-two\n" {
		t.Fatalf("stream response = %#v", stream.Data.Result)
	}
	streamRequests := runE2EJSON(t, env, "network", "requests", "--filter", "/api/network/stream", "--with-body", "--tab", tab, "--json").Data.NetworkRequests
	if len(streamRequests) != 1 || streamRequests[0].Status == nil || *streamRequests[0].Status != http.StatusOK || streamRequests[0].Failed || streamRequests[0].ResponseBody != "stream-chunk-one\nstream-chunk-two\n" {
		t.Fatalf("stream network lifecycle = %+v", streamRequests)
	}

	aborted := runE2EJSON(t, env, "eval", `await fetch('/api/network/abort').then(response => response.text()).then(
		value => ({value}), error => ({name: error.name, message: error.message})
	)`, "--tab", tab, "--json")
	abortResult, ok := aborted.Data.Result.(map[string]interface{})
	if !ok || abortResult["name"] != "TypeError" || abortResult["message"] == "" {
		t.Fatalf("aborted fetch result = %#v", aborted.Data.Result)
	}
	abortRequests := runE2EJSON(t, env, "network", "requests", "--filter", "/api/network/abort", "--tab", tab, "--json").Data.NetworkRequests
	if len(abortRequests) != 1 || abortRequests[0].Status == nil || *abortRequests[0].Status != http.StatusOK || !abortRequests[0].Failed || abortRequests[0].FailureReason == "" {
		t.Fatalf("aborted network lifecycle = %+v", abortRequests)
	}

	timedOut := runE2EJSON(t, env, "eval", `await (async () => {
		const controller = new AbortController();
		const timer = setTimeout(() => controller.abort(), 75);
		try {
			await fetch('/api/network/stream?case=timeout', {signal: controller.signal}).then(response => response.text());
			return {completed: true};
		} catch (error) {
			return {name: error.name, message: error.message};
		} finally {
			clearTimeout(timer);
		}
	})()`, "--tab", tab, "--json")
	timeoutResult, ok := timedOut.Data.Result.(map[string]interface{})
	if !ok || timeoutResult["name"] != "AbortError" || timeoutResult["message"] == "" {
		t.Fatalf("timed out stream result = %#v", timedOut.Data.Result)
	}
	timeoutRequests := runE2EJSON(t, env, "network", "requests", "--filter", "case=timeout", "--tab", tab, "--json").Data.NetworkRequests
	if len(timeoutRequests) != 1 || !timeoutRequests[0].Failed || !strings.Contains(timeoutRequests[0].FailureReason, "ERR_ABORTED") {
		t.Fatalf("timed out network lifecycle = %+v", timeoutRequests)
	}

	// A failed transfer must not poison the daemon or the tab's browser context.
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, "document.title", "E2E Verify Home")
	ping := runE2EJSON(t, env, "eval", `await fetch('/api/ping?after=failed-transfer').then(response => response.json()).then(value => value.ok)`, "--tab", tab, "--json")
	if ping.Data.Result != "true" {
		t.Fatalf("post-failure ping result = %#v", ping.Data.Result)
	}
}

func TestE2ECLIRedirectChain(t *testing.T) {
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

	env := startE2EDaemon(t, home)
	baseURL := site.URL()
	tab := runE2EJSON(t, env, "open", baseURL+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json").Data.Tab
	if tab == "" {
		t.Fatal("redirect fixture open response did not include a short tab id")
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	runE2EJSON(t, env, "network", "clear", "--tab", tab, "--json")
	runE2EJSON(t, env, "open", baseURL+"/redirect/start", "--tab", tab, "--wait-for", "#redirect-ready", "--timeout", "10000", "--json")
	if finalURL := runE2EJSON(t, env, "get", "url", "--tab", tab, "--json").Data.Value; finalURL != baseURL+"/redirect/final" {
		t.Fatalf("redirect final URL = %q", finalURL)
	}
	if title := runE2EJSON(t, env, "get", "title", "--tab", tab, "--json").Data.Value; title != "E2E Redirect Final" {
		t.Fatalf("redirect final title = %q", title)
	}

	requests := runE2EJSON(t, env, "network", "requests", "--filter", "/redirect/", "--tab", tab, "--json").Data.NetworkRequests
	wantRedirects := map[string]struct {
		status   int
		location string
	}{
		baseURL + "/redirect/start":  {status: http.StatusFound, location: "/redirect/middle"},
		baseURL + "/redirect/middle": {status: http.StatusTemporaryRedirect, location: "/redirect/final"},
	}
	for url, want := range wantRedirects {
		found := false
		for _, request := range requests {
			if request.URL != url {
				continue
			}
			found = true
			location := ""
			for name, value := range request.ResponseHeaders {
				if strings.EqualFold(name, "Location") {
					location = value
				}
			}
			if request.Status == nil || *request.Status != want.status || location != want.location {
				t.Fatalf("redirect network record for %s = %+v, want status %d location %q", url, request, want.status, want.location)
			}
		}
		if !found {
			t.Fatalf("redirect network records missing %s: %+v", url, requests)
		}
	}

	runE2EJSON(t, env, "back", "--tab", tab, "--wait-for", "#ready", "--timeout", "5000", "--json")
	if backURL := runE2EJSON(t, env, "get", "url", "--tab", tab, "--json").Data.Value; backURL != baseURL+"/" {
		t.Fatalf("back from redirect URL = %q, want home", backURL)
	}
	runE2EJSON(t, env, "forward", "--tab", tab, "--wait-for", "#redirect-ready", "--timeout", "10000", "--json")
	if forwardURL := runE2EJSON(t, env, "get", "url", "--tab", tab, "--json").Data.Value; forwardURL != baseURL+"/redirect/final" {
		t.Fatalf("forward through redirect URL = %q, want final URL", forwardURL)
	}
}

func TestE2ECLIURLFidelity(t *testing.T) {
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

	token := "e2e-url-fidelity-token"
	env, serverURL := startE2EServer(t, home, token)
	baseURL := site.URL()
	exactURL := baseURL + "/url-fidelity?case=exact"
	queryURL := baseURL + "/url-fidelity/%E8%B7%AF%E5%BE%91?case=query&name=%E6%B8%AC%E8%A9%A6+%F0%9F%9A%80&reserved=%26%3D%2F%3F"
	fragmentURL := queryURL + "#%E7%89%87%E6%AE%B5-%F0%9F%8C%9F"

	exactTab := runE2EJSON(t, env, "open", exactURL, "--new", "--wait-for", "#url-fidelity-ready", "--timeout", "10000", "--json").Data.Tab
	if exactTab == "" {
		t.Fatal("exact URL open response did not include a short tab id")
	}
	runE2EJSON(t, env, "eval", `window.__borzURLReuseSentinel = "preserved"`, "--tab", exactTab, "--json")
	reused := runE2EJSON(t, env, "open", exactURL, "--wait-for", "#url-fidelity-ready", "--timeout", "5000", "--json")
	if reused.Data.Tab != exactTab {
		t.Fatalf("exact URL open selected tab %q, want %q", reused.Data.Tab, exactTab)
	}
	if !reused.Data.Reused {
		t.Fatalf("exact URL open did not report data.reused: %+v", reused.Data)
	}
	if sentinel := runE2EJSON(t, env, "eval", `window.__borzURLReuseSentinel`, "--tab", exactTab, "--json").Data.Result; sentinel != "preserved" {
		t.Fatalf("exact URL reuse reloaded page state: result=%#v", sentinel)
	}

	queryTab := runE2EJSON(t, env, "open", queryURL, "--wait-for", "#url-fidelity-ready", "--timeout", "10000", "--json").Data.Tab
	fragmentTab := runE2EJSON(t, env, "open", fragmentURL, "--wait-for", "#url-fidelity-ready", "--timeout", "10000", "--json").Data.Tab
	if queryTab == "" || fragmentTab == "" || queryTab == exactTab || fragmentTab == exactTab || fragmentTab == queryTab {
		t.Fatalf("query and fragment URLs did not create distinct tabs: exact=%q query=%q fragment=%q", exactTab, queryTab, fragmentTab)
	}
	for _, tab := range []string{exactTab, queryTab, fragmentTab} {
		tab := tab
		t.Cleanup(func() {
			runE2ERESTJSON(t, serverURL, token, "/v1/close", map[string]interface{}{"tab": tab})
		})
	}

	for _, tc := range []struct {
		tab string
		url string
	}{
		{tab: exactTab, url: exactURL},
		{tab: queryTab, url: queryURL},
		{tab: fragmentTab, url: fragmentURL},
	} {
		if got := runE2EJSON(t, env, "get", "url", "--tab", tc.tab, "--json").Data.Value; got != tc.url {
			t.Fatalf("CLI get url for tab %s = %q, want %q", tc.tab, got, tc.url)
		}
	}

	if got := runE2EJSON(t, env, "eval", `document.querySelector("#url-unicode-param").textContent`, "--tab", queryTab, "--json").Data.Result; got != "測試 🚀" {
		t.Fatalf("decoded Unicode query parameter = %#v", got)
	}
	if got := runE2EJSON(t, env, "eval", `document.querySelector("#url-reserved-param").textContent`, "--tab", queryTab, "--json").Data.Result; got != "&=/?" {
		t.Fatalf("decoded reserved query parameter = %#v", got)
	}

	restURL := runE2ERESTJSON(t, serverURL, token, "/v1/get", map[string]interface{}{
		"attribute": "url", "tab": fragmentTab,
	})
	if restURL.Data.Tab != fragmentTab || restURL.Data.Value != fragmentURL {
		t.Fatalf("REST get url = tab %q value %q, want tab %q value %q", restURL.Data.Tab, restURL.Data.Value, fragmentTab, fragmentURL)
	}
}

func TestE2ECLITabDiagnosticsIsolation(t *testing.T) {
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

	env := startE2EDaemon(t, home)
	tabOne := runE2EJSON(t, env, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json").Data.Tab
	tabTwo := runE2EJSON(t, env, "open", site.URL()+"/page2", "--new", "--wait-for", "#page-two-ready", "--timeout", "10000", "--json").Data.Tab
	if tabOne == "" || tabTwo == "" || tabOne == tabTwo {
		t.Fatalf("diagnostics tabs are not distinct: tab one=%q tab two=%q", tabOne, tabTwo)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tabOne, "--json")
		runE2ECLI(t, env, "close", "--tab", tabTwo, "--json")
	})

	clearE2EDiagnostics(t, env, tabOne)
	clearE2EDiagnostics(t, env, tabTwo)
	emitE2EDiagnostics(t, env, tabOne, "e2e-diag-tab-one")
	emitE2EDiagnostics(t, env, tabTwo, "e2e-diag-tab-two")
	requireE2EDiagnostics(t, env, tabOne, "e2e-diag-tab-one", "e2e-diag-tab-two", "")
	requireE2EDiagnostics(t, env, tabTwo, "e2e-diag-tab-two", "e2e-diag-tab-one", "")

	clearE2EDiagnostics(t, env, tabOne)
	requireNoE2EDiagnostics(t, env, tabOne, "")
	requireE2EDiagnostics(t, env, tabTwo, "e2e-diag-tab-two", "e2e-diag-tab-one", "")

	clearE2EDiagnostics(t, env, tabOne)
	clearE2EDiagnostics(t, env, tabTwo)
	for _, tc := range []struct {
		tab   string
		label string
	}{
		{tabOne, "e2e-diag-tab-one"},
		{tabTwo, "e2e-diag-tab-two"},
	} {
		emitE2EDiagnostics(t, env, tc.tab, tc.label+"-old")
		emitE2EDiagnostics(t, env, tc.tab, tc.label+"-new")
	}
	requireE2EDiagnostics(t, env, tabOne, "e2e-diag-tab-one-new", "e2e-diag-tab-two-new", "last_action")
	requireE2EDiagnostics(t, env, tabTwo, "e2e-diag-tab-two-new", "e2e-diag-tab-one-new", "last_action")
}

func clearE2EDiagnostics(t *testing.T, env e2eDaemonEnv, tab string) {
	t.Helper()
	runE2EJSON(t, env, "console", "--clear", "--tab", tab, "--json")
	runE2EJSON(t, env, "errors", "--clear", "--tab", tab, "--json")
	runE2EJSON(t, env, "network", "clear", "--tab", tab, "--json")
}

func emitE2EDiagnostics(t *testing.T, env e2eDaemonEnv, tab, label string) {
	t.Helper()
	script := fmt.Sprintf(`console.log(%q);
		setTimeout(() => { throw new Error(%q); }, 0);
		await fetch(%q);
		true`, label, label, "/api/ping?diagnostics="+label)
	runE2EJSON(t, env, "eval", script, "--tab", tab, "--json")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp := runE2EJSON(t, env, "errors", "--filter", label, "--tab", tab, "--json")
		if len(resp.Data.JSErrors) == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for JavaScript error %q on tab %s", label, tab)
}

func requireE2EDiagnostics(t *testing.T, env e2eDaemonEnv, tab, own, other, since string) {
	t.Helper()
	args := func(command ...string) []string {
		command = append(command, "--filter", "e2e-diag-", "--tab", tab)
		if since != "" {
			command = append(command, "--since", since)
		}
		return append(command, "--json")
	}

	console := runE2EJSON(t, env, args("console")...).Data.ConsoleMessages
	if len(console) != 1 || !strings.Contains(console[0].Text, own) || strings.Contains(console[0].Text, other) {
		t.Fatalf("console diagnostics for tab %s = %+v, want only %q", tab, console, own)
	}
	errors := runE2EJSON(t, env, args("errors")...).Data.JSErrors
	if len(errors) != 1 || !strings.Contains(errors[0].Message, own) || strings.Contains(errors[0].Message, other) {
		t.Fatalf("error diagnostics for tab %s = %+v, want only %q", tab, errors, own)
	}
	network := runE2EJSON(t, env, args("network", "requests")...).Data.NetworkRequests
	if len(network) != 1 || !strings.Contains(network[0].URL, own) || strings.Contains(network[0].URL, other) {
		t.Fatalf("network diagnostics for tab %s = %+v, want only %q", tab, network, own)
	}
}

func requireNoE2EDiagnostics(t *testing.T, env e2eDaemonEnv, tab, since string) {
	t.Helper()
	args := []string{"--filter", "e2e-diag-", "--tab", tab}
	if since != "" {
		args = append(args, "--since", since)
	}
	if got := runE2EJSON(t, env, append([]string{"console"}, append(args, "--json")...)...).Data.ConsoleMessages; len(got) != 0 {
		t.Fatalf("console clear left diagnostics on tab %s: %+v", tab, got)
	}
	if got := runE2EJSON(t, env, append([]string{"errors"}, append(args, "--json")...)...).Data.JSErrors; len(got) != 0 {
		t.Fatalf("errors clear left diagnostics on tab %s: %+v", tab, got)
	}
	if got := runE2EJSON(t, env, append([]string{"network", "requests"}, append(args, "--json")...)...).Data.NetworkRequests; len(got) != 0 {
		t.Fatalf("network clear left diagnostics on tab %s: %+v", tab, got)
	}
}
