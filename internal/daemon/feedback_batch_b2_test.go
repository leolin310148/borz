package daemon

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
)

// A snapshot taken mid-reload sees no document body (both the primary and
// fallback builders return no root). It must wait for a body and retry once
// instead of failing (#129, #149).
func TestBuildSnapshot_RetriesAfterDocumentBodyAppears(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	var bodyReady atomic.Bool
	var builds atomic.Int32
	f.On("Runtime.evaluate", func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		str := func(v string) (interface{}, error) {
			return map[string]interface{}{"result": map[string]interface{}{"type": "string", "value": v}}, nil
		}
		switch {
		case strings.Contains(p.Expression, "document.querySelector(\"body\")"):
			bodyReady.Store(true)
			return str(`{"found":true,"href":"https://a","title":"A","readyState":"complete"}`)
		case strings.Contains(p.Expression, "buildDomTree") || strings.Contains(p.Expression, "fallback: true"):
			builds.Add(1)
			if !bodyReady.Load() {
				return map[string]interface{}{"result": map[string]interface{}{"type": "object", "value": map[string]interface{}{"rootId": "", "map": nil}}}, nil
			}
			return map[string]interface{}{"result": map[string]interface{}{"type": "object", "value": map[string]interface{}{
				"rootId": "1",
				"map": map[string]interface{}{
					"1": map[string]interface{}{"tagName": "body", "xpath": "html/body", "attributes": map[string]string{}, "children": []string{"2"}, "isVisible": true},
					"2": map[string]interface{}{"tagName": "button", "xpath": "html/body/button", "attributes": map[string]string{"aria-label": "Go"}, "children": []string{}, "isVisible": true, "isInteractive": true, "isTopElement": true, "highlightIndex": 0},
				},
			}}}, nil
		}
		return str(`{"readyState":"complete","href":"https://a/"}`)
	})
	c := connectCdp(t, f)
	resp := DispatchRequest(c, &protocol.Request{ID: "s", Action: protocol.ActionSnapshot, Interactive: true})
	if !resp.Success {
		t.Fatalf("snapshot failed: %s", resp.Error)
	}
	if !bodyReady.Load() || builds.Load() < 3 {
		t.Fatalf("expected a body wait and a retry (builds=%d, waited=%v)", builds.Load(), bodyReady.Load())
	}
	if resp.Data.SnapshotData == nil || len(resp.Data.SnapshotData.Refs) != 1 {
		t.Fatalf("retry snapshot refs = %+v", resp.Data.SnapshotData)
	}
}
