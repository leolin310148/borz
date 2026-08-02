package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
)

func TestHandlePress_ModifiersAndCommands(t *testing.T) {
	// Malformed arrays are rejected before the request is built.
	res, _ := handlePress(context.Background(), mkReq(map[string]any{"key": "a", "modifiers": "meta"}))
	if !res.IsError {
		t.Error("non-array modifiers should error")
	}
	res, _ = handlePress(context.Background(), mkReq(map[string]any{"key": "a", "commands": []any{42}}))
	if !res.IsError {
		t.Error("non-string command should error")
	}

	cap := capturingSend(t, ok())
	_, _ = handlePress(context.Background(), mkReq(map[string]any{
		"key": "a", "modifiers": []any{"meta"}, "commands": []any{"selectAll", " "},
	}))
	if len(cap.req.Modifiers) != 1 || cap.req.Modifiers[0] != "meta" {
		t.Errorf("modifiers = %v", cap.req.Modifiers)
	}
	if len(cap.req.Commands) != 1 || cap.req.Commands[0] != "selectAll" {
		t.Errorf("commands = %v", cap.req.Commands)
	}
}

func TestHandleTabFront(t *testing.T) {
	cap := capturingSend(t, ok())
	_, _ = handleTabFront(context.Background(), mkReq(map[string]any{"tab": "T1"}))
	if cap.req.Action != protocol.ActionTabFront {
		t.Errorf("action = %v", cap.req.Action)
	}
	if cap.req.TabID != "T1" {
		t.Errorf("tab = %v", cap.req.TabID)
	}
}

func TestHandlePageVisibility(t *testing.T) {
	res, _ := handlePageVisibility(context.Background(), mkReq(map[string]any{"state": "sideways"}))
	if !res.IsError {
		t.Error("an unknown state should error")
	}

	cap := capturingSend(t, ok())
	_, _ = handlePageVisibility(context.Background(), mkReq(map[string]any{"state": "VISIBLE", "tab": "T2"}))
	if cap.req.Action != protocol.ActionPageVisibility {
		t.Errorf("action = %v", cap.req.Action)
	}
	if cap.req.Visibility != "visible" {
		t.Errorf("visibility = %q (state should be lowercased)", cap.req.Visibility)
	}
	if cap.req.TabID != "T2" {
		t.Errorf("tab = %v", cap.req.TabID)
	}

	// Omitting state is a status read.
	cap = capturingSend(t, ok())
	_, _ = handlePageVisibility(context.Background(), mkReq(nil))
	if cap.req.Visibility != "" {
		t.Errorf("status read should send no visibility, got %q", cap.req.Visibility)
	}
}

func TestHandleDialog(t *testing.T) {
	// A typo must not silently arm "accept" on a destructive confirm().
	res, _ := handleDialog(context.Background(), mkReq(map[string]any{"command": "dismis"}))
	if !res.IsError {
		t.Error("unknown dialog command should error")
	}

	cap := capturingSend(t, ok())
	_, _ = handleDialog(context.Background(), mkReq(map[string]any{
		"command": "dismiss", "promptText": "Leo", "tab": "T1",
	}))
	if cap.req.Action != protocol.ActionDialog || cap.req.DialogResponse != "dismiss" {
		t.Errorf("request = %+v", cap.req)
	}
	if cap.req.PromptText != "Leo" || cap.req.TabID != "T1" {
		t.Errorf("request = %+v", cap.req)
	}

	// No command keeps the CLI's historical default.
	cap = capturingSend(t, ok())
	_, _ = handleDialog(context.Background(), mkReq(nil))
	if cap.req.DialogResponse != "accept" {
		t.Errorf("default command = %q", cap.req.DialogResponse)
	}

	// The daemon's own message wins, so the model can tell "answered the open
	// dialog" from "armed the next one".
	capturingSend(t, &protocol.Response{Success: true, Data: &protocol.ResponseData{
		DialogInfo: map[string]interface{}{"type": "handled", "message": "Open confirm dialog dismissed: Delete?"},
	}})
	res, _ = handleDialog(context.Background(), mkReq(map[string]any{"command": "dismiss"}))
	if got := firstText(t, res); got != "Open confirm dialog dismissed: Delete?" {
		t.Errorf("handled text = %q", got)
	}
}

func TestHandleDialog_StatusRendersOpenDialog(t *testing.T) {
	capturingSend(t, &protocol.Response{Success: true, Data: &protocol.ResponseData{
		DialogInfo: map[string]interface{}{
			"type":    "status",
			"armed":   true,
			"action":  "accept",
			"blocked": true,
			"pending": map[string]interface{}{
				"type": "confirm", "message": "Delete this record?", "defaultPrompt": "",
			},
			"history": []interface{}{
				map[string]interface{}{"type": "alert", "message": "saved", "autoHandled": true, "handledAs": "accept"},
				map[string]interface{}{"type": "prompt", "message": "name?", "autoHandled": false},
			},
		},
	}})
	res, _ := handleDialog(context.Background(), mkReq(map[string]any{"command": "status"}))
	text := firstText(t, res)
	for _, want := range []string{
		"Open dialog: confirm", "Delete this record?", "BLOCKING the page",
		"Armed handler: accept", "Recent dialogs (2)", "borz accept", "resolved outside borz",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("status text %q missing %q", text, want)
		}
	}
}

func TestHandleDialog_StatusWithNothingOpen(t *testing.T) {
	capturingSend(t, &protocol.Response{Success: true, Data: &protocol.ResponseData{
		DialogInfo: map[string]interface{}{"type": "status", "armed": false, "blocked": false},
	}})
	res, _ := handleDialog(context.Background(), mkReq(map[string]any{"command": "status"}))
	text := firstText(t, res)
	if !strings.Contains(text, "Open dialog: none") || !strings.Contains(text, "Armed handler: none") {
		t.Errorf("status text = %q", text)
	}
}

func TestHandleFileChooser(t *testing.T) {
	// accept requires files
	res, _ := handleFileChooser(context.Background(), mkReq(map[string]any{"command": "accept"}))
	if !res.IsError {
		t.Error("accept without files should error")
	}
	res, _ = handleFileChooser(context.Background(), mkReq(map[string]any{"command": "accept", "files": "x"}))
	if !res.IsError {
		t.Error("non-array files should error")
	}

	cap := capturingSend(t, ok())
	_, _ = handleFileChooser(context.Background(), mkReq(map[string]any{
		"command": "accept", "files": []any{"/tmp/a.pdf"}, "tab": "T1",
	}))
	if cap.req.Action != protocol.ActionFileChooser || cap.req.FileChooserCommand != "accept" {
		t.Errorf("request = %+v", cap.req)
	}
	if len(cap.req.Files) != 1 || cap.req.Files[0] != "/tmp/a.pdf" {
		t.Errorf("files = %v", cap.req.Files)
	}

	// No command defaults to status, and status needs no files.
	cap = capturingSend(t, ok())
	_, _ = handleFileChooser(context.Background(), mkReq(nil))
	if cap.req.FileChooserCommand != "status" {
		t.Errorf("default command = %q", cap.req.FileChooserCommand)
	}

	cap = capturingSend(t, ok())
	_, _ = handleFileChooser(context.Background(), mkReq(map[string]any{"command": "cancel"}))
	if cap.req.FileChooserCommand != "cancel" || len(cap.req.Files) != 0 {
		t.Errorf("cancel request = %+v", cap.req)
	}
}
