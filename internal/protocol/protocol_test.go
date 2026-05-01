package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequest_OmitEmpty(t *testing.T) {
	req := Request{ID: "abc", Action: ActionOpen}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	want := `{"id":"abc","action":"open"}`
	if got != want {
		t.Fatalf("marshal minimal Request = %s, want %s", got, want)
	}
}

func TestRequest_RoundTrip(t *testing.T) {
	px := 5
	y := 1.5
	touch := true
	in := Request{
		ID:         "id1",
		Action:     ActionClick,
		URL:        "https://example.com",
		New:        true,
		Ref:        "r3",
		Text:       "hello",
		Key:        "Enter",
		Direction:  "down",
		Pixels:     &px,
		Attribute:  "href",
		MaxDepth:   &px,
		TabID:      "tab-1",
		Index:      &px,
		Modifiers:  []string{"Shift", "Alt"},
		MouseType:  "click",
		Y:          &y,
		Button:     "left",
		ClickCount: &px,
		Viewport:   &ViewportOptions{Width: 390, Height: 844, DPR: 3, Mobile: true, Touch: &touch},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Request
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID != in.ID || out.Action != in.Action || out.URL != in.URL || !out.New {
		t.Errorf("round-trip mismatch: %+v", out)
	}
	if out.Pixels == nil || *out.Pixels != px {
		t.Errorf("Pixels not preserved: %+v", out.Pixels)
	}
	if out.Y == nil || *out.Y != y {
		t.Errorf("Y not preserved: %+v", out.Y)
	}
	if len(out.Modifiers) != 2 || out.Modifiers[1] != "Alt" {
		t.Errorf("Modifiers not preserved: %+v", out.Modifiers)
	}
	if out.Viewport == nil || out.Viewport.Width != 390 || out.Viewport.Touch == nil || !*out.Viewport.Touch {
		t.Errorf("Viewport not preserved: %+v", out.Viewport)
	}
}

func TestRequest_TabIDNumberOrString(t *testing.T) {
	// Numeric
	raw := []byte(`{"id":"x","action":"tab_select","tabId":42}`)
	var r Request
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("unmarshal num: %v", err)
	}
	if f, ok := r.TabID.(float64); !ok || f != 42 {
		t.Errorf("TabID numeric = %v (%T), want 42", r.TabID, r.TabID)
	}

	// String
	raw = []byte(`{"id":"x","action":"tab_select","tabId":"abc"}`)
	r = Request{}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("unmarshal str: %v", err)
	}
	if s, ok := r.TabID.(string); !ok || s != "abc" {
		t.Errorf("TabID string = %v, want abc", r.TabID)
	}
}

func TestResponse_SuccessShape(t *testing.T) {
	seq := 10
	resp := Response{
		ID:      "x",
		Success: true,
		Data: &ResponseData{
			URL:   "https://e.com",
			Title: "t",
			Seq:   &seq,
		},
	}
	b, _ := json.Marshal(resp)
	s := string(b)
	if !strings.Contains(s, `"success":true`) {
		t.Errorf("missing success: %s", s)
	}
	if strings.Contains(s, `"error"`) {
		t.Errorf("error should be omitted: %s", s)
	}
	if !strings.Contains(s, `"seq":10`) {
		t.Errorf("seq not serialized: %s", s)
	}
}

func TestResponse_ErrorShape(t *testing.T) {
	resp := Response{ID: "x", Success: false, Error: "boom"}
	b, _ := json.Marshal(resp)
	s := string(b)
	if !strings.Contains(s, `"error":"boom"`) {
		t.Errorf("missing error: %s", s)
	}
	if strings.Contains(s, `"data"`) {
		t.Errorf("data should be omitted: %s", s)
	}
}

func TestActionTypeConstants(t *testing.T) {
	// Sanity: ensure string values line up with wire format.
	pairs := map[ActionType]string{
		ActionOpen:       "open",
		ActionSnapshot:   "snapshot",
		ActionType_:      "type",
		ActionScreenshot: "screenshot",
		ActionViewport:   "viewport",
		ActionTabList:    "tab_list",
		ActionKey:        "key",
		ActionMouse:      "mouse",
	}
	for k, v := range pairs {
		if string(k) != v {
			t.Errorf("%v != %q", k, v)
		}
	}
}

func TestViewportPreset(t *testing.T) {
	mobile, ok := ViewportPreset("mobile")
	if !ok || mobile.Width != 390 || mobile.Height != 844 || !mobile.Mobile || mobile.Touch == nil || !*mobile.Touch {
		t.Fatalf("mobile preset = %+v ok=%v", mobile, ok)
	}
	if _, ok := ViewportPreset("unknown"); ok {
		t.Fatal("unknown preset should not resolve")
	}
}

func TestSnapshotData_RoundTrip(t *testing.T) {
	sd := SnapshotData{
		Snapshot: "root\n  button [ref=1]",
		Refs: map[string]*RefInfo{
			"1": {BackendDOMNodeID: 42, Role: "button", Name: "OK", TagName: "BUTTON"},
		},
		Elements: []*ElementInfo{
			{Ref: "1", BackendDOMNodeID: 42, Role: "button", Name: "OK", TagName: "BUTTON"},
		},
	}
	b, err := json.Marshal(sd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"elements"`) {
		t.Errorf("elements field missing in JSON: %s", b)
	}
	var got SnapshotData
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Refs["1"].BackendDOMNodeID != 42 || got.Refs["1"].Role != "button" {
		t.Errorf("ref mismatch: %+v", got.Refs["1"])
	}
	if len(got.Elements) != 1 || got.Elements[0].Ref != "1" || got.Elements[0].Role != "button" {
		t.Errorf("elements mismatch: %+v", got.Elements)
	}
}

func TestNetworkRequestInfo_OmitEmpty(t *testing.T) {
	n := NetworkRequestInfo{RequestID: "r1", URL: "u", Method: "GET", Type: "xhr", Timestamp: 1}
	b, _ := json.Marshal(n)
	s := string(b)
	// unset maps/pointers should not appear
	for _, k := range []string{"status", "requestHeaders", "requestBody", "responseHeaders"} {
		if strings.Contains(s, `"`+k+`"`) {
			t.Errorf("expected %q omitted: %s", k, s)
		}
	}
}

func TestTabInfo_JSON(t *testing.T) {
	tab := TabInfo{Index: 0, URL: "https://x", Title: "T", Active: true, TabID: 7}
	b, _ := json.Marshal(tab)
	var out TabInfo
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Index != 0 || out.URL != "https://x" || !out.Active {
		t.Errorf("tab mismatch: %+v", out)
	}
}

func TestTraceEvent_Pointers(t *testing.T) {
	ref := 3
	checked := true
	px := 200
	ev := TraceEvent{Type: "click", Timestamp: 1, URL: "u", Ref: &ref, Checked: &checked, Pixels: &px}
	b, _ := json.Marshal(ev)
	var out TraceEvent
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Ref == nil || *out.Ref != 3 || out.Checked == nil || !*out.Checked {
		t.Errorf("trace pointers not preserved: %+v", out)
	}
}

func TestDaemonInfo_JSON(t *testing.T) {
	d := DaemonInfo{PID: 100, Host: "127.0.0.1", Port: 19824, Token: "tok"}
	b, _ := json.Marshal(d)
	var out DaemonInfo
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != d {
		t.Errorf("daemon info mismatch: %+v", out)
	}
}
