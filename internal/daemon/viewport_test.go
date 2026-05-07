package daemon

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeViewportInfo_DirectObject(t *testing.T) {
	info, err := decodeViewportInfo(json.RawMessage(`{"width":390,"height":844,"dpr":3,"mobile":true,"touch":true}`))
	if err != nil {
		t.Fatalf("decode direct object: %v", err)
	}
	if info.Width != 390 || info.Height != 844 || info.DPR != 3 || !info.Mobile || !info.Touch {
		t.Fatalf("viewport info = %+v", info)
	}
}

func TestDecodeViewportInfo_EncodedObject(t *testing.T) {
	raw, err := json.Marshal(`{"width":1365,"height":900,"dpr":1,"mobile":false,"touch":false}`)
	if err != nil {
		t.Fatalf("marshal encoded object: %v", err)
	}
	info, err := decodeViewportInfo(raw)
	if err != nil {
		t.Fatalf("decode encoded object: %v", err)
	}
	if info.Width != 1365 || info.Height != 900 || info.DPR != 1 || info.Mobile || info.Touch {
		t.Fatalf("viewport info = %+v", info)
	}
}

func TestDecodeViewportInfo_RejectsNull(t *testing.T) {
	_, err := decodeViewportInfo(json.RawMessage(`null`))
	if err == nil {
		t.Fatal("expected null viewport decode to fail")
	}
	if !strings.Contains(err.Error(), "decode viewport info") {
		t.Fatalf("error = %q", err)
	}
}
