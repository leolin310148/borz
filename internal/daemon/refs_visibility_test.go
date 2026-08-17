package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
)

func TestDispatchSnapshotRefVisibilityPrecedence(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }
	tests := []struct {
		name        string
		settings    string
		override    *bool
		wantVisible bool
	}{
		{name: "historical default", wantVisible: true},
		{name: "settings hide", settings: `{"snapshot":{"showRefs":false}}`, wantVisible: false},
		{name: "request forces show", settings: `{"snapshot":{"showRefs":false}}`, override: boolPtr(true), wantVisible: true},
		{name: "request forces hide", override: boolPtr(false), wantVisible: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("BORZ_HOME", home)
			if tc.settings != "" {
				if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(tc.settings), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			f := newFakeCDP(t)
			setupOnePage(f, "T1", "https://a", "A")
			var expression string
			f.On("Runtime.evaluate", func(params json.RawMessage) (interface{}, error) {
				var p struct {
					Expression string `json:"expression"`
				}
				_ = json.Unmarshal(params, &p)
				expression = p.Expression
				return map[string]interface{}{"result": map[string]interface{}{"value": map[string]interface{}{
					"rootId": "r",
					"map": map[string]interface{}{
						"r": map[string]interface{}{"tagName": "body", "xpath": "/body", "children": []string{}},
					},
				}}}, nil
			})
			c := connectCdp(t, f)

			resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionSnapshot, ShowRefs: tc.override})
			if !resp.Success {
				t.Fatalf("snapshot: %+v", resp)
			}
			want := `"showHighlightElements":false`
			if tc.wantVisible {
				want = `"showHighlightElements":true`
			}
			if !strings.Contains(expression, want) {
				t.Fatalf("snapshot expression missing %s", want)
			}
		})
	}
}

func TestDispatchSnapshotRejectsInvalidSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{broken`), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Runtime.evaluate", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"result": map[string]interface{}{"value": map[string]interface{}{
			"rootId": "r", "map": map[string]interface{}{"r": map[string]interface{}{"tagName": "body", "children": []string{}}},
		}}}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionSnapshot})
	if resp.Success || !strings.Contains(resp.Error, "settings.json") {
		t.Fatalf("invalid settings response = %+v", resp)
	}

	showRefs := true
	resp = DispatchRequest(c, &protocol.Request{ID: "override", Action: protocol.ActionSnapshot, ShowRefs: &showRefs})
	if !resp.Success {
		t.Fatalf("explicit override should bypass invalid settings: %+v", resp)
	}
}

func TestDispatchClearRefsKeepsLatestRefMap(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)
	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "7", &protocol.RefInfo{Role: "button", Name: "Save"})

	var expression string
	f.On("Runtime.evaluate", func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		expression = p.Expression
		return map[string]interface{}{"result": map[string]interface{}{"value": true}}, nil
	})

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionClearRefs})
	if !resp.Success {
		t.Fatalf("clear refs: %+v", resp)
	}
	if !strings.Contains(expression, "playwright-highlight-container") {
		t.Fatalf("clear expression = %q", expression)
	}
	if tab := c.TabManager.GetTab("T1"); tab == nil || tab.Refs["7"] == nil {
		t.Fatal("clear refs invalidated the latest ref map")
	}
}
