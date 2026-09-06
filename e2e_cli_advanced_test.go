package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/client"
	e2everify "github.com/leolin310148/borz/internal/e2e_verify_site"
	"github.com/leolin310148/borz/internal/protocol"
)

func TestE2ECLIEvalOptions(t *testing.T) {
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
	opened := runE2EJSON(t, env, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := opened.Data.Tab
	if tab == "" {
		t.Fatalf("open response did not include tab: %+v", opened.Data)
	}
	t.Cleanup(func() { runE2EJSON(t, env, "close", "--tab", tab, "--json") })

	scriptPath := filepath.Join(t.TempDir(), "eval-options.js")
	script := `window.__borzEvalState = {
  unicode: user.name,
  nested: payload.levels[0].values[1],
  emptyString,
  emptyListLength: emptyList.length,
  emptyObjectKeys: Object.keys(emptyObject).length,
  nullValue
}`
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatalf("write eval script: %v", err)
	}

	fileResp := runE2EJSON(t, env,
		"eval", "--file", scriptPath,
		"--json-arg", `user={"name":"Unicode 雪人 ☃️"}`,
		"--json-arg", `payload={"levels":[{"values":["",42]}]}`,
		"--json-arg", `emptyString=""`,
		"--json-arg", `emptyList=[]`,
		"--json-arg", `emptyObject={}`,
		"--json-arg", `nullValue=null`,
		"--tab", tab, "--json",
	)
	result, ok := fileResp.Data.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("eval --file result = %#v", fileResp.Data.Result)
	}
	if result["unicode"] != "Unicode 雪人 ☃️" || result["nested"] != float64(42) ||
		result["emptyString"] != "" || result["emptyListLength"] != float64(0) ||
		result["emptyObjectKeys"] != float64(0) || result["nullValue"] != nil {
		t.Fatalf("eval --file JSON args result = %#v", result)
	}

	// Repeating the same --json-arg names must not collide with lexical
	// bindings left behind by the previous Runtime.evaluate call.
	repeated := runE2EJSON(t, env,
		"eval", "--file", scriptPath,
		"--json-arg", `user={"name":"Unicode 雪人 ☃️"}`,
		"--json-arg", `payload={"levels":[{"values":["",42]}]}`,
		"--json-arg", `emptyString=""`,
		"--json-arg", `emptyList=[]`,
		"--json-arg", `emptyObject={}`,
		"--json-arg", `nullValue=null`,
		"--tab", tab, "--json",
	)
	if repeated.Data.Result == nil {
		t.Fatalf("repeated eval --json-arg returned no result: %+v", repeated.Data)
	}

	for i := 0; i < 2; i++ {
		lexical := runE2ECLI(t, env, "eval", `const phase = 7; phase`, "--tab", tab, "--unwrap")
		if got := strings.TrimSpace(lexical); got != "7" {
			t.Fatalf("scoped lexical eval #%d = %q", i+1, got)
		}
	}
	returned := runE2ECLI(t, env, "eval", `const value = 9; return value`, "--tab", tab, "--unwrap")
	if got := strings.TrimSpace(returned); got != "9" {
		t.Fatalf("top-level return = %q", got)
	}

	unwrapped := runE2ECLI(t, env, "eval", "window.__borzEvalState.unicode", "--tab", tab, "--unwrap")
	if got := strings.TrimSpace(unwrapped); got != "Unicode 雪人 ☃️" {
		t.Fatalf("eval --unwrap = %q", got)
	}

	awaited := runE2ECLI(t, env, "eval", `await Promise.resolve(window.__borzEvalState.nested)`, "--tab", tab, "--unwrap")
	if got := strings.TrimSpace(awaited); got != "42" {
		t.Fatalf("top-level await result = %q", got)
	}

	noAutoAwait := runE2EJSONResponse(t, env, "eval", `await Promise.resolve("should not run")`, "--no-auto-await", "--tab", tab, "--json")
	if noAutoAwait.Success {
		t.Fatalf("eval --no-auto-await unexpectedly succeeded: %+v", noAutoAwait)
	}
	requireContains(t, noAutoAwait.Error, "SyntaxError", "eval --no-auto-await error")
	requireContains(t, noAutoAwait.Error, "await", "eval --no-auto-await error")

	scriptError := runE2EJSONResponse(t, env, "eval", `(() => { throw new Error("e2e-eval-intentional") })()`, "--tab", tab, "--json")
	if scriptError.Success {
		t.Fatalf("throwing eval unexpectedly succeeded: %+v", scriptError)
	}
	requireContains(t, scriptError.Error, "Error: e2e-eval-intentional", "eval script error")

	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, "window.__borzEvalState.unicode", "Unicode 雪人 ☃️")
}

func TestE2ECLIOutputShaping(t *testing.T) {
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
	opened := runE2EJSON(t, env, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := opened.Data.Tab
	if tab == "" {
		t.Fatalf("open response did not include tab: %+v", opened.Data)
	}
	t.Cleanup(func() { runE2EJSON(t, env, "close", "--tab", tab, "--json") })

	script := `({object: {label: "live", count: 2}, list: ["first", 7], string: "plain output"})`

	jsonOut := runE2ECLI(t, env, "eval", script, "--tab", tab, "--unwrap", "--json")
	var jsonResp protocol.Response
	if err := json.Unmarshal([]byte(jsonOut), &jsonResp); err != nil {
		t.Fatalf("eval --unwrap --json returned non-JSON output: %v\n%s", err, jsonOut)
	}
	result, ok := jsonResp.Data.Result.(map[string]interface{})
	if !ok || result["string"] != "plain output" {
		t.Fatalf("eval --unwrap --json did not preserve the response envelope: %#v", jsonResp.Data.Result)
	}

	objectOut := runE2ECLI(t, env, "eval", script+`.object`, "--tab", tab, "--unwrap")
	var object map[string]interface{}
	if err := json.Unmarshal([]byte(objectOut), &object); err != nil {
		t.Fatalf("unwrapped object is not JSON: %v\n%s", err, objectOut)
	}
	if object["label"] != "live" || object["count"] != float64(2) {
		t.Fatalf("unwrapped object = %#v", object)
	}

	listOut := runE2ECLI(t, env, "eval", script+`.list`, "--tab", tab, "--unwrap")
	var list []interface{}
	if err := json.Unmarshal([]byte(listOut), &list); err != nil {
		t.Fatalf("unwrapped list is not JSON: %v\n%s", err, listOut)
	}
	if len(list) != 2 || list[0] != "first" || list[1] != float64(7) {
		t.Fatalf("unwrapped list = %#v", list)
	}

	stringOut := runE2ECLI(t, env, "eval", script+`.string`, "--tab", tab, "--unwrap")
	if got := strings.TrimSpace(stringOut); got != "plain output" {
		t.Fatalf("unwrapped string = %q", got)
	}

	jqOut := runE2ECLI(t, env, "eval", script, "--tab", tab, "--unwrap", "--json", "--jq", ".result.object")
	var jqObject map[string]interface{}
	if err := json.Unmarshal([]byte(jqOut), &jqObject); err != nil {
		t.Fatalf("jq object output is not JSON: %v\n%s", err, jqOut)
	}
	if jqObject["label"] != "live" || jqObject["count"] != float64(2) {
		t.Fatalf("jq object output = %#v", jqObject)
	}

	jqList := runE2ECLI(t, env, "eval", script, "--tab", tab, "--jq", ".result.list")
	if err := json.Unmarshal([]byte(jqList), &list); err != nil || len(list) != 2 || list[0] != "first" || list[1] != float64(7) {
		t.Fatalf("jq list output = %q, parsed %#v, error %v", jqList, list, err)
	}

	jqString := runE2ECLI(t, env, "eval", script, "--tab", tab, "--jq", ".result.string")
	if got := strings.TrimSpace(jqString); got != "plain output" {
		t.Fatalf("jq string output = %q", got)
	}

	missing := runE2ECLI(t, env, "eval", script, "--tab", tab, "--jq", ".result.missing")
	if strings.TrimSpace(missing) != "null" {
		t.Fatalf("jq missing path output = %q, want null", missing)
	}

	jqFailure := runE2ECLI(t, env, "eval", `(() => { throw new Error("e2e-output-shaping") })()`, "--tab", tab, "--jq", ".error")
	requireContains(t, jqFailure, "Error: e2e-output-shaping", "jq failure output")
}

func TestE2ECLISelectorAndRefErrors(t *testing.T) {
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
		t.Fatal("selector/ref fixture open returned no tab")
	}
	t.Cleanup(func() { runE2ECLI(t, env, "close", "--tab", tab, "--json") })

	requireError := func(label string, response protocol.Response, fragments ...string) {
		t.Helper()
		if response.Success || response.ID == "" || response.Error == "" {
			t.Fatalf("%s response = %+v, want structured error", label, response)
		}
		for _, fragment := range fragments {
			if !strings.Contains(response.Error, fragment) {
				t.Fatalf("%s error %q missing %q", label, response.Error, fragment)
			}
		}
	}

	missingSelector := runE2EJSONResponse(t, env, "frame", "#missing-frame", "--tab", tab, "--json")
	requireError("missing selector", missingSelector, "iframe not found", "#missing-frame")

	invalidCSS := runE2EJSONResponse(t, env, "frame", "[", "--tab", tab, "--json")
	requireError("invalid CSS", invalidCSS, "invalid selector", "[")

	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--tab", tab, "--json").Data.SnapshotData
	if snapshot == nil {
		t.Fatal("selector/ref snapshot returned no data")
	}
	clickRef := refByName(t, snapshot, "Click counter")
	inputRef := refByName(t, snapshot, "E2E text input")
	selectRef := refByName(t, snapshot, "E2E color select")

	nonexistentRef := runE2EJSONResponse(t, env, "click", "e999999", "--tab", tab, "--json")
	requireError("nonexistent ref", nonexistentRef, "unknown ref: e999999", "Run snapshot first")

	wrongType := runE2EJSONResponse(t, env, "select", inputRef, "green", "--tab", tab, "--json")
	requireError("wrong element type", wrongType, "element is not a select")

	invalidValue := runE2EJSONResponse(t, env, "select", selectRef, "purple", "--tab", tab, "--json")
	requireError("invalid select value", invalidValue, "select value not found: purple")

	runE2EJSON(t, env, "open", baseURL+"/page2", "--tab", tab, "--wait-for", "#page-two-ready", "--timeout", "5000", "--json")
	staleRef := runE2EJSONResponse(t, env, "click", clickRef, "--tab", tab, "--json")
	requireError("stale ref", staleRef, "unknown ref: "+clickRef, "Run snapshot first")

	runE2EJSON(t, env, "open", baseURL+"/", "--tab", tab, "--wait-for", "#ready", "--timeout", "5000", "--json")
	refreshed := runE2EJSON(t, env, "snapshot", "-i", "--tab", tab, "--json").Data.SnapshotData
	validClickRef := refByName(t, refreshed, "Click counter")
	runE2EJSON(t, env, "click", validClickRef, "--tab", tab, "--json")
	clicked := runE2EJSON(t, env, "eval", `document.querySelector("#clicked-result").textContent`, "--tab", tab, "--json")
	if clicked.Data.Result != "clicked 1" {
		t.Fatalf("valid action after selector/ref errors = %#v, want clicked 1", clicked.Data.Result)
	}
}

func TestE2ECLIActionIdempotencyAndStateTransitions(t *testing.T) {
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
	tab := runE2EJSON(t, env, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json").Data.Tab
	if tab == "" {
		t.Fatal("action state fixture open returned no tab")
	}
	t.Cleanup(func() { runE2ECLI(t, env, "close", "--tab", tab, "--json") })

	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--tab", tab, "--json").Data.SnapshotData
	if snapshot == nil {
		t.Fatal("action state snapshot returned no data")
	}
	clickRef := refByName(t, snapshot, "Click counter")
	hoverRef := refByName(t, snapshot, "Hover target")
	inputRef := refByName(t, snapshot, "E2E text input")
	submitRef := refByName(t, snapshot, "Submit form")
	checkRef := refByName(t, snapshot, "E2E checkbox")
	selectRef := refByName(t, snapshot, "E2E color select")
	coveredComboboxRef := refByName(t, snapshot, "Covered combobox")

	assertState := func(label, script string) {
		t.Helper()
		resp := runE2EJSON(t, env, "eval", script, "--tab", tab, "--json")
		if ok, _ := resp.Data.Result.(bool); !ok {
			t.Fatalf("%s state check = %#v, want true", label, resp.Data.Result)
		}
	}

	for range 2 {
		runE2EJSON(t, env, "check", checkRef, "--tab", tab, "--json")
	}
	assertState("repeated check", `document.querySelector("#check-box").checked && document.querySelector("#checkbox-state").textContent === "checked"`)
	for range 2 {
		runE2EJSON(t, env, "uncheck", checkRef, "--tab", tab, "--json")
	}
	assertState("repeated uncheck", `!document.querySelector("#check-box").checked && document.querySelector("#checkbox-state").textContent === "unchecked"`)

	runE2EJSON(t, env, "fill", inputRef, "Unicode 測試 ☃️", "--tab", tab, "--json")
	assertState("Unicode fill", `document.querySelector("#text-input").value === "Unicode 測試 ☃️" && document.querySelector("#input-state").textContent === "Unicode 測試 ☃️"`)
	runE2EJSON(t, env, "fill", inputRef, "", "--tab", tab, "--json")
	assertState("empty fill", `document.querySelector("#text-input").value === "" && document.querySelector("#input-state").textContent === "empty"`)

	for range 2 {
		runE2EJSON(t, env, "select", selectRef, "green", "--tab", tab, "--json")
	}
	assertState("repeated select", `document.querySelector("#color-select").value === "green" && document.querySelector("#select-state").textContent === "green"`)
	runE2EJSON(t, env, "click", coveredComboboxRef, "--tab", tab, "--json")
	assertState("covered combobox click", `document.querySelector("#covered-combobox-state").textContent === "clicked"`)

	for range 2 {
		runE2EJSON(t, env, "hover", hoverRef, "--tab", tab, "--json")
	}
	assertState("repeated hover", `document.querySelector("#hover-result").textContent === "hovered"`)
	for range 2 {
		runE2EJSON(t, env, "click", clickRef, "--tab", tab, "--json")
	}
	assertState("repeated click", `document.querySelector("#clicked-result").textContent === "clicked 2"`)

	runE2EJSON(t, env, "eval", `window.__borzSubmitCount = 0; document.querySelector("#text-form").addEventListener("submit", () => { window.__borzSubmitCount += 1; }); true`, "--tab", tab, "--json")
	runE2EJSON(t, env, "fill", inputRef, "submit 測試", "--tab", tab, "--json")
	for range 2 {
		runE2EJSON(t, env, "click", submitRef, "--tab", tab, "--json")
	}
	assertState("submission event count", `window.__borzSubmitCount === 2 && document.querySelector("#submit-result").textContent === "submitted submit 測試"`)
}

func TestE2ECLIFetchRequests(t *testing.T) {
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
	opened := runE2EJSON(t, env, "open", baseURL+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := opened.Data.Tab
	if tab == "" {
		t.Fatalf("open response did not include tab: %+v", opened.Data)
	}
	t.Cleanup(func() { runE2EJSON(t, env, "close", "--tab", tab, "--json") })

	runE2EJSON(t, env, "eval", `document.cookie = "e2e_fetch_cookie=same-origin; Path=/; SameSite=Lax"`, "--tab", tab, "--json")
	fetchResult := func(path string, args ...string) map[string]interface{} {
		t.Helper()
		command := []string{"fetch", baseURL + path}
		command = append(command, args...)
		command = append(command, "--tab", tab, "--json")
		resp := runE2EJSON(t, env, command...)
		result, ok := resp.Data.Result.(map[string]interface{})
		if !ok {
			t.Fatalf("borz fetch %s result = %#v", path, resp.Data.Result)
		}
		return result
	}
	bodyOf := func(result map[string]interface{}) map[string]interface{} {
		t.Helper()
		body, ok := result["body"].(map[string]interface{})
		if !ok {
			t.Fatalf("fetch body = %#v", result)
		}
		return body
	}

	get := fetchResult("/api/fetch/get", "--header", "X-E2E-Header: get-value", "--header", "X-E2E-Second: second-value")
	getBody := bodyOf(get)
	if get["status"] != float64(http.StatusOK) || getBody["method"] != http.MethodGet || getBody["header"] != "get-value" || getBody["secondHeader"] != "second-value" {
		t.Fatalf("GET fetch result = %#v", get)
	}
	if cookie, _ := getBody["cookie"].(string); !strings.Contains(cookie, "e2e_fetch_cookie=same-origin") {
		t.Fatalf("GET fetch did not inherit same-origin cookie: %#v", getBody)
	}

	jsonPayload := `{"message":"Unicode 測試","nested":{"count":2}}`
	post := fetchResult("/api/fetch/post", "--method", "POST", "--header", "Content-Type: application/json", "--body", jsonPayload)
	postBody := bodyOf(post)
	decodedJSON, ok := postBody["jsonBody"].(map[string]interface{})
	if !ok || postBody["method"] != http.MethodPost || postBody["rawBody"] != jsonPayload || decodedJSON["message"] != "Unicode 測試" || decodedJSON["nested"].(map[string]interface{})["count"] != float64(2) {
		t.Fatalf("POST JSON fetch result = %#v", post)
	}

	formPayload := "name=borz+e2e&tag=one&tag=two"
	put := fetchResult("/api/fetch/put", "--method", "PUT", "--header", "Content-Type: application/x-www-form-urlencoded", "--body", formPayload)
	putBody := bodyOf(put)
	form, ok := putBody["formBody"].(map[string]interface{})
	if !ok || putBody["method"] != http.MethodPut || form["name"].([]interface{})[0] != "borz e2e" || len(form["tag"].([]interface{})) != 2 {
		t.Fatalf("PUT form fetch result = %#v", put)
	}

	non2xx := fetchResult("/api/fetch/status")
	if non2xx["status"] != float64(http.StatusUnprocessableEntity) || bodyOf(non2xx)["method"] != http.MethodGet {
		t.Fatalf("non-2xx fetch result = %#v", non2xx)
	}

	failure := fetchResult("/api/fetch/get", "--method", "TRACE")
	if message, ok := failure["error"].(string); !ok || strings.TrimSpace(message) == "" {
		t.Fatalf("failed fetch did not return a structured error: %#v", failure)
	}
}
