package daemon

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/protocol"
)

func intP(v int) *int { return &v }

func TestTabStateManager_AddResolveRemove(t *testing.T) {
	m := NewTabStateManager()

	tab := m.AddTab("target-ABCDEF1234567890")
	if tab == nil || tab.TargetID != "target-ABCDEF1234567890" {
		t.Fatalf("AddTab did not return expected state: %+v", tab)
	}
	if tab.ShortID == "" {
		t.Fatal("expected non-empty short id")
	}

	// Idempotent: same targetID returns same instance.
	if again := m.AddTab("target-ABCDEF1234567890"); again != tab {
		t.Fatal("AddTab should return existing state for same target id")
	}

	if got := m.ResolveShortID(tab.ShortID); got != tab.TargetID {
		t.Fatalf("ResolveShortID: got %q, want %q", got, tab.TargetID)
	}
	if got := m.GetShortID(tab.TargetID); got != tab.ShortID {
		t.Fatalf("GetShortID: got %q, want %q", got, tab.ShortID)
	}
	if got := m.GetTab(tab.TargetID); got != tab {
		t.Fatalf("GetTab mismatch")
	}

	if all := m.AllTabs(); len(all) != 1 {
		t.Fatalf("AllTabs len: got %d, want 1", len(all))
	}

	m.RemoveTab(tab.TargetID)
	if m.GetTab(tab.TargetID) != nil {
		t.Fatal("GetTab after remove should be nil")
	}
	if m.ResolveShortID(tab.ShortID) != "" {
		t.Fatal("short id mapping should be cleared after remove")
	}

	// Removing non-existent tab is a no-op.
	m.RemoveTab("does-not-exist")
}

func TestTabStateManager_ShortIDCollision(t *testing.T) {
	m := NewTabStateManager()
	// Two ids that share the same last 4 chars force the collision path
	// and grow length until unique.
	a := m.AddTab("XXXXAAAA1234")
	b := m.AddTab("YYYYBBBB1234")
	if a.ShortID == b.ShortID {
		t.Fatalf("short ids should differ: %q vs %q", a.ShortID, b.ShortID)
	}
	if len(b.ShortID) <= len(a.ShortID) {
		// b was forced to grow because the 4-char suffix "1234" was taken.
		t.Fatalf("expected b short id to be longer than a's (a=%q b=%q)", a.ShortID, b.ShortID)
	}
}

func TestTabStateManager_AllTabsDeterministicOrder(t *testing.T) {
	m := NewTabStateManager()
	newer := m.AddTab("target-newer")
	tieB := m.AddTab("target-tie-b")
	older := m.AddTab("target-older")
	tieA := m.AddTab("target-tie-a")

	base := time.Unix(100, 0)
	newer.CreatedAt = base.Add(2 * time.Second)
	tieB.CreatedAt = base.Add(time.Second)
	older.CreatedAt = base
	tieA.CreatedAt = base.Add(time.Second)

	got := m.AllTabs()
	want := []string{"target-older", "target-tie-a", "target-tie-b", "target-newer"}
	if len(got) != len(want) {
		t.Fatalf("AllTabs len: got %d want %d", len(got), len(want))
	}
	for i, tab := range got {
		if tab.TargetID != want[i] {
			t.Fatalf("AllTabs[%d].TargetID = %q, want %q (full order: %+v)", i, tab.TargetID, want[i], got)
		}
	}
}

func TestTabStateManager_NextAndCurrentSeq(t *testing.T) {
	m := NewTabStateManager()
	if m.CurrentSeq() != 0 {
		t.Fatalf("initial CurrentSeq: got %d want 0", m.CurrentSeq())
	}
	if got := m.NextSeq(); got != 1 {
		t.Fatalf("first NextSeq: got %d want 1", got)
	}
	if got := m.NextSeq(); got != 2 {
		t.Fatalf("second NextSeq: got %d want 2", got)
	}
	if got := m.CurrentSeq(); got != 2 {
		t.Fatalf("CurrentSeq after two NextSeq: got %d want 2", got)
	}
}

func TestTabState_RecordActionUsesNextSeq(t *testing.T) {
	m := NewTabStateManager()
	tab := m.AddTab("t1")
	seq := tab.RecordAction()
	if seq != 1 {
		t.Fatalf("RecordAction seq: got %d want 1", seq)
	}
	if tab.LastActionSeq != seq {
		t.Fatalf("LastActionSeq: got %d want %d", tab.LastActionSeq, seq)
	}
	if next := tab.RecordAction(); next != 2 {
		t.Fatalf("second RecordAction: got %d want 2", next)
	}
}

func TestTabState_NetworkLifecycle(t *testing.T) {
	m := NewTabStateManager()
	tab := m.AddTab("t1")

	tab.AddNetworkRequest("r1", protocol.NetworkRequestInfo{URL: "https://api.example.com/a", Method: "GET"})
	tab.AddNetworkRequest("r2", protocol.NetworkRequestInfo{URL: "https://api.example.com/b", Method: "POST"})

	// Update response for r1.
	tab.UpdateNetworkResponse("r1", intP(200), "OK", map[string]string{"x": "y"}, "application/json")
	// Update on unknown request id: no-op.
	tab.UpdateNetworkResponse("unknown", intP(500), "", nil, "")

	// Mark r2 failed.
	tab.UpdateNetworkFailure("r2", "net::ERR_FAIL")
	tab.UpdateNetworkFailure("unknown", "ignored")

	got := tab.GetNetworkRequests(QueryOptions{}).Items
	if len(got) != 2 {
		t.Fatalf("GetNetworkRequests len: got %d want 2", len(got))
	}

	var r1, r2 *protocol.NetworkRequestInfo
	for _, it := range got {
		switch it.RequestID {
		case "r1":
			it := it
			r1 = &it
		case "r2":
			it := it
			r2 = &it
		}
	}
	if r1 == nil || r2 == nil {
		t.Fatal("missing r1/r2 in results")
	}

	if r1.Status == nil || *r1.Status != 200 || r1.StatusText != "OK" || r1.ResponseHeaders["x"] != "y" || r1.MimeType != "application/json" {
		t.Fatalf("r1 response fields not updated: %+v", *r1)
	}
	if !r2.Failed || r2.FailureReason != "net::ERR_FAIL" {
		t.Fatalf("r2 failure fields not updated: %+v", *r2)
	}
}

func TestTabState_GetNetworkRequests_Filters(t *testing.T) {
	m := NewTabStateManager()
	tab := m.AddTab("t1")

	// Seed with distinct items and explicit Status so matchStatus has something to match.
	tab.NetworkRequests.Push(protocol.NetworkRequestInfo{RequestID: "1", URL: "https://a.test/x", Method: "GET", Status: intP(200), Seq: 1})
	tab.NetworkRequests.Push(protocol.NetworkRequestInfo{RequestID: "2", URL: "https://b.test/y", Method: "POST", Status: intP(404), Seq: 2})
	tab.NetworkRequests.Push(protocol.NetworkRequestInfo{RequestID: "3", URL: "https://b.test/z", Method: "GET", Status: intP(503), Seq: 3})
	tab.NetworkRequests.Push(protocol.NetworkRequestInfo{RequestID: "4", URL: "https://c.test/w", Method: "GET", Seq: 4}) // no status

	res := tab.GetNetworkRequests(QueryOptions{Filter: "b.test"})
	if len(res.Items) != 2 {
		t.Fatalf("filter b.test: got %d want 2", len(res.Items))
	}

	res = tab.GetNetworkRequests(QueryOptions{Method: "post"})
	if len(res.Items) != 1 || res.Items[0].RequestID != "2" {
		t.Fatalf("method post: got %+v", res.Items)
	}
	res = tab.GetNetworkRequests(QueryOptions{Method: " POST "})
	if len(res.Items) != 1 || res.Items[0].RequestID != "2" {
		t.Fatalf("method post with spaces: got %+v", res.Items)
	}
	res = tab.GetNetworkRequests(QueryOptions{Method: "   "})
	if len(res.Items) != 4 {
		t.Fatalf("blank method filter: got %d want 4", len(res.Items))
	}

	res = tab.GetNetworkRequests(QueryOptions{Status: "4xx"})
	if len(res.Items) != 1 || res.Items[0].RequestID != "2" {
		t.Fatalf("status 4xx: got %+v", res.Items)
	}
	res = tab.GetNetworkRequests(QueryOptions{Status: "5xx"})
	if len(res.Items) != 1 || res.Items[0].RequestID != "3" {
		t.Fatalf("status 5xx: got %+v", res.Items)
	}
	res = tab.GetNetworkRequests(QueryOptions{Status: "404"})
	if len(res.Items) != 1 || res.Items[0].RequestID != "2" {
		t.Fatalf("status 404: got %+v", res.Items)
	}
	res = tab.GetNetworkRequests(QueryOptions{Status: "not-a-number"})
	if len(res.Items) != 0 {
		t.Fatalf("status invalid pattern: got %+v", res.Items)
	}
	res = tab.GetNetworkRequests(QueryOptions{Status: "   "})
	if len(res.Items) != 4 {
		t.Fatalf("blank status filter: got %d want 4", len(res.Items))
	}

	// Since: int threshold excludes earlier seqs.
	res = tab.GetNetworkRequests(QueryOptions{Since: 2})
	if len(res.Items) != 2 {
		t.Fatalf("since int: got %d want 2", len(res.Items))
	}
	// Cursor should be the max seq in the filtered list.
	if res.Cursor != 4 {
		t.Fatalf("cursor: got %d want 4", res.Cursor)
	}

	// Since as float64 (JSON decoding path).
	res = tab.GetNetworkRequests(QueryOptions{Since: float64(3)})
	if len(res.Items) != 1 {
		t.Fatalf("since float64: got %d want 1", len(res.Items))
	}

	// Since as string numeric.
	res = tab.GetNetworkRequests(QueryOptions{Since: "2"})
	if len(res.Items) != 2 {
		t.Fatalf("since string: got %d want 2", len(res.Items))
	}
	res = tab.GetNetworkRequests(QueryOptions{Since: " 2 "})
	if len(res.Items) != 2 {
		t.Fatalf("since string with spaces: got %d want 2", len(res.Items))
	}

	// Since "last_action" uses LastActionSeq.
	tab.LastActionSeq = 2
	res = tab.GetNetworkRequests(QueryOptions{Since: "last_action"})
	if len(res.Items) != 2 {
		t.Fatalf("since last_action: got %d want 2", len(res.Items))
	}
	res = tab.GetNetworkRequests(QueryOptions{Since: " LAST_ACTION "})
	if len(res.Items) != 2 {
		t.Fatalf("since last_action case/space: got %d want 2", len(res.Items))
	}

	// Unknown since type falls through to 0.
	res = tab.GetNetworkRequests(QueryOptions{Since: true})
	if len(res.Items) != 4 {
		t.Fatalf("since bool (invalid): got %d want 4", len(res.Items))
	}

	// Limit trims to last N.
	res = tab.GetNetworkRequests(QueryOptions{Limit: 2})
	if len(res.Items) != 2 || res.Items[0].RequestID != "3" || res.Items[1].RequestID != "4" {
		t.Fatalf("limit 2: got %+v", res.Items)
	}
}

func TestTabState_GetConsoleMessages_Filters(t *testing.T) {
	m := NewTabStateManager()
	tab := m.AddTab("t1")

	tab.AddConsoleMessage(protocol.ConsoleMessageInfo{Type: "log", Text: "hello world"})
	tab.AddConsoleMessage(protocol.ConsoleMessageInfo{Type: "error", Text: "oops boom"})
	tab.AddConsoleMessage(protocol.ConsoleMessageInfo{Type: "warn", Text: "warning!"})

	res := tab.GetConsoleMessages(QueryOptions{Filter: "boom"})
	if len(res.Items) != 1 || res.Items[0].Type != "error" {
		t.Fatalf("filter boom: got %+v", res.Items)
	}

	res = tab.GetConsoleMessages(QueryOptions{Limit: 2})
	if len(res.Items) != 2 {
		t.Fatalf("limit: got %d want 2", len(res.Items))
	}

	res = tab.GetConsoleMessages(QueryOptions{Since: 1})
	if len(res.Items) != 2 {
		t.Fatalf("since 1: got %d want 2", len(res.Items))
	}
	if res.Cursor < 2 {
		t.Fatalf("cursor: got %d want >=2", res.Cursor)
	}
}

func TestTabState_GetJSErrors_Filters(t *testing.T) {
	m := NewTabStateManager()
	tab := m.AddTab("t1")

	tab.AddJSError(protocol.JSErrorInfo{Message: "TypeError: foo"})
	tab.AddJSError(protocol.JSErrorInfo{Message: "other", URL: "https://x.test/script.js"})
	tab.AddJSError(protocol.JSErrorInfo{Message: "unrelated"})

	// Filter matches in message.
	res := tab.GetJSErrors(QueryOptions{Filter: "TypeError"})
	if len(res.Items) != 1 {
		t.Fatalf("filter msg: got %+v", res.Items)
	}

	// Filter matches in URL when message doesn't match.
	res = tab.GetJSErrors(QueryOptions{Filter: "x.test"})
	if len(res.Items) != 1 {
		t.Fatalf("filter url: got %+v", res.Items)
	}

	// Filter matching nothing.
	res = tab.GetJSErrors(QueryOptions{Filter: "zzz"})
	if len(res.Items) != 0 {
		t.Fatalf("filter none: got %+v", res.Items)
	}

	res = tab.GetJSErrors(QueryOptions{Limit: 2})
	if len(res.Items) != 2 {
		t.Fatalf("limit: got %d want 2", len(res.Items))
	}

	res = tab.GetJSErrors(QueryOptions{Since: 1})
	if len(res.Items) != 2 {
		t.Fatalf("since: got %d want 2", len(res.Items))
	}
}

func TestTabState_Clear(t *testing.T) {
	m := NewTabStateManager()
	tab := m.AddTab("t1")

	tab.AddNetworkRequest("r1", protocol.NetworkRequestInfo{URL: "u"})
	tab.AddConsoleMessage(protocol.ConsoleMessageInfo{Text: "c"})
	tab.AddJSError(protocol.JSErrorInfo{Message: "e"})

	tab.ClearNetwork()
	if len(tab.GetNetworkRequests(QueryOptions{}).Items) != 0 {
		t.Fatal("ClearNetwork did not empty ring")
	}
	// After clear, updating a previously known id must be a no-op.
	tab.UpdateNetworkResponse("r1", intP(200), "", nil, "")
	tab.UpdateNetworkFailure("r1", "x")

	tab.ClearConsole()
	if len(tab.GetConsoleMessages(QueryOptions{}).Items) != 0 {
		t.Fatal("ClearConsole did not empty ring")
	}

	tab.ClearErrors()
	if len(tab.GetJSErrors(QueryOptions{}).Items) != 0 {
		t.Fatal("ClearErrors did not empty ring")
	}
}

func TestTabState_ConcurrentEventAccess(t *testing.T) {
	m := NewTabStateManager()
	tab := m.AddTab("t1")

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				requestID := fmt.Sprintf("r-%d-%d", worker, i)
				tab.AddNetworkRequest(requestID, protocol.NetworkRequestInfo{URL: "https://example.test", Method: "GET"})
				tab.UpdateNetworkResponse(requestID, intP(200), "OK", nil, "")
				tab.UpdateNetworkFailure(requestID, "net::ERR_FAILED")
				tab.AddConsoleMessage(protocol.ConsoleMessageInfo{Text: requestID})
				tab.AddJSError(protocol.JSErrorInfo{Message: requestID})
				tab.RecordAction()

				_ = tab.GetNetworkRequests(QueryOptions{Limit: 5})
				_ = tab.GetConsoleMessages(QueryOptions{Limit: 5})
				_ = tab.GetJSErrors(QueryOptions{Limit: 5})
				_, _, _ = tab.EventCounts()
				_ = tab.LastActionSequence()

				if i%20 == 0 {
					tab.ClearConsole()
					tab.ClearErrors()
				}
			}
		}(worker)
	}
	wg.Wait()
}

func TestMatchStatus(t *testing.T) {
	if matchStatus(nil, "4xx") {
		t.Fatal("nil status should never match")
	}
	if !matchStatus(intP(404), "4xx") {
		t.Fatal("404 should match 4xx")
	}
	if !matchStatus(intP(404), " 4XX ") {
		t.Fatal("404 should match case-insensitive 4xx with surrounding space")
	}
	if matchStatus(intP(500), "4xx") {
		t.Fatal("500 should not match 4xx")
	}
	if !matchStatus(intP(204), "2xx") {
		t.Fatal("204 should match 2xx")
	}
	if !matchStatus(intP(302), "3xx") {
		t.Fatal("302 should match 3xx")
	}
	if !matchStatus(intP(502), "5xx") {
		t.Fatal("502 should match 5xx")
	}
	if matchStatus(intP(200), "6xx") {
		t.Fatal("unsupported status class should not match")
	}
	if !matchStatus(intP(200), "200") {
		t.Fatal("exact match 200 failed")
	}
	if !matchStatus(intP(200), " 200 ") {
		t.Fatal("exact match 200 with surrounding space failed")
	}
	if matchStatus(intP(200), "not-a-number") {
		t.Fatal("invalid pattern should not match")
	}
}
