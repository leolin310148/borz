package daemon

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
)

// fakeBuildDomTree returns a function suitable for f.On("Runtime.evaluate", ...)
// that responds to buildDomTree calls with the given pre-built tree (one tree
// per call, in order). Other expressions return undefined.
func fakeBuildDomTreeSequence(trees ...*buildDomTreeResult) func(json.RawMessage) (interface{}, error) {
	var idx atomic.Int32
	return func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		if !strings.Contains(p.Expression, "buildDomTree") {
			return map[string]interface{}{"result": map[string]interface{}{"type": "undefined"}}, nil
		}
		i := int(idx.Add(1)) - 1
		if i >= len(trees) {
			i = len(trees) - 1
		}
		blob, _ := json.Marshal(trees[i])
		return map[string]interface{}{
			"result": map[string]interface{}{"type": "object", "value": json.RawMessage(blob)},
		}, nil
	}
}

func TestDispatch_Snapshot_Diff_FirstCallIsBaseline(t *testing.T) {
	tree := makeTree(t, "root", map[string]interface{}{
		"root": rawDomElementNode{TagName: "main", XPath: "/m", Children: []string{"btn"}},
		"btn": rawDomElementNode{TagName: "button", XPath: "/m/btn", HighlightIndex: ptrInt(1),
			Attributes: map[string]string{"id": "submit", "aria-label": "Submit"}},
	})
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://x", "X")
	f.On("Runtime.evaluate", fakeBuildDomTreeSequence(tree))
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "1", Action: protocol.ActionSnapshot, Diff: true})
	if !resp.Success {
		t.Fatalf("snapshot --diff failed: %+v", resp)
	}
	if resp.Data == nil || resp.Data.SnapshotDiffData == nil {
		t.Fatalf("expected SnapshotDiffData on response, got %+v", resp.Data)
	}
	dd := resp.Data.SnapshotDiffData
	if !dd.BaselineReset {
		t.Fatal("first --diff call must be a baseline reset")
	}
	if dd.Stats.Added == 0 {
		t.Fatalf("baseline reset should report nodes as added: %+v", dd.Stats)
	}
}

func TestDispatch_Snapshot_Diff_DetectsAttributeFlip(t *testing.T) {
	mk := func(disabled string) *buildDomTreeResult {
		return makeTree(t, "root", map[string]interface{}{
			"root": rawDomElementNode{TagName: "main", XPath: "/m", Children: []string{"btn"}},
			"btn": rawDomElementNode{TagName: "button", XPath: "/m/btn",
				HighlightIndex: ptrInt(1),
				Attributes: map[string]string{
					"id": "submit", "aria-label": "Submit", "aria-disabled": disabled,
				}},
		})
	}
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://x", "X")
	f.On("Runtime.evaluate", fakeBuildDomTreeSequence(mk("true"), mk("false")))
	c := connectCdp(t, f)

	// First call seeds the baseline (no --diff so we don't pay attention to the diff result here).
	if r := DispatchRequest(c, &protocol.Request{ID: "1", Action: protocol.ActionSnapshot}); !r.Success {
		t.Fatalf("seed snapshot failed: %+v", r)
	}
	// Second call asks for --diff, should detect aria-disabled true→false.
	resp := DispatchRequest(c, &protocol.Request{ID: "2", Action: protocol.ActionSnapshot, Diff: true})
	if !resp.Success {
		t.Fatalf("diff snapshot failed: %+v", resp)
	}
	dd := resp.Data.SnapshotDiffData
	if dd == nil {
		t.Fatal("expected diff data")
	}
	if dd.BaselineReset {
		t.Fatal("second snapshot on same URL must not be a baseline reset")
	}
	if dd.Stats.Changed != 1 {
		t.Fatalf("expected 1 change, got stats %+v", dd.Stats)
	}
	got := dd.Changed[0].AttrChanges["aria-disabled"]
	if got.Old != "true" || got.New != "false" {
		t.Fatalf("aria-disabled delta: %+v", got)
	}
}

func TestDispatch_Snapshot_NoDiff_DoesNotReturnDiffData(t *testing.T) {
	tree := makeTree(t, "root", map[string]interface{}{
		"root": rawDomElementNode{TagName: "button", XPath: "/b", HighlightIndex: ptrInt(1),
			Attributes: map[string]string{"aria-label": "Go"}},
	})
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://x", "X")
	f.On("Runtime.evaluate", fakeBuildDomTreeSequence(tree))
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "1", Action: protocol.ActionSnapshot})
	if !resp.Success {
		t.Fatalf("snapshot failed: %+v", resp)
	}
	if resp.Data.SnapshotDiffData != nil {
		t.Fatalf("plain snapshot must not include SnapshotDiffData, got %+v", resp.Data.SnapshotDiffData)
	}
	if resp.Data.SnapshotData == nil {
		t.Fatal("plain snapshot must include SnapshotData")
	}
}

func TestDispatch_Snapshot_NoDiff_StillSeedsBaseline(t *testing.T) {
	// A snapshot without --diff still has to capture the baseline so the next
	// call with --diff has something to compare against.
	mk := func(label string) *buildDomTreeResult {
		return makeTree(t, "root", map[string]interface{}{
			"root": rawDomElementNode{TagName: "button", XPath: "/b", HighlightIndex: ptrInt(1),
				Attributes: map[string]string{"id": "go", "aria-label": label}},
		})
	}
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://x", "X")
	f.On("Runtime.evaluate", fakeBuildDomTreeSequence(mk("Go"), mk("Going…")))
	c := connectCdp(t, f)

	if r := DispatchRequest(c, &protocol.Request{ID: "1", Action: protocol.ActionSnapshot}); !r.Success {
		t.Fatalf("seed: %+v", r)
	}
	resp := DispatchRequest(c, &protocol.Request{ID: "2", Action: protocol.ActionSnapshot, Diff: true})
	if !resp.Success {
		t.Fatalf("diff: %+v", resp)
	}
	dd := resp.Data.SnapshotDiffData
	if dd == nil || dd.BaselineReset {
		t.Fatalf("expected non-reset diff after a plain snapshot seeded the baseline, got %+v", dd)
	}
	if dd.Stats.Changed != 1 || dd.Changed[0].NameChanged == nil ||
		dd.Changed[0].NameChanged.Old != "Go" || dd.Changed[0].NameChanged.New != "Going…" {
		t.Fatalf("expected name change Go→Going…, got %+v", dd.Changed)
	}
}

func TestDispatch_Snapshot_TextMode_RejectsDiff(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://x", "X")
	c := connectCdp(t, f)
	resp := DispatchRequest(c, &protocol.Request{
		ID: "1", Action: protocol.ActionSnapshot, Mode: "text", Diff: true,
	})
	if resp.Success {
		t.Fatal("text mode + --diff must error out, but call succeeded")
	}
	if !strings.Contains(strings.ToLower(resp.Error), "diff") {
		t.Fatalf("error should mention diff, got %q", resp.Error)
	}
}
