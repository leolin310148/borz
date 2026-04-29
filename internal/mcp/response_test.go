package mcp

import (
	"errors"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
)

// firstText extracts the .Text of the first TextContent in a result.
func firstText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil {
		t.Fatal("nil result")
	}
	if len(res.Content) == 0 {
		t.Fatal("no content")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("first content not text: %T", res.Content[0])
	}
	return tc.Text
}

func isError(res *mcp.CallToolResult) bool {
	return res != nil && res.IsError
}

// --- checkError ---

func TestCheckError_NetworkError(t *testing.T) {
	got := checkError(nil, errors.New("boom"))
	if got == nil || !isError(got) {
		t.Fatalf("expected error result")
	}
	if !strings.Contains(firstText(t, got), "Command failed") {
		t.Errorf("text = %q", firstText(t, got))
	}
}

func TestCheckError_CommandUnsuccessful(t *testing.T) {
	resp := &protocol.Response{Success: false, Error: "nope"}
	got := checkError(resp, nil)
	if got == nil || !isError(got) {
		t.Fatalf("expected error result")
	}
	if firstText(t, got) != "nope" {
		t.Errorf("text = %q", firstText(t, got))
	}
}

func TestCheckError_Success(t *testing.T) {
	resp := &protocol.Response{Success: true}
	if got := checkError(resp, nil); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// --- textResult ---

func TestTextResult_NoData(t *testing.T) {
	resp := &protocol.Response{Success: true}
	got := textResult(resp, "hello")
	if firstText(t, got) != "hello" {
		t.Errorf("text = %q", firstText(t, got))
	}
}

func TestTextResult_WithURLAndTitle(t *testing.T) {
	resp := &protocol.Response{Success: true, Data: &protocol.ResponseData{URL: "https://a.com", Title: "A"}}
	got := firstText(t, textResult(resp, "hi"))
	if !strings.Contains(got, "https://a.com") || !strings.Contains(got, "A") {
		t.Errorf("text = %q", got)
	}
}

func TestTextResult_URLOnly(t *testing.T) {
	resp := &protocol.Response{Data: &protocol.ResponseData{URL: "https://a.com"}}
	got := firstText(t, textResult(resp, "hi"))
	if !strings.Contains(got, "https://a.com") || strings.Contains(got, "—") {
		t.Errorf("text = %q", got)
	}
}

// --- formatSnapshot ---

func TestFormatSnapshot_Empty(t *testing.T) {
	if got := firstText(t, formatSnapshot(&protocol.Response{})); got != "(empty snapshot)" {
		t.Errorf("text = %q", got)
	}
	resp := &protocol.Response{Data: &protocol.ResponseData{}}
	if got := firstText(t, formatSnapshot(resp)); got != "(empty snapshot)" {
		t.Errorf("text = %q", got)
	}
}

func TestFormatSnapshot_WithData(t *testing.T) {
	resp := &protocol.Response{Data: &protocol.ResponseData{SnapshotData: &protocol.SnapshotData{Snapshot: "tree"}}}
	if got := firstText(t, formatSnapshot(resp)); got != "tree" {
		t.Errorf("text = %q", got)
	}
}

// --- formatScreenshot ---

func TestFormatScreenshot_NoData(t *testing.T) {
	got := firstText(t, formatScreenshot(&protocol.Response{}))
	if !strings.Contains(got, "no image data") {
		t.Errorf("text = %q", got)
	}
}

func TestFormatScreenshot_WithPNG(t *testing.T) {
	resp := &protocol.Response{Data: &protocol.ResponseData{DataURL: "data:image/png;base64,iVBOR"}}
	res := formatScreenshot(resp)
	if len(res.Content) != 1 {
		t.Fatalf("content len = %d", len(res.Content))
	}
	img, ok := res.Content[0].(mcp.ImageContent)
	if !ok {
		t.Fatalf("not image content: %T", res.Content[0])
	}
	if img.Data != "iVBOR" {
		t.Errorf("data prefix not stripped: %q", img.Data)
	}
	if img.MIMEType != "image/png" {
		t.Errorf("mime = %q", img.MIMEType)
	}
}

func TestFormatScreenshot_WithJPEG(t *testing.T) {
	resp := &protocol.Response{Data: &protocol.ResponseData{DataURL: "data:image/jpeg;base64,AAAA"}}
	res := formatScreenshot(resp)
	img := res.Content[0].(mcp.ImageContent)
	if img.Data != "AAAA" {
		t.Errorf("jpeg prefix not stripped: %q", img.Data)
	}
}

// --- formatTabList ---

func TestFormatTabList_Empty(t *testing.T) {
	got := firstText(t, formatTabList(&protocol.Response{}))
	if got != "No tabs open" {
		t.Errorf("text = %q", got)
	}
}

func TestFormatTabList_WithTabs(t *testing.T) {
	resp := &protocol.Response{Data: &protocol.ResponseData{Tabs: []protocol.TabInfo{
		{Index: 0, URL: "https://a", Title: "A", Active: true, TabID: "t1"},
		{Index: 1, URL: "https://b", Title: "B", TabID: "t2"},
	}}}
	got := firstText(t, formatTabList(resp))
	if !strings.Contains(got, "Tabs (2 total)") {
		t.Errorf("missing header: %q", got)
	}
	if !strings.Contains(got, "* [0] https://a") {
		t.Errorf("active marker: %q", got)
	}
	if !strings.Contains(got, "  [1] https://b") {
		t.Errorf("inactive: %q", got)
	}
}

// --- formatEval ---

func TestFormatEval_NoData(t *testing.T) {
	if got := firstText(t, formatEval(&protocol.Response{})); got != "(no result)" {
		t.Errorf("got %q", got)
	}
}

func TestFormatEval_NilResult(t *testing.T) {
	resp := &protocol.Response{Data: &protocol.ResponseData{}}
	if got := firstText(t, formatEval(resp)); got != "undefined" {
		t.Errorf("got %q", got)
	}
}

func TestFormatEval_JSONResult(t *testing.T) {
	resp := &protocol.Response{Data: &protocol.ResponseData{Result: map[string]interface{}{"a": 1}}}
	got := firstText(t, formatEval(resp))
	if !strings.Contains(got, `"a": 1`) {
		t.Errorf("got %q", got)
	}
}

func TestFormatEval_UnmarshalableFallsBack(t *testing.T) {
	// channels aren't JSON-marshalable
	ch := make(chan int)
	resp := &protocol.Response{Data: &protocol.ResponseData{Result: ch}}
	got := firstText(t, formatEval(resp))
	if got == "" {
		t.Errorf("expected fallback %%v text, got empty")
	}
}

// --- formatGet ---

func TestFormatGet_Empty(t *testing.T) {
	if got := firstText(t, formatGet(&protocol.Response{})); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestFormatGet_ValuePreferred(t *testing.T) {
	resp := &protocol.Response{Data: &protocol.ResponseData{Value: "v", URL: "u", Title: "t"}}
	if got := firstText(t, formatGet(resp)); got != "v" {
		t.Errorf("got %q", got)
	}
}

func TestFormatGet_URLThenTitle(t *testing.T) {
	resp := &protocol.Response{Data: &protocol.ResponseData{URL: "u", Title: "t"}}
	if got := firstText(t, formatGet(resp)); got != "u" {
		t.Errorf("got %q", got)
	}
	resp = &protocol.Response{Data: &protocol.ResponseData{Title: "t"}}
	if got := firstText(t, formatGet(resp)); got != "t" {
		t.Errorf("got %q", got)
	}
}

func TestFormatGet_ResultJSON(t *testing.T) {
	resp := &protocol.Response{Data: &protocol.ResponseData{Result: []interface{}{1, 2, 3}}}
	got := firstText(t, formatGet(resp))
	if !strings.Contains(got, "1") || !strings.Contains(got, "3") {
		t.Errorf("got %q", got)
	}
}

// --- formatNetwork ---

func TestFormatNetwork_Empty(t *testing.T) {
	if got := firstText(t, formatNetwork(&protocol.Response{})); got != "No network requests captured" {
		t.Errorf("got %q", got)
	}
}

func TestFormatNetwork_WithRequests(t *testing.T) {
	s200 := 200
	resp := &protocol.Response{Data: &protocol.ResponseData{NetworkRequests: []protocol.NetworkRequestInfo{
		{URL: "https://a", Method: "GET", Type: "xhr", Status: &s200},
		{URL: "https://b", Method: "POST", Type: "fetch", Failed: true, FailureReason: "timeout"},
		{URL: "https://c", Method: "GET", Type: "xhr"},
		{URL: "https://d", Method: "GET", Type: "xhr", ResponseBody: strings.Repeat("x", 600)},
	}}}
	got := firstText(t, formatNetwork(resp))
	if !strings.Contains(got, "[200]") || !strings.Contains(got, "[FAILED]") || !strings.Contains(got, "[pending]") {
		t.Errorf("missing status variants: %q", got)
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("large body not truncated: %q", got)
	}
}

// --- formatConsole ---

func TestFormatConsole_Empty(t *testing.T) {
	if got := firstText(t, formatConsole(&protocol.Response{})); got != "No console messages captured" {
		t.Errorf("got %q", got)
	}
}

func TestFormatConsole_WithMessages(t *testing.T) {
	resp := &protocol.Response{Data: &protocol.ResponseData{ConsoleMessages: []protocol.ConsoleMessageInfo{
		{Type: "log", Text: "hi"},
		{Type: "error", Text: "oops"},
	}}}
	got := firstText(t, formatConsole(resp))
	if !strings.Contains(got, "[log] hi") || !strings.Contains(got, "[error] oops") {
		t.Errorf("got %q", got)
	}
}

// --- formatErrors ---

func TestFormatErrors_Empty(t *testing.T) {
	if got := firstText(t, formatErrors(&protocol.Response{})); got != "No JavaScript errors captured" {
		t.Errorf("got %q", got)
	}
}

func TestFormatErrors_WithErrors(t *testing.T) {
	line := 42
	resp := &protocol.Response{Data: &protocol.ResponseData{JSErrors: []protocol.JSErrorInfo{
		{Message: "fail", URL: "https://x", LineNumber: &line},
		{Message: "bare"},
	}}}
	got := firstText(t, formatErrors(resp))
	if !strings.Contains(got, "fail") || !strings.Contains(got, "at https://x:42") {
		t.Errorf("got %q", got)
	}
}
