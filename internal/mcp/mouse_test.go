package mcp

import (
	"context"
	"github.com/leolin310148/borz/internal/protocol"
	"testing"
)

func TestHandleMouse(t *testing.T) {
	cap := capturingSend(t, ok())
	result, _ := handleMouse(context.Background(), mkReq(map[string]any{"mouseType": "move", "x": 10.5, "y": 20.0, "button": "left", "tab": "abc", "waitFor": "#done"}))
	if result.IsError || cap.req.Action != protocol.ActionMouse || *cap.req.X != 10.5 || cap.req.Button != "left" || cap.req.TabID != "abc" || cap.req.WaitFor != "#done" {
		t.Fatalf("mouse: %+v %+v", result, cap.req)
	}
	for _, args := range []map[string]any{{}, {"mouseType": "click"}, {"mouseType": "click", "x": 1.0, "y": 2.0, "button": "bad"}} {
		result, _ := handleMouse(context.Background(), mkReq(args))
		if !result.IsError {
			t.Fatalf("accepted %v", args)
		}
	}
	capturingSend(t, &protocol.Response{Success: false, Error: "CDP error"})
	result, _ = handleMouse(context.Background(), mkReq(map[string]any{"mouseType": "click", "x": 1.0, "y": 2.0}))
	if !result.IsError {
		t.Fatal("ignored daemon failure")
	}
}
